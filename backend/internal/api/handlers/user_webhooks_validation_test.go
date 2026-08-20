package handlers

import "testing"

// A vendor filter is an allow-list, so an unknown value silently stops every
// delivery. It must be rejected on write, exactly like an unknown event type.
func TestValidateWebhookRequest_Vendors(t *testing.T) {
	// What module.All() returns in production: every compiled-in vendor,
	// whether or not it is currently active.
	known := []string{"philips", "dicom", "mindray"}

	tests := []struct {
		name    string
		vendors []string
		known   []string
		want    string
	}{
		{"known vendors accepted", []string{"philips", "dicom"}, known, ""},
		{"empty filter accepted", nil, known, ""},
		{"unknown vendor rejected", []string{"philpis"}, known, "unknown vendor: philpis"},
		{"one unknown among known rejected", []string{"philips", "nope"}, known, "unknown vendor: nope"},
		// Nothing to validate against — accepting is safer than refusing every
		// vendor and locking the editor.
		{"empty allow-list skips the check", []string{"philpis"}, nil, ""},
	}

	for _, tt := range tests {
		req := &webhookRequest{Name: "hook", URL: "https://example.com/hook", Vendors: tt.vendors}
		if got := validateWebhookRequest(req, tt.known); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

// Event validation is unaffected, and a fully valid request stays valid.
func TestValidateWebhookRequest_EventsAndVendorsTogether(t *testing.T) {
	known := []string{"philips"}
	req := &webhookRequest{
		Name:    "hook",
		URL:     "https://example.com/hook",
		Events:  []string{"ecg.ingested"},
		Vendors: []string{"philips"},
	}
	if got := validateWebhookRequest(req, known); got != "" {
		t.Errorf("valid request rejected: %q", got)
	}

	req.Events = []string{"nope"}
	if got := validateWebhookRequest(req, known); got != "unknown event type: nope" {
		t.Errorf("event validation lost: got %q", got)
	}
}
