package auth

import (
	"encoding/json"
	"slices"
	"testing"
)

// orgAliases reads both forms of the Keycloak organization claim, as decoded
// from a JSON ID token.
func TestOrgAliases(t *testing.T) {
	cases := []struct {
		name, claim string
		want        []string
	}{
		{"absent", `{}`, nil},
		{"list", `{"organization": ["cabinet-a"]}`, []string{"cabinet-a"}},
		{"several", `{"organization": ["cabinet-a", "cabinet-b"]}`, []string{"cabinet-a", "cabinet-b"}},
		{"object with attributes", `{"organization": {"cabinet-a": {"id": "42"}}}`, []string{"cabinet-a"}},
		{"single string", `{"organization": "cabinet-a"}`, []string{"cabinet-a"}},
		{"empty list", `{"organization": []}`, []string{}},
	}
	for _, c := range cases {
		var claims map[string]any
		if err := json.Unmarshal([]byte(c.claim), &claims); err != nil {
			t.Fatal(err)
		}
		got := orgAliases(claims["organization"])
		slices.Sort(got)
		if len(got) != len(c.want) || (len(got) > 0 && !slices.Equal(got, c.want)) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
