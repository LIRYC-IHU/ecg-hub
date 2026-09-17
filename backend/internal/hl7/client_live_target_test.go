package hl7

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The bug this guards against: connection settings were frozen at startup. The
// settings screen's test button built a throwaway client from the database and
// reached the new host, so saving looked like it worked — while the enricher,
// the retry scheduler and the ORU sender all kept the address they were
// constructed with until the process restarted.

func TestClient_LiveTargetOverridesTheBootAddress(t *testing.T) {
	newPort, stopNew := startMockHIS(t, validADRResponse)
	defer stopNew()

	// The boot target points at a port nothing listens on: if the client ever
	// falls back to it while the resolver has an answer, the query fails and
	// this test fails with it.
	c := NewClient("127.0.0.1", 1, time.Second, MSHConfig{}).
		WithLiveTarget(func() (Target, bool) {
			return Target{Host: "127.0.0.1", Port: newPort, Timeout: 2 * time.Second}, true
		})

	d, err := c.QueryPatient(context.TODO(), "P001")
	if err != nil {
		t.Fatalf("query did not reach the resolved host: %v", err)
	}
	if d.LastName != "Milhas" {
		t.Errorf("LastName = %q, want %q", d.LastName, "Milhas")
	}
}

func TestClient_LiveTargetFallsBackWhenTheResolverCannotAnswer(t *testing.T) {
	bootPort, stop := startMockHIS(t, validADRResponse)
	defer stop()

	// A database hiccup must not stop enrichment that worked a second earlier:
	// the last known good settings keep being used.
	c := NewClient("127.0.0.1", bootPort, 2*time.Second, MSHConfig{}).
		WithLiveTarget(func() (Target, bool) { return Target{}, false })

	if _, err := c.QueryPatient(context.TODO(), "P001"); err != nil {
		t.Fatalf("query did not fall back to the boot target: %v", err)
	}
}

func TestClient_LiveTargetIgnoresAnIncompleteAnswer(t *testing.T) {
	bootPort, stop := startMockHIS(t, validADRResponse)
	defer stop()

	// HL7 disabled mid-flight, or a half-filled settings row: a target with no
	// port is not something to dial, so the boot settings stand.
	c := NewClient("127.0.0.1", bootPort, 2*time.Second, MSHConfig{}).
		WithLiveTarget(func() (Target, bool) { return Target{Host: "somewhere"}, true })

	if _, err := c.QueryPatient(context.TODO(), "P001"); err != nil {
		t.Fatalf("query did not ignore the incomplete target: %v", err)
	}
}

func TestClient_ProvenanceRecordsTheHostActuallyQueried(t *testing.T) {
	// hl7_source is written from this value, and the PDF renderer treats a
	// non-empty hl7_source as "the HIS confirmed this identity". Recording the
	// boot host while querying another one would file the answer under a
	// hospital that never gave it.
	port, stop := startMockHIS(t, validADRResponse)
	defer stop()

	c := NewClient("stale-host", 1, time.Second, MSHConfig{}).
		WithLiveTarget(func() (Target, bool) {
			return Target{Host: "127.0.0.1", Port: port, Timeout: 2 * time.Second}, true
		})

	d, err := c.QueryPatient(context.TODO(), "P001")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if d.Source != "127.0.0.1" {
		t.Errorf("Source = %q, want the host actually queried (%q)", d.Source, "127.0.0.1")
	}
}

func TestClient_MSHComesFromTheResolvedTarget(t *testing.T) {
	// The facility fields live in the same settings row as the host, so a
	// renamed facility has to travel with a changed address.
	c := NewClient("127.0.0.1", 1, time.Second, MSHConfig{SendingFacility: "OLD_SITE"}).
		WithLiveTarget(func() (Target, bool) {
			return Target{
				Host: "127.0.0.1", Port: 1, Timeout: time.Second,
				MSH: MSHConfig{SendingFacility: "NEW_SITE", Version: "2.5", ProcessingID: "P"},
			}, true
		})

	msg := c.buildQRYMessage("P001", c.target().MSH)
	if !strings.Contains(msg, "NEW_SITE") {
		t.Errorf("MSH kept the boot facility:\n%s", msg)
	}
	if strings.Contains(msg, "OLD_SITE") {
		t.Errorf("MSH still carries the boot facility:\n%s", msg)
	}
}

func TestClient_MSHDefaultsApplyToAResolvedTarget(t *testing.T) {
	// NewClient fills Version and ProcessingID when an operator leaves them
	// blank; a target arriving from the database gets the same treatment.
	c := NewClient("127.0.0.1", 1, time.Second, MSHConfig{}).
		WithLiveTarget(func() (Target, bool) {
			return Target{Host: "127.0.0.1", Port: 2, Timeout: time.Second}, true
		})

	got := c.target()
	if got.MSH.Version != "2.5" {
		t.Errorf("Version = %q, want %q", got.MSH.Version, "2.5")
	}
	if got.MSH.ProcessingID != "P" {
		t.Errorf("ProcessingID = %q, want %q", got.MSH.ProcessingID, "P")
	}
}

func TestClient_WithoutAResolverKeepsBootSettings(t *testing.T) {
	// The constructor's behaviour is unchanged for every caller that does not
	// opt in — the test-connection button builds a throwaway client this way.
	c := NewClient("boot-host", 5000, 3*time.Second, MSHConfig{SendingFacility: "SITE"})
	got := c.target()
	if got.Host != "boot-host" || got.Port != 5000 || got.Timeout != 3*time.Second {
		t.Errorf("target() = %+v, want the boot settings", got)
	}
}
