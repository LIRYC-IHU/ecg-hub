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

// A filter that matches nothing stops every delivery without an error, a failed
// delivery or a history entry — the silent failure the vendor allow-list exists
// to prevent. For a MAC the way in is the casing: a person reads it off a label
// in upper case, the payload carries it lower.
func TestValidateWebhookRequest_NormalisesTheDeviceFilter(t *testing.T) {
	req := &webhookRequest{
		Name:    "Cardio B receiver",
		URL:     "https://receiver.example.org/hook",
		Devices: []string{"00:0E:10:19:44:8A", "0:e:10:19:44:8b"},
	}
	if msg := validateWebhookRequest(req, nil); msg != "" {
		t.Fatalf("validate: %s", msg)
	}
	want := []string{"00:0e:10:19:44:8a", "00:0e:10:19:44:8b"}
	for i, w := range want {
		if req.Devices[i] != w {
			t.Errorf("Devices[%d] = %q, want %q", i, req.Devices[i], w)
		}
	}
}

func TestValidateWebhookRequest_RejectsANonAddress(t *testing.T) {
	req := &webhookRequest{
		Name:    "Cardio B receiver",
		URL:     "https://receiver.example.org/hook",
		Devices: []string{"cardio-b"},
	}
	if msg := validateWebhookRequest(req, nil); msg == "" {
		t.Error("a device filter that is not a MAC address must be rejected on write")
	}
}

// A machine can be given a webhook before it is ever plugged in, so an address
// absent from the inventory is not an error.
func TestValidateWebhookRequest_AcceptsAnUnenrolledAddress(t *testing.T) {
	req := &webhookRequest{
		Name:    "Cardio B receiver",
		URL:     "https://receiver.example.org/hook",
		Devices: []string{"aa:bb:cc:dd:ee:ff"},
	}
	if msg := validateWebhookRequest(req, nil); msg != "" {
		t.Errorf("validate = %q, want an unenrolled but well-formed address to pass", msg)
	}
}
