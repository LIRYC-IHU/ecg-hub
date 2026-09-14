package handlers

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/device"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
)

// stubSettings stands in for the module-settings repository.
type stubSettings struct {
	set              device.Settings
	enabled          bool
	pairingOpen      bool
	denyUnidentified bool
	until            time.Time
}

func (s *stubSettings) DeviceSettings(context.Context) (device.Settings, error) {
	return s.set, nil
}

func (s *stubSettings) SetDeviceSettings(enabled, pairingOpen, denyUnidentified bool, pairingUntil time.Time) error {
	s.enabled, s.pairingOpen, s.denyUnidentified, s.until = enabled, pairingOpen, denyUnidentified, pairingUntil
	return nil
}

// stubPairing stands in for device.Pairing.
type stubPairing struct {
	held    map[string]device.Held
	dropped []string
}

func (s *stubPairing) Has(mac string) bool { _, ok := s.held[mac]; return ok }

func (s *stubPairing) Take(mac string) (device.Held, bool) {
	h, ok := s.held[mac]
	delete(s.held, mac)
	return h, ok
}

func (s *stubPairing) Drop(mac string) { s.dropped = append(s.dropped, mac) }

func codeOf(t *testing.T, err error) connect.Code {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	return connect.CodeOf(err)
}

// A MAC is the key everything else hangs off, so a request that does not carry
// one must be refused before any repository is touched — the handler here has
// no repository at all, which is what proves it.
func TestDeviceService_MutationsRejectAMalformedMAC(t *testing.T) {
	h := &DeviceServiceHandler{}
	ctx := context.Background()

	if _, err := h.ApproveDevice(ctx, &apiv1.ApproveDeviceRequest{Mac: "not-a-mac"}); codeOf(t, err) != connect.CodeInvalidArgument {
		t.Errorf("ApproveDevice code = %v, want InvalidArgument", codeOf(t, err))
	}
	if _, err := h.RevokeDevice(ctx, &apiv1.RevokeDeviceRequest{Mac: ""}); codeOf(t, err) != connect.CodeInvalidArgument {
		t.Errorf("RevokeDevice code = %v, want InvalidArgument", codeOf(t, err))
	}
	if _, err := h.DeleteDevice(ctx, &apiv1.DeleteDeviceRequest{Mac: "aa:bb"}); codeOf(t, err) != connect.CodeInvalidArgument {
		t.Errorf("DeleteDevice code = %v, want InvalidArgument", codeOf(t, err))
	}
}

func TestDeviceService_UpdateSettingsRejectsABadTimestamp(t *testing.T) {
	h := &DeviceServiceHandler{}
	_, err := h.UpdateSettings(context.Background(), &apiv1.UpdateDeviceSettingsRequest{
		Settings: &apiv1.DeviceSettings{Enabled: true, PairingOpen: true, PairingUntil: "tomorrow"},
	})
	if codeOf(t, err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", codeOf(t, err))
	}
}

func TestDeviceService_UpdateSettingsRequiresABody(t *testing.T) {
	h := &DeviceServiceHandler{}
	if _, err := h.UpdateSettings(context.Background(), &apiv1.UpdateDeviceSettingsRequest{}); codeOf(t, err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", codeOf(t, err))
	}
}

// Approving a device re-ingests the file it sent to identify itself, so the
// ward does not have to acquire a second ECG.
func TestDeviceService_ReingestHeldPushesTheFileBack(t *testing.T) {
	mac := "00:0e:10:19:44:8a"
	queue := ingestion.NewIngestQueue(1)
	h := &DeviceServiceHandler{
		Queue: queue,
		Pairing: &stubPairing{held: map[string]device.Held{
			mac: {
				Identity: device.Identity{MAC: mac, Source: "ftp"},
				Filename: "ecg.xml",
				Data:     []byte("content"),
			},
		}},
	}

	if !h.reingestHeld(mac) {
		t.Fatal("reingestHeld = false, want the held file to be queued")
	}
	item := <-queue
	if item.Filename != "ecg.xml" || string(item.Data) != "content" {
		t.Errorf("queued %+v, want the held file", item)
	}
	if item.Pairing {
		t.Error("the re-ingested file must be a normal item, not another pairing round")
	}
	if item.DeviceMAC != mac {
		t.Errorf("DeviceMAC = %q, want %q", item.DeviceMAC, mac)
	}
	if h.reingestHeld(mac) {
		t.Error("the file must be consumed — a second approval has nothing to re-ingest")
	}
}

// A full queue must not fail the approval: the device is enrolled either way.
func TestDeviceService_ReingestSurvivesAFullQueue(t *testing.T) {
	mac := "00:0e:10:19:44:8a"
	h := &DeviceServiceHandler{
		Queue:   ingestion.NewIngestQueue(0),
		Pairing: &stubPairing{held: map[string]device.Held{mac: {Filename: "ecg.xml"}}},
	}
	if h.reingestHeld(mac) {
		t.Error("reingestHeld = true, want false when the queue cannot take the file")
	}
}

func TestDeviceService_ReingestWithNothingHeld(t *testing.T) {
	h := &DeviceServiceHandler{Queue: ingestion.NewIngestQueue(1), Pairing: &stubPairing{held: map[string]device.Held{}}}
	if h.reingestHeld("00:0e:10:19:44:8a") {
		t.Error("reingestHeld = true, want false when no file is held")
	}
}

