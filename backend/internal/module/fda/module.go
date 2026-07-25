// Package fda provides the vendor module for FDA HL7 v3 aECG XML files
// (root element <AnnotatedECG>, namespace urn:hl7-org:v3).
//
// Some devices (e.g. Mindray BeneHeart) export the FDA aECG XML directly instead
// of a vendor-native format. Such files share the ".xml" extension with MUSE
// (<RestingECG>) and Philips (<restingecgdata>); the ingestion router probes
// Validate() on each candidate, and only this module accepts the <AnnotatedECG>
// root, so the three are unambiguous.
//
// Metadata extraction is delegated to the fda-to-dicom converter binary via its
// --metadata-json flag; export to DICOM is delegated to fda-to-dicom and PDF to
// fda-to-pdf through the ECGBridge.
//
// The module is registered automatically on import via init().
// Add the following blank import to cmd/ecg-hub/main.go:
//
//	_ "github.com/LIRYC-IHU/ecg-hub/internal/module/fda"
package fda

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

var fdaToDICOMBin = bridgeutil.ResolveBin("BRIDGE_FDA_TO_DICOM", "fda-to-dicom")

// Module implements module.Module for FDA HL7 v3 aECG XML files.
type Module struct{}

func (m *Module) Name() string                 { return "fda" }
func (m *Module) AcceptedExtensions() []string { return []string{".xml"} }

// Health always returns nil — the FDA module has no external dependencies
// beyond the converter binary, which is exercised on demand.
func (m *Module) Health() error { return nil }

// SupportedFormats returns the export formats this module can produce.
// "original" is the FDA aECG XML itself; the ECGBridge is the source of truth
// for which conversion formats are actually available at runtime.
func (m *Module) SupportedFormats() []module.ExportFormat {
	return []module.ExportFormat{
		{ID: "original", Label: "Original (FDA HL7 aECG XML)", Extension: ".xml"},
		{ID: "dicom", Label: "DICOM ECG", Extension: ".dcm"},
	}
}

// Validate confirms the file is an FDA aECG document — format identity only.
// It sniffs the XML root element (<AnnotatedECG> in the urn:hl7-org:v3 namespace)
// without a full unmarshal, so the router can deterministically pick this module
// over MUSE (root <RestingECG>) and Philips (root <restingecgdata>) for .xml files.
//
// The patient ID is intentionally NOT checked here: an FDA file without a patient
// ID is still an FDA file and must route to this module so the ingestion pipeline
// can send it to the "unidentified" review queue rather than mis-routing it.
func (m *Module) Validate(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("fda: validate: empty data")
	}
	root, err := xmlRootElement(data)
	if err != nil {
		return fmt.Errorf("fda: validate: not valid XML: %w", err)
	}
	if !strings.EqualFold(root.Local, "AnnotatedECG") {
		return fmt.Errorf("fda: validate: not an FDA aECG file (root <%s>)", root.Local)
	}
	return nil
}

// xmlRootElement returns the name (namespace + local) of the first XML start element,
// without decoding the whole document.
func xmlRootElement(data []byte) (xml.Name, error) {
	return xmlutil.RootElement(data)
}

// fdaMetadataJSON matches the flat JSON output of fda-to-dicom --metadata-json.
type fdaMetadataJSON struct {
	PatientID    string  `json:"patientID"`
	PatientName  string  `json:"patientName"` // "LAST^FIRST"
	Gender       string  `json:"gender"`      // "M" / "F"
	BirthDate    string  `json:"birthDate"`   // YYYYMMDD
	Age          string  `json:"age"`
	Manufacturer string  `json:"manufacturer"`
	DeviceModel  string  `json:"deviceModel"`
	SerialNumber string  `json:"serialNumber"`
	SoftwareVer  string  `json:"softwareVer"`
	Location     string  `json:"location"`
	SampleRate   float64 `json:"sampleRate"`
	HeartRate    float64 `json:"heartRate"`
	LeadsCount   int     `json:"leadsCount"`
	Datetime     string  `json:"datetime"` // YYYYMMDDHHMMSS (or YYYYMMDD)
}

