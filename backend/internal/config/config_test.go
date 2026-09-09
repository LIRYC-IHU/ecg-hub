package config

import (
	"net"
	"strconv"
	"testing"
)

func TestPublicOrigin(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "http://localhost"},
		{"   ", "http://localhost"},
		{"ecg-hub.chu.fr", "http://ecg-hub.chu.fr"},
		{"ecg-hub.chu.fr/", "http://ecg-hub.chu.fr"},
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

func TestIngestConfig_SetMaxFileBytes(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"", DefaultMaxFileBytes, false},
		{"   ", DefaultMaxFileBytes, false},
		{"1Mi", 1 << 20, false},
		{"10Mi", 10 << 20, false},
		{"512Ki", 512 << 10, false},
		{"0", 0, false}, // explicit opt-out
		{"-1", 0, true},
		{"banana", 0, true},
	}
	for _, c := range cases {
		var cfg IngestConfig
		err := cfg.SetMaxFileBytes(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("SetMaxFileBytes(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && cfg.MaxBytes() != c.want {
			t.Errorf("SetMaxFileBytes(%q) → MaxBytes() = %d, want %d", c.in, cfg.MaxBytes(), c.want)
		}
	}
}

// The bind address is what confines the API and the metrics endpoint under
// network_mode: host, where the compose file's port mappings — the thing that
// confined them on a bridge network — no longer exist.
func TestBindAddresses(t *testing.T) {
	cases := []struct {
		host string
		port int
		want string
	}{
		{"", 4444, ":4444"},                   // every interface, the bridge default
		{"127.0.0.1", 4444, "127.0.0.1:4444"}, // loopback, for host networking
		{"::1", 9091, "[::1]:9091"},
	}
	for _, c := range cases {
		if got := net.JoinHostPort(c.host, strconv.Itoa(c.port)); got != c.want {
			t.Errorf("bind address for host %q port %d = %q, want %q", c.host, c.port, got, c.want)
		}
	}
}
