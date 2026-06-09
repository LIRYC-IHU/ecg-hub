package ingestion

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// captureInserter records the last inserted QuarantineEntry.
type captureInserter struct{ last *models.QuarantineEntry }

func (c *captureInserter) Insert(e *models.QuarantineEntry) error {
	c.last = e
	return nil
}

func TestQuarantineStore_Record_SetsErrorCategory(t *testing.T) {
	cap := &captureInserter{}
	s := NewQuarantineStore("", cap)

	if err := s.Record(context.Background(), "bad.xml", []byte("garbage"), "parse_error: boom"); err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if cap.last == nil {
		t.Fatal("no entry inserted")
	}
	if cap.last.Category != models.QuarantineCategoryError {
		t.Errorf("Category = %q, want %q", cap.last.Category, models.QuarantineCategoryError)
	}
	if len(cap.last.Metadata) != 0 {
		t.Errorf("Metadata should be empty for error entries, got %s", cap.last.Metadata)
	}
}

func TestQuarantineStore_RecordUnidentified_PersistsMetadata(t *testing.T) {
	dir := t.TempDir()
	cap := &captureInserter{}
	s := NewQuarantineStore(dir, cap)

	recordedAt := time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC)
	meta := &module.ECGMetadata{
		PatientID:    "", // unidentified by definition
		VendorName:   "nihon-kohden",
		SourceFormat: "nihon_kohden_dat",
		RecordedAt:   recordedAt,
		Extra: map[string]any{
			"last_name":  "Doe",
			"first_name": "Jane",
			"sex":        "F",
			"birth_date": "19800101",
		},
	}
	item := IngestItem{Filename: "0004266041631332.DAT", Data: []byte("rawbytes"), Source: "ftp"}

	if err := s.RecordUnidentified(context.Background(), item, meta, "unidentified: no patient ID"); err != nil {
		t.Fatalf("RecordUnidentified returned error: %v", err)
	}
	e := cap.last
	if e == nil {
		t.Fatal("no entry inserted")
	}
	if e.Category != models.QuarantineCategoryUnidentified {
		t.Errorf("Category = %q, want %q", e.Category, models.QuarantineCategoryUnidentified)
	}
	if e.Vendor != "nihon-kohden" {
		t.Errorf("Vendor = %q, want nihon-kohden", e.Vendor)
	}
	if e.RecordedAt == nil || !e.RecordedAt.Equal(recordedAt) {
		t.Errorf("RecordedAt = %v, want %v", e.RecordedAt, recordedAt)
	}

	// The raw file must be written to disk so it can be re-ingested on assignment.
	if e.FilePath == "" {
		t.Fatal("FilePath empty; raw file was not persisted")
	}
	if _, err := os.Stat(e.FilePath); err != nil {
		t.Errorf("raw file not on disk: %v", err)
	}
	if filepath.Dir(e.FilePath) != dir {
		t.Errorf("file written outside quarantine dir: %s", e.FilePath)
	}

	// Metadata round-trips back into an ECGMetadata for re-ingestion.
	var got module.ECGMetadata
	if err := json.Unmarshal(e.Metadata, &got); err != nil {
		t.Fatalf("metadata not valid JSON: %v", err)
	}
	if got.VendorName != "nihon-kohden" {
		t.Errorf("decoded VendorName = %q, want nihon-kohden", got.VendorName)
	}
	if got.Extra["last_name"] != "Doe" {
		t.Errorf("decoded Extra[last_name] = %v, want Doe", got.Extra["last_name"])
	}
}
