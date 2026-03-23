package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

var (
	// ErrFormatNotSupported is returned when no converter binary is registered for the ECG vendor.
	ErrFormatNotSupported = errors.New("export: no XMLFDA converter available for this ECG vendor")
	// ErrConversionFailed is returned when the bridge binary exits non-zero or exceeds its timeout.
	ErrConversionFailed = errors.New("export: bridge conversion failed")
)

// Converter is the interface used by the download handler to produce XMLFDA output.
// Implemented by ECGBridge; can be stubbed in tests.
type Converter interface {
	ConvertToXMLFDA(ctx context.Context, sourcePath, vendor string, patient *models.Patient) ([]byte, error)
}

// ECGBridge dispatches XMLFDA conversion to the appropriate external binary based on ECG vendor.
// binaries maps vendor strings (as stored in ecgs.vendor) to binary path/name.
// Example: {"philips": "philips-to-fda", "muse": "muse-to-fda"}
type ECGBridge struct {
	binaries map[string]string
	timeout  time.Duration
}

// NewECGBridge constructs an ECGBridge with the given vendor→binary map and per-conversion timeout.
func NewECGBridge(binaries map[string]string, timeout time.Duration) *ECGBridge {
	return &ECGBridge{binaries: binaries, timeout: timeout}
}

// ConvertToXMLFDA converts the ECG file at sourcePath to FDA HL7 v3 aECG XML.
// vendor must match a key in the binaries map; patient demographics are optional.
// Returns ErrFormatNotSupported if no binary is registered for vendor.
// Returns ErrConversionFailed (wrapping stderr) on non-zero exit or deadline exceeded.
func (b *ECGBridge) ConvertToXMLFDA(ctx context.Context, sourcePath, vendor string, patient *models.Patient) ([]byte, error) {
	binary, ok := b.binaries[vendor]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFormatNotSupported, vendor)
	}

	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	args := []string{"--input", sourcePath}

	var metaTmp string // path of temp metadata file to clean up; "" means none created
	if meta := buildMetadataJSON(patient); meta != nil {
		f, err := os.CreateTemp("", "ecg-meta-*.json")
		if err == nil {
			metaTmp = f.Name()      // always track for cleanup regardless of encode outcome
			encodeOK := json.NewEncoder(f).Encode(meta) == nil
			_ = f.Close()
			if encodeOK {
				args = append(args, "--metadata", metaTmp)
			}
			// if encode failed: metaTmp is still set so defer removes the empty file
		}
	}
	if metaTmp != "" {
		defer os.Remove(metaTmp)
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		slog.Error("ecg-bridge: conversion error",
			"vendor", vendor, "binary", binary,
			"source", sourcePath, "stderr", stderr.String(), "error", err,
		)
		return nil, fmt.Errorf("%w: %s", ErrConversionFailed, stderr.String())
	}
	return stdout.Bytes(), nil
}

type bridgeMetadata struct {
	PatientName      string `json:"patientName,omitempty"`
	PatientID        string `json:"patientID,omitempty"`
	PatientBirthDate string `json:"patientBirthDate,omitempty"`
	PatientSex       string `json:"patientSex,omitempty"`
}

// buildMetadataJSON returns nil if no demographics are available (no temp file needed).
func buildMetadataJSON(p *models.Patient) *bridgeMetadata {
	if p == nil || (p.FirstName == "" && p.LastName == "" && p.DateOfBirth == nil) {
		return nil
	}
	m := &bridgeMetadata{PatientID: p.PatientID}
	if p.FirstName != "" || p.LastName != "" {
		m.PatientName = p.LastName + " " + p.FirstName
	}
	if p.DateOfBirth != nil {
		m.PatientBirthDate = p.DateOfBirth.Format("20060102")
	}
	m.PatientSex = p.Gender
	return m
}
