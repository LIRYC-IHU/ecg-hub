package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"BS1174", "BS1174"},
		{"../../etc/passwd", ".._.._etc_passwd"},
		{"a/b", "a_b"},
		{`a\b`, "a_b"},
		{"", "_"},
		{".", "_"},
		{"..", "_"},
		{"   ", "_"},
		{"with\x00null", "with_null"},
		{"line\nbreak", "line_break"},
		{"C:name", "C_name"},
	} {
		if got := SafeName(tc.in); got != tc.want {
			t.Errorf("SafeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// SafeName output must never be a traversal component once joined.
func TestSafeNameNeverEscapes(t *testing.T) {
	base := "/data/ecg"
	for _, hostile := range []string{
		"../../etc/passwd", "..", ".", "", "....//....//x", "/absolute", `\\server\share`,
	} {
		got := filepath.Join(base, SafeName(hostile))
		if !strings.HasPrefix(got, base+"/") {
			t.Errorf("SafeName(%q) escaped: joined to %q", hostile, got)
		}
	}
}

func TestEnsureWithin(t *testing.T) {
	base := "/data/ecg"
	for _, tc := range []struct {
		name    string
		parts   []string
		wantErr bool
	}{
		{"plain file", []string{"a.xml"}, false},
		{"nested", []string{"P001", "a.xml"}, false},
		{"climbs out", []string{"..", "evil.xml"}, true},
		{"climbs out deeper", []string{"P001", "..", "..", "evil.xml"}, true},
		{"embedded traversal", []string{"../../etc/passwd"}, true},
		// filepath.Join treats a leading separator in a component as relative, so
		// an absolute-looking part lands under base rather than at the root.
		// Confined, therefore allowed.
		{"absolute-looking part is confined", []string{"/etc/passwd"}, false},
		{"base itself", []string{"."}, true},
		{"sibling prefix", []string{"..", "ecg-evil", "x"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EnsureWithin(base, tc.parts...)
			if tc.wantErr {
				if err == nil {
					t.Errorf("EnsureWithin(%q, %q) = %q, want error", base, tc.parts, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("EnsureWithin(%q, %q): %v", base, tc.parts, err)
			}
			if !strings.HasPrefix(got, base+"/") {
				t.Errorf("got %q, want a path under %q", got, base)
			}
		})
	}
}

// "/data/ecg-evil" must not pass as being inside "/data/ecg": a string prefix
// check would accept it.
func TestEnsureWithinRejectsSiblingSharingAPrefix(t *testing.T) {
	if got, err := EnsureWithin("/data/ecg", "..", "ecg-evil", "x.xml"); err == nil {
		t.Errorf("sibling directory accepted: %q", got)
	}
}
