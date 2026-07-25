// Package muse provides the vendor module for GE MUSE RestingECG XML files.
//
// MUSE files are XML (like Philips) and arrive via FTP. Because both Philips and
// MUSE claim the ".xml" extension, the ingestion router probes Validate() on each
// candidate — MUSE files have a <RestingECG> root, Philips files have a
// <restingecgdata> root in the Philips namespace, so the two are unambiguous.
//
// Metadata extraction is delegated to the muse-to-fda converter binary via its
// --metadata-json flag; export to FDA aECG XML and DICOM is delegated to
// muse-to-fda / muse-to-dicom through the ECGBridge.
//
// The module is registered automatically on import via init().
// Add the following blank import to cmd/ecg-hub/main.go:
//
//	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/muse"
package muse

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/bridgeutil"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
	"github.com/LIRYC-IHU/ecg-hub/internal/xmlutil"
)

// Compile-time contract check.
var _ module.Module = (*Module)(nil)

func init() {
	module.Register(&Module{})
}

var museToFDABin = bridgeutil.ResolveBin("BRIDGE_MUSE_TO_FDA", "muse-to-fda")

// Module implements module.Module for GE MUSE RestingECG XML files.
type Module struct{}

func (m *Module) Name() string                 { return "muse" }
func (m *Module) AcceptedExtensions() []string { return []string{".xml"} }

// Health always returns nil — the MUSE module has no external dependencies
// beyond the converter binary, which is exercised on demand.
func (m *Module) Health() error { return nil }

// SupportedFormats returns the export formats this module can produce.
func (m *Module) SupportedFormats() []module.ExportFormat {
	return []module.ExportFormat{
		{ID: "original", Label: "Original (GE MUSE XML)", Extension: ".xml"},
		{ID: "xmlfda", Label: "FDA HL7 aECG XML", Extension: ".xml"},
		{ID: "dicom", Label: "DICOM ECG", Extension: ".dcm"},
	}
}

// museProbe is a minimal view used by Validate to confirm the file is a MUSE
// RestingECG document with a patient ID — without invoking the converter.
// xml.Unmarshal errors when the root element is not <RestingECG>, which cleanly
// rejects Philips (and any non-MUSE) XML during routing.
type museProbe struct {
	XMLName xml.Name `xml:"RestingECG"`
	Patient struct {
		PatientID string `xml:"PatientID"`
	} `xml:"PatientDemographics"`
}

// Validate confirms the file is a MUSE RestingECG document — format identity only.
// It is a fast, native check (no subprocess) so the router can probe .xml candidates
// cheaply. xml.Unmarshal errors when the root element is not <RestingECG>, which
// cleanly rejects Philips (root <restingecgdata>) and any other XML.
//
// The patient ID is intentionally NOT checked here: a MUSE file without a patient ID
// is still a MUSE file and must route to this module so the ingestion pipeline can
// send it to the "unidentified" review queue rather than mis-routing it.
func (m *Module) Validate(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("muse: validate: empty data")
	}
	var p museProbe
	if err := xmlutil.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("muse: validate: not a valid MUSE RestingECG XML: %w", err)
	}
	return nil
}

// museMetadataJSON matches the JSON output of muse-to-fda --metadata-json.
type museMetadataJSON struct {
	MuseVersion string `json:"museVersion"`
	Patient     struct {
		ID   string `json:"id"`
		Name string `json:"name"` // "LAST^FIRST"
		Sex  string `json:"sex"`  // "M" / "F"
		Age  string `json:"age"`
	} `json:"patient"`
	Study struct {
		Date string `json:"date"` // YYYYMMDD
		Time string `json:"time"` // HHMMSS
		UID  string `json:"uid"`
	} `json:"study"`
	Signal struct {
		SamplingRateHz      float64 `json:"samplingRateHz"`
		SensitivityUvPerLsb float64 `json:"sensitivityUvPerLsb"`
	} `json:"signal"`
	Measurements struct {
		HeartRate   float64 `json:"heartRate"`
		AtrialRate  float64 `json:"atrialRate"`
		PRInterval  float64 `json:"prInterval"`
		QRSDuration float64 `json:"qrsDuration"`
		QTInterval  float64 `json:"qtInterval"`
		QTcInterval float64 `json:"qtcInterval"`
		PAxis       float64 `json:"pAxis"`
		QRSAxis     float64 `json:"qrsAxis"`
		TAxis       float64 `json:"tAxis"`
	} `json:"measurements"`
	Diagnosis []string `json:"diagnosis"`
}

