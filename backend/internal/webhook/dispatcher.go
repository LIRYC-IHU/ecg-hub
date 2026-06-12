package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/events"
)

// retryBackoff is the wait time before each delivery attempt (attempt 1 has no wait).
var retryBackoff = []time.Duration{0, 5 * time.Second, 25 * time.Second}

// Dispatcher fans out ingestion and HL7 events to user-configured webhooks
// (user_webhooks table). It complements the legacy static Notifier
// (config.yaml) — both can be active at the same time.
//
// Deliveries are asynchronous and best-effort: 3 attempts with backoff, then
// the outcome (status code / error) is recorded on the webhook row for UI
// feedback. A failing receiver never blocks ingestion.
type Dispatcher struct {
	repo    *repository.UserWebhookRepository
	db      *gorm.DB // ECG lookup to enrich HL7 events with patient/vendor
	encKey  string   // AES key for decrypting per-webhook secrets
	baseURL string   // public base URL prefixed to payload links ("" = relative links)

	client         *http.Client
	insecureClient *http.Client

	wg   sync.WaitGroup
	stop chan struct{}
}

// NewDispatcher builds a Dispatcher. baseURL is the public origin of this
// server (e.g. "http://ecg-hub.chu.fr") used to build absolute callback links.
func NewDispatcher(repo *repository.UserWebhookRepository, db *gorm.DB, encKey, baseURL string) *Dispatcher {
	return &Dispatcher{
		repo:    repo,
		db:      db,
		encKey:  encKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
		insecureClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // per-webhook opt-in for self-signed receivers
			},
		},
		stop: make(chan struct{}),
	}
}

// Run subscribes to the event hub and dispatches until Stop is called.
// Call as a goroutine: go d.Run(hub).
func (d *Dispatcher) Run(hub *events.Hub) {
	ch, unsub := hub.Subscribe()
	defer unsub()
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			d.dispatch(Payload{
				Event: e.Type,
				Data: PayloadData{
					ECGID:        e.ECGID,
					PatientID:    e.PatientID,
					QuarantineID: e.QuarantineID,
					Vendor:       e.Vendor,
					Filename:     e.Filename,
					Reason:       e.Reason,
				},
			})
		case <-d.stop:
			return
		}
	}
}

// Stop terminates Run and waits for in-flight deliveries (bounded by the
// HTTP client timeout and remaining backoff).
func (d *Dispatcher) Stop() {
	close(d.stop)
	d.wg.Wait()
}

// Notify implements the HL7 jobs' notifier interface (same signature as the
// legacy Notifier) so HL7 lifecycle events also reach user webhooks.
// Legacy event names are mapped to namespaced webhook event types.
func (d *Dispatcher) Notify(event string, ecgID string) error {
	mapped := ""
	switch event {
	case "hl7_exhausted":
		mapped = models.WebhookEventHL7Exhausted
	case "hl7_rejected":
		mapped = models.WebhookEventHL7Rejected
	default:
		return nil // not a user-webhook event (e.g. "test")
	}

	data := PayloadData{ECGID: ecgID}
	// Enrich with patient/vendor so the receiver can filter without a callback.
	var ecg models.ECG
	if err := d.db.Select("patient_id", "vendor").Where("id = ?", ecgID).First(&ecg).Error; err == nil {
		data.PatientID = ecg.PatientID
		data.Vendor = ecg.Vendor
	}
	d.dispatch(Payload{Event: mapped, Data: data})
	return nil
}

// Payload is the JSON body delivered to user webhooks. Links contains the API
// endpoints the receiver can call (authenticated with its own API key or JWT)
// to fetch the referenced ECG data.
type Payload struct {
	Event     string            `json:"event"`
	WebhookID string            `json:"webhook_id"`
	Timestamp string            `json:"timestamp"`
	Data      PayloadData       `json:"data"`
	Links     map[string]string `json:"links,omitempty"`
}

