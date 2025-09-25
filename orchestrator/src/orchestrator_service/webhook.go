package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
}

// NewWebhookNotifier creates a new webhook notifier
func NewWebhookNotifier(log logging.StandardLogger) *WebhookNotifier {
	return &WebhookNotifier{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: log,
	}
}

// SendNotification sends a webhook notification to the specified URL
func (w *WebhookNotifier) SendNotification(ctx context.Context, webhookURL string, payload WebhookPayload) error {
	if webhookURL == "" {
		return nil // No webhook URL provided, skip notification
	}

	w.log.Infof("Sending webhook notification to %s for experiment %s", webhookURL, payload.ExperimentName)

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
		return fmt.Errorf("failed to send webhook request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-success status code: %d", resp.StatusCode)
	}

	w.log.Infof("Successfully sent webhook notification to %s for experiment %s", webhookURL, payload.ExperimentName)
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
