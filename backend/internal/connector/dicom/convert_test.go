package dicom

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
)

// A C-STORE peer speaks DICOM and nothing else. A vendor file accepted by the
// extension filter — everything, or .xml, or .dat — has to be converted before
// it can be sent; handing it over raw produced a rejection from the PACS, which
// is a failure reported by the wrong side of the link.

type fakeConverter struct {
	supported map[string]bool
	calls     int
	gotVendor string
	gotOpts   export.ConvertOptions
	gotPath   string
	out       []byte
	err       error
}

func (f *fakeConverter) SupportsFormat(vendor, format string) bool {
	return format == "dicom" && f.supported[vendor]
}

func (f *fakeConverter) Convert(_ context.Context, sourcePath, vendor, _ string,
	_ *models.Patient, opts export.ConvertOptions) ([]byte, error) {
	f.calls++
	f.gotVendor = vendor
	f.gotOpts = opts
	f.gotPath = sourcePath
	return f.out, f.err
}

type fakePatients struct {
	patient *models.Patient
	err     error
}

func (f *fakePatients) FindByPatientID(string) (*models.Patient, error) {
	return f.patient, f.err
}

func newTestConnector(t *testing.T) *DICOMConnector {
	t.Helper()
	c, err := New(connector.Config{
		Name:     "pacs",
		Protocol: "dicom_cstore",
		DICOM:    connector.DICOMEndpoint{Host: "127.0.0.1", Port: 11112, CallingAE: "HUB", CalledAE: "PACS"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func writeFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("source"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestMaterializeDICOM_AlreadyDICOMIsSentAsIs(t *testing.T) {
	conv := &fakeConverter{supported: map[string]bool{"philips": true}}
	c := newTestConnector(t).WithConverter(conv, nil)
	src := writeFile(t, "a.dcm")

	got, cleanup, err := c.materializeDICOM(context.Background(),
		&models.ECG{Vendor: "philips", OriginalFilename: "a.dcm"}, src)
	defer cleanup()

	if err != nil {
		t.Fatalf("materializeDICOM: %v", err)
	}
	if got != src {
		t.Errorf("path = %q, want the source %q", got, src)
	}
	if conv.calls != 0 {
		t.Error("converted a file that was already DICOM")
	}
}

func TestMaterializeDICOM_ConvertsAVendorFile(t *testing.T) {
	conv := &fakeConverter{supported: map[string]bool{"philips": true}, out: []byte("DICM-bytes")}
	patient := &models.Patient{PatientID: "BS1172", LastName: "Petit"}
	c := newTestConnector(t).WithConverter(conv, &fakePatients{patient: patient})
	src := writeFile(t, "a.xml")

	got, cleanup, err := c.materializeDICOM(context.Background(),
		&models.ECG{Vendor: "philips", OriginalFilename: "a.xml", PatientID: "BS1172"}, src)
	defer cleanup()

	if err != nil {
		t.Fatalf("materializeDICOM: %v", err)
	}
	if got == src {
		t.Fatal("the source path was returned unconverted")
	}
	body, readErr := os.ReadFile(got)
	if readErr != nil || string(body) != "DICM-bytes" {
		t.Errorf("converted file = %q (%v), want the converter's output", body, readErr)
	}
	if conv.gotVendor != "philips" || conv.gotPath != src {
		t.Errorf("converted %q from %q, want philips from %q", conv.gotVendor, conv.gotPath, src)
	}
	// The whole reason for pairing this with the wait-for-HL7 option: the
	// document must carry the establishment's demographics.
	if !conv.gotOpts.InjectPatient {
		t.Error("demographics were not injected although the patient was found")
	}

	// The converted file is temporary and must not survive the forward.
	cleanup()
	if _, statErr := os.Stat(got); statErr == nil {
		t.Error("the converted file was left behind")
	}
}

func TestMaterializeDICOM_ConvertsWithoutAPatient(t *testing.T) {
	// A missing patient row is not a reason to drop a trace: the document goes
	// out carrying what the acquisition device recorded.
	conv := &fakeConverter{supported: map[string]bool{"muse": true}, out: []byte("DICM")}
	c := newTestConnector(t).WithConverter(conv, &fakePatients{patient: nil})

	_, cleanup, err := c.materializeDICOM(context.Background(),
		&models.ECG{Vendor: "muse", OriginalFilename: "a.xml"}, writeFile(t, "a.xml"))
	defer cleanup()

	if err != nil {
		t.Fatalf("materializeDICOM: %v", err)
	}
	if conv.gotOpts.InjectPatient {
		t.Error("injection was requested with no patient to inject")
	}
}

func TestMaterializeDICOM_Refusals(t *testing.T) {
	// Each of these fails the job, which retries and then exhausts with the
	// reason visible in the history — rather than sending something a PACS will
	// reject, or dropping the file silently.
	tests := []struct {
		name    string
		conv    Converter
		ecg     *models.ECG
		wantErr string
	}{
		{
			name:    "no converter configured",
			conv:    nil,
			ecg:     &models.ECG{Vendor: "philips", OriginalFilename: "a.xml"},
			wantErr: "no converter is configured",
		},
		{
			// What a quarantined file looks like: nothing recognised the format.
			name:    "no vendor to convert from",
			conv:    &fakeConverter{supported: map[string]bool{"philips": true}},
			ecg:     &models.ECG{Vendor: "", OriginalFilename: "a.xml"},
			wantErr: "no vendor",
		},
		{
			name:    "vendor has no DICOM converter",
			conv:    &fakeConverter{supported: map[string]bool{"philips": true}},
			ecg:     &models.ECG{Vendor: "fukuda", OriginalFilename: "a.ecg"},
			wantErr: "no DICOM converter for vendor",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestConnector(t)
			if tc.conv != nil {
				c = c.WithConverter(tc.conv, nil)
			}
			_, cleanup, err := c.materializeDICOM(context.Background(), tc.ecg, writeFile(t, "a.xml"))
			defer cleanup()

			if err == nil {
				t.Fatal("no error — a non-DICOM file would have been handed to the association")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
