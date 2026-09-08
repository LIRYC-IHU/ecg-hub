package device

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const procARPSample = `IP address       HW type     Flags       HW address            Mask     Device
10.27.26.1       0x1         0x2         00:1b:21:aa:bb:cc     *        eth0
10.27.26.40      0x1         0x2         00:0e:10:19:44:8a     *        eth0
10.27.26.41      0x1         0x0         00:00:00:00:00:00     *        eth0
10.27.26.42      0x1         0x2         0:e:10:19:44:8b       *        eth0
`

func TestParseProcARP(t *testing.T) {
	got, err := parseProcARP(strings.NewReader(procARPSample))
	if err != nil {
		t.Fatalf("parseProcARP: %v", err)
	}
	want := map[string]string{
		"10.27.26.1":  "00:1b:21:aa:bb:cc",
		"10.27.26.40": "00:0e:10:19:44:8a",
		"10.27.26.42": "00:0e:10:19:44:8b", // leading zeros restored
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %v", len(got), len(want), got)
	}
	for ip, mac := range want {
		if got[ip] != mac {
			t.Errorf("entry %s = %q, want %q", ip, got[ip], mac)
		}
	}
	if _, ok := got["10.27.26.41"]; ok {
		t.Error("an incomplete ARP entry (flags 0x0) must be skipped")
	}
}

func TestParseLittleEndianHexIP(t *testing.T) {
	// /proc/net/route stores a Docker bridge gateway as 010011AC.
	if got, want := parseLittleEndianHexIP("010011AC"), "172.17.0.1"; got != want {
		t.Errorf("parseLittleEndianHexIP = %q, want %q", got, want)
	}
	if got := parseLittleEndianHexIP("zz"); got != "" {
		t.Errorf("parseLittleEndianHexIP of garbage = %q, want empty", got)
	}
}

func TestCanonicalIP(t *testing.T) {
	cases := []struct{ in, want string }{
		{"10.0.0.4", "10.0.0.4"},
		{"10.0.0.4:51234", "10.0.0.4"},
		{"::ffff:10.0.0.4", "10.0.0.4"}, // dual-stack listener
		{"[fe80::1]:2121", "fe80::1"},
		{"not-an-address", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := canonicalIP(c.in); got != c.want {
			t.Errorf("canonicalIP(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// testResolver builds a Resolver over fixed tables, with no filesystem.
func testResolver(arp map[string]string, gws []string) *Resolver {
	return &Resolver{
		now:     time.Now,
		readARP: func() (map[string]string, error) { return arp, nil },
		readGWs: func() ([]string, error) { return gws, nil },
	}
}

func TestResolverLookup(t *testing.T) {
	r := testResolver(map[string]string{
		"172.17.0.1":  "02:42:aa:bb:cc:dd", // the bridge gateway
		"10.27.26.40": "00:0e:10:19:44:8a",
	}, []string{"172.17.0.1"})

	mac, err := r.Lookup("10.27.26.40:51234")
	if err != nil || mac != "00:0e:10:19:44:8a" {
		t.Errorf("Lookup of a device = (%q, %v), want the device MAC", mac, err)
	}

	// The whole point: the gateway's own MAC is not a device identity.
	if _, err := r.Lookup("172.17.0.1"); !errors.Is(err, ErrSharedHop) {
		t.Errorf("Lookup of the gateway = %v, want ErrSharedHop", err)
	}

	if _, err := r.Lookup("10.27.26.99"); !errors.Is(err, ErrNoEntry) {
		t.Errorf("Lookup of an unknown address = %v, want ErrNoEntry", err)
	}
}

// A container on a Docker bridge sees every connection arrive from the gateway,
// so its ARP table holds nothing but the gateway. Enabling the whitelist there
// would approve the whole network through one device — the resolver must say so.
func TestResolverDegradedBehindABridge(t *testing.T) {
	bridge := testResolver(map[string]string{
		"172.17.0.1": "02:42:aa:bb:cc:dd",
	}, []string{"172.17.0.1"})
	if !bridge.Degraded() {
		t.Error("a table holding only the gateway must report Degraded")
	}

	hostMode := testResolver(map[string]string{
		"172.17.0.1":  "02:42:aa:bb:cc:dd",
		"10.27.26.40": "00:0e:10:19:44:8a",
	}, []string{"172.17.0.1"})
	if hostMode.Degraded() {
		t.Error("a table holding a real device must not report Degraded")
	}
}

func TestResolverCachesTheTable(t *testing.T) {
	reads := 0
	now := time.Now()
	r := &Resolver{
		now: func() time.Time { return now },
		readARP: func() (map[string]string, error) {
			reads++
			return map[string]string{"10.0.0.4": "aa:bb:cc:dd:ee:ff"}, nil
		},
		readGWs: func() ([]string, error) { return nil, nil },
	}
	for i := 0; i < 5; i++ {
		if _, err := r.Lookup("10.0.0.4"); err != nil {
			t.Fatalf("Lookup: %v", err)
		}
	}
	if reads != 1 {
		t.Errorf("read the ARP table %d times within the TTL, want 1", reads)
	}

	now = now.Add(tableTTL + time.Second)
	if _, err := r.Lookup("10.0.0.4"); err != nil {
		t.Fatalf("Lookup after the TTL: %v", err)
	}
	if reads != 2 {
		t.Errorf("read the ARP table %d times across the TTL, want 2", reads)
	}
}
