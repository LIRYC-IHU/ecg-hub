package ihe

import "testing"

func TestParseCX(t *testing.T) {
	tests := []struct {
		name                     string
		raw                      string
		id, namespace, universal string
	}{
		{"bare identifier", "12345", "12345", "", ""},
		{"universal id only", "12345^^^&1.2.250.1.213.1.4.8&ISO", "12345", "", "1.2.250.1.213.1.4.8"},
		{"namespace only", "12345^^^CHU_BORDEAUX", "12345", "CHU_BORDEAUX", ""},
		{"namespace and universal id", "12345^^^CHU&1.2.3&ISO", "12345", "CHU", "1.2.3"},
		{"check digit and type code ignored", "12345^7^M11^CHU&1.2.3&ISO^PI", "12345", "CHU", "1.2.3"},
		{"surrounding spaces trimmed", " 12345 ^^^ CHU ", "12345", "CHU", ""},
		{"empty", "", "", "", ""},
		{"separators only", "^^^", "", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCX(tc.raw)
			if got.ID != tc.id {
				t.Errorf("ID = %q, want %q", got.ID, tc.id)
			}
			if got.NamespaceID != tc.namespace {
				t.Errorf("NamespaceID = %q, want %q", got.NamespaceID, tc.namespace)
			}
			if got.UniversalID != tc.universal {
				t.Errorf("UniversalID = %q, want %q", got.UniversalID, tc.universal)
			}
		})
	}
}

func TestAuthorityMatches(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"no authority configured accepts anything", "1^^^OTHER&9.9.9&ISO", "", true},
		{"universal id matches", "1^^^&1.2.3&ISO", "1.2.3", true},
		{"namespace matches", "1^^^CHU", "CHU", true},
		{"case insensitive", "1^^^chu", "CHU", true},
		{"bare id accepted on a single-domain install", "1", "1.2.3", true},
		{"different universal id refused", "1^^^&9.9.9&ISO", "1.2.3", false},
		{"different namespace refused", "1^^^OTHER", "CHU", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseCX(tc.raw).AuthorityMatches(tc.want); got != tc.ok {
				t.Errorf("AuthorityMatches(%q) = %v, want %v", tc.want, got, tc.ok)
			}
		})
	}
}
