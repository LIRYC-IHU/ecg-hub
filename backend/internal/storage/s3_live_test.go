package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"testing"
	"time"
)

// A round trip against a real S3-compatible endpoint. Skipped unless
// S3_TEST_ENDPOINT and S3_TEST_BUCKET are set, so it never runs in CI by
// accident, and always runs the same code path production uses.
//
//	S3_TEST_ENDPOINT=https://s3.example.org S3_TEST_BUCKET=ecg-hub \
//	S3_TEST_PATH_STYLE=1 S3_ACCESS_KEY=... S3_SECRET_KEY=... \
//	go test ./internal/storage/ -run TestS3LiveRoundTrip -v
func TestS3LiveRoundTrip(t *testing.T) {
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	bucket := os.Getenv("S3_TEST_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("set S3_TEST_ENDPOINT and S3_TEST_BUCKET to run the live S3 round trip")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := NewS3Store(ctx, S3Config{
		Endpoint:  endpoint,
		Bucket:    bucket,
		Prefix:    "go-test/",
		AccessKey: os.Getenv("S3_ACCESS_KEY"),
		SecretKey: os.Getenv("S3_SECRET_KEY"),
		Region:    os.Getenv("S3_TEST_REGION"),
		PathStyle: os.Getenv("S3_TEST_PATH_STYLE") != "",
	})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	SetRemote(store)
	defer SetRemote(nil)

	data := []byte(fmt.Sprintf("<ecg run=%q>waveform</ecg>", time.Now().Format(time.RFC3339Nano)))
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	key := fmt.Sprintf("P001/live_%d.xml", time.Now().UnixNano())

	ref, err := store.Put(ctx, key, data, hash)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Logf("stored ref: %s", ref)
	defer func() {
		if err := Remove(context.Background(), ref); err != nil {
			t.Errorf("cleanup Remove: %v", err)
		}
	}()

	// The whole point of the ref indirection: every consumer below is calling
	// the same helper it calls for a local file.
	if found, err := Exists(ctx, ref); err != nil || !found {
		t.Fatalf("Exists = %v, %v; want true, nil", found, err)
	}

	got, err := ReadFile(ctx, ref)
	if err != nil || string(got) != string(data) {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}

	if err := Verify(ctx, ref, hash); err != nil {
		t.Errorf("Verify(matching hash) = %v, want nil", err)
	}
	if err := Verify(ctx, ref, hex.EncodeToString(make([]byte, 32))); err != ErrIntegrityFailure {
		t.Errorf("Verify(wrong hash) = %v, want ErrIntegrityFailure", err)
	}

	// Materialize is what the converters and PACS connectors use, so the temp
	// copy must carry the original extension and must be cleaned up.
	localPath, cleanup, err := Materialize(ctx, ref)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	spooled, readErr := os.ReadFile(localPath)
	if readErr != nil || string(spooled) != string(data) {
		cleanup()
		t.Fatalf("materialised copy = %q, %v", spooled, readErr)
	}
	if filepathExt := localPath[len(localPath)-4:]; filepathExt != ".xml" {
		t.Errorf("materialised copy %q lost the .xml extension the converters dispatch on", localPath)
	}
	cleanup()
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Errorf("cleanup left the temp copy behind at %s", localPath)
	}

	// A missing object is a clean false, not an error: the download handler
	// turns it into a 404 and anything else into a 502.
	if found, err := Exists(ctx, store.Ref("P001/does-not-exist.xml")); err != nil || found {
		t.Errorf("Exists(missing) = %v, %v; want false, nil", found, err)
	}
	if _, err := Open(ctx, store.Ref("P001/does-not-exist.xml")); err == nil {
		t.Error("Open(missing) = nil error, want failure before the body streams")
	}

	rc, err := Open(ctx, ref)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	if _, err := io.Copy(io.Discard, rc); err != nil {
		t.Errorf("streaming the object failed: %v", err)
	}
}
