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
	"strings"
	"sync"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

// FormatMeta describes a downloadable export format for the UI.
type FormatMeta struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Extension string `json:"extension"`
}

// ExportFormatOrder is the canonical display order of export formats.
var ExportFormatOrder = []string{"original", "pdf", "xmlfda", "dicom"}

// exportFormatMeta holds UI metadata for each known format. "original" has no fixed
// extension — it depends on the source vendor — so it is left blank.
var exportFormatMeta = map[string]FormatMeta{
	"original": {ID: "original", Label: "Original", Extension: ""},
	"pdf":      {ID: "pdf", Label: "PDF Report", Extension: ".pdf"},
	"xmlfda":   {ID: "xmlfda", Label: "FDA HL7 aECG XML", Extension: ".xml"},
	"dicom":    {ID: "dicom", Label: "DICOM ECG", Extension: ".dcm"},
}

// FormatMetaFor returns the UI metadata for a format ID.
func FormatMetaFor(id string) (FormatMeta, bool) {
	m, ok := exportFormatMeta[id]
	return m, ok
}

var (
	// ErrFormatNotSupported is returned when no converter binary is registered for the vendor+format combination.
	ErrFormatNotSupported = errors.New("export: no converter available for this vendor/format combination")
	// ErrConversionFailed is returned when the bridge binary exits non-zero or exceeds its timeout.
	ErrConversionFailed = errors.New("export: bridge conversion failed")
)

// ConvertOptions controls patient-data handling in the converted output.
// Zero value = passthrough (file values kept verbatim).
type ConvertOptions struct {
	// Anonymize strips patient-identifying fields (name, ID, birth date) from
	// the output — passes --anonymize to the converter binary. Intended for
	// research exports.
	Anonymize bool
	// InjectPatient overwrites the patient/acquisition fields in the output
	// with the HL7-enriched demographics from the patients table, using the
	// converter's stdin-JSON protocol. Requires a non-nil patient.
	InjectPatient bool
}

// Converter is the interface used by the download handler to produce converted output.
// Implemented by ECGBridge; can be stubbed in tests.
type Converter interface {
	Convert(ctx context.Context, sourcePath, vendor, format string, patient *models.Patient, opts ConvertOptions) ([]byte, error)
	SupportsFormat(vendor, format string) bool
	SupportedFormats(vendor string) []string
	ConverterVersion(vendor string) string
	ConvertToXMLFDA(ctx context.Context, sourcePath, vendor string, patient *models.Patient) ([]byte, error)
}

// ECGBridge dispatches format conversion to the appropriate external binary based on ECG vendor and format.
// binaries maps "vendor:format" keys (e.g. "philips:xmlfda") to binary path/name.
type ECGBridge struct {
	binaries map[string]string
	timeout  time.Duration
	// pdfBinary is the path to the fda-to-pdf renderer. PDF is produced by first
	// converting the source to FDA aECG XML, then rendering that — so any vendor
	// that supports "xmlfda" also supports "pdf". Empty disables the pdf format.
	pdfBinary string
	// versionCache memoises `binary --version` output per vendor. Converter
	// versions are fixed for the process lifetime, so we probe each binary at
	// most once. Keys are vendor names, values are version strings ("" = none).
	versionCache sync.Map
}

// NewECGBridge constructs an ECGBridge with the given vendor:format→binary map and per-conversion timeout.
func NewECGBridge(binaries map[string]string, timeout time.Duration) *ECGBridge {
	return &ECGBridge{binaries: binaries, timeout: timeout}
}

// WithPDFBinary enables the "pdf" export format, rendered via the given
// fda-to-pdf binary. Returns the bridge for chaining.
func (b *ECGBridge) WithPDFBinary(path string) *ECGBridge {
	b.pdfBinary = path
	return b
}

// SupportsFormat returns true if a binary is registered for the given vendor+format combination,
// or if format is "original" (which never requires a binary).
func (b *ECGBridge) SupportsFormat(vendor, format string) bool {
	if format == "original" {
		return true
	}
	if format == "pdf" {
		// PDF is rendered from the vendor's FDA aECG XML, so it is available
		// wherever both the fda-to-pdf binary and an xmlfda converter exist.
		_, hasXML := b.binaries[vendor+":xmlfda"]
		return b.pdfBinary != "" && hasXML
	}
	_, ok := b.binaries[vendor+":"+format]
	return ok
}

// SupportedFormats returns the ordered list of format IDs available for a vendor.
// "original" is always included; conversion formats are included only when a
// converter binary is registered for that vendor — this is the single source of
// truth for what the UI may offer and what Convert can actually produce.
func (b *ECGBridge) SupportedFormats(vendor string) []string {
	out := make([]string, 0, len(ExportFormatOrder))
	for _, f := range ExportFormatOrder {
		if b.SupportsFormat(vendor, f) {
			out = append(out, f)
		}
	}
	return out
}

// ConverterVersion returns the version reported by the vendor's converter binary,
// or "" if the vendor has no registered converter or the binary reports nothing.
// The result is memoised — the binary is probed at most once per vendor.
func (b *ECGBridge) ConverterVersion(vendor string) string {
	if cached, ok := b.versionCache.Load(vendor); ok {
		return cached.(string)
	}
	version := ""
	if binary := b.converterBinary(vendor); binary != "" {
		version = probeVersion(binary, b.timeout)
	}
	b.versionCache.Store(vendor, version)
	return version
}

