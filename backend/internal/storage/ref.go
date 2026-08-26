package storage

import (
	"context"
	"fmt"
	"io"
	"os"
)

// A ref is what ecgs.file_path and quarantine_entries.file_path hold: today an
// absolute path on the local volume.
//
// Every consumer of a stored file goes through the helpers below instead of
// calling os.Open on the column directly, so that a second kind of ref (an
// object-store URI) can be taught to the application in one place rather than
// at each of the fifteen sites that read an ECG.
//
// None of this changes local behaviour: on a local ref every helper is a thin
// wrapper over the os package, and Materialize hands the path straight back
// without copying a byte.
//
// The context parameters are unused while local is the only backend. They are
// part of the signatures from the start because a remote backend needs them,
// and adding them later would mean touching every call site a second time.

// Open returns a reader for ref. The caller closes it.
func Open(_ context.Context, ref string) (io.ReadCloser, error) {
	f, err := os.Open(ref)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", ref, err)
	}
	return f, nil
}

// ReadFile returns the whole content of ref.
func ReadFile(ctx context.Context, ref string) ([]byte, error) {
	rc, err := Open(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("storage: read %s: %w", ref, err)
	}
	return data, nil
}

// Exists reports whether ref is readable.
//
// A missing file is (false, nil): callers turn that into a 404. Any other
// failure is (false, err) — on a remote backend "the store is unreachable" and
// "the object is gone" are different answers, and reporting the first as the
// second would tell an operator their ECG had vanished.
func Exists(_ context.Context, ref string) (bool, error) {
	_, err := os.Stat(ref)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("storage: stat %s: %w", ref, err)
}

// Remove deletes ref. A ref that is already gone is not an error — deletion is
// called on paths whose file may have been removed by hand or by the janitor,
// and the DB row must still go.
func Remove(_ context.Context, ref string) error {
	if err := os.Remove(ref); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage: remove %s: %w", ref, err)
	}
	return nil
}

// Materialize returns a path at which ref can be read from the local
// filesystem, together with a cleanup function the caller must always call
// (defer it immediately, even on the error-free path).
//
// It exists for the consumers that cannot take a reader: the vendor converters,
// which exec a binary on a path, and the PACS connectors, which hand the path
// to a DICOM library. On a local ref it is free — the same path back, and a
// cleanup that does nothing. A remote backend will spool to a temp file here,
// which is why the cleanup is not optional.
func Materialize(_ context.Context, ref string) (string, func(), error) {
	return ref, func() {}, nil
}
