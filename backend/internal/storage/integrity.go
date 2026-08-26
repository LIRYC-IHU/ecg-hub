package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// ErrIntegrityFailure is returned when a file's SHA-256 does not match the stored hash.
var ErrIntegrityFailure = fmt.Errorf("storage: file integrity check failed — file may have been tampered with")

// Verify reads ref, computes its SHA-256, and compares it to expectedHash.
// Returns ErrIntegrityFailure when they differ, nil when they match or when expectedHash is empty
// (backward compat: files ingested before hash was added have no stored hash).
func Verify(ctx context.Context, ref, expectedHash string) error {
	if expectedHash == "" {
		return nil // no stored hash — skip check (legacy files)
	}

	rc, err := Open(ctx, ref)
	if err != nil {
		return fmt.Errorf("storage: open file for integrity check: %w", err)
	}
	defer rc.Close()

	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return fmt.Errorf("storage: hash file: %w", err)
	}

	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expectedHash {
		return ErrIntegrityFailure
	}
	return nil
}
