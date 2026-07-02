package handlers

import (
	"net/http"
	"testing"
)

// TestIsAllowedImageType checks the logo allowlist accepts only raster images and
// rejects SVG (which can carry scripts) and any non-image type.
func TestIsAllowedImageType(t *testing.T) {
	allowed := []string{"image/png", "image/jpeg", "image/webp"}
	for _, ct := range allowed {
		if !isAllowedImageType(ct) {
			t.Errorf("%q should be allowed", ct)
		}
	}
	denied := []string{
		"image/svg+xml",
		"text/xml; charset=utf-8",
		"text/html; charset=utf-8",
		"text/plain; charset=utf-8",
		"application/octet-stream",
		"image/gif",
	}
	for _, ct := range denied {
		if isAllowedImageType(ct) {
			t.Errorf("%q should be denied", ct)
		}
	}
}

// TestLogoTypeDetection verifies the upload path's byte-sniffing: a PNG is accepted
// from its magic bytes, and a script-bearing SVG is detected as text and rejected —
// closing the stored-XSS vector regardless of the client-supplied Content-Type.
func TestLogoTypeDetection(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if ct := http.DetectContentType(png); !isAllowedImageType(ct) {
		t.Errorf("PNG magic bytes should be allowed, detected %q", ct)
	}

	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`)
	if ct := http.DetectContentType(svg); isAllowedImageType(ct) {
		t.Errorf("SVG must be rejected, but was detected as allowed type %q", ct)
	}
}
