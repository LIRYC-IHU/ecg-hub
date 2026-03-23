package export

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ─── buildMetadataJSON ────────────────────────────────────────────────────────

func TestBuildMetadataJSON_NilPatient(t *testing.T) {
	if buildMetadataJSON(nil) != nil {
		t.Error("expected nil for nil patient")
	}
}

func TestBuildMetadataJSON_EmptyDemographics(t *testing.T) {
	p := &models.Patient{PatientID: "P001"}
	if buildMetadataJSON(p) != nil {
		t.Error("expected nil when no demographics are populated")
	}
}

func TestBuildMetadataJSON_WithDemographics(t *testing.T) {
	dob := mustParseDate("1970-01-15")
	p := &models.Patient{
		PatientID: "P001",
		FirstName: "John",
		LastName:  "Doe",
		DateOfBirth: &dob,
		Gender:    "M",
	}
	meta := buildMetadataJSON(p)
	if meta == nil {
		t.Fatal("expected non-nil metadata")
	}
	if meta.PatientName != "Doe John" {
		t.Errorf("PatientName = %q, want %q", meta.PatientName, "Doe John")
	}
	if meta.PatientBirthDate != "19700115" {
		t.Errorf("PatientBirthDate = %q, want %q", meta.PatientBirthDate, "19700115")
	}
	if meta.PatientSex != "M" {
		t.Errorf("PatientSex = %q, want %q", meta.PatientSex, "M")
	}
	if meta.PatientID != "P001" {
		t.Errorf("PatientID = %q, want %q", meta.PatientID, "P001")
	}
}

// ─── ECGBridge.ConvertToXMLFDA ────────────────────────────────────────────────

func TestConvertToXMLFDA_UnsupportedVendor(t *testing.T) {
	bridge := NewECGBridge(map[string]string{"philips": "philips-to-fda"}, 5*time.Second)
	_, err := bridge.ConvertToXMLFDA(context.Background(), "/some/file.dcm", "dicom", nil)
	if err == nil {
		t.Fatal("expected error for unsupported vendor")
	}
	if !errors.Is(err, ErrFormatNotSupported) {
		t.Errorf("expected ErrFormatNotSupported, got %v", err)
	}
}

func TestConvertToXMLFDA_BinaryNotFound(t *testing.T) {
	// Use a binary name that doesn't exist on PATH.
	bridge := NewECGBridge(map[string]string{"philips": "no-such-binary-xyz"}, 5*time.Second)
	_, err := bridge.ConvertToXMLFDA(context.Background(), "/some/file.xml", "philips", nil)
	if err == nil {
		t.Fatal("expected error when binary is missing")
	}
	if !errors.Is(err, ErrConversionFailed) {
		t.Errorf("expected ErrConversionFailed, got %v", err)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func mustParseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}
