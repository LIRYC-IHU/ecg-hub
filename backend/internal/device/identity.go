// Package device resolves the hardware identity behind an incoming ingestion
// connection and decides whether that device is allowed to send.
//
// The identity is the MAC address, read from the kernel's ARP cache. That only
// works while the device shares a broadcast domain with the server: behind a
// router — or behind Docker's port mapping, where every connection arrives from
// the bridge gateway — the address resolved is the last hop's, identical for
// every device. Approving one device would then approve the whole network, so
// the resolver refuses to hand back an address it recognises as the gateway's
// and the caller is told the MAC is unavailable rather than given a wrong one.
//
// A MAC is trivially spoofable. This is an enrolment control, not an
// authentication boundary.
package device

import "strings"

// Identity is what is known about the far end of an incoming connection.
type Identity struct {
	// MAC is the normalised lower-case address, or "" when it could not be
	// resolved — no ARP entry, or an entry that turned out to be the gateway's.
	MAC string
	// IP is the remote address the connection came from. Display and logging
	// only: DHCP moves it, so it is never an identity.
	IP string
	// Source is the ingestion port: "ftp", "dicom" or "ectp".
	Source string
}

// Resolved reports whether a usable hardware identity was found.
func (i Identity) Resolved() bool { return i.MAC != "" }

// NormalizeMAC renders a hardware address as lower-case colon-separated bytes
// with their leading zeros, "aa:bb:cc:dd:ee:ff".
//
// macOS prints ARP entries with the zeros trimmed ("0:e:10:19:44:8a"), Linux
// does not, and either can be the format a row was stored in. Normalising on
// the way in is what lets the stored MAC be compared as a plain string.
// Returns "" when s is not six hex bytes — an incomplete ARP entry, mostly.
func NormalizeMAC(s string) string {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 6 {
		return ""
	}
	out := make([]string, 6)
	for i, p := range parts {
		if len(p) == 0 || len(p) > 2 || !isHex(p) {
			return ""
		}
		if len(p) == 1 {
			p = "0" + p
		}
		out[i] = strings.ToLower(p)
	}
	return strings.Join(out, ":")
}

// OUI is the manufacturer prefix: the first three bytes, upper-case. Shown next
// to a bare MAC so an operator has something to recognise a device by. The
// prefix is kept as-is — the IEEE registry that maps it to a manufacturer name
// is megabytes, and not worth shipping to save one lookup.
func OUI(mac string) string {
	parts := strings.Split(mac, ":")
	if len(parts) < 3 {
		return ""
	}
	return strings.ToUpper(strings.Join(parts[:3], ":"))
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