func TestDeviceService_SubscribeWithoutAHubIsUnimplemented(t *testing.T) {
	h := &DeviceServiceHandler{}
	err := h.SubscribeDevices(context.Background(), &apiv1.SubscribeDevicesRequest{}, nil)
	if codeOf(t, err) != connect.CodeUnimplemented {
		t.Errorf("code = %v, want Unimplemented", codeOf(t, err))
	}
}

func TestBoundedText(t *testing.T) {
	if got, err := boundedText("  spaced  ", 10, "label"); err != nil || got != "spaced" {
		t.Errorf("boundedText = (%q, %v), want trimmed", got, err)
	}
	if _, err := boundedText("abcdef", 3, "label"); codeOf(t, err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument for an over-long value", codeOf(t, err))
	}
}

// Opening the pairing window without an expiry must not open it forever: a
// forgotten window fills the pending queue with noise, which is where a real
// device goes unnoticed.
func TestDeviceService_PairingWindowGetsADefaultExpiry(t *testing.T) {
	repo := &stubSettings{}
	h := &DeviceServiceHandler{Settings: repo, WhitelistAvailable: true}

	before := time.Now()
	_, err := h.UpdateSettings(context.Background(), &apiv1.UpdateDeviceSettingsRequest{
		Settings: &apiv1.DeviceSettings{Enabled: true, PairingOpen: true},
	})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if repo.until.IsZero() {
		t.Fatal("pairing was opened with no expiry at all")
	}
	want := before.Add(device.DefaultPairingWindow)
	if repo.until.Before(want) || repo.until.After(want.Add(time.Minute)) {
		t.Errorf("expiry = %v, want about %v", repo.until, want)
	}
}

// An explicit expiry is the caller's, including one far enough out to mean
// "leave it open" for someone who says so deliberately.
func TestDeviceService_ExplicitPairingExpiryIsKept(t *testing.T) {
	repo := &stubSettings{}
	h := &DeviceServiceHandler{Settings: repo, WhitelistAvailable: true}
	chosen := time.Now().Add(4 * time.Hour).UTC().Truncate(time.Second)

	if _, err := h.UpdateSettings(context.Background(), &apiv1.UpdateDeviceSettingsRequest{
		Settings: &apiv1.DeviceSettings{
			Enabled: true, PairingOpen: true, PairingUntil: chosen.Format(time.RFC3339),
		},
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if !repo.until.Equal(chosen) {
		t.Errorf("expiry = %v, want the one asked for %v", repo.until, chosen)
	}
}

// Closing the window must not acquire an expiry it does not need.
func TestDeviceService_ClosingPairingKeepsNoExpiry(t *testing.T) {
	repo := &stubSettings{}
	h := &DeviceServiceHandler{Settings: repo, WhitelistAvailable: true}
	if _, err := h.UpdateSettings(context.Background(), &apiv1.UpdateDeviceSettingsRequest{
		Settings: &apiv1.DeviceSettings{Enabled: true, PairingOpen: false},
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if !repo.until.IsZero() {
		t.Errorf("expiry = %v, want none when the window is closed", repo.until)
	}
}

// A MAC typed off a label arrives in upper case; stored that way it would never
// match the lower-case address the resolver hands the gate — an enrolment that
// silently enrols nothing.
func TestDeviceService_AddDeviceRejectsANonAddress(t *testing.T) {
	h := &DeviceServiceHandler{}
	for _, bad := range []string{"", "cardio-b", "00:0e:10:19:44"} {
		if _, err := h.AddDevice(context.Background(), &apiv1.AddDeviceRequest{Mac: bad}); codeOf(t, err) != connect.CodeInvalidArgument {
			t.Errorf("AddDevice(%q) code = %v, want InvalidArgument", bad, codeOf(t, err))
		}
	}
}

func TestDeviceService_UpdateSettingsCarriesTheDenyPolicy(t *testing.T) {
	repo := &stubSettings{}
	h := &DeviceServiceHandler{Settings: repo, WhitelistAvailable: true}
	if _, err := h.UpdateSettings(context.Background(), &apiv1.UpdateDeviceSettingsRequest{
		Settings: &apiv1.DeviceSettings{Enabled: true, DenyUnidentified: true},
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if !repo.denyUnidentified {
		t.Error("deny_unidentified was not stored")
	}
}

// A deployment that cannot run the whitelist must not let anyone switch it on.
//
// Storing the flag would be worse than refusing it: the screen would report a
// control that filters connections while no gate is registered to filter any,
// which is the exact confusion the availability flag exists to remove.
func TestDeviceService_UpdateSettingsRefusedWhenTheWhitelistIsUnavailable(t *testing.T) {
	h := &DeviceServiceHandler{WhitelistAvailable: false}

	_, err := h.UpdateSettings(context.Background(), &apiv1.UpdateDeviceSettingsRequest{
		Settings: &apiv1.DeviceSettings{Enabled: true},
	})
	if got := codeOf(t, err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", got)
	}
}

// GetSettings stays readable either way — the screen needs the answer in order
// to explain itself, so refusing the read would leave it with nothing to say.
func TestDeviceService_GetSettingsReportsAvailability(t *testing.T) {
	for _, available := range []bool{true, false} {
		h := &DeviceServiceHandler{
			WhitelistAvailable: available,
			Settings:           &stubSettings{},
		}
		resp, err := h.GetSettings(context.Background(), &apiv1.GetDeviceSettingsRequest{})
		if err != nil {
			t.Fatalf("GetSettings(available=%v): %v", available, err)
		}
		if resp.Available != available {
			t.Errorf("Available = %v, want %v", resp.Available, available)
		}
	}
}
