package connector_test

import (
	"context"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ─── Stub ────────────────────────────────────────────────────────────────────

type stubConnector struct{ name string }

func (s *stubConnector) Name() string                                              { return s.name }
func (s *stubConnector) Accepts(_ *models.ECG) bool                                { return true }
func (s *stubConnector) Forward(_ context.Context, _ *models.ECG, _ string) error { return nil }
func (s *stubConnector) Health() error                                             { return nil }

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestRegister_andGet(t *testing.T) {
	c := &stubConnector{name: "test-reg-get"}
	connector.Register(c)

	got, ok := connector.Get("test-reg-get")
	if !ok {
		t.Fatal("Get: connector not found after Register")
	}
	if got.Name() != "test-reg-get" {
		t.Fatalf("Get: expected name %q, got %q", "test-reg-get", got.Name())
	}
}

func TestRegister_duplicatePanics(t *testing.T) {
	c := &stubConnector{name: "test-dup"}
	connector.Register(c)

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate Register, got none")
		}
	}()
	connector.Register(&stubConnector{name: "test-dup"}) // must panic
}

func TestGet_unknownReturnsFalse(t *testing.T) {
	_, ok := connector.Get("definitely-not-registered-xyz")
	if ok {
		t.Error("Get: expected false for unknown connector")
	}
}

func TestActive_byNames(t *testing.T) {
	a := &stubConnector{name: "active-a"}
	b := &stubConnector{name: "active-b"}
	connector.Register(a)
	connector.Register(b)

	got := connector.Active([]string{"active-b", "active-a"})
	if len(got) != 2 {
		t.Fatalf("Active: expected 2 connectors, got %d", len(got))
	}
	// Order must follow the names slice.
	if got[0].Name() != "active-b" {
		t.Errorf("Active: expected first %q, got %q", "active-b", got[0].Name())
	}
	if got[1].Name() != "active-a" {
		t.Errorf("Active: expected second %q, got %q", "active-a", got[1].Name())
	}
}

func TestActive_unknownNameSkipped(t *testing.T) {
	connector.Register(&stubConnector{name: "active-known"})

	got := connector.Active([]string{"active-known", "no-such-connector"})
	if len(got) != 1 {
		t.Fatalf("Active: expected 1 connector, got %d", len(got))
	}
	if got[0].Name() != "active-known" {
		t.Errorf("Active: unexpected name %q", got[0].Name())
	}
}

func TestActive_emptyNamesReturnsAll(t *testing.T) {
	// At least one connector is registered from prior tests.
	got := connector.Active(nil)
	if len(got) == 0 {
		t.Error("Active(nil): expected at least one connector from registry")
	}
}

func TestStatusConstants(t *testing.T) {
	// Smoke-test: ensure constants have their documented values.
	cases := map[string]string{
		"pending":   connector.StatusPending,
		"sent":      connector.StatusSent,
		"failed":    connector.StatusFailed,
		"exhausted": connector.StatusExhausted,
	}
	for want, got := range cases {
		if got != want {
			t.Errorf("status constant: expected %q, got %q", want, got)
		}
	}
}