// PayloadData identifies the resource the event refers to.
type PayloadData struct {
	ECGID        string `json:"ecg_id,omitempty"`
	PatientID    string `json:"patient_id,omitempty"`
	QuarantineID string `json:"quarantine_id,omitempty"`
	Vendor       string `json:"vendor,omitempty"`
	Filename     string `json:"filename,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// buildLinks returns the callback API endpoints relevant to the event.
func (d *Dispatcher) buildLinks(data PayloadData) map[string]string {
	links := map[string]string{}
	if data.ECGID != "" {
		links["ecg_metadata"] = d.baseURL + "/api/v1/ecgs/" + data.ECGID + "/metadata"
		links["ecg_download"] = d.baseURL + "/api/v1/ecgs/" + data.ECGID + "/download"
		links["ecg_waveform"] = d.baseURL + "/api/v1/ecgs/" + data.ECGID + "/waveform"
	}
	if data.PatientID != "" {
		links["patient_ecgs"] = d.baseURL + "/api/v1/patients/" + data.PatientID + "/ecgs"
	}
	if data.QuarantineID != "" {
		links["quarantine"] = d.baseURL + "/api/v1/admin/quarantine"
	}
	return links
}

// dispatch fans the event out to every enabled webhook whose event and vendor
// filters match. Each delivery runs in its own goroutine.
func (d *Dispatcher) dispatch(p Payload) {
	hooks, err := d.repo.ListEnabled()
	if err != nil {
		slog.Error("webhook dispatcher: list enabled", "error", err)
		return
	}
	p.Timestamp = time.Now().UTC().Format(time.RFC3339)
	p.Links = d.buildLinks(p.Data)

	for i := range hooks {
		hook := hooks[i]
		if !matches(jsonList(hook.Events), p.Event) {
			continue
		}
		// Vendor filter only applies when the event carries a vendor.
		if p.Data.Vendor != "" && !matches(jsonList(hook.Vendors), p.Data.Vendor) {
			continue
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.deliverWithRetry(hook, p)
		}()
	}
}

// deliverWithRetry attempts delivery up to len(retryBackoff) times, then
// records the final outcome on the webhook row.
func (d *Dispatcher) deliverWithRetry(hook models.UserWebhook, p Payload) {
	var status int
	var lastErr error
	for attempt, wait := range retryBackoff {
		if wait > 0 {
			select {
			case <-time.After(wait):
			case <-d.stop:
				return
			}
		}
		status, lastErr = d.Deliver(hook, p)
		if lastErr == nil {
			break
		}
		slog.Warn("webhook dispatcher: delivery failed",
			"webhook", hook.Name, "event", p.Event, "attempt", attempt+1, "error", lastErr)
	}

	errMsg := ""
	if lastErr != nil {
		errMsg = lastErr.Error()
	} else {
		slog.Info("webhook dispatcher: delivered",
			"webhook", hook.Name, "event", p.Event, "status", status)
	}
	if err := d.repo.RecordDelivery(hook.ID, status, errMsg); err != nil {
		slog.Warn("webhook dispatcher: record delivery", "webhook", hook.ID, "error", err)
	}
}

// Deliver sends one signed POST to hook and returns the HTTP status code.
// Exported so the "test webhook" handler can perform a synchronous delivery.
func (d *Dispatcher) Deliver(hook models.UserWebhook, p Payload) (int, error) {
	p.WebhookID = hook.ID
	body, err := json.Marshal(p)
	if err != nil {
		return 0, fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, hook.URL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ECG-Hub-Event", p.Event)
	req.Header.Set("X-ECG-Hub-Timestamp", p.Timestamp)
	req.Header.Set("X-ECG-Hub-Webhook-ID", hook.ID)

	// HMAC signature with the per-webhook secret (when configured).
	if hook.SecretEncrypted != "" {
		secret, err := auth.DecryptString(hook.SecretEncrypted, d.encKey)
		if err != nil {
			return 0, fmt.Errorf("decrypt secret: %w", err)
		}
		req.Header.Set("X-ECG-Hub-Signature", "sha256="+sign(body, secret))
	}

	// Optional static Authorization header (e.g. "Bearer <token>").
	if hook.AuthHeaderEncrypted != "" {
		authHeader, err := auth.DecryptString(hook.AuthHeaderEncrypted, d.encKey)
		if err != nil {
			return 0, fmt.Errorf("decrypt auth header: %w", err)
		}
		req.Header.Set("Authorization", authHeader)
	}

	client := d.client
	if hook.InsecureSkipVerify {
		client = d.insecureClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("receiver returned HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// sign returns the HMAC-SHA256 hex signature of body using secret.
// Delivered in the X-ECG-Hub-Signature header as "sha256=<hex>".
func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// matches reports whether value passes the filter list (empty list = match all).
func matches(filter []string, value string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if f == value {
			return true
		}
	}
	return false
}

// jsonList decodes a jsonb string array; invalid/empty input yields nil.
func jsonList(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
