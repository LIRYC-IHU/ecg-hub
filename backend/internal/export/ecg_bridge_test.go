package export

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ─── ECGBridge.ConvertToXMLFDA ────────────────────────────────────────────────

func TestConvertToXMLFDA_UnsupportedVendor(t *testing.T) {
	bridge := NewECGBridge(map[string]string{"philips:xmlfda": "philips-to-fda"}, 5*time.Second)
	_, err := bridge.ConvertToXMLFDA(context.Background(), "/some/file.dcm", "dicom", nil)
	if err == nil {
		t.Fatal("expected error for unsupported vendor")
	}
	if !errors.Is(err, ErrFormatNotSupported) {
		t.Errorf("expected ErrFormatNotSupported, got %v", err)
	}
}

func TestConvertToXMLFDA_BinaryNotFound(t *testing.T) {
	// Use a binary name that doesn't exist on PATH.
	bridge := NewECGBridge(map[string]string{"philips:xmlfda": "no-such-binary-xyz"}, 5*time.Second)
	_, err := bridge.ConvertToXMLFDA(context.Background(), "/some/file.xml", "philips", nil)
	if err == nil {
		t.Fatal("expected error when binary is missing")
	}
	if !errors.Is(err, ErrConversionFailed) {
		t.Errorf("expected ErrConversionFailed, got %v", err)
	}
}

// ─── ConvertOptions: anonymize flag + HL7 metadata injection ─────────────────

// writeEchoScript creates a shell script that prints its arguments and stdin,
// standing in for a converter binary.
func writeEchoScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/fake-converter"
	script := "#!/bin/sh\nprintf 'ARGS:%s\\n' \"$*\"\ncat\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// writeInputFile creates a real source file: Convert validates that the input
// path names an existing regular file before invoking the converter.
func writeInputFile(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/in.xml"
	if err := os.WriteFile(path, []byte("<xml/>"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}
	return path
}

func TestConvert_AnonymizeFlagPassed(t *testing.T) {
	bin := writeEchoScript(t)
	bridge := NewECGBridge(map[string]string{"philips:xmlfda": bin}, 5*time.Second)

	out, err := bridge.Convert(context.Background(), writeInputFile(t), "philips", "xmlfda", nil,
		ConvertOptions{Anonymize: true})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !strings.Contains(string(out), "--anonymize") {
		t.Errorf("expected --anonymize in args, got: %s", out)
	}
}

func TestConvert_InjectPatientStdin(t *testing.T) {
	bin := writeEchoScript(t)
	bridge := NewECGBridge(map[string]string{"philips:xmlfda": bin}, 5*time.Second)

	patient := &models.Patient{PatientID: "P42", FirstName: "John", LastName: "DOE", Gender: "M"}
	out, err := bridge.Convert(context.Background(), writeInputFile(t), "philips", "xmlfda", patient,
		ConvertOptions{InjectPatient: true})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, want := range []string{`"patientID":"P42"`, `"patientName":"DOE^John"`, `"gender":"M"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("stdin JSON should contain %s, got: %s", want, out)
		}
	}
	if strings.Contains(string(out), "--anonymize") {
		t.Errorf("anonymize must not be passed when not requested")
	}
}

func TestConvert_NoOptions_NoExtraArgsNoStdin(t *testing.T) {
	bin := writeEchoScript(t)
	bridge := NewECGBridge(map[string]string{"philips:xmlfda": bin}, 5*time.Second)

	out, err := bridge.Convert(context.Background(), writeInputFile(t), "philips", "xmlfda",
		&models.Patient{PatientID: "P1"}, ConvertOptions{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if strings.Contains(string(out), "--anonymize") || strings.Contains(string(out), "patientID") {
		t.Errorf("zero options must not alter args or stdin, got: %s", out)
	}
}

// --- identity provenance ---------------------------------------------------

// A rendered document must carry the identity marking unless the identity on it
// came from the HIS. Both halves matter: knowing the HIS demographics is not
// the same as putting them on the document.
func TestIdentityConfirmed(t *testing.T) {
	his := &models.Patient{PatientID: "1", HL7Source: "ADT^A19"}
	deviceOnly := &models.Patient{PatientID: "1"}

	for _, tc := range []struct {
		name    string
		patient *models.Patient
		opts    ConvertOptions
		want    bool
	}{
		{"HIS demographics injected", his, ConvertOptions{InjectPatient: true}, true},
		{"HIS known but not injected", his, ConvertOptions{}, false},
		{"injected but never enriched", deviceOnly, ConvertOptions{InjectPatient: true}, false},
		{"neither", deviceOnly, ConvertOptions{}, false},
		{"no patient at all", nil, ConvertOptions{InjectPatient: true}, false},
		{"anonymised", his, ConvertOptions{Anonymize: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := identityConfirmed(tc.patient, tc.opts); got != tc.want {
				t.Errorf("identityConfirmed() = %v, want %v", got, tc.want)
			}
		})
	}
}
