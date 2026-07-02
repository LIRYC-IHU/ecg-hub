package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
)

// newStorage builds a StorageConfig whose MaxSize is parsed via the same path
// the loader uses, so tests exercise the real parsing contract.
func newStorage(t *testing.T, dir, maxSize string) config.StorageConfig {
	t.Helper()
	cfg := config.StorageConfig{VolumePath: dir}
	if err := cfg.SetMaxSize(maxSize); err != nil {
		t.Fatalf("SetMaxSize(%q): %v", maxSize, err)
	}
	return cfg
}

func writeTestFile(t *testing.T, dir, name string, sizeBytes int) {
	t.Helper()
	data := make([]byte, sizeBytes)
	if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
		t.Fatalf("writeTestFile: %v", err)
	}
}

func TestJanitor_Rotate_DisabledWhenZero(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.xml", 1024)

	j := NewJanitor(newStorage(t, dir, "0"))
	deleted, freed, err := j.Rotate(dir)
	if err != nil || deleted != 0 || freed != 0 {
		t.Errorf("expected no-op when max_size=0, got deleted=%d freed=%d err=%v", deleted, freed, err)
	}
}

func TestJanitor_Rotate_NoOpWhenUnderLimit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.xml", 512)

	j := NewJanitor(newStorage(t, dir, "100Gi"))
	deleted, _, err := j.Rotate(dir)
	if err != nil || deleted != 0 {
		t.Errorf("expected no-op when under limit, got deleted=%d err=%v", deleted, err)
	}
}

func TestJanitor_Rotate_DeletesOldestFirst(t *testing.T) {
	dir := t.TempDir()

	// Two 600-byte files, limit = 1Ki (1024 B) → exactly one file must be purged,
	// and it must be the oldest.
	writeTestFile(t, dir, "old.xml", 600)
	oldPath := filepath.Join(dir, "old.xml")
	if err := os.Chtimes(oldPath, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	writeTestFile(t, dir, "new.xml", 600)

	j := NewJanitor(newStorage(t, dir, "1Ki"))
	deleted, freed, err := j.Rotate(dir)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deletion, got %d", deleted)
	}
	if freed != 600 {
		t.Errorf("expected 600 bytes freed, got %d", freed)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old.xml should have been purged")
	}
	if _, err := os.Stat(filepath.Join(dir, "new.xml")); err != nil {
		t.Errorf("new.xml should still exist: %v", err)
	}
}

func TestJanitor_Rotate_PurgesFilesOverLimit(t *testing.T) {
	dir := t.TempDir()

	// 3 × 1KiB files, limit = 2Ki → one file must be purged to fit under the cap.
	for _, name := range []string{"a", "b", "c"} {
		writeTestFile(t, dir, name+".xml", 1024)
	}

	j := NewJanitor(newStorage(t, dir, "2Ki"))
	deleted, freed, err := j.Rotate(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deletion, got %d", deleted)
	}
	if freed != 1024 {
		t.Errorf("expected 1024 bytes freed, got %d", freed)
	}
}

func TestJanitor_Start_DisabledWhenZero(t *testing.T) {
	j := NewJanitor(newStorage(t, t.TempDir(), "0"))
	j.Start(0) // should not panic, done channel should be closed
	j.Stop()   // should not block
}

func TestJanitor_RotateOnce_AlertOnlyByDefault(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.xml", 2048)

	// Over cap, but allow_rotation defaults to false — nothing may be deleted.
	cfg := newStorage(t, dir, "1Ki")
	cfg.QuarantinePath = t.TempDir()
	j := NewJanitor(cfg)
	j.rotateOnce()

	if _, err := os.Stat(filepath.Join(dir, "a.xml")); err != nil {
		t.Errorf("ECG file must not be deleted in alert-only mode: %v", err)
	}
}

func TestJanitor_RotateOnce_QuarantineNeverRotated(t *testing.T) {
	vol := t.TempDir()
	quar := t.TempDir()
	writeTestFile(t, quar, "q.xml", 2048)

	// Rotation explicitly enabled and quarantine over the cap — quarantined
	// files are pending operator review and must never be purged.
	cfg := newStorage(t, vol, "1Ki")
	cfg.QuarantinePath = quar
	cfg.AllowRotation = true
	j := NewJanitor(cfg)
	j.rotateOnce()

	if _, err := os.Stat(filepath.Join(quar, "q.xml")); err != nil {
		t.Errorf("quarantine file must never be rotated: %v", err)
	}
}

func TestJanitor_RotateOnce_RotatesWhenOptedIn(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "old.xml", 600)
	oldPath := filepath.Join(dir, "old.xml")
	if err := os.Chtimes(oldPath, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	writeTestFile(t, dir, "new.xml", 600)

	cfg := newStorage(t, dir, "1Ki")
	cfg.QuarantinePath = t.TempDir()
	cfg.AllowRotation = true
	j := NewJanitor(cfg)
	j.rotateOnce()

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old.xml should have been purged when allow_rotation=true")
	}
	if _, err := os.Stat(filepath.Join(dir, "new.xml")); err != nil {
		t.Errorf("new.xml should still exist: %v", err)
	}
}