// Parse extracts ECGMetadata from an FDA aECG XML file by delegating to
// fda-to-dicom --metadata-json.
func (m *Module) Parse(ctx context.Context, data []byte) (*module.ECGMetadata, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("fda: parse: empty data")
	}

	// The converter binary assumes UTF-8; transcode non-UTF-8 vendor exports so it
	// does not reject the declared encoding.
	data, err := xmlutil.ToUTF8(data)
	if err != nil {
		return nil, fmt.Errorf("fda: parse: %w", err)
	}

	// The converter requires a file path, so spill to a temp file.
	tmp, err := os.CreateTemp("", "fda-parse-*.xml")
	if err != nil {
		return nil, fmt.Errorf("fda: parse: create temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("fda: parse: write temp: %w", err)
	}
	tmp.Close()

	cmd := exec.CommandContext(ctx, fdaToDICOMBin, "--input", tmp.Name(), "--metadata-json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("fda: parse: fda-to-dicom: %s: %w", stderr.String(), err)
	}

	var md fdaMetadataJSON
	if err := json.Unmarshal(stdout.Bytes(), &md); err != nil {
		return nil, fmt.Errorf("fda: parse: json decode: %w", err)
	}
	// A missing patient ID is not a parse failure: the ingestion router sends the
	// parsed metadata (PatientID == "") to the "unidentified" review queue.

	extra := map[string]any{}
	if md.Manufacturer != "" {
		extra["manufacturer"] = md.Manufacturer
	}
	if md.SerialNumber != "" {
		extra["serial_number"] = md.SerialNumber
	}
	if md.SoftwareVer != "" {
		extra["software_version"] = md.SoftwareVer
	}
	if md.Location != "" {
		extra["location"] = md.Location
	}
	if md.Gender != "" {
		extra["sex"] = md.Gender
	}
	if md.Age != "" {
		extra["patient_age"] = md.Age
	}
	if md.BirthDate != "" {
		extra["birth_date"] = md.BirthDate
	}
	if md.HeartRate > 0 {
		extra["heart_rate"] = md.HeartRate
	}
	if last, first := splitName(md.PatientName); last != "" || first != "" {
		if last != "" {
			extra["last_name"] = last
		}
		if first != "" {
			extra["first_name"] = first
		}
		extra["patient_name"] = md.PatientName
	}

	return &module.ECGMetadata{
		PatientID:    strings.TrimSpace(md.PatientID),
		RecordedAt:   parseFDADateTime(md.Datetime),
		VendorName:   m.Name(),
		SourceFormat: "fda_aecg_xml",
		DeviceModel:  strings.TrimSpace(md.DeviceModel),
		LeadCount:    md.LeadsCount,
		SampleRate:   md.SampleRate,
		Extra:        extra,
	}, nil
}

// parseFDADateTime parses the fda-to-dicom datetime field, which is the HL7
// effectiveTime concatenated as YYYYMMDDHHMMSS (or YYYYMMDD when no time).
// Returns the zero time when absent or unparseable.
func parseFDADateTime(dt string) time.Time {
	dt = strings.TrimSpace(dt)
	switch {
	case dt == "":
		return time.Time{}
	case len(dt) >= 14:
		if parsed, err := time.ParseInLocation("20060102150405", dt[:14], time.UTC); err == nil {
			return parsed
		}
	case len(dt) >= 8:
		if parsed, err := time.ParseInLocation("20060102", dt[:8], time.UTC); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// splitName splits an HL7 "LAST^FIRST" name into (last, first).
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

// UpdateFile reads the FDA aECG XML at filePath, applies the non-nil fields in
// patch via streaming token replacement, and writes the result back to the same path.
func (m *Module) UpdateFile(filePath string, patch module.MetadataPatch) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("fda: update_file: read: %w", err)
	}
	updated, err := applyUpdates(data, patchToMap(patch))
	if err != nil {
		return fmt.Errorf("fda: update_file: %w", err)
	}
	if err := os.WriteFile(filePath, updated, 0o644); err != nil {
		return fmt.Errorf("fda: update_file: write: %w", err)
	}
	return nil
}

// RenamePatientID rewrites the <PatientID> element in the FDA aECG XML bytes and
// returns the modified bytes. The input is not modified.
func (m *Module) RenamePatientID(data []byte, newID string) ([]byte, error) {
	if strings.TrimSpace(newID) == "" {
		return nil, fmt.Errorf("fda: rename_patient_id: newID is empty")
	}
	return applyUpdates(data, map[string]string{"patient_id": newID})
}

