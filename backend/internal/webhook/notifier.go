package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
)

// Notifier fires signed webhook events on critical system events (FR30, NFR-S5).
// Payloads are signed with HMAC-SHA256 using the configured secret.
// The signature is delivered in the X-ECG-Hub-Signature header as "sha256=<hex>".
type Notifier struct {
	cfg    config.WebhookConfig
	secret string
	client *http.Client
}

// NewNotifier constructs a Notifier. secret is WEBHOOK_SECRET from the environment.
func NewNotifier(cfg config.WebhookConfig, secret string) *Notifier {
	return &Notifier{
		cfg:    cfg,
		secret: secret,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// payload is the JSON body sent for every webhook event.
type payload struct {
	Event     string `json:"event"`
	ECGID     string `json:"ecg_id"`
	Timestamp string `json:"timestamp"`
}

// Notify delivers a signed webhook for the given event. Returns nil when disabled.
// Errors are logged but never propagate to the caller — webhooks are best-effort.
func (n *Notifier) Notify(event string, ecgID string) error {
	if !n.cfg.Enabled {
		return nil
	}
	if n.cfg.URL == "" {
		slog.Warn("webhook: enabled but no URL configured — skipping", "event", event)
		return nil
	}

	p := payload{
		Event:     event,
		ECGID:     ecgID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(p)
	if err != nil {
		slog.Error("webhook: marshal failed", "error", err, "event", event)
		return err
	}

	sig := sign(body, n.secret)

	req, err := http.NewRequest(http.MethodPost, n.cfg.URL, bytes.NewReader(body))
	if err != nil {
		slog.Error("webhook: build request failed", "error", err, "url", n.cfg.URL)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ECG-Hub-Event", event)
	req.Header.Set("X-ECG-Hub-Signature", "sha256="+sig)
	req.Header.Set("X-ECG-Hub-Timestamp", p.Timestamp)

	resp, err := n.client.Do(req)
	if err != nil {
		slog.Error("webhook: delivery failed", "error", err, "event", event, "url", n.cfg.URL)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = fmt.Errorf("webhook: receiver returned HTTP %d", resp.StatusCode)
		slog.Error(err.Error(), "event", event, "url", n.cfg.URL)
		return err
	}

	slog.Info("webhook: delivered", "event", event, "ecg_id", ecgID, "status", resp.StatusCode)
	return nil
}

// Test fires a test webhook event and returns the HTTP status code alongside any error.
func (n *Notifier) Test() (int, error) {
	if !n.cfg.Enabled {
		return 0, fmt.Errorf("webhook not enabled")
	}
	if n.cfg.URL == "" {
		return 0, fmt.Errorf("webhook URL not configured")
	}

	p := payload{
		Event:     "test",
		ECGID:     "0",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	body, _ := json.Marshal(p)
	sig := sign(body, n.secret)

	req, err := http.NewRequest(http.MethodPost, n.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ECG-Hub-Event", "test")
	req.Header.Set("X-ECG-Hub-Signature", "sha256="+sig)
	req.Header.Set("X-ECG-Hub-Timestamp", p.Timestamp)

	resp, err := n.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// Status returns the current webhook configuration status (never exposes the secret).
type Status struct {
	Enabled          bool   `json:"enabled"`
	URL              string `json:"url"`
	SecretConfigured bool   `json:"secret_configured"`
}

// GetStatus returns the webhook configuration state for the admin UI.
func (n *Notifier) GetStatus() Status {
	return Status{
		Enabled:          n.cfg.Enabled,
		URL:              n.cfg.URL,
		SecretConfigured: n.secret != "",
	}
}

// sign returns the HMAC-SHA256 hex signature of body using secret.
func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
