package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

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
//     link-local (which covers 169.254.169.254), unspecified, interface-local
//     or otherwise non-global unicast;
//   - private ranges (RFC1918 / RFC4193) are refused unless the operator has
//     explicitly allowed them.
//
// allowPrivate exists because a self-hosted receiver on the cluster network is
// a legitimate configuration; it is off unless switched on deliberately.
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
		resolved, err := net.LookupIP(host)
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
	return nil
}
