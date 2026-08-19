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

	// PatientID (0010,0020). A missing patient ID is not a parse failure: the parsed
	// metadata (PatientID == "") is routed to the "unidentified" review queue.
	patientID, _ := extractString(dataset, tag.PatientID)
	patientID = strings.TrimSpace(patientID)

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

// dateTimeTagPair names a DICOM date tag and its companion time tag.
type dateTimeTagPair struct {
	name string
	date tag.Tag
	time tag.Tag // zero Tag when the date tag already carries a full DT value
}

// recordedAtTags is the order in which acquisition timestamps are looked up.
// StudyDate is the intended source, but plenty of exports leave it empty while
// still carrying the moment of acquisition elsewhere — reading the ingest date
// as the exam date instead is worse than any of these fallbacks.
var recordedAtTags = []dateTimeTagPair{
	{"StudyDate (0008,0020)", tag.StudyDate, tag.StudyTime},
	{"AcquisitionDateTime (0008,002A)", tag.AcquisitionDateTime, tag.Tag{}},
	{"AcquisitionDate (0008,0022)", tag.AcquisitionDate, tag.AcquisitionTime},
	{"ContentDate (0008,0023)", tag.ContentDate, tag.ContentTime},
	{"SeriesDate (0008,0021)", tag.SeriesDate, tag.SeriesTime},
}

// parseStudyDateTime derives the acquisition time from the first populated tag
// in recordedAtTags. Returns zero time when the file carries no usable date at
// all — callers must render that as unknown rather than substituting a date of
// their own.
func parseStudyDateTime(ds dicomlib.Dataset) time.Time {
	for _, src := range recordedAtTags {
		dateStr, err := extractString(ds, src.date)
		if err != nil {
			continue
		}
		dateStr = strings.TrimSpace(dateStr)
		if dateStr == "" {
			continue
		}

		timeStr := ""
		if src.time != (tag.Tag{}) {
			raw, _ := extractString(ds, src.time)
			timeStr = strings.TrimSpace(raw)
		}

		if t, ok := parseDicomDateTime(dateStr, timeStr); ok {
			if src.date != tag.StudyDate {
				slog.Info("dicom: StudyDate empty — using fallback tag", "tag", src.name, "recorded_at", t)
			}
			return t
		}
		slog.Warn("dicom: could not parse date tag", "tag", src.name, "value", dateStr)
	}

	slog.Warn("dicom: no usable acquisition date (StudyDate, AcquisitionDateTime, AcquisitionDate, ContentDate, SeriesDate all absent or empty) — RecordedAt will be zero")
	return time.Time{}
}

// parseDicomDateTime combines a DICOM DA value (YYYYMMDD) with an optional TM
// value (HHMMSS[.FFFFFF]). A DT value (YYYYMMDDHHMMSS[.FFFFFF][&ZZXX]) may
// arrive in dateStr on its own, so it is handled here too.
func parseDicomDateTime(dateStr, timeStr string) (time.Time, bool) {
	// Drop the fractional seconds and any UTC offset suffix of a DT value.
	if i := strings.IndexAny(dateStr, ".+-&"); i != -1 {
		dateStr = dateStr[:i]
	}

	// DT value carrying its own time component.
	if len(dateStr) >= 14 {
		if t, err := time.ParseInLocation("20060102150405", dateStr[:14], time.UTC); err == nil {
			return t, true
		}
	}
	if len(dateStr) != 8 {
		return time.Time{}, false
	}

	// DICOM TM format: HHMMSS.FFFFFF — use the first 6 chars for HHMMSS.
	if len(timeStr) >= 6 {
		if t, err := time.ParseInLocation("20060102150405", dateStr+timeStr[:6], time.UTC); err == nil {
			return t, true
		}
	}

	if t, err := time.ParseInLocation("20060102", dateStr, time.UTC); err == nil {
		return t, true
	}
	return time.Time{}, false
}
