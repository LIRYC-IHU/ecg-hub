package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// TestMain enables loopback webhook delivery for the whole package test run:
// httptest servers bind to 127.0.0.1, which the SSRF guard blocks in production.
func TestMain(m *testing.M) {
	allowLoopbackWebhookForTest = true
	os.Exit(m.Run())
}

func TestMatches(t *testing.T) {
	tests := []struct {
		name   string
		filter []string
		value  string
		want   bool
	}{
		{"empty filter matches all", nil, "ecg.ingested", true},
		{"value in filter", []string{"ecg.ingested", "hl7.exhausted"}, "ecg.ingested", true},
		{"value not in filter", []string{"hl7.exhausted"}, "ecg.ingested", false},
	}
	for _, tt := range tests {
		if got := matches(tt.filter, tt.value); got != tt.want {
			t.Errorf("%s: matches(%v, %q) = %v, want %v", tt.name, tt.filter, tt.value, got, tt.want)
		}
	}
}

func TestJSONList(t *testing.T) {
	if got := jsonList([]byte(`["a","b"]`)); len(got) != 2 || got[0] != "a" {
		t.Errorf("jsonList: got %v", got)
	}
	if got := jsonList(nil); got != nil {
		t.Errorf("jsonList(nil): got %v, want nil", got)
	}
	if got := jsonList([]byte(`not-json`)); got != nil {
		t.Errorf("jsonList(invalid): got %v, want nil", got)
	}
}

// TestDeliver_SignatureAndHeaders verifies the receiver sees the documented
// headers, a valid HMAC signature and the payload links.
func TestDeliver_SignatureAndHeaders(t *testing.T) {
	const encKey = "test-enc-key"
	const secret = "hook-secret"

	var gotBody []byte
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	encSecret, err := auth.EncryptString(secret, encKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	encAuth, err := auth.EncryptString("Bearer tok123", encKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	d := NewDispatcher(nil, nil, encKey, "http://hub.local")
	hook := models.UserWebhook{
		ID:                  "hook-1",
		URL:                 srv.URL,
		SecretEncrypted:     encSecret,
		AuthHeaderEncrypted: encAuth,
	}
	payload := Payload{
		Event:     "ecg.ingested",
		Timestamp: "2026-06-11T00:00:00Z",
		Data:      PayloadData{ECGID: "e1", PatientID: "p1", Vendor: "mindray"},
	}
	payload.Links = d.buildLinks(payload.Data)

	status, err := d.Deliver(hook, payload)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}

	if got := gotHeaders.Get("X-ECG-Hub-Event"); got != "ecg.ingested" {
		t.Errorf("event header = %q", got)
	}
	if got := gotHeaders.Get("Authorization"); got != "Bearer tok123" {
		t.Errorf("authorization header = %q", got)
	}
	if got := gotHeaders.Get("X-ECG-Hub-Webhook-ID"); got != "hook-1" {
		t.Errorf("webhook id header = %q", got)
	}

	// Verify the HMAC signature over the exact delivered body.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	wantSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := gotHeaders.Get("X-ECG-Hub-Signature"); got != wantSig {
		t.Errorf("signature = %q, want %q", got, wantSig)
	}

	// Verify payload shape: webhook_id injected, links present.
	var decoded Payload
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if decoded.WebhookID != "hook-1" {
		t.Errorf("payload webhook_id = %q", decoded.WebhookID)
	}
	if decoded.Links["ecg_download"] != "http://hub.local/api/v1/ecgs/e1/download" {
		t.Errorf("ecg_download link = %q", decoded.Links["ecg_download"])
	}
	if decoded.Links["patient_ecgs"] != "http://hub.local/api/v1/patients/p1/ecgs" {
		t.Errorf("patient_ecgs link = %q", decoded.Links["patient_ecgs"])
	}
}

// TestDeliver_Non2xxIsError verifies non-2xx receiver responses are reported.
func TestDeliver_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	d := NewDispatcher(nil, nil, "k", "")
	status, err := d.Deliver(models.UserWebhook{URL: srv.URL}, Payload{Event: "test"})
	if err == nil {
		t.Fatal("want error for HTTP 502")
	}
	if status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", status)
	}
}

func TestMultiNotifier(t *testing.T) {
	calls := 0
	a := notifierFunc(func(string, string) error { calls++; return nil })
	b := notifierFunc(func(string, string) error { calls++; return nil })
	m := NewMultiNotifier(a, nil, b)
	if err := m.Notify("hl7_exhausted", "e1"); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (nil target skipped)", calls)
	}
}

type notifierFunc func(event, ecgID string) error

func (f notifierFunc) Notify(event, ecgID string) error { return f(event, ecgID) }

// TestWebhookDialControl_SSRF verifies the dial guard blocks loopback / metadata /
// unspecified / multicast while allowing public and RFC1918 internal targets.
func TestWebhookDialControl_SSRF(t *testing.T) {
	// Exercise the production policy (loopback blocked); restore for other tests.
	allowLoopbackWebhookForTest = false
	defer func() { allowLoopbackWebhookForTest = true }()

	blocked := []string{
		"127.0.0.1:80",        // loopback
		"[::1]:443",           // IPv6 loopback
		"169.254.169.254:80",  // cloud metadata (link-local)
		"0.0.0.0:80",          // unspecified
		"224.0.0.1:80",        // multicast
	}
	for _, addr := range blocked {
		if err := webhookDialControl("tcp", addr, nil); err == nil {
			t.Errorf("expected %s to be blocked by SSRF guard", addr)
		}
	}

	allowed := []string{
		"8.8.8.8:443",      // public
		"10.1.2.3:80",      // RFC1918 — legitimate internal research server
		"192.168.1.10:80",  // RFC1918
		"172.16.0.5:443",   // RFC1918
	}
	for _, addr := range allowed {
		if err := webhookDialControl("tcp", addr, nil); err != nil {
			t.Errorf("expected %s to be allowed, got %v", addr, err)
		}
	}
}

// TestValidateWebhookURL verifies only http/https URLs with a host are accepted.
func TestValidateWebhookURL(t *testing.T) {
	bad := []string{"", "ftp://x", "file:///etc/passwd", "http://", "://nope"}
	for _, u := range bad {
		if err := validateWebhookURL(u); err == nil {
			t.Errorf("expected %q to be rejected", u)
		}
	}
	good := []string{"http://example.com/hook", "https://10.0.0.1:9000/x"}
	for _, u := range good {
		if err := validateWebhookURL(u); err != nil {
			t.Errorf("expected %q to be accepted, got %v", u, err)
		}
	}
}
