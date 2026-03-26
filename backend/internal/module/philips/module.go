// Package philips provides the vendor module for Philips SierraECG XML files (format 1.03).
//
// It consolidates file parsing, metadata update, validation, and patient ID
// renaming into a single module.Module implementation.
//
// The module is registered automatically on import via init().
// Add the following blank import to cmd/ecg-hub/main.go:
//
//	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/philips"
package philips

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// Compile-time contract check.
var _ module.Module = (*Module)(nil)

func init() {
	module.Register(&Module{})
}

// Module implements module.Module for Philips SierraECG 1.03 XML files.
type Module struct{}

func (m *Module) Name() string                { return "philips" }
func (m *Module) AcceptedExtensions() []string { return []string{".xml"} }

// Health always returns nil — the Philips module has no external dependencies.
func (m *Module) Health() error { return nil }

// SupportedFormats returns the export formats this module can produce.
func (m *Module) SupportedFormats() []module.ExportFormat {
	return []module.ExportFormat{
		{ID: "original", Label: "Original (SierraECG XML)", Extension: ".xml"},
		{ID: "xmlfda", Label: "FDA HL7 aECG XML", Extension: ".xml"},
		{ID: "dicom", Label: "DICOM ECG", Extension: ".dcm"},
	}
}

// Validate checks that data is a non-empty Philips SierraECG XML file with a patient ID.
// Lighter than Parse — used for quick format rejection before persisting.
func (m *Module) Validate(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("philips: validate: empty data")
	}
	var doc philipsDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		slog.Debug("philips: validate: xml unmarshal failed", "error", err)
		return fmt.Errorf("philips: validate: not a valid Philips XML: %w", err)
	}
	slog.Debug("philips: validate: xml parsed",
		"xml_name_space", doc.XMLName.Space,
		"xml_name_local", doc.XMLName.Local,
		"patient_id", doc.Patient.General.PatientID,
		"doc_type", doc.DocInfo.DocType,
		"doc_version", doc.DocInfo.DocVersion,
	)
	if doc.Patient.General.PatientID == "" {
		return fmt.Errorf("philips: validate: missing patientid (namespace mismatch or missing field)")
	}
	return nil
}

// Parse extracts ECGMetadata from a Philips SierraECG 1.03 XML file.
func (m *Module) Parse(_ context.Context, data []byte) (*module.ECGMetadata, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("philips: parse: empty data")
	}

	var doc philipsDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("philips: parse: xml unmarshal: %w", err)
	}

	patientID := doc.Patient.General.PatientID
	if patientID == "" {
		return nil, fmt.Errorf("philips: parse: missing patientid")
	}

	recordedAt, err := parseDateTime(doc.DataAcq.Date, doc.DataAcq.Time)
	if err != nil {
		// Non-fatal: proceed with zero time.
		recordedAt = time.Time{}
	}

	nrow := doc.ReportInfo.ReportFormat.WaveformFormat.Main.NRow
	ncol := doc.ReportInfo.ReportFormat.WaveformFormat.Main.NColumn
	leadCount := nrow * ncol

	durationMs := doc.Waveforms.Parsed.DurationPerChannel
	var durationSeconds float64
	if durationMs > 0 {
		durationSeconds = float64(durationMs) / 1000.0
	}

	var sampleRate float64
	if sr := doc.DataAcq.SignalChars.SamplingRate; sr > 0 {
		sampleRate = float64(sr)
	}

	extra := map[string]any{
		"document_type":    doc.DocInfo.DocType,
		"document_version": doc.DocInfo.DocVersion,
	}
	if doc.Patient.General.Name.LastName != "" {
		extra["last_name"] = doc.Patient.General.Name.LastName
	}
	if doc.Patient.General.Name.FirstName != "" {
		extra["first_name"] = doc.Patient.General.Name.FirstName
	}
	if doc.Patient.General.Sex != "" {
		extra["sex"] = doc.Patient.General.Sex
	}

	return &module.ECGMetadata{
		PatientID:       patientID,
		RecordedAt:      recordedAt,
		VendorName:      m.Name(),
		SourceFormat:    "philips_sierraecg_xml",
		DeviceModel:     doc.DataAcq.Machine.Detail,
		LeadCount:       leadCount,
		DurationSeconds: durationSeconds,
		SampleRate:      sampleRate,
		Extra:           extra,
	}, nil
}

