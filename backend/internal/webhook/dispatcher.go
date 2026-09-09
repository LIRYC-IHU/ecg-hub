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
	"net"
	"net/http"
	"net/url"
	"sync"
	"syscall"
	"time"

	"gorm.io/datatypes"
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
	repo         *repository.UserWebhookRepository
	deliveryRepo *repository.WebhookDeliveryRepository // logs delivery history; nil disables logging
	db           *gorm.DB                              // ECG lookup to enrich HL7 events with patient/vendor
	encKey       string                                // AES key for decrypting per-webhook secrets
	baseURL      string                                // public base URL prefixed to payload links ("" = relative links)
	retention    time.Duration                         // delivery-history retention; 0 = keep everything

	client         *http.Client
	insecureClient *http.Client

	wg   sync.WaitGroup
	stop chan struct{}
}

// NewDispatcher builds a Dispatcher. baseURL is the public origin of this
// server (e.g. "http://ecg-hub.chu.fr") used to build absolute callback links.
// deliveryRepo may be nil to disable delivery history logging. retention is how
// long delivery history is kept; 0 keeps everything.
func NewDispatcher(repo *repository.UserWebhookRepository, deliveryRepo *repository.WebhookDeliveryRepository, db *gorm.DB, encKey, baseURL string, retention time.Duration) *Dispatcher {
	// Shared dialer with an SSRF guard: Control runs after DNS resolution with the
	// IP about to be dialed, so disallowed targets are blocked even across DNS
	// rebinding and HTTP redirects.
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: webhookDialControl}
	return &Dispatcher{
		repo:         repo,
		deliveryRepo: deliveryRepo,
		db:           db,
		encKey:       encKey,
		baseURL:      baseURL,
		retention:    retention,
		client: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: noWebhookRedirect,
			Transport:     &http.Transport{DialContext: dialer.DialContext},
		},
		insecureClient: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: noWebhookRedirect,
			Transport: &http.Transport{
				DialContext:     dialer.DialContext,
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // per-webhook opt-in for self-signed receivers
			},
		},
		stop: make(chan struct{}),
	}
}

// allowLoopbackWebhookForTest relaxes the loopback block so unit tests can deliver
// to httptest servers (which bind to 127.0.0.1). It is NEVER set in production code.
var allowLoopbackWebhookForTest bool

// noWebhookRedirect stops webhook deliveries from following HTTP redirects — a 3xx
// to an internal URL would otherwise sidestep the SSRF guard. Returning
// ErrUseLastResponse hands the redirect response back to Deliver, which treats any
// non-2xx as a failure.
func noWebhookRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// webhookDialControl is a net.Dialer.Control hook that refuses connections an
// attacker with webhook.manage could use to reach the ECG Hub host itself or cloud
// metadata (SSRF). It runs on the post-resolution IP, so it also defeats DNS
// rebinding. RFC1918 private ranges are intentionally allowed: internal research
// servers are a legitimate webhook target on the deployment subnet.
func webhookDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("webhook: invalid dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("webhook: unresolved dial host %q", host)
	}
	if ip.IsLoopback() && allowLoopbackWebhookForTest {
		return nil
	}
	if ip.IsLoopback() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return fmt.Errorf("webhook: refusing to connect to disallowed address %s (SSRF guard)", ip)
	}
	return nil
}

// validateWebhookURL rejects non-HTTP(S) schemes and empty hosts before any network
// call. The dial-time guard (webhookDialControl) enforces the IP policy; this gives
// a clear early error for obviously invalid targets.
func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("webhook: invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook: unsupported URL scheme %q (only http/https allowed)", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("webhook: URL has no host")
	}
	return nil
}

// Run subscribes to the event hub and dispatches until Stop is called.
// Call as a goroutine: go d.Run(hub).
func (d *Dispatcher) Run(hub *events.Hub) {
	ch, unsub := hub.Subscribe()
	defer unsub()

	// Retention runs on the same goroutine as dispatch: one delete statement a
	// few times a day costs nothing, and sharing the loop means it stops with
	// the dispatcher instead of needing its own lifecycle. The first pass is
	// immediate so a server that was down past the retention window catches up
	// on boot rather than at the next tick.
	d.pruneDeliveries()
	prune := time.NewTicker(pruneInterval)
	defer prune.Stop()

	for {
		select {
		case <-prune.C:
			d.pruneDeliveries()
		case e, ok := <-ch:
			if !ok {
				return
			}
			data := PayloadData{
				ECGID:        e.ECGID,
				PatientID:    e.PatientID,
				QuarantineID: e.QuarantineID,
				Vendor:       e.Vendor,
				DeviceMAC:    e.DeviceMAC,
				Filename:     e.Filename,
				Reason:       e.Reason,
			}
			data.DeviceLabel = d.deviceLabel(data.DeviceMAC)
			d.dispatch(Payload{Event: e.Type, Data: data})
		case <-d.stop:
			return
		}
	}
}

// pruneInterval is how often the delivery history is trimmed. Retention is
// measured in days, so checking a few times a day is ample and keeps the delete
// batches small.
const pruneInterval = 6 * time.Hour

