package config

import "testing"

func TestPublicOrigin(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "http://localhost"},
		{"   ", "http://localhost"},
		{"ecg-hub.chu.fr", "http://ecg-hub.chu.fr"},
		{"http://10.0.0.5", "http://10.0.0.5"},
		{"https://ecg-hub.chu.fr", "https://ecg-hub.chu.fr"},
		{"https://ecg-hub.chu.fr/", "https://ecg-hub.chu.fr"},
		{" ecg-hub.chu.fr ", "http://ecg-hub.chu.fr"},
	}
	for _, c := range cases {
		if got := PublicOrigin(c.in); got != c.want {
			t.Errorf("PublicOrigin(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