// UpdateFile reads the Philips XML at filePath, applies the non-nil fields in patch
// via streaming token replacement, and writes the result back to the same path.
func (m *Module) UpdateFile(filePath string, patch module.MetadataPatch) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("philips: update_file: read: %w", err)
	}
	updated, err := applyUpdates(data, patchToMap(patch))
	if err != nil {
		return fmt.Errorf("philips: update_file: %w", err)
	}
	if err := os.WriteFile(filePath, updated, 0o644); err != nil {
		return fmt.Errorf("philips: update_file: write: %w", err)
	}
	return nil
}

// patchToMap converts a MetadataPatch to the string map expected by applyUpdates.
// Only non-nil fields are included.
func patchToMap(patch module.MetadataPatch) map[string]string {
	m := map[string]string{}
	if patch.PatientID != nil {
		m["patient_id"] = *patch.PatientID
	}
	if patch.RecordedAt != nil {
		m["recorded_at"] = patch.RecordedAt.UTC().Format(time.RFC3339)
	}
	if patch.LastName != nil {
		m["last_name"] = *patch.LastName
	}
	if patch.FirstName != nil {
		m["first_name"] = *patch.FirstName
	}
	if patch.Sex != nil {
		m["sex"] = *patch.Sex
	}
	if patch.DeviceModel != nil {
		m["device_model"] = *patch.DeviceModel
	}
	if patch.DocumentType != nil {
		m["document_type"] = *patch.DocumentType
	}
	if patch.DocumentVersion != nil {
		m["document_version"] = *patch.DocumentVersion
	}
	return m
}

// RenamePatientID rewrites the <patientid> element in the Philips XML bytes
// and returns the modified bytes. The input is not modified.
func (m *Module) RenamePatientID(data []byte, newID string) ([]byte, error) {
	if newID == "" {
		return nil, fmt.Errorf("philips: rename_patient_id: newID is empty")
	}
	return applyUpdates(data, map[string]string{"patient_id": newID})
}

// --- XML field path definitions ---

// fieldPathEntry describes how to update a single editable field in the XML.
type fieldPathEntry struct {
	// path is the sequence of XML local element names from root to the target element.
	path []string
	// attr, when non-empty, means update this attribute on the start element at path.
	// When empty, update the text content of the element at path.
	attr string
}

// fieldPaths maps canonical field keys to their XML location in a Philips SierraECG file.
var fieldPaths = map[string]fieldPathEntry{
	"patient_id": {
		path: []string{"restingecgdata", "patient", "generalpatientdata", "patientid"},
	},
	"last_name": {
		path: []string{"restingecgdata", "patient", "generalpatientdata", "name", "lastname"},
	},
	"first_name": {
		path: []string{"restingecgdata", "patient", "generalpatientdata", "name", "firstname"},
	},
	"sex": {
		path: []string{"restingecgdata", "patient", "generalpatientdata", "sex"},
	},
	"document_type": {
		path: []string{"restingecgdata", "documentinfo", "documenttype"},
	},
	"document_version": {
		path: []string{"restingecgdata", "documentinfo", "documentversion"},
	},
	"sample_rate": {
		path: []string{"restingecgdata", "dataacquisition", "signalcharacteristics", "samplingrate"},
	},
	// recorded_at: special-cased below (date + time attributes on <dataacquisition>).
	// device_model: attribute on <machine> inside <dataacquisition>.
	// lead_count: nrow*ncol split — not updated in file (two attributes on separate element).
	// duration_seconds: durationperchannel in ms — not updated in file.
}

var dataAcqPath = []string{"restingecgdata", "dataacquisition"}
var machinePath = []string{"restingecgdata", "dataacquisition", "machine"}

