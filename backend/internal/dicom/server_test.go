package dicom

import (
	"strings"
	"testing"
)

// The SOP Instance UID is as sender-controlled as the rest of the payload, and
// it is what the fallback name is built from — normalise it too.
func TestBuildDICOMFilename_FallbackIsNormalised(t *testing.T) {
	cases := []struct{ uid, want string }{
		{"1.2.840.113619.2.55", "1.2.840.113619.2.55.dcm"},
		{"", "unknown.dcm"},
		{"../../etc/passwd", "etc_passwd.dcm"},
		{"..", "unknown.dcm"},
		{"uid\x00with\nctl", "uid_with_ctl.dcm"},
		{strings.Repeat("9", 200), strings.Repeat("9", 64) + ".dcm"},
	}
	for _, c := range cases {
		// Not a parseable data set, so the fallback path is the one taken.
		got := buildDICOMFilename([]byte("not a dicom data set"), c.uid)
		if got != c.want {
			t.Errorf("buildDICOMFilename(_, %q) = %q, want %q", c.uid, got, c.want)
		}
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("buildDICOMFilename(_, %q) leaked a separator: %q", c.uid, got)
		}
	}
}
