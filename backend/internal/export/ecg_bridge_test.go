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

func TestConvert_AnonymizeFlagPassed(t *testing.T) {
	bin := writeEchoScript(t)
	bridge := NewECGBridge(map[string]string{"philips:xmlfda": bin}, 5*time.Second)

	out, err := bridge.Convert(context.Background(), "/in.xml", "philips", "xmlfda", nil,
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
	out, err := bridge.Convert(context.Background(), "/in.xml", "philips", "xmlfda", patient,
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

	out, err := bridge.Convert(context.Background(), "/in.xml", "philips", "xmlfda",
		&models.Patient{PatientID: "P1"}, ConvertOptions{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if strings.Contains(string(out), "--anonymize") || strings.Contains(string(out), "patientID") {
		t.Errorf("zero options must not alter args or stdin, got: %s", out)
	}
}
