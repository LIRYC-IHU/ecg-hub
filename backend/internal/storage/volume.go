package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

// Volume manages file storage at a fixed base path on the local filesystem.
type Volume struct {
	basePath string
}

// NewVolume creates a Volume rooted at basePath.
func NewVolume(basePath string) *Volume {
	return &Volume{basePath: basePath}
}

// Write creates (or overwrites) basePath/filename with data.
// The base directory is created if it does not exist.
// Returns the absolute path of the written file.
func (v *Volume) Write(filename string, data []byte) (string, error) {
	start := time.Now()
	if err := os.MkdirAll(v.basePath, 0755); err != nil {
		return "", fmt.Errorf("storage: create volume dir: %w", err)
	}
	fullPath := filepath.Join(v.basePath, filename)
	err := os.WriteFile(fullPath, data, 0644)
	appmetrics.StorageOpDuration.WithLabelValues("write").Observe(time.Since(start).Seconds())
	if err != nil {
		return "", fmt.Errorf("storage: write %s: %w", filename, err)
	}
	return fullPath, nil
}

// GetPath returns the absolute path for basePath/filename (supports relative sub-paths).
func (v *Volume) GetPath(filename string) string {
	return filepath.Join(v.basePath, filename)
}

// WriteForPatient stores the file under basePath/<patientID>/filename.
// Creates the patient subdirectory if it does not exist.
// Returns the path relative to basePath (e.g. "P001/ecg_2026-01-01.xml") stored in DB.
func (v *Volume) WriteForPatient(patientID, filename string, data []byte) (string, error) {
	start := time.Now()
	// Sanitise patientID so it is safe as a directory name.
	dir := filepath.Join(v.basePath, sanitisePath(patientID))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("storage: create patient dir: %w", err)
	}
	fullPath := filepath.Join(dir, filename)
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", fmt.Errorf("storage: write %s: %w", fullPath, err)
	}
	appmetrics.StorageOpDuration.WithLabelValues("write").Observe(time.Since(start).Seconds())
	// Return path relative to volume root so it is portable (volume mount can change).
	rel := filepath.Join(sanitisePath(patientID), filename)
	return rel, nil
}

// ExistsForPatient reports whether basePath/<patientID>/filename exists.
func (v *Volume) ExistsForPatient(patientID, filename string) bool {
	_, err := os.Stat(filepath.Join(v.basePath, sanitisePath(patientID), filename))
	return err == nil
}

// Exists reports whether basePath/filename exists on disk (supports relative sub-paths).
func (v *Volume) Exists(filename string) bool {
	_, err := os.Stat(filepath.Join(v.basePath, filename))
	return err == nil
}

// sanitisePath replaces characters unsafe for directory names.
func sanitisePath(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '/' || c == '\\' || c == ':' || c == '*' || c == '?' || c == '"' || c == '<' || c == '>' || c == '|' {
			out = append(out, '_')
		} else {
			out = append(out, c)
		}
	}
	result := string(out)
	// A value of exactly "." or ".." is a path-traversal component once joined
	// (filepath.Join treats it specially); prefix it so it stays a literal name.
	if result == "." || result == ".." {
		return "_" + result
	}
	return result
}
