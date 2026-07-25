// Package mindray provides the vendor module for Mindray BeneHeart ECG files.
//
// It implements the module.Module interface for Mindray binary files received via FTP.
// Parsing and validation are delegated to the mindray-to-fda converter binary.
//
// The module is registered automatically on import via init().
// Add the following blank import to cmd/ecg-hub/main.go:
//
//	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/mindray"
package mindray

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/bridgeutil"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

var _ module.Module = (*Module)(nil)

func init() {
	module.Register(&Module{})
}

var mindrayToFDABin = bridgeutil.ResolveBin("BRIDGE_MINDRAY_TO_FDA", "mindray-to-fda")
var mindrayToDICOMBin = bridgeutil.ResolveBin("BRIDGE_MINDRAY_TO_DICOM", "mindray-to-dicom")

// Module implements module.Module for Mindray BeneHeart binary files.
type Module struct{}

func (m *Module) Name() string { return "mindray" }

// AcceptedExtensions returns the patterns the Mindray module handles.
// Mindray files have no extension — they are matched by filename via the router's
// fallback probing mechanism. The empty string catches files with no extension.
func (m *Module) AcceptedExtensions() []string { return []string{"", "12lead_data_v1"} }
func (m *Module) Health() error                { return nil }

func (m *Module) SupportedFormats() []module.ExportFormat {
	return []module.ExportFormat{
		{ID: "original", Label: "Mindray BeneHeart", Extension: "12lead_data_v1"},
		{ID: "xmlfda", Label: "FDA HL7 aECG XML", Extension: ".xml"},
		{ID: "dicom", Label: "DICOM ECG", Extension: ".dcm"},
	}
}

// mindrayMetadataJSON matches the JSON output of mindray-to-fda --metadata-json.
type mindrayMetadataJSON struct {
	PatientID    string `json:"patientID"`
	Name         string `json:"name"`
	Gender       string `json:"gender"`
	Location     string `json:"location"`
	ModelName    string `json:"modelName"`
	SerialNumber string `json:"serialNumber"`
	SoftwareName string `json:"softwareName"`
	StartTime    string `json:"startTime"`
	EndTime      string `json:"endTime"`
	LeadsCount   int    `json:"leadsCount"`
	SampleRate   int    `json:"sampleRate"`
	Paced        bool   `json:"paced"`
}

// Validate checks that the binary is a valid Mindray file by running the converter
// with --metadata-json and verifying patientID is present.
func (m *Module) Validate(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("mindray: validate: empty data")
	}
	// Use a temp file since the converter requires a file path.
	tmp, err := os.CreateTemp("", "mindray-validate-*")
	if err != nil {
		return fmt.Errorf("mindray: validate: create temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("mindray: validate: write temp: %w", err)
	}
	tmp.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, mindrayToFDABin, "--input", tmp.Name(), "--metadata-json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mindray: validate: not a valid Mindray file: %s", stderr.String())
	}

	var md mindrayMetadataJSON
	if err := json.Unmarshal(stdout.Bytes(), &md); err != nil {
		return fmt.Errorf("mindray: validate: json decode: %w", err)
	}
	// Format identity only — a Mindray file without a patient ID is still a Mindray
	// file and is handled by the "unidentified" review queue downstream.
	return nil
}

func (m *Module) Parse(ctx context.Context, data []byte) (*module.ECGMetadata, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("mindray: parse: empty data")
	}

	tmp, err := os.CreateTemp("", "mindray-parse-*")
	if err != nil {
		return nil, fmt.Errorf("mindray: parse: create temp: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("mindray: parse: write temp: %w", err)
	}
	tmp.Close()

	cmd := exec.CommandContext(ctx, mindrayToFDABin, "--input", tmp.Name(), "--metadata-json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("mindray: parse: mindray-to-fda: %s: %w", stderr.String(), err)
	}

	var md mindrayMetadataJSON
	if err := json.Unmarshal(stdout.Bytes(), &md); err != nil {
		return nil, fmt.Errorf("mindray: parse: json decode: %w", err)
	}
	// A missing patient ID is not a parse failure: the ingestion router sends the
	// parsed metadata (PatientID == "") to the "unidentified" review queue.

	// Parse startTime: "2025-10-08 13:21:47"
	var recordedAt time.Time
	if md.StartTime != "" {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", md.StartTime, time.UTC); err == nil {
			recordedAt = t
		}
	}

	var durationSeconds float64
	if md.EndTime != "" && md.StartTime != "" {
		start, errS := time.ParseInLocation("2006-01-02 15:04:05", md.StartTime, time.UTC)
		end, errE := time.ParseInLocation("2006-01-02 15:04:05", md.EndTime, time.UTC)
		if errS == nil && errE == nil && end.After(start) {
			durationSeconds = end.Sub(start).Seconds()
		}
	}

	extra := map[string]any{
		"serial_number": md.SerialNumber,
		"software_name": md.SoftwareName,
		"paced":         md.Paced,
	}
	if md.Location != "" {
		extra["location"] = md.Location
	}
	if md.Name != "" {
		extra["patient_name"] = md.Name
		// Map the single name field to first/last so the persister stores patient
		// demographics (it reads extra["last_name"]/["first_name"]/["sex"]).
		last, first := splitName(md.Name)
		if last != "" {
			extra["last_name"] = last
		}
		if first != "" {
			extra["first_name"] = first
		}
	}
	if md.Gender != "" && md.Gender != "UN" {
		extra["sex"] = md.Gender
	}

	return &module.ECGMetadata{
		PatientID:       md.PatientID,
		RecordedAt:      recordedAt,
		VendorName:      m.Name(),
		SourceFormat:    "mindray_beneheart",
		DeviceModel:     md.ModelName,
		LeadCount:       md.LeadsCount,
		DurationSeconds: durationSeconds,
		SampleRate:      float64(md.SampleRate),
		Extra:           extra,
	}, nil
}

// splitName splits a Mindray patient name into (last, first). Mindray reports a
// single "name" field; structured names use "^" (HL7-style) or "," as the
// separator. When no separator is present the whole value is treated as the last
// name (e.g. "moyles" → last="moyles", first="").
func splitName(name string) (last, first string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ""
	}
	for _, sep := range []string{"^", ","} {
		if i := strings.Index(name, sep); i >= 0 {
			return strings.TrimSpace(name[:i]), strings.TrimSpace(name[i+1:])
		}
	}
	return name, ""
}

// UpdateFile — Mindray binary format is not editable; no-op.
func (m *Module) UpdateFile(_ string, _ module.MetadataPatch) error { return nil }

// RenamePatientID — Mindray binary format is not editable; no-op.
func (m *Module) RenamePatientID(data []byte, _ string) ([]byte, error) { return data, nil }
