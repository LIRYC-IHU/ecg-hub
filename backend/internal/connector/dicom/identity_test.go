package dicom

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	dicomlib "github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/dicomsop"
	"github.com/LIRYC-IHU/ecg-hub/internal/ecgwaveform"
)

func tagValue(t *testing.T, data []byte, tg tag.Tag) string {
	t.Helper()
	ds, err := dicomlib.ParseUntilEOF(bytes.NewReader(data), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	e, err := ds.FindElementByTag(tg)
	if err != nil {
		return ""
	}
	if v, ok := e.Value.GetValue().([]string); ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

func sample(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/test_ecg.dcm")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCoerceIdentity_WritesTheHubsIdentity(t *testing.T) {
	src := sample(t)
	dob := time.Date(1961, 3, 7, 0, 0, 0, 0, time.UTC)
	out, err := coerceIdentity(src, "IPP-42", &models.Patient{
		LastName: "DURAND", FirstName: "Marie", DateOfBirth: &dob, Gender: "F",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[tag.Tag]string{
		tag.PatientID:                 "IPP-42",
		tag.PatientName:               "DURAND^Marie",
		tag.PatientBirthDate:          "19610307",
		tag.PatientSex:                "F",
		tag.ImplementationClassUID:    dicomsop.ImplementationClassUID,
		tag.ImplementationVersionName: dicomsop.ImplementationVersionName,
	}
	for tg, v := range want {
		if got := tagValue(t, out, tg); got != v {
			t.Errorf("%v = %q, want %q", tg, got, v)
		}
	}
	// The waveform survives the rewrite: the PACS gets the same tracing.
	if _, err := ecgwaveform.Parse(out); err != nil {
		t.Errorf("rewritten object no longer parses as an ECG: %v", err)
	}
	// Same instance with corrected demographics — not a new object.
	if a, b := tagValue(t, src, tag.SOPInstanceUID), tagValue(t, out, tag.SOPInstanceUID); a != b {
		t.Errorf("SOP Instance UID changed: %q -> %q", a, b)
	}
}

// What the hub does not know stays as the device wrote it: an empty field
// tells the archive less than the device's value.
func TestCoerceIdentity_KeepsWhatTheHubDoesNotKnow(t *testing.T) {
	src := sample(t)
	out, err := coerceIdentity(src, "IPP-42", &models.Patient{Gender: "U"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range []tag.Tag{tag.PatientName, tag.PatientBirthDate, tag.PatientSex} {
		if a, b := tagValue(t, src, tg), tagValue(t, out, tg); a != b {
			t.Errorf("%v: device value %q replaced by %q although the hub holds none", tg, a, b)
		}
	}
	if got := tagValue(t, out, tag.PatientID); got != "IPP-42" {
		t.Errorf("Patient ID = %q, want IPP-42", got)
	}
}

func manageIdentityConnector(t *testing.T, p *models.Patient) *DICOMConnector {
	t.Helper()
	c, err := New(connector.Config{
		Name:           "pacs",
		Protocol:       "dicom_cstore",
		ManageIdentity: true,
		DICOM:          connector.DICOMEndpoint{Host: "127.0.0.1", Port: 11112, CallingAE: "HUB", CalledAE: "PACS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c.WithConverter(nil, &fakePatients{patient: p})
}

func TestMaterializeDICOM_ManageIdentitySendsTheHubsIdentity(t *testing.T) {
	src := filepath.Join(t.TempDir(), "a.dcm")
	if err := os.WriteFile(src, sample(t), 0o644); err != nil {
		t.Fatal(err)
	}
	c := manageIdentityConnector(t, &models.Patient{LastName: "DURAND", FirstName: "Marie"})

	got, cleanup, err := c.materializeDICOM(context.Background(),
		&models.ECG{ID: "e1", PatientID: "IPP-42", OriginalFilename: "a.dcm"}, src)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if got == src {
		t.Fatal("sent the stored file as received; the option promises the hub's identity")
	}
	out, _ := os.ReadFile(got)
	if id := tagValue(t, out, tag.PatientID); id != "IPP-42" {
		t.Errorf("Patient ID sent = %q, want IPP-42", id)
	}
	// The stored original is never modified: only the outbound copy is.
	if orig, _ := os.ReadFile(src); !bytes.Equal(orig, sample(t)) {
		t.Error("the stored file was modified")
	}
}

func TestMaterializeDICOM_ManageIdentityRefusesRatherThanSendTheDevicesIdentity(t *testing.T) {
	src := filepath.Join(t.TempDir(), "a.dcm")
	if err := os.WriteFile(src, []byte("not dicom"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := manageIdentityConnector(t, nil)
	_, cleanup, err := c.materializeDICOM(context.Background(),
		&models.ECG{ID: "e1", PatientID: "IPP-42", OriginalFilename: "a.dcm"}, src)
	defer cleanup()
	if err == nil {
		t.Fatal("an object whose identity could not be written was sent anyway")
	}
}

// A quarantined file that did not parse has no patient to give it: it is
// proxied as received.
func TestMaterializeDICOM_ManageIdentityProxiesAFileWithNoPatient(t *testing.T) {
	src := filepath.Join(t.TempDir(), "a.dcm")
	if err := os.WriteFile(src, []byte("unparsed"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := manageIdentityConnector(t, nil)
	got, cleanup, err := c.materializeDICOM(context.Background(),
		&models.ECG{OriginalFilename: "a.dcm"}, src)
	defer cleanup()
	if err != nil || got != src {
		t.Fatalf("got (%q, %v), want the file as received", got, err)
	}
}
