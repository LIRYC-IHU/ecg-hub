package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// ErrIntegrityFailure is returned when a file's SHA-256 does not match the stored hash.
var ErrIntegrityFailure = fmt.Errorf("storage: file integrity check failed — file may have been tampered with")

// VerifyFile reads the file at path, computes its SHA-256, and compares it to expectedHash.
// Returns ErrIntegrityFailure when they differ, nil when they match or when expectedHash is empty
// (backward compat: files ingested before hash was added have no stored hash).
func VerifyFile(path, expectedHash string) error {
	if expectedHash == "" {
		return nil // no stored hash — skip check (legacy files)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("storage: open file for integrity check: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("storage: hash file: %w", err)
	}

	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expectedHash {
		return ErrIntegrityFailure
	}
	return nil
}
