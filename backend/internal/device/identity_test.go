package device

import "testing"

func TestNormalizeMAC(t *testing.T) {
	cases := []struct{ in, want string }{
		{"00:0e:10:19:44:8a", "00:0e:10:19:44:8a"},
		{"0:e:10:19:44:8a", "00:0e:10:19:44:8a"}, // macOS trims leading zeros
		{"AA:BB:CC:DD:EE:FF", "aa:bb:cc:dd:ee:ff"},
		{"  aa:bb:cc:dd:ee:ff  ", "aa:bb:cc:dd:ee:ff"},
		{"", ""},
		{"aa:bb:cc:dd:ee", ""},       // too short
		{"aa:bb:cc:dd:ee:ff:00", ""}, // too long
		{"aa-bb-cc-dd-ee-ff", ""},    // not the format the tables use
		{"gg:bb:cc:dd:ee:ff", ""},    // not hex
		{"aaa:bb:cc:dd:ee:ff", ""},   // not a byte
	}
	for _, c := range cases {
		if got := NormalizeMAC(c.in); got != c.want {
			t.Errorf("NormalizeMAC(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOUI(t *testing.T) {
	if got, want := OUI("00:0e:10:19:44:8a"), "00:0E:10"; got != want {
		t.Errorf("OUI = %q, want %q", got, want)
	}
	if got := OUI("nonsense"); got != "" {
		t.Errorf("OUI of a non-MAC = %q, want empty", got)
	}
}

func TestIdentityResolved(t *testing.T) {
	if (Identity{IP: "10.0.0.4"}).Resolved() {
		t.Error("an identity with no MAC must not report as resolved")
	}
	if !(Identity{MAC: "aa:bb:cc:dd:ee:ff"}).Resolved() {
		t.Error("an identity with a MAC must report as resolved")
	}
}
