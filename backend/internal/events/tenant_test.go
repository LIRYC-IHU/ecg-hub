package events

import (
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

func TestEventFor(t *testing.T) {
	cases := []struct {
		event, consumer string
		want            bool
	}{
		{"", "", true},                     // mode off: everything is the default tenant
		{"", models.DefaultTenantID, true}, // device events reach the default tenant
		{"tenant-b", "tenant-b", true},     // own tenant
		{"tenant-b", "tenant-a", false},    // never across tenants
		{"tenant-b", "", false},            // nor to the default tenant
		{models.DefaultTenantID, "tenant-a", false},
	}
	for _, c := range cases {
		if got := (Event{TenantID: c.event}).For(c.consumer); got != c.want {
			t.Errorf("event %q for %q: got %v, want %v", c.event, c.consumer, got, c.want)
		}
	}
}
