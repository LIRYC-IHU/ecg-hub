package storage

import (
	"fmt"
	"os"
	"path/filepath"
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
	if err := os.MkdirAll(v.basePath, 0755); err != nil {
		return "", fmt.Errorf("storage: create volume dir: %w", err)
	}
	fullPath := filepath.Join(v.basePath, filename)
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", fmt.Errorf("storage: write %s: %w", filename, err)
	}
	return fullPath, nil
}

// Exists reports whether basePath/filename exists on disk.
func (v *Volume) Exists(filename string) bool {
	_, err := os.Stat(filepath.Join(v.basePath, filename))
	return err == nil
}