// converterBinary picks the representative converter binary for a vendor,
// preferring the FDA aECG XML converter (the primary tool) and falling back to
// any registered binary for that vendor. Returns "" when none is registered.
func (b *ECGBridge) converterBinary(vendor string) string {
	if bin, ok := b.binaries[vendor+":xmlfda"]; ok {
		return bin
	}
	prefix := vendor + ":"
	for key, bin := range b.binaries {
		if strings.HasPrefix(key, prefix) {
			return bin
		}
	}
	return ""
}

// probeVersion runs `binary --version` and extracts the reported version.
// Best-effort: any error (missing binary, timeout, non-zero exit) yields "" so
// version reporting never blocks or fails the caller. Cobra prints the form
// "<name> version <v>"; we keep just the version token when present.
func probeVersion(binary string, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if i := strings.LastIndex(line, " version "); i >= 0 {
		return strings.TrimSpace(line[i+len(" version "):])
	}
	return line
}

// Convert converts the ECG file at sourcePath to the requested format.
// vendor and format must match a key in the binaries map (e.g. "philips", "xmlfda").
// patient demographics are optional unless opts.InjectPatient is set.
// Returns ErrFormatNotSupported if no binary is registered for vendor+format.
// Returns ErrConversionFailed (wrapping stderr) on non-zero exit or deadline exceeded.
func (b *ECGBridge) Convert(ctx context.Context, sourcePath, vendor, format string, patient *models.Patient, opts ConvertOptions) ([]byte, error) {
	// PDF is a two-step format: source → FDA aECG XML → PDF. Handling it here
	// means anonymisation and HL7 patient injection (applied in the xmlfda step)
	// carry through to the rendered report for free.
	if format == "pdf" {
		return b.convertToPDF(ctx, sourcePath, vendor, patient, opts)
	}

	key := vendor + ":" + format
	binary, ok := b.binaries[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFormatNotSupported, key)
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	args := []string{"--input", sourcePath}
	if opts.Anonymize {
		args = append(args, "--anonymize")
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// HL7 metadata injection: the converters accept a JSON object on stdin and
	// overwrite the corresponding patient/acquisition fields in the output.
	// Only fields present in the JSON overwrite — absent fields keep the file value.
	if opts.InjectPatient && patient != nil {
		inject := buildInjectJSON(patient)
		if len(inject) > 0 {
			cmd.Stdin = bytes.NewReader(inject)
		}
	}

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
	return b.Convert(ctx, sourcePath, vendor, "xmlfda", patient, ConvertOptions{})
}

// convertToPDF renders a printable PDF report by first converting the source to
// FDA aECG XML (honouring anonymise/inject) and then running the fda-to-pdf
// binary on it. fda-to-pdf reads -i <file> and writes -o <file>.
func (b *ECGBridge) convertToPDF(ctx context.Context, sourcePath, vendor string, patient *models.Patient, opts ConvertOptions) ([]byte, error) {
	if b.pdfBinary == "" {
		return nil, fmt.Errorf("%w: %s:pdf (no fda-to-pdf binary)", ErrFormatNotSupported, vendor)
	}

	xml, err := b.Convert(ctx, sourcePath, vendor, "xmlfda", patient, opts)
	if err != nil {
		return nil, err
	}

	in, err := os.CreateTemp("", "ecg-*.fda.xml")
	if err != nil {
		return nil, fmt.Errorf("%w: create temp xml: %v", ErrConversionFailed, err)
	}
	defer os.Remove(in.Name())
	if _, err := in.Write(xml); err != nil {
		in.Close()
		return nil, fmt.Errorf("%w: write temp xml: %v", ErrConversionFailed, err)
	}
	in.Close()
	outPath := in.Name() + ".pdf"
	defer os.Remove(outPath)

	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, b.pdfBinary, "-i", in.Name(), "-o", outPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	appmetrics.ModuleConversionDuration.WithLabelValues(vendor, "pdf").Observe(time.Since(start).Seconds())
	if err != nil {
		appmetrics.ModuleConversionErrors.WithLabelValues(vendor, "pdf").Inc()
		slog.Error("ecg-bridge: pdf conversion error",
			"vendor", vendor, "binary", b.pdfBinary, "stderr", stderr.String(), "error", err)
		return nil, fmt.Errorf("%w: %s", ErrConversionFailed, stderr.String())
	}
	return os.ReadFile(outPath)
}

// buildInjectJSON serialises the HL7-enriched patient demographics into the
// converters' stdin-JSON protocol. Keys: patientID, patientName ("LAST^First",
// HL7 PN order), gender. Only populated fields are included so file values are
// preserved for anything the HIS did not provide. Returns nil when there is
// nothing to inject.
func buildInjectJSON(p *models.Patient) []byte {
	fields := map[string]string{}
	if p.PatientID != "" {
		fields["patientID"] = p.PatientID
	}
	if p.LastName != "" || p.FirstName != "" {
		fields["patientName"] = p.LastName + "^" + p.FirstName
	}
	if p.Gender != "" {
		fields["gender"] = p.Gender
	}
	if len(fields) == 0 {
		return nil
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil
	}
	return raw
}