// muStandardLeadCount is the lead count of a MUSE RestingECG study: 8 leads are
// stored and 4 (III, aVR, aVL, aVF) are derived, for the standard 12-lead set.
const muStandardLeadCount = 12

// Parse extracts ECGMetadata from a MUSE RestingECG XML file by delegating to
// muse-to-fda --metadata-json.
func (m *Module) Parse(ctx context.Context, data []byte) (*module.ECGMetadata, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("muse: parse: empty data")
	}

	// The converter binary assumes UTF-8; transcode non-UTF-8 vendor exports
	// (e.g. Windows-1252 MUSE files) so it does not reject the declared encoding.
	data, err := xmlutil.ToUTF8(data)
	if err != nil {
		return nil, fmt.Errorf("muse: parse: %w", err)
	}

	// The converter requires a file path, so spill to a temp file.
	tmp, err := os.CreateTemp("", "muse-parse-*.xml")
	if err != nil {
		return nil, fmt.Errorf("muse: parse: create temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("muse: parse: write temp: %w", err)
	}
	tmp.Close()

	cmd := exec.CommandContext(ctx, museToFDABin, "--input", tmp.Name(), "--metadata-json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("muse: parse: muse-to-fda: %s: %w", stderr.String(), err)
	}

	var md museMetadataJSON
	if err := json.Unmarshal(stdout.Bytes(), &md); err != nil {
		return nil, fmt.Errorf("muse: parse: json decode: %w", err)
	}
	// A missing patient ID is not a parse failure: the ingestion router sends the
	// parsed metadata (PatientID == "") to the "unidentified" review queue.

	recordedAt := parseStudyDateTime(md.Study.Date, md.Study.Time)

	extra := map[string]any{}
	if md.MuseVersion != "" {
		extra["muse_version"] = md.MuseVersion
	}
	if md.Study.UID != "" {
		extra["study_uid"] = md.Study.UID
	}
	if md.Patient.Age != "" {
		extra["patient_age"] = md.Patient.Age
	}
	if md.Patient.Sex != "" {
		extra["sex"] = md.Patient.Sex
	}
	if last, first := splitName(md.Patient.Name); last != "" || first != "" {
		if last != "" {
			extra["last_name"] = last
		}
		if first != "" {
			extra["first_name"] = first
		}
		extra["patient_name"] = md.Patient.Name
	}
	if len(md.Diagnosis) > 0 {
		extra["diagnosis"] = md.Diagnosis
	}

	return &module.ECGMetadata{
		PatientID:    strings.TrimSpace(md.Patient.ID),
		RecordedAt:   recordedAt,
		VendorName:   m.Name(),
		SourceFormat: "muse_restingecg_xml",
		DeviceModel:  "GE MUSE",
		LeadCount:    muStandardLeadCount,
		SampleRate:   md.Signal.SamplingRateHz,
		Extra:        extra,
	}, nil
}

// parseStudyDateTime combines a MUSE study date (YYYYMMDD) and time (HHMMSS) into
// a UTC timestamp. Returns the zero time when the date is absent or unparseable.
func parseStudyDateTime(date, t string) time.Time {
	date = strings.TrimSpace(date)
	if date == "" {
		return time.Time{}
	}
	t = strings.TrimSpace(t)
	if t == "" {
		if parsed, err := time.ParseInLocation("20060102", date, time.UTC); err == nil {
			return parsed
		}
		return time.Time{}
	}
	if parsed, err := time.ParseInLocation("20060102150405", date+t, time.UTC); err == nil {
		return parsed
	}
	return time.Time{}
}

// splitName splits a MUSE "LAST^FIRST" name into (last, first).
func splitName(name string) (last, first string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ""
	}
	if i := strings.Index(name, "^"); i >= 0 {
		return strings.TrimSpace(name[:i]), strings.TrimSpace(name[i+1:])
	}
	return name, ""
}

