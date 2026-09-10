package device

import (
	"context"
	"testing"
	"time"
)

type fakeDescriber struct {
	calls []string // "mac|vendor|model|serial"
	err   error
}

func (f *fakeDescriber) Describe(_ context.Context, mac, vendor, model, serial string) error {
	f.calls = append(f.calls, mac+"|"+vendor+"|"+model+"|"+serial)
	return f.err
}

var pairID = Identity{MAC: "00:0e:10:19:44:8a", IP: "10.27.26.40", Source: "ftp"}

func TestPairingHoldAndTake(t *testing.T) {
	d := &fakeDescriber{}
	p := NewPairing(d)

	if err := p.Hold(context.Background(), pairID, "ecg.xml", []byte("data"), "philips", "PageWriter", "SN1"); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if len(d.calls) != 1 || d.calls[0] != pairID.MAC+"|philips|PageWriter|SN1" {
		t.Errorf("describer saw %v", d.calls)
	}
	if p.Len() != 1 {
		t.Errorf("Len = %d, want 1", p.Len())
	}

	h, ok := p.Take(pairID.MAC)
	if !ok || string(h.Data) != "data" || h.Filename != "ecg.xml" {
		t.Errorf("Take = (%+v, %v), want the held file", h, ok)
	}
	if p.Len() != 0 {
		t.Error("Take must remove the held file")
	}
	if _, ok := p.Take(pairID.MAC); ok {
		t.Error("a second Take must find nothing")
	}
}

func TestPairingRequiresAMAC(t *testing.T) {
	p := NewPairing(nil)
	if err := p.Hold(context.Background(), Identity{IP: "10.0.0.4"}, "f.xml", []byte("x"), "", "", ""); err == nil {
		t.Error("holding a file for a device with no MAC must fail — there is nothing to key it on")
	}
}

// A failing describer must not lose the file: the description is a convenience
// for whoever approves, the file is the thing that must survive.
func TestPairingHoldsDespiteDescriberError(t *testing.T) {
	p := NewPairing(&fakeDescriber{err: context.DeadlineExceeded})
	if err := p.Hold(context.Background(), pairID, "f.xml", []byte("x"), "philips", "", ""); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if _, ok := p.Take(pairID.MAC); !ok {
		t.Error("the file must be held even when the description could not be recorded")
	}
}

func TestPairingReplacesTheSameDevice(t *testing.T) {
	p := NewPairing(nil)
	ctx := context.Background()
	_ = p.Hold(ctx, pairID, "first.xml", []byte("first"), "", "", "")
	_ = p.Hold(ctx, pairID, "second.xml", []byte("second"), "", "", "")
	if p.Len() != 1 {
		t.Errorf("Len = %d, want 1 — a device holds one file", p.Len())
	}
	h, _ := p.Take(pairID.MAC)
	if string(h.Data) != "second" {
		t.Errorf("held %q, want the newest attempt", h.Data)
	}
}

func TestPairingBoundsWhatItHolds(t *testing.T) {
	p := NewPairing(nil)
	p.maxDevices = 2
	ctx := context.Background()
	macs := []string{"00:00:00:00:00:01", "00:00:00:00:00:02", "00:00:00:00:00:03"}
	for i, mac := range macs {
		err := p.Hold(ctx, Identity{MAC: mac, Source: "ftp"}, "f.xml", []byte("x"), "", "", "")
		if i < 2 && err != nil {
			t.Fatalf("Hold %d: %v", i, err)
		}
		if i == 2 && err == nil {
			t.Error("holding past maxDevices must fail rather than grow without bound")
		}
	}
	if p.Len() != 2 {
		t.Errorf("Len = %d, want 2", p.Len())
	}
}

func TestPairingExpires(t *testing.T) {
	now := time.Now()
	p := NewPairing(nil)
	p.now = func() time.Time { return now }

	if err := p.Hold(context.Background(), pairID, "f.xml", []byte("x"), "", "", ""); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	now = now.Add(DefaultPairingHold + time.Minute)
	if _, ok := p.Take(pairID.MAC); ok {
		t.Error("a file nobody acted on within the TTL must not still be held")
	}
}
