package hl7

import (
	"errors"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// RAD-12 §4.12.4.3.2 draws the line this file is about: a field sent as two
// double quotes shall be removed from the receiving system, while a field simply
// omitted leaves the stored value alone. Both read as an empty string, so
// getting it wrong means an A08 quietly erases whatever the sender had no reason
// to repeat.

var adtMappings = []models.HL7Mapping{
	{SourcePath: "PID.3", TargetField: "patient_id"},
	{SourcePath: "PID.5.1", TargetField: "last_name"},
	{SourcePath: "PID.5.2", TargetField: "first_name"},
	{SourcePath: "PID.7", TargetField: "date_of_birth"},
	{SourcePath: "PID.8", TargetField: "gender"},
	{SourcePath: "PID.18", TargetField: "nda"},
}

func inbound(pid string) *InboundMessage {
	raw := "MSH|^~\\&|TESTAPP|TESTFACILITY|ECG-HUB|LIRYC|20260925093600+0200||ADT^A08^ADT_A01|MSG1|P|2.5.1\r" +
		"EVN|A08|20260925093600+0200\r" + pid + "\rPV1|1|O\r"
	return &InboundMessage{Raw: raw, TriggerEvent: "A08", SendingFacility: "TESTFACILITY", ControlID: "MSG1"}
}

func TestBuildPatientUpdate_ReadsTheMessage(t *testing.T) {
	u := BuildPatientUpdate(inbound(
		"PID|1||BS1215^^^TESTFACILITY^MR||DUPONT^JEAN^^^^L||19850115|M|||12 RUE DE TEST^^BORDEAUX^^33000^FR||0556000000|||S||NDA-42"), adtMappings)

	// PID-3 is a CX; the identifier is its first component whatever else rides
	// along — an assigning authority, a type code.
	if u.PatientID != "BS1215" {
		t.Errorf("PatientID = %q, want BS1215", u.PatientID)
	}
	if !u.LastName.Set() || u.LastName.Value != "DUPONT" {
		t.Errorf("LastName = %+v", u.LastName)
	}
	if !u.FirstName.Set() || u.FirstName.Value != "JEAN" {
		t.Errorf("FirstName = %+v", u.FirstName)
	}
	if !u.DateOfBirth.Set() || u.DateOfBirth.Value != "19850115" {
		t.Errorf("DateOfBirth = %+v", u.DateOfBirth)
	}
	if !u.NDA.Set() || u.NDA.Value != "NDA-42" {
		t.Errorf("NDA = %+v", u.NDA)
	}
	// EVN-2 is preferred over MSH-7: it says when the event was recorded, where
	// MSH-7 only says when this copy of the message was built.
	want := time.Date(2026, 9, 25, 7, 36, 0, 0, time.UTC)
	if !u.EventAt.Equal(want) {
		t.Errorf("EventAt = %s, want %s", u.EventAt.UTC(), want)
	}
}

func TestBuildPatientUpdate_OmittedIsNotCleared(t *testing.T) {
	// The message stops after the name: everything past it was never sent, and
	// must not be taken as an instruction to erase.
	u := BuildPatientUpdate(inbound("PID|1||BS1215||DUPONT^JEAN"), adtMappings)

	if !u.LastName.Set() {
		t.Error("the name that was sent was not read")
	}
	for name, f := range map[string]Field{
		"DateOfBirth": u.DateOfBirth, "Gender": u.Gender, "NDA": u.NDA,
	} {
		if f.Present {
			t.Errorf("%s was reported as present though the message omitted it: %+v", name, f)
		}
		if f.Clear {
			t.Errorf("%s would erase a stored value the message never mentioned", name)
		}
	}
}

func TestBuildPatientUpdate_ExplicitNullClears(t *testing.T) {
	// Two double quotes: RAD-12 says remove it.
	u := BuildPatientUpdate(inbound(`PID|1||BS1215||DUPONT^JEAN||""|M`), adtMappings)

	if !u.DateOfBirth.Present || !u.DateOfBirth.Clear {
		t.Errorf("an explicit null did not ask for the value to be cleared: %+v", u.DateOfBirth)
	}
	if u.DateOfBirth.Set() {
		t.Error("a cleared field must not also be a value to store")
	}
	if !u.Gender.Set() || u.Gender.Value != "M" {
		t.Errorf("a field after the null was not read: %+v", u.Gender)
	}
}

func TestBuildPatientUpdate_EmptyWhenNothingChanges(t *testing.T) {
	u := BuildPatientUpdate(inbound("PID|1||BS1215"), adtMappings)
	if !u.Empty() {
		t.Errorf("a message carrying only an identifier asked for a change: %+v", u)
	}
}

// ─── the handler ─────────────────────────────────────────────────────────────

type applierStub struct {
	got   *PatientUpdate
	found bool
	err   error
}

func (a *applierStub) ApplyPatientUpdate(u *PatientUpdate) (bool, error) {
	a.got = u
	return a.found, a.err
}

func handle(t *testing.T, msg *InboundMessage, stub *applierStub, maps []models.HL7Mapping) (string, string) {
	t.Helper()
	h := NewPatientUpdateHandler(stub, func() ([]models.HL7Mapping, error) { return maps, nil })
	return h(msg)
}

func TestPatientUpdateHandler_AppliesAnA08(t *testing.T) {
	stub := &applierStub{found: true}
	code, _ := handle(t, inbound("PID|1||BS1215||DUPONT^JEAN||19850115|M"), stub, adtMappings)

	if code != ACKAccepted {
		t.Errorf("ack = %q, want AA", code)
	}
	if stub.got == nil || stub.got.PatientID != "BS1215" {
		t.Fatalf("the update did not reach the repository: %+v", stub.got)
	}
}

func TestPatientUpdateHandler_UnknownPatientIsAccepted(t *testing.T) {
	// Patients are created when an ECG arrives, not by an ADT feed. The message
	// is valid and simply does not concern us, so a rejection would only make
	// the sender retry something that will never apply.
	stub := &applierStub{found: false}
	code, _ := handle(t, inbound("PID|1||GHOST||DUPONT^JEAN"), stub, adtMappings)

	if code != ACKAccepted {
		t.Errorf("ack = %q, want AA for a patient we do not hold", code)
	}
}

func TestPatientUpdateHandler_OtherTriggersAreLeftAlone(t *testing.T) {
	// A40 is a merge and has its own path; A02/A03/A06/A07 carry visit
	// information stored nowhere here. None is an error.
	for _, trigger := range []string{"A40", "A02", "A03", "A06", "A07", "A99"} {
		stub := &applierStub{found: true}
		msg := inbound("PID|1||BS1215||DUPONT^JEAN")
		msg.TriggerEvent = trigger

		code, _ := handle(t, msg, stub, adtMappings)
		if code != ACKAccepted {
			t.Errorf("%s: ack = %q, want AA", trigger, code)
		}
		if stub.got != nil {
			t.Errorf("%s reached the patient update path", trigger)
		}
	}
}

func TestPatientUpdateHandler_RefusesWithoutMappings(t *testing.T) {
	// Nothing can be read out of the message, and an empty update applied
	// wholesale would blank the record.
	stub := &applierStub{found: true}
	code, text := handle(t, inbound("PID|1||BS1215||DUPONT^JEAN"), stub, nil)

	if code != ACKError {
		t.Errorf("ack = %q, want AE", code)
	}
	if text == "" {
		t.Error("the reason was not sent back")
	}
	if stub.got != nil {
		t.Error("an update was applied with no mappings to read it with")
	}
}

func TestPatientUpdateHandler_RefusesWithoutAnIdentifier(t *testing.T) {
	stub := &applierStub{found: true}
	code, _ := handle(t, inbound("PID|1||||DUPONT^JEAN"), stub, adtMappings)

	if code != ACKError {
		t.Errorf("ack = %q, want AE", code)
	}
	if stub.got != nil {
		t.Error("an update was applied without knowing who it was for")
	}
}

func TestPatientUpdateHandler_ReportsAFailedUpdate(t *testing.T) {
	// The sender is entitled to know it did not take, so it can retry.
	stub := &applierStub{found: true, err: errors.New("connection lost")}
	code, _ := handle(t, inbound("PID|1||BS1215||DUPONT^JEAN"), stub, adtMappings)

	if code != ACKError {
		t.Errorf("ack = %q, want AE", code)
	}
}

func TestParseHL7Time(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Time
		ok   bool
	}{
		{"20260925093600+0200", time.Date(2026, 9, 25, 7, 36, 0, 0, time.UTC), true},
		{"20260925093600", time.Date(2026, 9, 25, 9, 36, 0, 0, time.UTC), true},
		{"202609250936", time.Date(2026, 9, 25, 9, 36, 0, 0, time.UTC), true},
		{"20260925", time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), true},
		{"", time.Time{}, false},
		{"not a time", time.Time{}, false},
	}
	for _, tc := range tests {
		got, ok := parseHL7Time(tc.raw)
		if ok != tc.ok {
			t.Errorf("parseHL7Time(%q) ok = %v, want %v", tc.raw, ok, tc.ok)
			continue
		}
		if ok && !got.Equal(tc.want) {
			t.Errorf("parseHL7Time(%q) = %s, want %s", tc.raw, got.UTC(), tc.want)
		}
	}
}
