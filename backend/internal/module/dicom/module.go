// Package dicom provides the vendor module for DICOM ECG files.
//
// It implements the module.Module interface for DICOM files received via FTP
// or the DICOM C-STORE SCP server (internal/dicom/server.go).
//
// The module is registered automatically on import via init().
// Add the following blank import to cmd/ecg-hub/main.go:
//
//	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/dicom"
package dicom

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	dicomlib "github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// Compile-time contract check.
var _ module.Module = (*Module)(nil)

func init() {
	module.Register(&Module{})
}

// Module implements module.Module for DICOM ECG files.
type Module struct{}

func (m *Module) Name() string                 { return "dicom" }
func (m *Module) AcceptedExtensions() []string { return []string{".dcm", ".dicom"} }

// Health always returns nil — the DICOM module has no external dependencies.
func (m *Module) Health() error { return nil }

// SupportedFormats returns the export formats this module can produce.
func (m *Module) SupportedFormats() []module.ExportFormat {
	return []module.ExportFormat{
		{ID: "original", Label: "Original (DICOM ECG)", Extension: ".dcm"},
	}
}

// Validate checks that data contains a valid DICOM file by verifying the
// 128-byte preamble followed by the "DICM" magic word (bytes 128–131).
// This is a lightweight check — no full parse is performed.
func (m *Module) Validate(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("dicom: validate: empty data")
	}
	if len(data) >= 132 && string(data[128:132]) == "DICM" {
		return nil
	}
	return fmt.Errorf("dicom: validate: not a DICOM file (DICM preamble not found at bytes 128–131)")
}

// Parse extracts ECGMetadata from a DICOM file.
// PatientID (tag 0010,0020) is required — returns an error if absent.
// RecordedAt is derived from StudyDate (0008,0020) + StudyTime (0008,0030).
func (m *Module) Parse(_ context.Context, data []byte) (*module.ECGMetadata, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("dicom: parse: empty data")
	}

	dataset, err := dicomlib.ParseUntilEOF(bytes.NewReader(data), nil)
	if err != nil {
		return nil, fmt.Errorf("dicom: parse: %w", err)
	}

	// PatientID (0010,0020) — mandatory.
	patientID, err := extractString(dataset, tag.PatientID)
	if err != nil || strings.TrimSpace(patientID) == "" {
		return nil, fmt.Errorf("dicom: parse: missing PatientID (tag 0010,0020)")
	}

	// RecordedAt from StudyDate (0008,0020) + StudyTime (0008,0030).
	recordedAt := parseStudyDateTime(dataset)

	// SOPInstanceUID (0008,0018) and Modality (0008,0060) — stored in Extra.
	extra := map[string]any{}
	if sopUID, err := extractString(dataset, tag.SOPInstanceUID); err == nil && sopUID != "" {
		extra["sop_instance_uid"] = sopUID
	}
	if modality, err := extractString(dataset, tag.Modality); err == nil && modality != "" {
		extra["modality"] = modality
	}

	modality, _ := extra["modality"].(string)
	recordModality(modality)

	return &module.ECGMetadata{
		PatientID:    strings.TrimSpace(patientID),
		RecordedAt:   recordedAt,
		VendorName:   m.Name(),
		SourceFormat: "dicom",
		Extra:        extra,
	}, nil
}

// UpdateFile applies the given patch to the DICOM file at filePath.
// Only PatientID is currently patched — other fields require complex tag rewriting.
// Best-effort: returns a wrapped error on failure without aborting the HTTP request.
func (m *Module) UpdateFile(filePath string, patch module.MetadataPatch) error {
	if patch.PatientID == nil {
		// Nothing to update.
		return nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("dicom: update_file: read: %w", err)
	}
	updated, err := rewritePatientID(data, *patch.PatientID)
	if err != nil {
		return fmt.Errorf("dicom: update_file: %w", err)
	}
	if err := os.WriteFile(filePath, updated, 0o644); err != nil {
		return fmt.Errorf("dicom: update_file: write: %w", err)
	}
	return nil
}

// RenamePatientID rewrites the PatientID tag (0010,0020) in the given DICOM
// bytes and returns the modified bytes. The input bytes are not modified.
func (m *Module) RenamePatientID(data []byte, newID string) ([]byte, error) {
	if newID == "" {
		return nil, fmt.Errorf("dicom: rename_patient_id: newID is empty")
	}
	return rewritePatientID(data, newID)
}

// rewritePatientID parses a DICOM dataset, replaces the PatientID element,
// and serialises the result back to bytes.
func rewritePatientID(data []byte, newID string) ([]byte, error) {
	dataset, err := dicomlib.ParseUntilEOF(bytes.NewReader(data), nil)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	newElem, err := dicomlib.NewElement(tag.PatientID, []string{newID})
	if err != nil {
		return nil, fmt.Errorf("new element: %w", err)
	}

	// Replace or append the PatientID element.
	replaced := false
	for i, elem := range dataset.Elements {
		if elem.Tag == tag.PatientID {
			dataset.Elements[i] = newElem
			replaced = true
			break
		}
	}
	if !replaced {
		dataset.Elements = append(dataset.Elements, newElem)
	}

	var buf bytes.Buffer
	if err := dicomlib.Write(&buf, dataset); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	return buf.Bytes(), nil
}

// extractString retrieves the first string value of a DICOM tag from a dataset.
func extractString(ds dicomlib.Dataset, t tag.Tag) (string, error) {
	elem, err := ds.FindElementByTag(t)
	if err != nil {
		return "", err
	}
	vals, ok := elem.Value.GetValue().([]string)
	if !ok || len(vals) == 0 {
		return "", fmt.Errorf("dicom: tag %v is not a string", t)
	}
	return vals[0], nil
}

// parseStudyDateTime derives time.Time from StudyDate (0008,0020) and
// StudyTime (0008,0030). Returns zero time if parsing fails.
func parseStudyDateTime(ds dicomlib.Dataset) time.Time {
	dateStr, err := extractString(ds, tag.StudyDate)
	if err != nil || strings.TrimSpace(dateStr) == "" {
		slog.Warn("dicom: StudyDate (0008,0020) absent or empty — RecordedAt will be zero")
		return time.Time{}
	}
	dateStr = strings.TrimSpace(dateStr)

	timeStr, _ := extractString(ds, tag.StudyTime)
	timeStr = strings.TrimSpace(timeStr)

	// DICOM TM format: HHMMSS.FFFFFF — use first 6 chars for HHMMSS.
	if len(timeStr) >= 6 {
		combined := dateStr + timeStr[:6]
		if t, err := time.ParseInLocation("20060102150405", combined, time.UTC); err == nil {
			return t
		}
	}

	if t, err := time.ParseInLocation("20060102", dateStr, time.UTC); err == nil {
		return t
	}

	slog.Warn("dicom: could not parse StudyDate", "value", dateStr)
	return time.Time{}
}
