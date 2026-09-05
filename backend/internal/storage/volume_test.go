package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVolume_Write_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	v := NewVolume(dir)

	fullPath, err := v.Write("ecg.xml", []byte("<ecg/>"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	want := filepath.Join(dir, "ecg.xml")
	if fullPath != want {
		t.Errorf("returned path = %q, want %q", fullPath, want)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "<ecg/>" {
		t.Errorf("file content = %q, want %q", data, "<ecg/>")
	}
}

func TestVolume_Write_CreatesDir(t *testing.T) {
	parent := t.TempDir()
	// Sub-directory that doesn't exist yet
	dir := filepath.Join(parent, "sub", "volume")
	v := NewVolume(dir)

	_, err := v.Write("ecg.xml", []byte("data"))
	if err != nil {
		t.Fatalf("Write with non-existent base dir failed: %v", err)
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("base dir was not created")
	}
}

func TestVolume_Exists_TrueWhenPresent(t *testing.T) {
	dir := t.TempDir()
	v := NewVolume(dir)

	if _, err := v.Write("ecg.xml", []byte("data")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if !v.Exists("ecg.xml") {
		t.Error("Exists returned false after Write")
	}
}

func TestVolume_Exists_FalseWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	v := NewVolume(dir)

	if v.Exists("ghost.xml") {
		t.Error("Exists returned true for a file that was never written")
	}
}

// The patient identifier is re-inserted into the file name by the ingestion
// naming code, so sanitising only the directory left it free to climb out
// through the name joined to it.
func TestWriteForPatientContainsBothComponents(t *testing.T) {
	base := t.TempDir()
	v := NewVolume(base)

	for _, tc := range []struct{ name, patientID, filename string }{
		{"traversal in filename", "P001", "../../escaped.xml"},
		{"traversal in both", "../../x", "../../escaped.xml"},
		{"separators in patient id", "a/b/c", "f.xml"},
		{"dot dot patient id", "..", "f.xml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rel, err := v.WriteForPatient(tc.patientID, tc.filename, []byte("x"))
			if err != nil {
				return // refusing outright is an acceptable outcome
			}
			abs := filepath.Join(base, rel)
			if !strings.HasPrefix(filepath.Clean(abs), filepath.Clean(base)+string(filepath.Separator)) {
				t.Errorf("wrote outside the volume: %q", abs)
			}
			if _, err := os.Stat(abs); err != nil {
				t.Errorf("returned path %q does not exist: %v", rel, err)
			}
		})
	}

	// Nothing may exist above the volume root.
	if entries, err := os.ReadDir(filepath.Dir(base)); err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), "escaped") {
				t.Errorf("file escaped the volume: %s", e.Name())
			}
		}
	}
}

// ExistsForPatient decides whether a name is free, so it has to resolve to the
// same path WriteForPatient uses or a stored ECG could be overwritten.
func TestExistsForPatientMatchesWritePath(t *testing.T) {
	v := NewVolume(t.TempDir())
	const pid, name = "a/b", "../f.xml"
	if v.ExistsForPatient(pid, name) {
		t.Fatal("reported existing before the write")
	}
	if _, err := v.WriteForPatient(pid, name, []byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !v.ExistsForPatient(pid, name) {
		t.Error("write succeeded but ExistsForPatient resolves elsewhere")
	}
}
