package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"

	logging "github.com/ipfs/go-log/v2"
)

// WebhookPayload represents the data sent to the webhook endpoint
type WebhookPayload struct {
	ExperimentName string   `json:"experiment_name"`
	Success        bool     `json:"success"`
	Error          string   `json:"error,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
}

// WebhookNotifier handles webhook notifications
type WebhookNotifier struct {
	client *http.Client
	log    logging.StandardLogger
	// allowPrivate permits webhook destinations on RFC1918/RFC4193 addresses,
	// for operators running a receiver on the cluster network. Off by default.
	allowPrivate bool
	// validateURL guards the destination. It is a field so that tests can aim
	// at an httptest server, which necessarily listens on loopback and would
	// otherwise be refused. Nil means the real check.
	validateURL func(string) error
	// checkDialIP guards the address the transport actually connects to. Nil
	// means the real check; tests that aim at an httptest server replace it.
	checkDialIP func(net.IP) error
}

// NewWebhookNotifier creates a new webhook notifier
func NewWebhookNotifier(log logging.StandardLogger) *WebhookNotifier {
	n := &WebhookNotifier{log: log}
	n.client = &http.Client{
		Timeout: 30 * time.Second,
		// Without this, a destination that passes validateWebhookURL can
		// still redirect the POST to an internal address, and the default
		// policy would follow it up to ten times.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: n.transport(),
	}
	return n
}

// transport builds a transport that re-checks the address at dial time.
//
// validateWebhookURL resolves the host itself, but the transport then resolves
// it a second time, and nothing tied the two results together. An attacker
// controls both the destination and, for a name they own, its DNS: a record
// with a zero TTL can answer with a public address for the validation lookup
// and with 169.254.169.254 for the dial. Dialer.Control runs after resolution,
// with the address the socket is about to connect to, so checking there closes
// that window whatever DNS says.
func (w *WebhookNotifier) transport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("refusing to dial %q: %v", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("refusing to dial %q: not an IP address", host)
			}
			check := w.checkDialIP
			if check == nil {
				check = func(ip net.IP) error { return checkWebhookIP(ip, w.allowPrivate) }
			}
			if err := check(ip); err != nil {
				return fmt.Errorf("refusing to connect to %s: %w", ip, err)
			}
			return nil
		},
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = dialer.DialContext
	return t
}

// redactURL keeps scheme and host and drops everything after them. For
// Slack-style webhooks the path carries the secret, so the full URL must never
// reach the log.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(redacted)"
	}
	return u.Scheme + "://" + u.Host + "/(redacted)"
}

// SendNotification sends a webhook notification to the specified URL
func (w *WebhookNotifier) SendNotification(ctx context.Context, webhookURL string, payload WebhookPayload) error {
	if webhookURL == "" {
		return nil // No webhook URL provided, skip notification
	}

	validate := w.validateURL
	if validate == nil {
		validate = func(u string) error { return validateWebhookURL(u, w.allowPrivate) }
	}
	if err := validate(webhookURL); err != nil {
		return err
	}

	w.log.Infof("Sending webhook notification to %s for experiment %s", redactURL(webhookURL), payload.ExperimentName)

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mina-orchestrator/1.0")

	resp, err := w.client.Do(req)
	if err != nil {
		// *url.Error stringifies with the full URL in it, secret path and all.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return fmt.Errorf("failed to send webhook request to %s: %v", redactURL(webhookURL), urlErr.Err)
		}
		return fmt.Errorf("failed to send webhook request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-success status code: %d", resp.StatusCode)
	}

	w.log.Infof("Successfully sent webhook notification to %s for experiment %s", redactURL(webhookURL), payload.ExperimentName)
	return nil
}

// SendSuccessNotification sends a success notification
func (w *WebhookNotifier) SendSuccessNotification(ctx context.Context, webhookURL, experimentName string, warnings []string) {
	payload := WebhookPayload{
		ExperimentName: experimentName,
		Success:        true,
		Warnings:       warnings,
	}

	if err := w.SendNotification(ctx, webhookURL, payload); err != nil {
		w.log.Errorf("Failed to send success webhook notification: %v", err)
	}
}

// SendErrorNotification sends an error notification
func (w *WebhookNotifier) SendErrorNotification(ctx context.Context, webhookURL, experimentName, errorMessage string, warnings []string) {
	payload := WebhookPayload{
		ExperimentName: experimentName,
		Success:        false,
		Error:          errorMessage,
		Warnings:       warnings,
	}

	if err := w.SendNotification(ctx, webhookURL, payload); err != nil {
		w.log.Errorf("Failed to send error webhook notification: %v", err)
	}
}
