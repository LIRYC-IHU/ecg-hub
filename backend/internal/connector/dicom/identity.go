package dicom

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"strings"

	dicomlib "github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/dicomsop"
)

// coerceIdentity writes ECG Hub's patient identity over the one the device
// recorded (PS3.4 B.4.1.3 "coercion"), for connectors that manage identity.
//
// Patient ID always comes from the hub: it is what the ECG is attached to.
// Name, birth date and sex come from the patient record when it holds them; a
// value the hub does not know is left as the device wrote it rather than
// blanked, since an empty field tells the archive less than the device's.
//
// The file is rewritten by ECG Hub, so its File Meta names ECG Hub as the
// implementation that wrote it (PS3.10 7.1), not the library's default.
func coerceIdentity(data []byte, patientID string, p *models.Patient) ([]byte, error) {
	ds, err := dicomlib.ParseUntilEOF(bytes.NewReader(data), nil)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	set := map[tag.Tag]string{
		tag.PatientID:                 patientID,
		tag.ImplementationClassUID:    dicomsop.ImplementationClassUID,
		tag.ImplementationVersionName: dicomsop.ImplementationVersionName,
	}
	if p != nil {
		if name := dicomPersonName(p.LastName, p.FirstName); name != "" {
			set[tag.PatientName] = name
		}
		if p.DateOfBirth != nil && !p.DateOfBirth.IsZero() {
			set[tag.PatientBirthDate] = p.DateOfBirth.Format("20060102")
		}
		if sex := dicomSex(p.Gender); sex != "" {
			set[tag.PatientSex] = sex
		}
	}

	for t, v := range set {
		elem, err := dicomlib.NewElement(t, []string{v})
		if err != nil {
			return nil, fmt.Errorf("new element %v: %w", t, err)
		}
		replaced := false
		for i, e := range ds.Elements {
			if e.Tag == t {
				ds.Elements[i] = elem
				replaced = true
				break
			}
		}
		if !replaced {
			ds.Elements = append(ds.Elements, elem)
		}
	}

	var buf bytes.Buffer
	if err := dicomlib.Write(&buf, ds); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	return buf.Bytes(), nil
}

// dicomPersonName builds a PN value, Family^Given (PS3.5 6.2.1).
func dicomPersonName(last, first string) string {
	last, first = strings.TrimSpace(last), strings.TrimSpace(first)
	switch {
	case last == "" && first == "":
		return ""
	case first == "":
		return last
	default:
		return last + "^" + first
	}
}

// dicomSex maps an HL7 administrative sex (PID-8) to Patient's Sex, whose only
// defined terms are M, F and O (PS3.3 C.7.1.1). Unknown maps to nothing, so the
// device's value is kept.
func dicomSex(hl7 string) string {
	switch strings.ToUpper(strings.TrimSpace(hl7)) {
	case "M":
		return "M"
	case "F":
		return "F"
	case "O", "A", "N":
		return "O"
	default:
		return ""
	}
}

// withHubIdentity returns a temporary copy of the DICOM file at filePath
// carrying the hub's identity for ecg. The cleanup removes it; never nil.
//
// A file with no patient ID — one quarantined because it did not parse — has
// no identity to give and is proxied as received. A coercion that fails is an
// error, not a fallback to the device's identity: sending it anyway would
// break the very promise the option makes.
func (c *DICOMConnector) withHubIdentity(ecg *models.ECG, filePath string) (string, func(), error) {
	noop := func() {}
	if ecg.PatientID == "" {
		return filePath, noop, nil
	}

	var patient *models.Patient
	if c.patients != nil {
		if p, err := c.patients.FindByPatientID(ecg.PatientID); err == nil {
			patient = p
		} else {
			slog.Warn("connector: patient lookup failed, coercing the Patient ID only",
				"connector", c.Name(), "ecg_id", ecg.ID, "patient_id", ecg.PatientID, "error", err)
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", noop, fmt.Errorf("dicom[%s]: read %s: %w", c.cfg.Name, ecg.OriginalFilename, err)
	}
	out, err := coerceIdentity(data, ecg.PatientID, patient)
	if err != nil {
		return "", noop, fmt.Errorf("dicom[%s]: write the hub's identity into %s: %w", c.cfg.Name, ecg.OriginalFilename, err)
	}

	tmp, err := os.CreateTemp("", "proxy-*.dcm")
	if err != nil {
		return "", noop, fmt.Errorf("dicom[%s]: create temp dicom: %w", c.cfg.Name, err)
	}
	cleanup := func() { os.Remove(tmp.Name()) }
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		cleanup()
		return "", noop, fmt.Errorf("dicom[%s]: write temp dicom: %w", c.cfg.Name, err)
	}
	tmp.Close()

	slog.Info("connector: hub identity written before forwarding",
		"connector", c.Name(), "ecg_id", ecg.ID, "patient_id", ecg.PatientID,
		"demographics", patient != nil)
	return tmp.Name(), cleanup, nil
}
