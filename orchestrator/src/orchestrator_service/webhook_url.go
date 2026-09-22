package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// lookupIP resolves a host name. It is a variable so that tests can run with
// no network: the real net.LookupIP made the whole webhook suite depend on
// working DNS.
var lookupIP = net.LookupIP

// deniedCIDRs are address ranges that net.IP's own predicates report as
// ordinary global unicast, but that no legitimate webhook receiver uses and
// that reach infrastructure when the orchestrator dials them.
//
// 100.64.0.0/10 is the operationally important one: RFC 6598 carrier-grade
// NAT space, which several CNI plugins and Tailscale use for pod, service and
// node addresses, and which carries Alibaba's metadata service at
// 100.100.100.200.
var deniedCIDRs = mustParseCIDRs(
	"100.64.0.0/10",   // RFC 6598 carrier-grade NAT, CNI/Tailscale space
	"192.0.0.0/24",    // RFC 6890 IETF protocol assignments
	"198.18.0.0/15",   // RFC 2544 benchmarking
	"240.0.0.0/4",     // RFC 1112 reserved
	"192.0.2.0/24",    // RFC 5737 documentation
	"198.51.100.0/24", // RFC 5737 documentation
	"203.0.113.0/24",  // RFC 5737 documentation
	"2002::/16",       // RFC 3056 6to4
	"fec0::/10",       // deprecated site-local
)

// nat64Prefixes embed an IPv4 address in their last four bytes, so
// 64:ff9b::7f00:1 is a route to 127.0.0.1. The embedded address is checked as
// if it had been given directly.
var nat64Prefixes = mustParseCIDRs(
	"64:ff9b::/96",   // RFC 6052 well-known prefix
	"64:ff9b:1::/48", // RFC 8215 local-use prefix
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("webhook denylist: bad CIDR %q: %v", c, err))
		}
		nets = append(nets, n)
	}
	return nets
}

// validateWebhookURL decides whether the orchestrator is willing to POST to a
// caller-supplied destination.
//
// The create-experiment endpoint is unauthenticated, so without this check any
// caller can make the orchestrator issue an arbitrary POST from inside the
// cluster, under its own pod identity — an internal Elasticsearch, the cloud
// metadata service, or the orchestrator's own /cancel route. The response body
// is never returned to the caller, so the primitive is blind, but it is still
// a write.
//
// The rules are deliberately coarse:
//
//   - the scheme must be http or https;
//   - the host must resolve, and no address it resolves to may be loopback,
//     link-local (which covers 169.254.169.254), unspecified, interface-local,
//     otherwise non-global unicast, or inside deniedCIDRs;
//   - private ranges (RFC1918 / RFC4193) are refused unless the operator has
//     explicitly allowed them.
//
// allowPrivate exists because a self-hosted receiver on the cluster network is
// a legitimate configuration; it is off unless switched on deliberately.
//
// This check happens before the request is built. It cannot stand on its own,
// because the transport resolves the name a second time and an attacker who
// controls the DNS record can answer differently then; the dial-time check in
// newWebhookTransport closes that window. Both are needed: this one gives the
// caller a clear 400, the dialer gives the guarantee.
func validateWebhookURL(raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid webhook_url: %v", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("invalid webhook_url: scheme %q is not supported (use http or https)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("invalid webhook_url: no host")
	}

	// A literal IP is checked directly; a name is checked against every address
	// it resolves to, so a DNS entry pointing at a private address is refused
	// the same way a literal would be.
	var addrs []net.IP
	if ip := net.ParseIP(host); ip != nil {
		addrs = []net.IP{ip}
	} else {
		resolved, err := lookupIP(host)
		if err != nil {
			return fmt.Errorf("invalid webhook_url: cannot resolve host %q: %v", host, err)
		}
		addrs = resolved
	}

	for _, ip := range addrs {
		if err := checkWebhookIP(ip, allowPrivate); err != nil {
			return fmt.Errorf("invalid webhook_url: host %q resolves to %s: %w", host, ip, err)
		}
	}
	return nil
}

func checkWebhookIP(ip net.IP, allowPrivate bool) error {
	// A NAT64 address is a route to the IPv4 address it embeds, so that
	// address decides.
	for _, prefix := range nat64Prefixes {
		if prefix.Contains(ip) {
			embedded := net.IPv4(ip[12], ip[13], ip[14], ip[15])
			if err := checkWebhookIP(embedded, allowPrivate); err != nil {
				return fmt.Errorf("a NAT64 route to %s: %w", embedded, err)
			}
			return nil
		}
	}

	switch {
	case ip.IsLoopback():
		return fmt.Errorf("a loopback address")
	case ip.IsUnspecified():
		return fmt.Errorf("an unspecified address")
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		// 169.254.169.254 is the cloud instance metadata service.
		return fmt.Errorf("a link-local address")
	case ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return fmt.Errorf("a multicast address")
	case !ip.IsGlobalUnicast():
		return fmt.Errorf("not a global unicast address")
	case ip.IsPrivate() && !allowPrivate:
		return fmt.Errorf("a private address (set allow-private-webhooks to permit this)")
	}

	if !allowPrivate {
		for _, denied := range deniedCIDRs {
			if denied.Contains(ip) {
				return fmt.Errorf("an address in the reserved range %s", denied)
			}
		}
	}
	return nil
}