// UpdateFile reads the MUSE XML at filePath, applies the non-nil fields in patch
// via streaming token replacement, and writes the result back to the same path.
func (m *Module) UpdateFile(filePath string, patch module.MetadataPatch) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("muse: update_file: read: %w", err)
	}
	updated, err := applyUpdates(data, patchToMap(patch))
	if err != nil {
		return fmt.Errorf("muse: update_file: %w", err)
	}
	if err := os.WriteFile(filePath, updated, 0o644); err != nil {
		return fmt.Errorf("muse: update_file: write: %w", err)
	}
	return nil
}

// RenamePatientID rewrites the <PatientID> element in the MUSE XML bytes and
// returns the modified bytes. The input is not modified.
func (m *Module) RenamePatientID(data []byte, newID string) ([]byte, error) {
	if strings.TrimSpace(newID) == "" {
		return nil, fmt.Errorf("muse: rename_patient_id: newID is empty")
	}
	return applyUpdates(data, map[string]string{"patient_id": newID})
}

// patchToMap converts a MetadataPatch to the string map expected by applyUpdates.
// Only non-nil fields are included. Sex is mapped from the canonical "M"/"F" to
// the MUSE Gender vocabulary (MALE/FEMALE).
func patchToMap(patch module.MetadataPatch) map[string]string {
	out := map[string]string{}
	if patch.PatientID != nil {
		out["patient_id"] = *patch.PatientID
	}
	if patch.LastName != nil {
		out["last_name"] = *patch.LastName
	}
	if patch.FirstName != nil {
		out["first_name"] = *patch.FirstName
	}
	if patch.Sex != nil {
		out["gender"] = museGender(*patch.Sex)
	}
	return out
}

// museGender maps the canonical "M"/"F" sex code to the MUSE Gender vocabulary.
func museGender(sex string) string {
	switch strings.ToUpper(strings.TrimSpace(sex)) {
	case "M", "MALE":
		return "MALE"
	case "F", "FEMALE":
		return "FEMALE"
	default:
		return sex
	}
}

// fieldPaths maps canonical field keys to their element path in a MUSE RestingECG file.
var fieldPaths = map[string][]string{
	"patient_id": {"RestingECG", "PatientDemographics", "PatientID"},
	"last_name":  {"RestingECG", "PatientDemographics", "PatientLastName"},
	"first_name": {"RestingECG", "PatientDemographics", "PatientFirstName"},
	"gender":     {"RestingECG", "PatientDemographics", "Gender"},
}

// applyUpdates performs streaming XML token replacement, replacing the text
// content of each targeted element. Elements not present in the document are
// skipped (best-effort) — MUSE demographics blocks are optional.
func applyUpdates(data []byte, fields map[string]string) ([]byte, error) {
	contentUpdates := map[string]string{}
	for key, val := range fields {
		if val == "" {
			continue
		}
		if path, ok := fieldPaths[key]; ok {
			contentUpdates[strings.Join(path, "/")] = val
		}
	}
	if len(contentUpdates) == 0 {
		return data, nil
	}

	// Token streaming below uses the stdlib decoder/encoder; transcode non-UTF-8
	// input to UTF-8 first so the rewritten file is valid and self-consistent.
	data, err := xmlutil.ToUTF8(data)
	if err != nil {
		return nil, fmt.Errorf("muse: apply_updates: %w", err)
	}

	dec := xml.NewDecoder(bytes.NewReader(data))
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)

	var stack []string
	var replaceContent string
	replaceDepth := -1

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode token: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			depth := len(stack)
			if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}
			if newVal, ok := contentUpdates[strings.Join(stack, "/")]; ok {
				replaceContent = newVal
				replaceDepth = depth
			}

		case xml.EndElement:
			// Empty target element: emit the replacement text before closing.
			if replaceDepth == len(stack) && replaceContent != "" {
				if err := enc.EncodeToken(xml.CharData(replaceContent)); err != nil {
					return nil, err
				}
				replaceContent = ""
				replaceDepth = -1
			}
			stack = stack[:len(stack)-1]
			if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}

		case xml.CharData:
			if replaceDepth == len(stack) && replaceContent != "" {
				if err := enc.EncodeToken(xml.CharData(replaceContent)); err != nil {
					return nil, err
				}
				replaceContent = ""
				replaceDepth = -1
			} else if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}

		default:
			if err := enc.EncodeToken(tok); err != nil {
				return nil, err
			}
		}
	}

	if err := enc.Flush(); err != nil {
		return nil, fmt.Errorf("encode flush: %w", err)
	}
	return buf.Bytes(), nil
}
