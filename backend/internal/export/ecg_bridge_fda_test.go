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

// writeFakeRenderer stands in for fda-to-pdf: it records the arguments it was
// given into the file named by -o, which is what convertToPDF reads back.
func writeFakeRenderer(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/fake-renderer"
	script := `#!/bin/sh
all="$*"
out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then out="$a"; fi
  prev="$a"
done
printf 'ARGS:%s\n' "$all" > "$out"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write renderer: %v", err)
	}
	return path
}

// An ECG ingested as FDA aECG XML is already in the format the renderer reads.
// Requiring an "fda:xmlfda" converter for it meant the one vendor closest to the
// renderer's own input was the only one that could not be rendered.

func TestSupportsFormat_PDFFromAnFDASource(t *testing.T) {
	// No fda:xmlfda entry, on purpose: there is no such converter and there is
	// no reason for one.
	bridge := NewECGBridge(map[string]string{"philips:xmlfda": "philips-to-fda"}, time.Second).
		WithPDFBinary("fda-to-pdf")

	if !bridge.SupportsFormat("fda", "pdf") {
		t.Error("an FDA source should be renderable without a conversion step")
	}
	if !bridge.SupportsFormat("philips", "pdf") {
		t.Error("a vendor with an xmlfda converter should still be renderable")
	}
	if bridge.SupportsFormat("mindray", "pdf") {
		t.Error("a vendor with no xmlfda converter cannot be rendered")
	}
}

func TestSupportsFormat_NoRendererMeansNoPDF(t *testing.T) {
	// Without the renderer binary nothing is renderable, the FDA shortcut
	// included — it skips the conversion step, not the rendering one.
	bridge := NewECGBridge(map[string]string{"philips:xmlfda": "philips-to-fda"}, time.Second)

	for _, vendor := range []string{"fda", "philips"} {
		if bridge.SupportsFormat(vendor, "pdf") {
			t.Errorf("%s:pdf reported as supported with no fda-to-pdf binary", vendor)
		}
	}
}

func TestConvertToPDF_FDASourceIsNotAnonymisable(t *testing.T) {
	// Anonymisation is applied by the xmlfda step, which an FDA source skips.
	// Ignoring the option would hand back a file the caller believes is
	// anonymous while it still carries the patient's name and identifier — so
	// the request is refused instead.
	bridge := NewECGBridge(map[string]string{}, time.Second).WithPDFBinary(writeEchoScript(t))

	_, err := bridge.Convert(context.Background(), writeInputFile(t), "fda", "pdf", nil,
		ConvertOptions{Anonymize: true})

	if !errors.Is(err, ErrFormatNotSupported) {
		t.Fatalf("error = %v, want ErrFormatNotSupported", err)
	}
	if !strings.Contains(err.Error(), "anonymised") {
		t.Errorf("error should say why: %v", err)
	}
}

func TestConvertToPDF_FDASourceIsAlwaysMarkedUnverified(t *testing.T) {
	// No converter rewrites an FDA aECG, so the demographics on the page are the
	// acquisition device's whatever the caller asked for. identityConfirmed must
	// therefore stay false even for an HL7-enriched patient, and the renderer be
	// told the identity is unverified. Reporting a confirmation that never
	// happened is the failure that matters here.
	bridge := NewECGBridge(map[string]string{}, 5*time.Second).WithPDFBinary(writeFakeRenderer(t))

	patient := &models.Patient{PatientID: "P1", LastName: "BLIN", HL7Source: "his.chu.local"}
	out, err := bridge.Convert(context.Background(), writeInputFile(t), "fda", "pdf", patient,
		ConvertOptions{InjectPatient: true})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(string(out), "-identity-unverified") {
		t.Errorf("renderer was not told the identity is unverified:\n%s", out)
	}
}

func TestConvertToPDF_FDASourceIsRenderedInPlace(t *testing.T) {
	// The source file itself must reach the renderer: no conversion step, and no
	// temporary copy that would leave the "-i" path pointing at something else.
	bridge := NewECGBridge(map[string]string{}, 5*time.Second).WithPDFBinary(writeFakeRenderer(t))

	src := writeInputFile(t)
	out, err := bridge.Convert(context.Background(), src, "fda", "pdf", nil, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(string(out), "-i "+src) {
		t.Errorf("renderer did not read the source file directly:\n%s", out)
	}
	// A derived file must never appear in the ECG storage volume beside the
	// original, not even briefly.
	if _, statErr := os.Stat(src + ".pdf"); statErr == nil {
		t.Error("the render was written next to the stored ECG")
	}
}

func TestConvertToPDF_OtherVendorsStillNeedTheirConverter(t *testing.T) {
	// The shortcut is for FDA sources only — every other vendor still goes
	// through its xmlfda converter, which is what applies anonymise and inject.
	bridge := NewECGBridge(map[string]string{}, time.Second).WithPDFBinary(writeEchoScript(t))

	_, err := bridge.Convert(context.Background(), writeInputFile(t), "philips", "pdf", nil, ConvertOptions{})
	if !errors.Is(err, ErrFormatNotSupported) {
		t.Fatalf("error = %v, want ErrFormatNotSupported", err)
	}
}

// A document that carries the establishment's name next to the acquisition
// device's age is a wrong-patient hazard: the date of birth is precisely what a
// clinician cross-checks against the name, so the two have to come from the same
// source or not be shown at all.
func TestBuildInjectJSON_BirthDateTravelsWithTheName(t *testing.T) {
	dob := time.Date(1968, 4, 22, 0, 0, 0, 0, time.UTC)
	got := string(buildInjectJSON(&models.Patient{
		PatientID: "BS1170", LastName: "Fontaine", FirstName: "Sébastien",
		Gender: "M", DateOfBirth: &dob,
	}))

	for _, want := range []string{
		`"patientID":"BS1170"`,
		`"patientName":"Fontaine^Sébastien"`,
		`"gender":"M"`,
		`"birthDate":"19680422"`,
		// Present and empty: in this protocol a field that is present overwrites,
		// so this clears the file's age instead of leaving it to contradict the
		// date of birth above it.
		`"age":""`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("inject payload missing %s\n  got: %s", want, got)
		}
	}
}

func TestBuildInjectJSON_NoBirthDateLeavesTheFileAlone(t *testing.T) {
	// Only fields the HIS actually provided are overwritten; an unknown date of
	// birth must not blank out what the device recorded.
	got := string(buildInjectJSON(&models.Patient{PatientID: "P1", LastName: "Doe"}))

	for _, unwanted := range []string{"birthDate", "age"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("payload should not mention %q when the HIS gave no date of birth: %s", unwanted, got)
		}
	}
}
