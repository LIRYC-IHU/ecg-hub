package dicom_test

import (
	"bytes"
	"context"
	"testing"

	dicomlib "github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
	dicommod "github.com/LIRYC-IHU/ecg-hub/internal/module/dicom"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// buildMinimalDICOM creates a minimal valid DICOM byte slice with the given
// PatientID and StudyDate. If patientID is empty, the tag is omitted.
func buildMinimalDICOM(t *testing.T, patientID, studyDate string) []byte {
	t.Helper()

	var elems []*dicomlib.Element

	// TransferSyntaxUID (0002,0010) is required by dicom.Write.
	// "1.2.840.10008.1.2.1" = Explicit VR Little Endian transfer syntax.
	tsUID, err := dicomlib.NewElement(tag.TransferSyntaxUID, []string{"1.2.840.10008.1.2.1"})
	if err != nil {
		t.Fatalf("NewElement TransferSyntaxUID: %v", err)
	}
	elems = append(elems, tsUID)

	if patientID != "" {
		e, err := dicomlib.NewElement(tag.PatientID, []string{patientID})
		if err != nil {
			t.Fatalf("NewElement PatientID: %v", err)
		}
		elems = append(elems, e)
	}
	if studyDate != "" {
		e, err := dicomlib.NewElement(tag.StudyDate, []string{studyDate})
		if err != nil {
			t.Fatalf("NewElement StudyDate: %v", err)
		}
		elems = append(elems, e)
	}
	// Add a SOPInstanceUID so the DICOM file has a known UID.
	sopUID, err := dicomlib.NewElement(tag.SOPInstanceUID, []string{"1.2.3.4.5.6.7.8.9"})
	if err != nil {
		t.Fatalf("NewElement SOPInstanceUID: %v", err)
	}
	elems = append(elems, sopUID)

	ds := dicomlib.Dataset{Elements: elems}

	var buf bytes.Buffer
	if err := dicomlib.Write(&buf, ds); err != nil {
		t.Fatalf("dicom.Write: %v", err)
	}
	return buf.Bytes()
}

var m = &dicommod.Module{}

func TestModule_Name(t *testing.T) {
	if got := m.Name(); got != "dicom" {
		t.Errorf("Name() = %q; want %q", got, "dicom")
	}
}

func TestModule_AcceptedExtensions(t *testing.T) {
	exts := m.AcceptedExtensions()
	want := map[string]bool{".dcm": true, ".dicom": true}
	for _, e := range exts {
		if !want[e] {
			t.Errorf("unexpected extension %q", e)
		}
		delete(want, e)
	}
	for missing := range want {
		t.Errorf("missing extension %q", missing)
	}
}

func TestModule_Health(t *testing.T) {
	if err := m.Health(); err != nil {
		t.Errorf("Health() = %v; want nil", err)
	}
}

func TestModule_Validate_Valid(t *testing.T) {
	data := buildMinimalDICOM(t, "P001", "20240312")
	if err := m.Validate(data); err != nil {
		t.Errorf("Validate(valid DICOM) = %v; want nil", err)
	}
}

func TestModule_Validate_Empty(t *testing.T) {
	if err := m.Validate(nil); err == nil {
		t.Error("Validate(nil) = nil; want error")
	}
	if err := m.Validate([]byte{}); err == nil {
		t.Error("Validate(empty) = nil; want error")
	}
}

func TestModule_Validate_NoPreamble(t *testing.T) {
	// Arbitrary non-DICOM bytes.
	data := make([]byte, 200)
	copy(data, []byte("not a dicom file at all"))
	if err := m.Validate(data); err == nil {
		t.Error("Validate(no preamble) = nil; want error")
	}
}

func TestModule_Validate_NotDICOM(t *testing.T) {
	// XML that looks like something else.
	if err := m.Validate([]byte("<restingecgdata><patient></patient></restingecgdata>")); err == nil {
		t.Error("Validate(XML) = nil; want error")
	}
}

func TestModule_Parse_ValidFile(t *testing.T) {
	data := buildMinimalDICOM(t, "PATIENT-42", "20240312")
	meta, err := m.Parse(context.Background(), data)
	if err != nil {
		t.Fatalf("Parse(valid) error: %v", err)
	}
	if meta.PatientID != "PATIENT-42" {
		t.Errorf("PatientID = %q; want %q", meta.PatientID, "PATIENT-42")
	}
	if meta.VendorName != "dicom" {
		t.Errorf("VendorName = %q; want %q", meta.VendorName, "dicom")
	}
	if meta.SourceFormat != "dicom" {
		t.Errorf("SourceFormat = %q; want %q", meta.SourceFormat, "dicom")
	}
	if !meta.RecordedAt.IsZero() {
		// StudyDate 20240312 should parse to 2024-03-12.
		if meta.RecordedAt.Year() != 2024 || meta.RecordedAt.Month() != 3 || meta.RecordedAt.Day() != 12 {
			t.Errorf("RecordedAt = %v; want 2024-03-12", meta.RecordedAt)
		}
	}
	if _, ok := meta.Extra["sop_instance_uid"]; !ok {
		t.Error("Extra missing sop_instance_uid")
	}
}

func TestModule_Parse_EmptyData(t *testing.T) {
	if _, err := m.Parse(context.Background(), nil); err == nil {
		t.Error("Parse(nil) = nil error; want error")
	}
	if _, err := m.Parse(context.Background(), []byte{}); err == nil {
		t.Error("Parse(empty) = nil error; want error")
	}
}

func TestModule_Parse_MissingPatientID(t *testing.T) {
	// Build DICOM without PatientID.
	data := buildMinimalDICOM(t, "", "20240312")
	if _, err := m.Parse(context.Background(), data); err == nil {
		t.Error("Parse(no PatientID) = nil error; want error")
	}
}

func TestModule_RenamePatientID(t *testing.T) {
	original := buildMinimalDICOM(t, "OLD-ID", "20240312")
	updated, err := m.RenamePatientID(original, "NEW-ID")
	if err != nil {
		t.Fatalf("RenamePatientID error: %v", err)
	}

	// Parse the updated bytes and verify the new ID.
	ds, err := dicomlib.ParseUntilEOF(bytes.NewReader(updated), nil)
	if err != nil {
		t.Fatalf("parse updated bytes: %v", err)
	}
	elem, err := ds.FindElementByTag(tag.PatientID)
	if err != nil {
		t.Fatalf("PatientID tag not found: %v", err)
	}
	vals, ok := elem.Value.GetValue().([]string)
	if !ok || len(vals) == 0 || vals[0] != "NEW-ID" {
		t.Errorf("PatientID after rename = %v; want NEW-ID", vals)
	}

	// Verify old ID is absent.
	if bytes.Contains(updated, []byte("OLD-ID")) {
		t.Error("old PatientID still present in updated bytes")
	}
}

func TestModule_RenamePatientID_EmptyID(t *testing.T) {
	data := buildMinimalDICOM(t, "P001", "20240312")
	if _, err := m.RenamePatientID(data, ""); err == nil {
		t.Error("RenamePatientID('') = nil error; want error")
	}
}

func TestModule_InterfaceSatisfied(t *testing.T) {
	// Ensure Module satisfies module.Module at runtime.
	var _ module.Module = (*dicommod.Module)(nil)
}

// TestModule_UpdateFile_NoPatientIDPatch verifies that UpdateFile with no
// PatientID patch is a no-op and does not return an error.
func TestModule_UpdateFile_NoPatientIDPatch(t *testing.T) {
	// UpdateFile on an empty patch should always return nil.
	err := m.UpdateFile("/nonexistent/path.dcm", module.MetadataPatch{})
	if err != nil {
		t.Errorf("UpdateFile(empty patch) = %v; want nil", err)
	}
}
