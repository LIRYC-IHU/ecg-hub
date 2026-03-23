package storage

import (
	"os"
	"path/filepath"
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
