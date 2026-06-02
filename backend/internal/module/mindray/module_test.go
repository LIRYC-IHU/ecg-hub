package mindray

import "testing"

func TestSplitName(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantLast  string
		wantFirst string
	}{
		{"single token", "moyles", "moyles", ""},
		{"caret separated", "Doe^John", "Doe", "John"},
		{"comma separated", "Doe, John", "Doe", "John"},
		{"trims whitespace", "  Doe ^ John  ", "Doe", "John"},
		{"empty", "", "", ""},
		{"caret takes precedence", "Doe^John, Jr", "Doe", "John, Jr"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			last, first := splitName(tc.in)
			if last != tc.wantLast || first != tc.wantFirst {
				t.Errorf("splitName(%q) = (%q, %q), want (%q, %q)", tc.in, last, first, tc.wantLast, tc.wantFirst)
			}
		})
	}
}
