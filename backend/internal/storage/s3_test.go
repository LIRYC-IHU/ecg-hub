package storage

import (
	"context"
	"crypto/md5" //nolint:gosec // mirrors the ETag comparison under test
	"encoding/hex"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestSplitEndpoint(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		useSSL     bool
		wantHost   string
		wantSecure bool
		wantErr    bool
	}{
		{"host only honours the flag", "s3.example.org", true, "s3.example.org", true, false},
		{"host and port", "minio.lan:9000", false, "minio.lan:9000", false, false},
		{"a pasted https URL wins over the flag", "https://s3.example.org", false, "s3.example.org", true, false},
		{"a pasted http URL wins over the flag", "http://minio.lan:9000", true, "minio.lan:9000", false, false},
		{"trailing slash is not part of the host", "s3.example.org/", false, "s3.example.org", false, false},
		{"empty is an error", "", false, "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, secure, err := splitEndpoint(tc.raw, tc.useSSL)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("splitEndpoint(%q) = %q, want an error", tc.raw, host)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitEndpoint(%q): %v", tc.raw, err)
			}
			if host != tc.wantHost || secure != tc.wantSecure {
				t.Errorf("splitEndpoint(%q) = %q, %v; want %q, %v", tc.raw, host, secure, tc.wantHost, tc.wantSecure)
			}
		})
	}
}

// The bucket travels inside the ref so that repointing storage.s3.bucket never
// silently redirects reads of files written before the change.
func TestRefRoundTrip(t *testing.T) {
	s := &S3Store{bucket: "ecg-hub", prefix: "prod/"}
	ref := s.Ref("P001/ecg.xml")
	if ref != "s3://ecg-hub/prod/P001/ecg.xml" {
		t.Fatalf("Ref = %q", ref)
	}
	bucket, key, ok := parseS3Ref(ref)
	if !ok || bucket != "ecg-hub" || key != "prod/P001/ecg.xml" {
		t.Errorf("parseS3Ref(%q) = %q, %q, %v", ref, bucket, key, ok)
	}
}

func TestParseS3RefRejectsMalformed(t *testing.T) {
	for _, ref := range []string{"/data/ecg/P001/x.xml", "s3://", "s3://bucket", "s3:///key", "s3://bucket/"} {
		if _, _, ok := parseS3Ref(ref); ok {
			t.Errorf("parseS3Ref(%q) accepted a malformed ref", ref)
		}
	}
}

// A ref on object storage while no backend is configured must be an error and
// never a fallback to disk: reading it as a local path would report a missing
// file for an ECG that exists in the bucket.
func TestRemoteRefWithoutBackendIsAnError(t *testing.T) {
	SetRemote(nil)
	if _, err := Open(context.Background(), "s3://ecg-hub/P001/x.xml"); err == nil {
		t.Fatal("Open on an s3 ref with no backend = nil error, want failure")
	}
	if _, err := Exists(context.Background(), "s3://ecg-hub/P001/x.xml"); err == nil {
		t.Fatal("Exists on an s3 ref with no backend = nil error, want failure")
	}
	if _, _, err := Materialize(context.Background(), "s3://ecg-hub/P001/x.xml"); err == nil {
		t.Fatal("Materialize on an s3 ref with no backend = nil error, want failure")
	}
	if err := Remove(context.Background(), "s3://ecg-hub/P001/x.xml"); err == nil {
		t.Fatal("Remove on an s3 ref with no backend = nil error, want failure")
	}
}

// The local copy is deleted on the strength of Put returning nil, so an upload
// the server stored differently must not pass.
func TestVerifyUpload(t *testing.T) {
	data := []byte("<ecg>data</ecg>")
	sum := md5.Sum(data) //nolint:gosec // mirrors the ETag comparison under test
	etag := hex.EncodeToString(sum[:])

	if err := verifyUpload(minio.UploadInfo{Size: int64(len(data)), ETag: etag}, data); err != nil {
		t.Errorf("matching upload rejected: %v", err)
	}
	if err := verifyUpload(minio.UploadInfo{Size: int64(len(data)), ETag: `"` + etag + `"`}, data); err != nil {
		t.Errorf("quoted etag rejected: %v", err)
	}
	if err := verifyUpload(minio.UploadInfo{Size: int64(len(data)), ETag: strings.Repeat("a", 32)}, data); err == nil {
		t.Error("etag mismatch accepted")
	}
	if err := verifyUpload(minio.UploadInfo{Size: 3, ETag: etag}, data); err == nil {
		t.Error("size mismatch accepted")
	}
	// A multipart ETag is not an MD5; only the size can be checked there.
	if err := verifyUpload(minio.UploadInfo{Size: int64(len(data)), ETag: etag + "-3"}, data); err != nil {
		t.Errorf("multipart etag rejected: %v", err)
	}
}

func TestObjectKeyKeepsTheVolumeLayout(t *testing.T) {
	if got := objectKey("/data/ecg", "/data/ecg/P001/ecg_2026.xml"); got != "P001/ecg_2026.xml" {
		t.Errorf("objectKey = %q, want the path relative to the volume root", got)
	}
	// A row pointing outside the configured root still gets uploaded rather than
	// being skipped forever.
	if got := objectKey("/data/ecg", "/elsewhere/ecg.xml"); got != "ecg.xml" {
		t.Errorf("objectKey(outside root) = %q, want the bare filename", got)
	}
}
