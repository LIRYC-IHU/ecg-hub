package handlers

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/device"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
)

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
