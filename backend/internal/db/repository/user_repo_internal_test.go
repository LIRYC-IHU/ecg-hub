package repository

import (
	"slices"
	"testing"
)

// audit_logs.user_id mixes internal UUIDs with "system" and raw usernames.
// ecg_hub_users.id is a uuid column, so anything that is not a UUID has to be
// dropped before the IN clause — otherwise Postgres rejects the whole statement
// and every row on the page loses its username, not just the odd one out.
func TestFilterUUIDs(t *testing.T) {
	const (
		idA = "50774688-f198-44e6-9b5e-dfe60f96f97d"
		idB = "245efaf2-5692-43c3-ab64-0c2526b94143"
	)

	tests := []struct {
		name string
		ids  []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"all uuids", []string{idA, idB}, []string{idA, idB}},
		{"drops system", []string{idA, "system", idB}, []string{idA, idB}},
		{"drops username", []string{"jonathan", idA}, []string{idA}},
		{"nothing usable", []string{"system", "jonathan"}, []string{}},
		{"drops empty string", []string{"", idA}, []string{idA}},
		{"drops near-miss", []string{"50774688-f198-44e6-9b5e-dfe60f96f97", idA}, []string{idA}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterUUIDs(tt.ids)
			if !slices.Equal(got, tt.want) {
				t.Errorf("filterUUIDs(%v) = %v, want %v", tt.ids, got, tt.want)
			}
		})
	}
}
