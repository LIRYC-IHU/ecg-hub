package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// remote is the object-store backend, set once at boot when storage.backend is
// "s3" and nil otherwise. It is a package-level value for the same reason
// module.GlobalRegistry is: the consumers of a ref are handlers, workers and
// connectors spread across the application, and threading a store through every
// one of their constructors buys nothing when there is exactly one store.
var (
	remoteMu sync.RWMutex
	remote   *S3Store
)

// IsRemoteRef reports whether ref lives in the object store rather than on the
// local volume. Callers need it where a ref is treated as a filesystem path —
// joining an s3:// ref to the volume root produces a local path that can never
// exist.
func IsRemoteRef(ref string) bool {
	return strings.HasPrefix(ref, s3Scheme)
}

// SetRemote installs the object-store backend. Called once from main.
func SetRemote(s *S3Store) {
	remoteMu.Lock()
	defer remoteMu.Unlock()
	remote = s
}

// remoteFor returns the backend that owns ref, or nil when ref is a local path.
//
// A ref that names the object store while no backend is configured is an error
// and not a fallback to disk: it means the operator turned S3 off while files
// still live there, and reading it as a local path would report a missing file
// for an ECG that exists.
func remoteFor(ref string) (*S3Store, error) {
	if !IsRemoteRef(ref) {
		return nil, nil
	}
	remoteMu.RLock()
	defer remoteMu.RUnlock()
	if remote == nil {
		return nil, fmt.Errorf("storage: %s is on object storage but no S3 backend is configured", ref)
	}
	return remote, nil
}

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
// A ref that starts with "s3://" is served by the object-store backend when one
// is configured; anything else is a path on the local volume. The two forms
// coexist on purpose: files written before S3 was enabled keep being read from
// disk, so turning the option on needs no migration and no downtime.

// Open returns a reader for ref. The caller closes it.
func Open(ctx context.Context, ref string) (io.ReadCloser, error) {
	if s, err := remoteFor(ref); err != nil {
		return nil, err
	} else if s != nil {
		return s.open(ctx, ref)
	}
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
func Exists(ctx context.Context, ref string) (bool, error) {
	if s, err := remoteFor(ref); err != nil {
		return false, err
	} else if s != nil {
		return s.exists(ctx, ref)
	}
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
func Remove(ctx context.Context, ref string) error {
	if s, err := remoteFor(ref); err != nil {
		return err
	} else if s != nil {
		return s.remove(ctx, ref)
	}
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
func Materialize(ctx context.Context, ref string) (string, func(), error) {
	if s, err := remoteFor(ref); err != nil {
		return "", func() {}, err
	} else if s != nil {
		return s.materialize(ctx, ref)
	}
	return ref, func() {}, nil
}
