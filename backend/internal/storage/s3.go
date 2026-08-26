package storage

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // ETag comparison, not a security primitive
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// s3Scheme prefixes a ref that lives in the object store: "s3://bucket/key".
//
// The bucket is part of the ref on purpose. Refs outlive configuration: an
// operator who repoints storage.s3.bucket must still be able to read the ECGs
// written before the change, and a ref that only carried the key would silently
// resolve to the wrong bucket.
const s3Scheme = "s3://"

// S3Config describes an S3-compatible endpoint. Tested against RustFS and MinIO;
// AWS S3 works with an empty Endpoint.
type S3Config struct {
	Endpoint  string // host[:port], no scheme — a full URL is accepted and split
	Region    string
	Bucket    string
	Prefix    string // optional key prefix, e.g. "prod/"
	AccessKey string
	SecretKey string
	UseSSL    bool
	PathStyle bool // required by MinIO/RustFS/Ceph unless wildcard DNS is set up
}

// S3Store reads and writes ECG files in an S3-compatible bucket.
type S3Store struct {
	client *minio.Client
	bucket string
	prefix string
}

// NewS3Store builds the client and checks that the bucket is reachable, so a
// misconfigured endpoint fails at boot with a clear message rather than on the
// first ECG of the day.
func NewS3Store(ctx context.Context, cfg S3Config) (*S3Store, error) {
	endpoint, secure, err := splitEndpoint(cfg.Endpoint, cfg.UseSSL)
	if err != nil {
		return nil, err
	}

	opts := &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       secure,
		Region:       cfg.Region,
		BucketLookup: minio.BucketLookupDNS,
	}
	if cfg.PathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}

	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("storage: s3 client: %w", err)
	}

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("storage: s3 bucket %q unreachable at %s: %w", cfg.Bucket, endpoint, err)
	}
	if !exists {
		return nil, fmt.Errorf("storage: s3 bucket %q does not exist at %s — create it first", cfg.Bucket, endpoint)
	}

	return &S3Store{client: client, bucket: cfg.Bucket, prefix: strings.TrimPrefix(cfg.Prefix, "/")}, nil
}

// splitEndpoint accepts either "host:port" or a full URL, because an operator
// pasting an endpoint from a provider's console pastes the URL. A scheme in the
// value wins over the useSSL flag — what was typed is what was meant.
func splitEndpoint(raw string, useSSL bool) (host string, secure bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, errors.New("storage: s3 endpoint is empty")
	}
	if !strings.Contains(raw, "://") {
		return strings.TrimSuffix(raw, "/"), useSSL, nil
	}
	u, parseErr := url.Parse(raw)
	if parseErr != nil || u.Host == "" {
		return "", false, fmt.Errorf("storage: s3 endpoint %q is not a valid URL", raw)
	}
	return u.Host, u.Scheme == "https", nil
}

// Ref returns the ref stored in the database for a given object key.
func (s *S3Store) Ref(key string) string {
	return s3Scheme + s.bucket + "/" + s.key(key)
}

// key applies the configured prefix.
func (s *S3Store) key(k string) string {
	return s.prefix + strings.TrimPrefix(k, "/")
}

// Put uploads data and returns the ref to store in the database.
//
// The content hash is passed through as object metadata so the bucket carries
// its own record of what the file should be, readable by anything that is not
// this application.
func (s *S3Store) Put(ctx context.Context, key string, data []byte, contentHash string) (string, error) {
	opts := minio.PutObjectOptions{ContentType: "application/octet-stream", SendContentMd5: true}
	if contentHash != "" {
		opts.UserMetadata = map[string]string{"Ecg-Sha256": contentHash}
	}
	info, err := s.client.PutObject(ctx, s.bucket, s.key(key), bytes.NewReader(data), int64(len(data)), opts)
	if err != nil {
		return "", fmt.Errorf("storage: s3 put %s: %w", key, err)
	}
	if err := verifyUpload(info, data); err != nil {
		return "", fmt.Errorf("storage: s3 put %s: %w", key, err)
	}
	return s.Ref(key), nil
}