// pruneDeliveries drops delivery history older than the configured retention.
// Best-effort: a failed prune is logged and retried on the next tick — it must
// never affect delivery.
func (d *Dispatcher) pruneDeliveries() {
	if d.deliveryRepo == nil || d.retention <= 0 {
		return
	}
	cutoff := time.Now().Add(-d.retention)
	n, err := d.deliveryRepo.DeleteOlderThan(cutoff)
	if err != nil {
		slog.Warn("webhook dispatcher: prune delivery history", "error", err)
		return
	}
	if n > 0 {
		slog.Info("webhook dispatcher: delivery history pruned",
			"deleted", n, "older_than", cutoff.UTC().Format(time.RFC3339))
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
	// Enrich with patient/vendor/device so the receiver can filter without a
	// callback.
	var ecg models.ECG
	if err := d.db.Select("patient_id", "vendor", "device_mac").Where("id = ?", ecgID).First(&ecg).Error; err == nil {
		data.PatientID = ecg.PatientID
		data.Vendor = ecg.Vendor
		data.DeviceMAC = ecg.DeviceMAC
		data.DeviceLabel = d.deviceLabel(ecg.DeviceMAC)
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
	// The machine the ECG came off. device_label is the operator's name for it
	// and the useful half — a receiver routing by ward wants "Cardio B, room
	// 214", not an address — but the MAC travels too, because a label can be
	// renamed and a rule keyed on it would silently stop matching.
	DeviceMAC   string `json:"device_mac,omitempty"`
	DeviceLabel string `json:"device_label,omitempty"`
	Filename    string `json:"filename,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// deviceLabel resolves the operator's name for a MAC. Empty when the device is
// unnamed, unknown, or was never identified — the payload then carries the
// address alone, which is still more than nothing.
func (d *Dispatcher) deviceLabel(mac string) string {
	if mac == "" {
		return ""
	}
	var label string
	if err := d.db.Model(&models.Device{}).
		Where("mac = ?", mac).
		Limit(1).
		Pluck("label", &label).Error; err != nil {
		slog.Warn("webhook: cannot resolve the device label", "mac", mac, "error", err)
		return ""
	}
	return label
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

// dispatch fans the event out to every enabled webhook whose event, vendor and
// device filters match. Each delivery runs in its own goroutine.
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
		// Device filter, same rule: it applies only when the event says which
		// machine sent the ECG. An event without one — a manual upload, or a
		// deployment that cannot identify hardware — is still delivered, for
		// the same reason the vendor filter works that way: dropping events a
		// filter cannot judge loses them silently.
		if p.Data.DeviceMAC != "" && !matches(jsonList(hook.Devices), p.Data.DeviceMAC) {
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
// records the final outcome on the webhook row and in the delivery log.
func (d *Dispatcher) deliverWithRetry(hook models.UserWebhook, p Payload) {
	p.WebhookID = hook.ID // stored in the log even if every attempt fails before Deliver sets it
	var status int
	var lastErr error
	attempts := 0
	for attempt, wait := range retryBackoff {
		if wait > 0 {
			select {
			case <-time.After(wait):
			case <-d.stop:
				return
			}
		}
		attempts = attempt + 1
		status, lastErr = d.Deliver(hook, p)
		if lastErr == nil {
			break
		}
		slog.Warn("webhook dispatcher: delivery failed",
			"webhook", hook.Name, "event", p.Event, "attempt", attempts, "error", lastErr)
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
	d.logDelivery(hook.ID, p, status, errMsg, attempts)
}

// logDelivery persists one delivery-history row (best-effort — a logging
// failure never affects delivery itself). No-op when history is disabled.
func (d *Dispatcher) logDelivery(webhookID string, p Payload, status int, errMsg string, attempts int) {
	if d.deliveryRepo == nil {
		return
	}
	body, err := json.Marshal(p)
	if err != nil {
		slog.Warn("webhook dispatcher: marshal delivery log payload", "webhook", webhookID, "error", err)
		return
	}
	entry := &models.WebhookDelivery{
		WebhookID:   webhookID,
		Event:       p.Event,
		Payload:     datatypes.JSON(body),
		StatusCode:  status,
		Error:       errMsg,
		Attempts:    attempts,
		DeliveredAt: time.Now(),
	}
	if err := d.deliveryRepo.Create(entry); err != nil {
		slog.Warn("webhook dispatcher: log delivery", "webhook", webhookID, "error", err)
	}
}

// Deliver sends one signed POST to hook and returns the HTTP status code.
// Exported so the "test webhook" handler can perform a synchronous delivery.
func (d *Dispatcher) Deliver(hook models.UserWebhook, p Payload) (int, error) {
	p.WebhookID = hook.ID
	if err := validateWebhookURL(hook.URL); err != nil {
		return 0, err
	}
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

// DeliverAndLog performs one synchronous delivery and records it exactly as an
// event-driven delivery would: the feedback columns on the webhook row *and* a
// delivery-history entry. The "test webhook" handlers use it so a test event is
// as visible — and as resendable — as any other delivery.
func (d *Dispatcher) DeliverAndLog(hook models.UserWebhook, p Payload) (int, error) {
	p.WebhookID = hook.ID // Deliver sets it on its own copy; the log needs it too
	status, err := d.Deliver(hook, p)
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	if recErr := d.repo.RecordDelivery(hook.ID, status, errMsg); recErr != nil {
		slog.Warn("webhook dispatcher: record delivery", "webhook", hook.ID, "error", recErr)
	}
	d.logDelivery(hook.ID, p, status, errMsg, 1)
	return status, err
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
