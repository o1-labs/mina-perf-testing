package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	logging "github.com/ipfs/go-log/v2"
)

// TestValidateWebhookURL proves each destination class is refused before a
// socket is opened. POST /api/v0/experiment/run is unauthenticated, so the
// webhook URL is attacker-controlled and the orchestrator would otherwise be
// an arbitrary-POST primitive inside the cluster.
func TestValidateWebhookURL(t *testing.T) {
	for _, tc := range []struct {
		name         string
		url          string
		allowPrivate bool
		wantErr      string
	}{
		// --- must be refused ---
		{"cloud metadata service", "http://169.254.169.254/latest/meta-data/", false, "link-local"},
		{"internal elasticsearch", "http://10.0.0.5:9200/experiments/_doc", false, "private address"},
		{"rfc1918 172.16", "http://172.16.3.4/x", false, "private address"},
		{"rfc1918 192.168", "http://192.168.1.10/x", false, "private address"},
		{"loopback by name", "http://localhost:8080/api/v0/experiment/cancel", false, "loopback"},
		{"loopback by address", "http://127.0.0.1:8080/x", false, "loopback"},
		{"ipv6 loopback", "http://[::1]:8080/x", false, "loopback"},
		{"unspecified", "http://0.0.0.0:9090/x", false, "unspecified"},
		{"file scheme", "file:///etc/passwd", false, "scheme"},
		{"gopher scheme", "gopher://example.com/x", false, "scheme"},
		{"no host", "http:///justapath", false, "no host"},

		// Ranges net.IP reports as ordinary global unicast. 100.64.0.0/10 is
		// the one that matters here: several CNI plugins and Tailscale put
		// pod, service and node addresses in it, and Alibaba's metadata
		// service lives at 100.100.100.200.
		{"cgnat", "http://100.64.0.1/x", false, "reserved range"},
		{"alibaba metadata", "http://100.100.100.200/x", false, "reserved range"},
		{"ietf protocol assignments", "http://192.0.0.1/x", false, "reserved range"},
		{"benchmarking", "http://198.18.0.1/x", false, "reserved range"},
		{"reserved class e", "http://240.0.0.1/x", false, "reserved range"},
		{"nat64 to loopback", "http://[64:ff9b::7f00:1]/x", false, "loopback"},

		// --- must be accepted ---
		{"public https", "https://hooks.slack.com/services/T000/B000/XXXX", false, ""},
		{"public http", "http://example.com/hook", false, ""},
		{"private allowed by operator", "http://10.0.0.5:9200/x", true, ""},

		// allowPrivate is a concession for a receiver on the cluster network.
		// It must not re-open loopback or the metadata service.
		{"loopback still refused when private allowed", "http://127.0.0.1:8080/x", true, "loopback"},
		{"metadata still refused when private allowed", "http://169.254.169.254/x", true, "link-local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWebhookURL(tc.url, tc.allowPrivate)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateWebhookURL(%q) = %v, want accepted", tc.url, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateWebhookURL(%q) accepted it; want refused (%s)", tc.url, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateWebhookURL(%q) = %v, want mention of %q", tc.url, err, tc.wantErr)
			}
		})
	}
}

// TestRedactURL pins that the secret-bearing path never reaches a log line.
func TestRedactURL(t *testing.T) {
	const secret = "T000/B000/SUPERSECRETTOKEN"
	got := redactURL("https://hooks.slack.com/services/" + secret)
	if strings.Contains(got, "SUPERSECRETTOKEN") {
		t.Fatalf("redactURL leaked the token: %s", got)
	}
	if !strings.Contains(got, "hooks.slack.com") {
		t.Fatalf("redactURL dropped the host, leaving nothing to debug with: %s", got)
	}
	if got := redactURL("://not a url"); strings.Contains(got, "not a url") {
		t.Fatalf("redactURL passed through an unparseable URL: %s", got)
	}
}

func testNotifier() *WebhookNotifier {
	n := NewWebhookNotifier(logging.Logger("webhook-test"))
	// httptest servers listen on loopback, which validateWebhookURL refuses by
	// design, so the delivery tests opt out of the address check only.
	n.validateURL = func(string) error { return nil }
	n.checkDialIP = func(net.IP) error { return nil }
	return n
}

// TestSendNotificationChecksTheAddressItDials proves the second half of the
// SSRF guard. validateWebhookURL resolves the name itself, but the transport
// resolves it again, so a hostile zero-TTL record could answer public for the
// check and internal for the dial. Here validation is stubbed to accept
// everything -- standing in for exactly that -- and the connection must still
// be refused, by Dialer.Control, before any byte is sent.
func TestSendNotificationChecksTheAddressItDials(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(logging.Logger("webhook-test"))
	n.validateURL = func(string) error { return nil } // the check is defeated
	err := n.SendNotification(context.Background(), srv.URL, WebhookPayload{ExperimentName: "exp-1"})
	if reached {
		t.Fatal("the request reached a loopback receiver; the dial-time check did not run")
	}
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("err = %v, want a refusal naming the loopback address", err)
	}
}

