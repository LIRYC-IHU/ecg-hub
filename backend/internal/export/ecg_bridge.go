package export

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

var (
	// ErrFormatNotSupported is returned when no converter binary is registered for the vendor+format combination.
	ErrFormatNotSupported = errors.New("export: no converter available for this vendor/format combination")
	// ErrConversionFailed is returned when the bridge binary exits non-zero or exceeds its timeout.
	ErrConversionFailed = errors.New("export: bridge conversion failed")
)

// Converter is the interface used by the download handler to produce converted output.
// Implemented by ECGBridge; can be stubbed in tests.
type Converter interface {
	Convert(ctx context.Context, sourcePath, vendor, format string, patient *models.Patient) ([]byte, error)
	SupportsFormat(vendor, format string) bool
	ConvertToXMLFDA(ctx context.Context, sourcePath, vendor string, patient *models.Patient) ([]byte, error)
}

// ECGBridge dispatches format conversion to the appropriate external binary based on ECG vendor and format.
// binaries maps "vendor:format" keys (e.g. "philips:xmlfda") to binary path/name.
type ECGBridge struct {
	binaries map[string]string
	timeout  time.Duration
}

// NewECGBridge constructs an ECGBridge with the given vendor:format→binary map and per-conversion timeout.
func NewECGBridge(binaries map[string]string, timeout time.Duration) *ECGBridge {
	return &ECGBridge{binaries: binaries, timeout: timeout}
}

// SupportsFormat returns true if a binary is registered for the given vendor+format combination,
// or if format is "original" (which never requires a binary).
func (b *ECGBridge) SupportsFormat(vendor, format string) bool {
	if format == "original" {
		return true
	}
	_, ok := b.binaries[vendor+":"+format]
	return ok
}

// Convert converts the ECG file at sourcePath to the requested format.
// vendor and format must match a key in the binaries map (e.g. "philips", "xmlfda").
// patient demographics are optional.
// Returns ErrFormatNotSupported if no binary is registered for vendor+format.
// Returns ErrConversionFailed (wrapping stderr) on non-zero exit or deadline exceeded.
func (b *ECGBridge) Convert(ctx context.Context, sourcePath, vendor, format string, patient *models.Patient) ([]byte, error) {
	key := vendor + ":" + format
	slog.Error("++++++++++++++++++++++++++ ECGBridge: converting %s with key %s using source file %s\n", format, key, sourcePath)
	binary, ok := b.binaries[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFormatNotSupported, key)
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "--input", sourcePath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	appmetrics.ModuleConversionDuration.WithLabelValues(vendor, format).Observe(time.Since(start).Seconds())
	if err != nil {
		appmetrics.ModuleConversionErrors.WithLabelValues(vendor, format).Inc()
		slog.Error("ecg-bridge: conversion error",
			"vendor", vendor, "format", format, "binary", binary,
			"source", sourcePath, "stderr", stderr.String(), "error", err,
		)
		return nil, fmt.Errorf("%w: %s", ErrConversionFailed, stderr.String())
	}
	return stdout.Bytes(), nil
}

// ConvertToXMLFDA converts the ECG file at sourcePath to FDA HL7 v3 aECG XML.
// Delegates to Convert with format="xmlfda".
func (b *ECGBridge) ConvertToXMLFDA(ctx context.Context, sourcePath, vendor string, patient *models.Patient) ([]byte, error) {
	return b.Convert(ctx, sourcePath, vendor, "xmlfda", patient)
}