// applyUpdates performs streaming XML token replacement, applying all field values.
func applyUpdates(data []byte, fields map[string]string) ([]byte, error) {
	// Build content-replacement map: pathKey → new value.
	contentUpdates := map[string]string{}
	for fieldKey, val := range fields {
		if val == "" {
			continue
		}
		if entry, ok := fieldPaths[fieldKey]; ok && entry.attr == "" {
			contentUpdates[pathKey(entry.path)] = val
		}
	}

	// Parse recorded_at → date and time strings for attribute update.
	var newDate, newTime string
	if ra, ok := fields["recorded_at"]; ok && ra != "" {
		t, err := time.Parse(time.RFC3339, ra)
		if err == nil {
			newDate = t.UTC().Format("2006-01-02")
			newTime = t.UTC().Format("15:04:05")
		}
	}
	newDeviceModel := fields["device_model"]

	dec := xml.NewDecoder(bytes.NewReader(data))
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)

	var stack []string   // local names of current element ancestry
	var replaceContent string // non-empty: replace next CharData with this
	replaceDepth := -1   // stack depth at which we entered replace mode

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

			// Attribute-based updates.
			if pathEqual(stack, dataAcqPath) && (newDate != "" || newTime != "") {
				t.Attr = updateAttr(t.Attr, "date", newDate)
				t.Attr = updateAttr(t.Attr, "time", newTime)
			}
			if pathEqual(stack, machinePath) && newDeviceModel != "" {
				t.Attr = updateAttr(t.Attr, "detaildescription", newDeviceModel)
			}

			if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}

			// Content-based update: mark next CharData for replacement.
			if newVal, ok := contentUpdates[pathKey(stack)]; ok {
				replaceContent = newVal
				replaceDepth = depth
			}

		case xml.EndElement:
			// If we're closing the replacement target and haven't emitted CharData
			// yet (empty element), emit it now.
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
			} else {
				if err := enc.EncodeToken(t); err != nil {
					return nil, err
				}
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

func pathKey(path []string) string { return strings.Join(path, "/") }

func pathEqual(stack, path []string) bool {
	if len(stack) != len(path) {
		return false
	}
	for i, p := range path {
		if stack[i] != p {
			return false
		}
	}
	return true
}

func updateAttr(attrs []xml.Attr, key, val string) []xml.Attr {
	if val == "" {
		return attrs
	}
	for i, a := range attrs {
		if a.Name.Local == key {
			attrs[i].Value = val
			return attrs
		}
	}
	return append(attrs, xml.Attr{Name: xml.Name{Local: key}, Value: val})
}

func parseDateTime(date, t string) (time.Time, error) {
	if date == "" {
		return time.Time{}, fmt.Errorf("philips: empty acquisition date")
	}
	layout := "2006-01-02 15:04:05"
	parsed, err := time.ParseInLocation(layout, date+" "+t, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("philips: parse datetime %q %q: %w", date, t, err)
	}
	return parsed, nil
}

// --- Philips SierraECG 1.03 XML document structure ---

type philipsDoc struct {
	XMLName xml.Name `xml:"http://www3.medical.philips.com restingecgdata"`

	DocInfo struct {
		DocType    string `xml:"documenttype"`
		DocVersion string `xml:"documentversion"`
	} `xml:"documentinfo"`

	DataAcq struct {
		Date string `xml:"date,attr"`
		Time string `xml:"time,attr"`

		Machine struct {
			Detail string `xml:"detaildescription,attr"`
		} `xml:"machine"`

		SignalChars struct {
			SamplingRate int `xml:"samplingrate"`
		} `xml:"signalcharacteristics"`
	} `xml:"dataacquisition"`

	ReportInfo struct {
		ReportFormat struct {
			WaveformFormat struct {
				Main struct {
					NRow    int `xml:"nrow,attr"`
					NColumn int `xml:"ncolumn,attr"`
				} `xml:"mainwaveformformat"`
			} `xml:"waveformformat"`
		} `xml:"reportformat"`
	} `xml:"reportinfo"`

	Patient struct {
		General struct {
			PatientID string `xml:"patientid"`
			Name      struct {
				LastName  string `xml:"lastname"`
				FirstName string `xml:"firstname"`
			} `xml:"name"`
			Sex string `xml:"sex"`
		} `xml:"generalpatientdata"`
	} `xml:"patient"`

	Waveforms struct {
		Parsed struct {
			DurationPerChannel int `xml:"durationperchannel,attr"`
		} `xml:"parsedwaveforms"`
	} `xml:"waveforms"`
}