// patchToMap converts a MetadataPatch to the string map expected by applyUpdates.
// Only non-nil fields are included. The FDA aECG name is a single <name> element,
// so LastName/FirstName are combined into the HL7 "LAST^FIRST" form.
func patchToMap(patch module.MetadataPatch) map[string]string {
	out := map[string]string{}
	if patch.PatientID != nil {
		out["patient_id"] = *patch.PatientID
	}
	last, first := "", ""
	if patch.LastName != nil {
		last = *patch.LastName
	}
	if patch.FirstName != nil {
		first = *patch.FirstName
	}
	if patch.LastName != nil || patch.FirstName != nil {
		if first != "" {
			out["name"] = last + "^" + first
		} else {
			out["name"] = last
		}
	}
	if patch.Sex != nil {
		out["gender"] = fdaGender(*patch.Sex)
	}
	return out
}

// fdaGender maps the canonical "M"/"F" sex code to the HL7 AdministrativeGender code.
func fdaGender(sex string) string {
	switch strings.ToUpper(strings.TrimSpace(sex)) {
	case "M", "MALE":
		return "M"
	case "F", "FEMALE":
		return "F"
	default:
		return sex
	}
}

// tailPaths maps canonical field keys to the trailing element path that uniquely
// identifies the target inside the FDA aECG document. Matching the tail (rather
// than the full path from root) handles both the componentOf and direct-subject
// document shapes.
var tailPaths = map[string][]string{
	"patient_id": {"subjectDemographicPerson", "PatientID"},
	"name":       {"subjectDemographicPerson", "name"},
}

// genderTail targets the administrativeGenderCode element whose "code" attribute
// carries the sex value.
var genderTail = []string{"subjectDemographicPerson", "administrativeGenderCode"}

// applyUpdates performs streaming XML token replacement. Text content updates
// (patient_id, name) and the gender attribute are applied in a single pass.
// Targets not present in the document are skipped (best-effort).
func applyUpdates(data []byte, fields map[string]string) ([]byte, error) {
	contentUpdates := map[string]struct{ value string }{}
	for key, val := range fields {
		if val == "" && key != "patient_id" {
			continue
		}
		if tail, ok := tailPaths[key]; ok {
			contentUpdates[strings.Join(tail, "/")] = struct{ value string }{val}
		}
	}
	newGender := fields["gender"]

	if len(contentUpdates) == 0 && newGender == "" {
		return data, nil
	}

	// Token streaming below uses the stdlib decoder/encoder; transcode non-UTF-8
	// input to UTF-8 first so the rewritten file is valid and self-consistent.
	data, err := xmlutil.ToUTF8(data)
	if err != nil {
		return nil, fmt.Errorf("fda: apply_updates: %w", err)
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

			// Gender attribute update on subjectDemographicPerson/administrativeGenderCode.
			if newGender != "" && stackHasTail(stack, genderTail) {
				t.Attr = updateAttr(t.Attr, "code", newGender)
			}

			if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}

			for tailKey, upd := range contentUpdates {
				if stackHasTail(stack, strings.Split(tailKey, "/")) {
					replaceContent = upd.value
					replaceDepth = depth
				}
			}

		case xml.EndElement:
			// Empty target element: emit the replacement text before closing.
			if replaceDepth == len(stack) {
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
			if replaceDepth == len(stack) {
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

// stackHasTail reports whether stack ends with the given tail element sequence.
func stackHasTail(stack, tail []string) bool {
	if len(stack) < len(tail) {
		return false
	}
	off := len(stack) - len(tail)
	for i, name := range tail {
		if stack[off+i] != name {
			return false
		}
	}
	return true
}

// updateAttr sets attr key to val on the element, adding it when absent.
func updateAttr(attrs []xml.Attr, key, val string) []xml.Attr {
	for i, a := range attrs {
		if a.Name.Local == key {
			attrs[i].Value = val
			return attrs
		}
	}
	return append(attrs, xml.Attr{Name: xml.Name{Local: key}, Value: val})
}
