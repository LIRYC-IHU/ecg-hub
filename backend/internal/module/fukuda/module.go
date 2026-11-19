// Package fukuda provides the vendor module for Fukuda Denshi .ECG files
// (FX-8322 and compatible cardiographs).
//
// The device's network protocol is not known yet, so files reach the module
// through the generic ingestion paths (UI upload, FTP drop). Parsing and
// validation are delegated to the fukuda-to-fda converter binary.
//
// The module is registered automatically on import via init().
package fukuda

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/bridgeutil"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

var _ module.Module = (*Module)(nil)

func init() {
	module.Register(&Module{})
}

var fukudaToFDABin = bridgeutil.ResolveBin("BRIDGE_FUKUDA_TO_FDA", "fukuda-to-fda")

// Module implements module.Module for Fukuda .ECG files.
type Module struct{}

func (m *Module) Name() string { return "fukuda" }

// AcceptedExtensions: the router lowercases the extension before matching.
func (m *Module) AcceptedExtensions() []string { return []string{".ecg"} }
func (m *Module) Health() error                { return nil }

func (m *Module) SupportedFormats() []module.ExportFormat {
	return []module.ExportFormat{
		{ID: "original", Label: "Fukuda ECG", Extension: ".ECG"},
		{ID: "xmlfda", Label: "FDA HL7 aECG XML", Extension: ".xml"},
		{ID: "dicom", Label: "DICOM ECG", Extension: ".dcm"},
	}
}

// metadataJSON matches the output of fukuda-to-fda --metadata-json.
type metadataJSON struct {
	PatientID    string `json:"patientID"`
	FamilyName   string `json:"familyName"`
	GivenName    string `json:"givenName"`
	Gender       string `json:"gender"`
	BirthDate    string `json:"birthDate"`
	DeviceModel  string `json:"deviceModel"`
	Datetime     string `json:"datetime"`
	SampleRate   int    `json:"sampleRate"`
	TotalSamples int    `json:"totalSamples"`
	HeartRate    int    `json:"heartRate"`
	PRInterval   int    `json:"prInterval"`
	QRSDuration  int    `json:"qrsDuration"`
	QTInterval   int    `json:"qtInterval"`
	QTcInterval  int    `json:"qtcInterval"`
	QRSAxis      int    `json:"qrsAxis"`
}

// readMetadata runs the converter on data and decodes its metadata.
func readMetadata(ctx context.Context, data []byte) (*metadataJSON, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	tmp, err := os.CreateTemp("", "fukuda-*.ECG")
	if err != nil {
		return nil, fmt.Errorf("create temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("write temp: %w", err)
	}
	tmp.Close()

	cmd := exec.CommandContext(ctx, fukudaToFDABin, "--input", tmp.Name(), "--metadata-json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("fukuda-to-fda: %s: %w", bytes.TrimSpace(stderr.Bytes()), err)
	}
	var md metadataJSON
	if err := json.Unmarshal(stdout.Bytes(), &md); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return &md, nil
}

// Validate checks format identity only: a Fukuda file without a patient ID is
// still a Fukuda file and goes to the "unidentified" review queue downstream.
func (m *Module) Validate(data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := readMetadata(ctx, data); err != nil {
		return fmt.Errorf("fukuda: validate: not a valid Fukuda file: %w", err)
	}
	return nil
}

func (m *Module) Parse(ctx context.Context, data []byte) (*module.ECGMetadata, error) {
	md, err := readMetadata(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("fukuda: parse: %w", err)
	}
	return m.toMetadata(md), nil
}

func (m *Module) toMetadata(md *metadataJSON) *module.ECGMetadata {
	var recordedAt time.Time
	if t, err := time.Parse("20060102150405", md.Datetime); err == nil {
		recordedAt = t
	}
	var duration float64
	if md.SampleRate > 0 && md.TotalSamples > 0 {
		duration = float64(md.TotalSamples) / float64(md.SampleRate)
	}

	extra := map[string]any{}
	set := func(k, v string) {
		if v != "" {
			extra[k] = v
		}
	}
	set("last_name", md.FamilyName)
	set("first_name", md.GivenName)
	if md.Gender != "U" {
		set("sex", md.Gender)
	}
	set("birth_date", md.BirthDate)
	for k, v := range map[string]int{
		"heart_rate":   md.HeartRate,
		"pr_interval":  md.PRInterval,
		"qrs_duration": md.QRSDuration,
		"qt_interval":  md.QTInterval,
		"qtc_interval": md.QTcInterval,
	} {
		if v > 0 {
			extra[k] = v
		}
	}

	return &module.ECGMetadata{
		PatientID:    md.PatientID,
		RecordedAt:   recordedAt,
		VendorName:   m.Name(),
		SourceFormat: "fukuda_ecg",
		DeviceModel:  md.DeviceModel,
		// The file stores the 8 measured leads (I, II, V1-V6); the converters
		// derive III, aVR, aVL, aVF, so every export is a 12-lead ECG.
		LeadCount:       12,
		DurationSeconds: duration,
		SampleRate:      float64(md.SampleRate),
		Extra:           extra,
	}
}

// UpdateFile — the Fukuda binary format is not editable; no-op.
func (m *Module) UpdateFile(_ string, _ module.MetadataPatch) error { return nil }

// RenamePatientID — the Fukuda binary format is not editable; no-op.
func (m *Module) RenamePatientID(data []byte, _ string) ([]byte, error) { return data, nil }
