package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ecg.xml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// Materialize on a local ref must hand the path straight back and leave the file
// alone: it is called on the download and forwarding paths, so a copy here would
// be a copy of every ECG served, and a cleanup that deleted the original would
// delete the clinical record itself.
func TestMaterializeLocalIsFreeAndNonDestructive(t *testing.T) {
	path := writeTemp(t, "<ecg/>")

	got, cleanup, err := Materialize(context.Background(), path)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got != path {
		t.Errorf("Materialize returned %q, want the ref unchanged (%q)", got, path)
	}
	cleanup()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cleanup removed the source file: %v", err)
	}
}

func TestExistsDistinguishesMissingFromBroken(t *testing.T) {
	path := writeTemp(t, "<ecg/>")

	if found, err := Exists(context.Background(), path); err != nil || !found {
		t.Errorf("Exists(present) = %v, %v; want true, nil", found, err)
	}
	if found, err := Exists(context.Background(), path+".nope"); err != nil || found {
		t.Errorf("Exists(missing) = %v, %v; want false, nil", found, err)
	}
}

// Deletion is best-effort by design: the DB row must go even when the file was
// already removed by hand or by the janitor.
func TestRemoveMissingIsNotAnError(t *testing.T) {
	if err := Remove(context.Background(), filepath.Join(t.TempDir(), "gone.xml")); err != nil {
		t.Errorf("Remove(missing) = %v, want nil", err)
	}
}

func TestVerify(t *testing.T) {
	content := "<ecg>data</ecg>"
	path := writeTemp(t, content)
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])

	if err := Verify(context.Background(), path, hash); err != nil {
		t.Errorf("Verify(matching) = %v, want nil", err)
	}
	if err := Verify(context.Background(), path, ""); err != nil {
		t.Errorf("Verify(no stored hash) = %v, want nil (legacy files)", err)
	}
	if err := Verify(context.Background(), path, hex.EncodeToString(make([]byte, 32))); err != ErrIntegrityFailure {
		t.Errorf("Verify(tampered) = %v, want ErrIntegrityFailure", err)
	}
}

func TestReadFile(t *testing.T) {
	path := writeTemp(t, "<ecg/>")
	data, err := ReadFile(context.Background(), path)
	if err != nil || string(data) != "<ecg/>" {
		t.Fatalf("ReadFile = %q, %v", data, err)
	}
	if _, err := ReadFile(context.Background(), path+".nope"); err == nil {
		t.Error("ReadFile(missing) = nil error, want failure")
	}
}
