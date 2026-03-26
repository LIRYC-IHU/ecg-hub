package export

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ─── ECGBridge.ConvertToXMLFDA ────────────────────────────────────────────────

func TestConvertToXMLFDA_UnsupportedVendor(t *testing.T) {
	bridge := NewECGBridge(map[string]string{"philips:xmlfda": "philips-to-fda"}, 5*time.Second)
	_, err := bridge.ConvertToXMLFDA(context.Background(), "/some/file.dcm", "dicom", nil)
	if err == nil {
		t.Fatal("expected error for unsupported vendor")
	}
	if !errors.Is(err, ErrFormatNotSupported) {
		t.Errorf("expected ErrFormatNotSupported, got %v", err)
	}
}

func TestConvertToXMLFDA_BinaryNotFound(t *testing.T) {
	// Use a binary name that doesn't exist on PATH.
	bridge := NewECGBridge(map[string]string{"philips:xmlfda": "no-such-binary-xyz"}, 5*time.Second)
	_, err := bridge.ConvertToXMLFDA(context.Background(), "/some/file.xml", "philips", nil)
	if err == nil {
		t.Fatal("expected error when binary is missing")
	}
	if !errors.Is(err, ErrConversionFailed) {
		t.Errorf("expected ErrConversionFailed, got %v", err)
	}
}

