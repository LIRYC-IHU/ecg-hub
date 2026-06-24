package hl7

import (
	"strings"
	"testing"
	"time"
)

func segmentsOf(msg string) map[string]string {
	out := map[string]string{}
	for _, seg := range strings.Split(strings.TrimRight(msg, "\r"), "\r") {
		if len(seg) < 3 {
			continue
		}
		out[seg[:3]] = seg
	}
	return out
}

func TestBuildORU_CoreSegments(t *testing.T) {
	msh := MSHConfig{
		SendingApplication:   "ECG-HUB",
		SendingFacility:      "CARDIO",
		ReceivingApplication: "HIS",
		ReceivingFacility:    "CHU",
		Version:              "2.5",
		ProcessingID:         "P",
	}
	p := ORUPatient{
		PatientID:   "P001",
		LastName:    "Milhas",
		FirstName:   "Jonathan",
		DateOfBirth: "19800101",
		Gender:      "M",
		NDA:         "NDA42",
	}
	obs := ORUObservation{
		FillerOrderNo: "ecg-123",
		ObservedAt:    time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC),
	}

	msg := BuildORU(msh, p, obs)

	if !strings.HasSuffix(msg, "\r") {
		t.Fatalf("message must end with CR")
	}
	segs := segmentsOf(msg)

	if !strings.HasPrefix(segs["MSH"], "MSH|^~\\&|ECG-HUB|CARDIO|HIS|CHU|") {
		t.Errorf("MSH header wrong: %q", segs["MSH"])
	}
	if !strings.Contains(segs["MSH"], "|ORU^R01|") {
		t.Errorf("MSH missing ORU^R01 trigger: %q", segs["MSH"])
	}
	if !strings.Contains(segs["PID"], "Milhas^Jonathan") {
		t.Errorf("PID name wrong: %q", segs["PID"])
	}
	if !strings.Contains(segs["PID"], "19800101") || !strings.Contains(segs["PID"], "NDA42") {
		t.Errorf("PID demographics wrong: %q", segs["PID"])
	}
	if !strings.Contains(segs["OBR"], "ecg-123") {
		t.Errorf("OBR filler order wrong: %q", segs["OBR"])
	}
	if !strings.Contains(segs["OBR"], "ECG^Electrocardiogram") {
		t.Errorf("OBR should default service id: %q", segs["OBR"])
	}
	if _, ok := segs["OBX"]; ok {
		t.Errorf("no OBX expected when PDFBase64 is empty")
	}
}

func TestBuildORU_WithPDF(t *testing.T) {
	msg := BuildORU(MSHConfig{Version: "2.5", ProcessingID: "P"},
		ORUPatient{PatientID: "P1", LastName: "Doe"},
		ORUObservation{FillerOrderNo: "e1", PDFBase64: "QUJD", PDFTitle: "Resting ECG"})

	segs := segmentsOf(msg)
	obx := segs["OBX"]
	if obx == "" {
		t.Fatalf("expected an OBX segment")
	}
	if !strings.HasPrefix(obx, "OBX|1|ED|PDF^Resting ECG^L|") {
		t.Errorf("OBX header wrong: %q", obx)
	}
	if !strings.Contains(obx, "^application^pdf^Base64^QUJD") {
		t.Errorf("OBX ED value wrong: %q", obx)
	}
}

func TestBuildORU_EscapesSeparators(t *testing.T) {
	msg := BuildORU(MSHConfig{Version: "2.5", ProcessingID: "P"},
		ORUPatient{PatientID: "P|1", LastName: "O^Brien & Co"},
		ORUObservation{FillerOrderNo: "e1"})

	segs := segmentsOf(msg)
	pid := segs["PID"]
	// Raw separators must not survive un-escaped inside the field value.
	if strings.Contains(pid, "O^Brien") {
		t.Errorf("caret not escaped in PID: %q", pid)
	}
	if !strings.Contains(pid, "\\S\\Brien") {
		t.Errorf("caret should be escaped to \\S\\: %q", pid)
	}
	if !strings.Contains(pid, "\\T\\") {
		t.Errorf("ampersand should be escaped to \\T\\: %q", pid)
	}
	if !strings.Contains(pid, "P\\F\\1") {
		t.Errorf("pipe should be escaped to \\F\\: %q", pid)
	}
}
