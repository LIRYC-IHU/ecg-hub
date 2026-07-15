// Package bridgeutil centralises resolution and validation of the external
// ecg-bridge converter binaries invoked via os/exec. The executed program must
// only ever come from deployment configuration (env vars set by the operator),
// never from request data — the helpers here enforce that contract once so
// every exec call site shares the same hardening.
package bridgeutil

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ResolveBin resolves a converter binary path from deployment configuration.
// Per-binary env var (e.g. BRIDGE_PHILIPS_TO_FDA) takes precedence, then
// BRIDGE_BIN_DIR/name, else the bare name (resolved against $PATH by exec).
// Env overrides must be absolute paths: a relative override would make the
// executed program depend on the process working directory, so it is ignored
// with a warning and the default resolution applies instead.
func ResolveBin(envKey, name string) string {
	if v := os.Getenv(envKey); v != "" {
		if filepath.IsAbs(v) {
			return filepath.Clean(v)
		}
		slog.Warn("bridge: ignoring non-absolute converter override", "env", envKey, "value", v)
	}
	if dir := os.Getenv("BRIDGE_BIN_DIR"); dir != "" {
		return filepath.Join(dir, name)
	}
	return name
}

// CheckInputFile validates a path before it is passed as a CLI argument to a
// converter binary: it must name an existing regular file and must not start
// with "-", which a converter's flag parser could mistake for an option.
func CheckInputFile(path string) error {
	if path == "" {
		return fmt.Errorf("bridge: empty input path")
	}
	if strings.HasPrefix(path, "-") {
		return fmt.Errorf("bridge: input path %q looks like a flag", path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("bridge: input path: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("bridge: input path %q is not a regular file", path)
	}
	return nil
}