// verifyUpload checks what came back against what was sent, because the local
// copy is deleted on the strength of this call returning nil.
//
// SendContentMd5 already makes the server reject a corrupted body, so this is
// the second lock: for a single-part upload the ETag is the MD5 of the object,
// and every S3 implementation returns the stored size. A multipart ETag is not
// an MD5 (it carries a "-N" suffix) — there the size is all there is to check,
// and claiming more would be theatre.
func verifyUpload(info minio.UploadInfo, data []byte) error {
	if info.Size != int64(len(data)) {
		return fmt.Errorf("size mismatch: sent %d bytes, stored %d", len(data), info.Size)
	}
	etag := strings.Trim(info.ETag, `"`)
	if !isPlainMD5(etag) {
		return nil
	}
	sum := md5.Sum(data) //nolint:gosec // matching the server's ETag, not a security check
	if want := hex.EncodeToString(sum[:]); !strings.EqualFold(etag, want) {
		return fmt.Errorf("etag mismatch: server stored %s, expected %s", etag, want)
	}
	return nil
}

// isPlainMD5 reports whether an ETag is a bare 32-hex digest, i.e. a
// single-part upload whose ETag can be compared to an MD5.
func isPlainMD5(etag string) bool {
	if len(etag) != 32 {
		return false
	}
	_, err := hex.DecodeString(etag)
	return err == nil
}

// parseS3Ref splits "s3://bucket/key" into its parts.
func parseS3Ref(ref string) (bucket, key string, ok bool) {
	if !strings.HasPrefix(ref, s3Scheme) {
		return "", "", false
	}
	rest := strings.TrimPrefix(ref, s3Scheme)
	bucket, key, found := strings.Cut(rest, "/")
	if !found || bucket == "" || key == "" {
		return "", "", false
	}
	return bucket, key, true
}

func (s *S3Store) open(ctx context.Context, ref string) (io.ReadCloser, error) {
	bucket, key, ok := parseS3Ref(ref)
	if !ok {
		return nil, fmt.Errorf("storage: malformed object ref %q", ref)
	}
	obj, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: s3 get %s: %w", ref, err)
	}
	// GetObject is lazy: it does not talk to the server until the first read, so
	// a missing object would surface halfway through a response body. Stat here
	// to fail before the caller starts streaming.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, fmt.Errorf("storage: s3 get %s: %w", ref, err)
	}
	return obj, nil
}

func (s *S3Store) exists(ctx context.Context, ref string) (bool, error) {
	bucket, key, ok := parseS3Ref(ref)
	if !ok {
		return false, fmt.Errorf("storage: malformed object ref %q", ref)
	}
	_, err := s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if minio.ToErrorResponse(err).StatusCode == 404 {
		return false, nil
	}
	return false, fmt.Errorf("storage: s3 stat %s: %w", ref, err)
}

func (s *S3Store) remove(ctx context.Context, ref string) error {
	bucket, key, ok := parseS3Ref(ref)
	if !ok {
		return fmt.Errorf("storage: malformed object ref %q", ref)
	}
	if err := s.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: s3 remove %s: %w", ref, err)
	}
	return nil
}

// materialize spools the object to a temp file for the consumers that exec a
// binary on a path (the vendor converters) or hand the path to a DICOM library
// (the PACS connectors).
func (s *S3Store) materialize(ctx context.Context, ref string) (string, func(), error) {
	rc, err := s.open(ctx, ref)
	if err != nil {
		return "", func() {}, err
	}
	defer rc.Close()

	_, key, _ := parseS3Ref(ref)
	f, err := os.CreateTemp("", "ecg-*"+extOf(key))
	if err != nil {
		return "", func() {}, fmt.Errorf("storage: temp file for %s: %w", ref, err)
	}
	cleanup := func() {
		f.Close()
		_ = os.Remove(f.Name())
	}
	if _, err := io.Copy(f, rc); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("storage: spool %s: %w", ref, err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("storage: spool %s: %w", ref, err)
	}
	return f.Name(), cleanup, nil
}

// extOf keeps the original extension on the temp copy: the vendor converters
// dispatch on it.
func extOf(key string) string {
	if i := strings.LastIndex(key, "."); i >= 0 && len(key)-i <= 6 {
		return key[i:]
	}
	return ""
}