// TestValidateWebhookURLWorksOffline pins that the suite does not need DNS:
// the resolver is a variable, so a name can be answered from the test.
func TestValidateWebhookURLWorksOffline(t *testing.T) {
	saved := lookupIP
	defer func() { lookupIP = saved }()

	lookupIP = func(host string) ([]net.IP, error) {
		switch host {
		case "receiver.example":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		case "rebind.example":
			// One answer is public, the other is the metadata service. Any
			// refused address refuses the whole destination.
			return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("169.254.169.254")}, nil
		}
		return nil, fmt.Errorf("no such host")
	}

	if err := validateWebhookURL("https://receiver.example/hook", false); err != nil {
		t.Fatalf("public name refused: %v", err)
	}
	if err := validateWebhookURL("https://rebind.example/hook", false); err == nil {
		t.Fatal("a name that also resolves to the metadata service must be refused")
	}
	if err := validateWebhookURL("https://missing.example/hook", false); err == nil {
		t.Fatal("a name that does not resolve must be refused")
	}
}

// TestSendNotificationPayload asserts the bytes and headers a receiver sees.
func TestSendNotificationPayload(t *testing.T) {
	type received struct {
		body        WebhookPayload
		contentType string
		userAgent   string
	}
	got := make(chan received, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p WebhookPayload
		if err := json.Unmarshal(b, &p); err != nil {
			t.Errorf("receiver got invalid JSON: %v", err)
		}
		got <- received{p, r.Header.Get("Content-Type"), r.Header.Get("User-Agent")}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	want := WebhookPayload{ExperimentName: "exp-1", Success: true, Warnings: []string{"w1"}}
	if err := testNotifier().SendNotification(context.Background(), srv.URL, want); err != nil {
		t.Fatalf("SendNotification: %v", err)
	}

	r := <-got
	if r.body.ExperimentName != want.ExperimentName || r.body.Success != want.Success {
		t.Errorf("payload = %+v, want %+v", r.body, want)
	}
	if len(r.body.Warnings) != 1 || r.body.Warnings[0] != "w1" {
		t.Errorf("warnings = %v, want [w1]", r.body.Warnings)
	}
	if r.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", r.contentType)
	}
	if r.userAgent != "mina-orchestrator/1.0" {
		t.Errorf("User-Agent = %q, want mina-orchestrator/1.0", r.userAgent)
	}
}

// TestSendNotificationNon2xx: a receiver that rejects the delivery is an error.
func TestSendNotificationNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := testNotifier().SendNotification(context.Background(), srv.URL, WebhookPayload{ExperimentName: "exp-1"})
	if err == nil {
		t.Fatal("want an error for a 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error should name the status code, got %v", err)
	}
}

// TestSendNotificationDoesNotFollowRedirects: a destination that passes
// validation must not be able to bounce the POST somewhere that would not.
func TestSendNotificationDoesNotFollowRedirects(t *testing.T) {
	var reached bool
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer internal.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/experiments/_doc", http.StatusFound)
	}))
	defer redirector.Close()

	err := testNotifier().SendNotification(context.Background(), redirector.URL, WebhookPayload{ExperimentName: "exp-1"})
	if reached {
		t.Fatal("the redirect was followed; a 302 must not carry the POST to a second host")
	}
	// 302 is not 2xx, so the delivery is reported as a failure rather than
	// silently succeeding.
	if err == nil {
		t.Fatal("want an error when the receiver answers with a redirect, got nil")
	}
}

// TestSendNotificationRefusesInternalDestination is the end-to-end form of
// TestValidateWebhookURL: no socket is opened at all.
func TestSendNotificationRefusesInternalDestination(t *testing.T) {
	n := NewWebhookNotifier(logging.Logger("webhook-test")) // validation ON
	err := n.SendNotification(context.Background(), "http://169.254.169.254/latest/meta-data/",
		WebhookPayload{ExperimentName: "exp-1"})
	if err == nil {
		t.Fatal("want the metadata service to be refused, got nil")
	}
	if !strings.Contains(err.Error(), "link-local") {
		t.Fatalf("error = %v, want it to name the reason", err)
	}
}

// TestSendNotificationSkipsEmptyURL: no webhook configured is not an error.
func TestSendNotificationSkipsEmptyURL(t *testing.T) {
	if err := testNotifier().SendNotification(context.Background(), "", WebhookPayload{}); err != nil {
		t.Fatalf("empty webhook URL should be a no-op, got %v", err)
	}
}
