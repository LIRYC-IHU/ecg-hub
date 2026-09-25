package hl7

import (
	"errors"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// RAD-12 §4.12.4.4: "The PID segment contains the dominant patient information
// [...] The MRG segment identifies the 'old' or secondary patient records to be
// de-referenced."
//
// The direction is the whole of it. Read backwards, a merge files the surviving
// patient's traces under an identifier the HIS has just retired.

func mergeMsg(pid, mrg string) *InboundMessage {
	raw := "MSH|^~\\&|TESTAPP|TESTFACILITY|ECG-HUB|LIRYC|20260925093600+0200||ADT^A40^ADT_A39|MRG1|P|2.5.1\r" +
		"EVN|A40|20260925093600+0200\r" + pid + "\r" + mrg + "\rPV1|1|O\r"
	return &InboundMessage{Raw: raw, TriggerEvent: "A40", SendingFacility: "TESTFACILITY", ControlID: "MRG1"}
}

type mergerStub struct {
	calls [][2]string
	moved int64
	err   error
}

func (m *mergerStub) RekeyPatient(oldID, newID string) (int64, error) {
	m.calls = append(m.calls, [2]string{oldID, newID})
	return m.moved, m.err
}

// applierNoop satisfies the A08 half of the handler without being exercised.
type applierNoop struct{}

func (applierNoop) ApplyPatientUpdate(*PatientUpdate) (bool, error) { return true, nil }

func mergeHandle(t *testing.T, msg *InboundMessage, m PatientMerger) (string, string) {
	t.Helper()
	h := NewADTHandler(applierNoop{}, m, func() ([]models.HL7Mapping, error) {
		return adtMappings, nil
	})
	r := h(msg)
	return r.ack(), r.Text
}

func TestBuildPatientMerge_ReadsBothIdentifiers(t *testing.T) {
	m := BuildPatientMerge(mergeMsg(
		"PID|1||BS1215^^^TESTFACILITY^MR||DUPONT^JEAN",
		"MRG|MRN-BS1215^^^TESTFACILITY^MR|||||DUPONT^JEAN"), adtMappings)

	if m.SurvivingID != "BS1215" {
		t.Errorf("SurvivingID = %q, want the PID-3 identifier", m.SurvivingID)
	}
	if m.PriorID != "MRN-BS1215" {
		t.Errorf("PriorID = %q, want the MRG-1 identifier", m.PriorID)
	}
}

func TestBuildPatientMerge_MRG1IsADefaultNotARequiredMapping(t *testing.T) {
	// MRG-1 is fixed by the standard, so a site should not have to declare it —
	// the same treatment MSA.1 gets on the query path.
	m := BuildPatientMerge(mergeMsg("PID|1||BS1215", "MRG|OLD-1"), []models.HL7Mapping{
		{SourcePath: "PID.3", TargetField: "patient_id"},
	})
	if m.PriorID != "OLD-1" {
		t.Errorf("PriorID = %q with no prior_patient_id mapping, want OLD-1", m.PriorID)
	}
}

func TestBuildPatientMerge_AConfiguredPathWins(t *testing.T) {
	m := BuildPatientMerge(mergeMsg("PID|1||BS1215", "MRG|IGNORED||ELSEWHERE"), append(adtMappings,
		models.HL7Mapping{SourcePath: "MRG.3", TargetField: "prior_patient_id"}))
	if m.PriorID != "ELSEWHERE" {
		t.Errorf("PriorID = %q, want the configured path to be used", m.PriorID)
	}
}

func TestMergeHandler_MergesInTheRightDirection(t *testing.T) {
	stub := &mergerStub{moved: 4}
	code, _ := mergeHandle(t, mergeMsg(
		"PID|1||BS1215^^^TESTFACILITY^MR||DUPONT^JEAN",
		"MRG|MRN-BS1215^^^TESTFACILITY^MR"), stub)

	if code != ACKAccepted {
		t.Errorf("ack = %q, want AA", code)
	}
	// RekeyPatient(old, new): the prior identifier goes, the PID-3 one survives.
	if len(stub.calls) != 1 || stub.calls[0] != [2]string{"MRN-BS1215", "BS1215"} {
		t.Fatalf("merged %v, want MRN-BS1215 into BS1215", stub.calls)
	}
}

func TestMergeHandler_RefusesAHalfMerge(t *testing.T) {
	// Acting on one identifier alone would move a patient somewhere nobody named.
	for _, tc := range []struct{ name, pid, mrg string }{
		{"no prior identifier", "PID|1||BS1215", "MRG|"},
		{"no surviving identifier", "PID|1||", "MRG|MRN-BS1215"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &mergerStub{}
			code, text := mergeHandle(t, mergeMsg(tc.pid, tc.mrg), stub)

			if code != ACKError {
				t.Errorf("ack = %q, want AE", code)
			}
			if text == "" {
				t.Error("the reason was not sent back")
			}
			if len(stub.calls) != 0 {
				t.Errorf("a patient was moved on a half merge: %v", stub.calls)
			}
		})
	}
}

func TestMergeHandler_MergeIntoItselfDoesNothing(t *testing.T) {
	stub := &mergerStub{}
	code, _ := mergeHandle(t, mergeMsg("PID|1||BS1215", "MRG|BS1215"), stub)

	if code != ACKAccepted {
		t.Errorf("ack = %q, want AA", code)
	}
	if len(stub.calls) != 0 {
		t.Errorf("a patient was moved onto itself: %v", stub.calls)
	}
}

func TestMergeHandler_ReportsAFailure(t *testing.T) {
	// The sender is entitled to know the merge did not take.
	stub := &mergerStub{err: errors.New("constraint violation")}
	code, _ := mergeHandle(t, mergeMsg("PID|1||BS1215", "MRG|MRN-BS1215"), stub)

	if code != ACKError {
		t.Errorf("ack = %q, want AE", code)
	}
}

func TestMergeHandler_WithoutAMergerA40IsLeftAlone(t *testing.T) {
	// The A08-only handler must not silently half-apply a merge.
	h := NewPatientUpdateHandler(applierNoop{}, func() ([]models.HL7Mapping, error) {
		return adtMappings, nil
	})
	code := h(mergeMsg("PID|1||BS1215", "MRG|MRN-BS1215")).ack()
	if code != ACKAccepted {
		t.Errorf("ack = %q, want AA", code)
	}
}

func TestMergeHandler_AReplayIsHarmless(t *testing.T) {
	// Once the prior identifier has been merged away there is no record under
	// it, so RekeyPatient does nothing and reports nothing moved. That is why
	// this path needs none of the staleness checking an A08 does.
	stub := &mergerStub{moved: 0}
	code, _ := mergeHandle(t, mergeMsg("PID|1||BS1215", "MRG|MRN-BS1215"), stub)

	if code != ACKAccepted {
		t.Errorf("ack = %q, want AA on a merge that had already happened", code)
	}
}
