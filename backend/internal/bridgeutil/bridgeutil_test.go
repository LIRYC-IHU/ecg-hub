package bridgeutil

import (
	"os"
	"strings"
	"testing"
)

func TestResolveBin_EnvOverrideAbsolute(t *testing.T) {
	t.Setenv("TEST_BRIDGE_BIN", "/opt/bridge/philips-to-fda")
	if got := ResolveBin("TEST_BRIDGE_BIN", "philips-to-fda"); got != "/opt/bridge/philips-to-fda" {
		t.Errorf("expected env override, got %q", got)
	}
}

func TestResolveBin_EnvOverrideRelativeIgnored(t *testing.T) {
	t.Setenv("TEST_BRIDGE_BIN", "bin/philips-to-fda")
	t.Setenv("BRIDGE_BIN_DIR", "")
	if got := ResolveBin("TEST_BRIDGE_BIN", "philips-to-fda"); got != "philips-to-fda" {
		t.Errorf("relative override must be ignored, got %q", got)
	}
}

func TestResolveBin_BinDirFallback(t *testing.T) {
	t.Setenv("TEST_BRIDGE_BIN", "")
	t.Setenv("BRIDGE_BIN_DIR", "/opt/bridge")
	if got := ResolveBin("TEST_BRIDGE_BIN", "philips-to-fda"); got != "/opt/bridge/philips-to-fda" {
		t.Errorf("expected BRIDGE_BIN_DIR join, got %q", got)
	}
}

func TestResolveBin_BareNameDefault(t *testing.T) {
	t.Setenv("TEST_BRIDGE_BIN", "")
	t.Setenv("BRIDGE_BIN_DIR", "")
	if got := ResolveBin("TEST_BRIDGE_BIN", "philips-to-fda"); got != "philips-to-fda" {
		t.Errorf("expected bare name, got %q", got)
	}
}

func TestCheckInputFile(t *testing.T) {
	real := t.TempDir() + "/in.xml"
	if err := os.WriteFile(real, []byte("<xml/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		path    string
		wantErr string // "" = no error expected
	}{
		{"regular file", real, ""},
		{"empty path", "", "empty input path"},
		{"leading dash", "--anonymize", "looks like a flag"},
		{"missing file", t.TempDir() + "/nope.xml", "input path"},
		{"directory", t.TempDir(), "not a regular file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckInputFile(tc.path)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
