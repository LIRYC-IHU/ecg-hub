package device

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrNoEntry means the kernel has no completed ARP entry for the address. The
// device is reachable — the connection is open — but nothing local knows its
// hardware address, which is what happens across a router.
var ErrNoEntry = errors.New("device: no ARP entry")

// ErrSharedHop means the address resolved to a hop every device shares — the
// default gateway, or Docker's bridge. Returning it as an identity would make
// approving one device approve everything behind that hop, so it is refused.
var ErrSharedHop = errors.New("device: address resolves to a shared hop, not a device")

// tableTTL bounds how often the ARP table is read. Entries change on the order
// of minutes, connections arrive far faster than that, and one read serves
// every lookup in the window.
const tableTTL = 5 * time.Second

// Resolver maps a remote IP to the hardware address behind it.
//
// Zero value is not usable — call NewResolver.
type Resolver struct {
	mu       sync.Mutex
	table    map[string]string // ip -> normalised mac
	gwMACs   map[string]bool   // macs belonging to a default gateway
	loadedAt time.Time

	now     func() time.Time
	readARP func() (map[string]string, error)
	readGWs func() ([]string, error)
}

// NewResolver builds a Resolver reading the running kernel's tables.
func NewResolver() *Resolver {
	dir := procNetDir()
	return &Resolver{
		now:     time.Now,
		readARP: func() (map[string]string, error) { return readARPTable(dir) },
		readGWs: func() ([]string, error) { return readDefaultGateways(dir) },
	}
}

// HostProcNetEnv names the directory holding the host's /proc/net files.
const HostProcNetEnv = "HOST_PROC_NET"

// procNetDir chooses which /proc/net to read.
//
// A container on a bridge network has its own network namespace, so
// /proc/net/arp lists its bridge peers — the other containers — and never the
// devices on the site network, whose addresses are in the host's table. The
// server then cannot identify any device, which is a real deployment and not a
// misconfiguration: it is what docker-compose.yml describes.
//
// Bind-mounting the host's files read-only fixes it without host networking and
// without a capability:
//
//	volumes:
//	  - /proc/net/arp:/host/proc/net/arp:ro
//	  - /proc/net/route:/host/proc/net/route:ro
//	environment:
//	  HOST_PROC_NET: /host/proc/net
//
// It hands the container a list of the host's layer-2 neighbours. That is worth
// stating, and it is a small thing next to what the container already does:
// terminate the device protocols themselves.
//
// Unset, or pointing at files that are not there, falls back to the container's
// own /proc/net — the previous behaviour.
func procNetDir() string {
	dir := strings.TrimSpace(os.Getenv(HostProcNetEnv))
	if dir == "" {
		return "/proc/net"
	}
	// A regular file, not merely something at that path. Docker creates an
	// empty directory when a bind mount's source does not exist on the host —
	// which is what happens on Docker Desktop, where the machine has no
	// /proc/net at all — and a directory would otherwise pass this check and
	// fail later on every read.
	fi, err := os.Stat(filepath.Join(dir, "arp"))
	switch {
	case err != nil:
		slog.Warn("device: ignoring "+HostProcNetEnv+" — no arp file there, falling back to this container's own table",
			"dir", dir, "error", err)
		return "/proc/net"
	case !fi.Mode().IsRegular():
		slog.Warn("device: ignoring "+HostProcNetEnv+" — arp is not a file, so the bind mount did not resolve on the host",
			"dir", dir, "mode", fi.Mode().String())
		return "/proc/net"
	}
	slog.Info("device: reading the host's neighbour tables", "dir", dir)
	return dir
}

// Lookup returns the normalised MAC behind ip.
//
// Errors are the interesting part: ErrNoEntry and ErrSharedHop both mean "no
// usable identity", and the caller must treat them as such rather than falling
// back to the IP. They are distinguished because they say different things
// about the deployment — the first that the device is routed, the second that
// the server sits behind a NAT it cannot see past.
func (r *Resolver) Lookup(ip string) (string, error) {
	ip = canonicalIP(ip)
	if ip == "" {
		return "", ErrNoEntry
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.refreshLocked(); err != nil {
		return "", err
	}

	mac, ok := r.table[ip]
	if !ok || mac == "" {
		return "", ErrNoEntry
	}
	if r.gwMACs[mac] {
		return "", ErrSharedHop
	}
	return mac, nil
}

// refreshLocked reloads both tables when the cached copy has aged out.
func (r *Resolver) refreshLocked() error {
	if r.table != nil && r.now().Sub(r.loadedAt) < tableTTL {
		return nil
	}
	table, err := r.readARP()
	if err != nil {
		return fmt.Errorf("device: read ARP table: %w", err)
	}
	gwMACs := make(map[string]bool)
	// A gateway with no ARP entry of its own simply contributes nothing, which
	// is the safe direction: the check only ever refuses addresses.
	if gws, err := r.readGWs(); err == nil {
		for _, gw := range gws {
			if mac := table[gw]; mac != "" {
				gwMACs[mac] = true
			}
		}
	}
	r.table, r.gwMACs, r.loadedAt = table, gwMACs, r.now()
	return nil
}

// canonicalIP strips the port and unwraps an IPv4-mapped IPv6 address, which is
// what a dual-stack listener reports (::ffff:10.0.0.4). Returns "" for anything
// that is not an address.
func canonicalIP(s string) string {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.String()
}

// ─── kernel tables ───────────────────────────────────────────────────────────

// readARPTable returns ip -> MAC for every completed entry.
//
// /proc/net/arp is read directly rather than shelling out to arp(8): the
// container image is not guaranteed to carry net-tools, and a file read costs
// nothing per connection. macOS has no procfs, so development on a Mac falls
// back to the command.
func readARPTable(dir string) (map[string]string, error) {
	f, err := os.Open(filepath.Join(dir, "arp"))
	if err != nil {
		if os.IsNotExist(err) {
			return readARPTableCommand()
		}
		return nil, err
	}
	defer f.Close()
	return parseProcARP(f)
}

// parseProcARP reads the /proc/net/arp columns:
//
//	IP address  HW type  Flags  HW address         Mask  Device
//	10.0.0.4    0x1      0x2    00:0e:10:19:44:8a  *     eth0
//
// Flags is a bitmask; ATF_COM (0x2) marks a completed entry. An incomplete one
// carries the placeholder 00:00:00:00:00:00 and must not be mistaken for a
// device, so entries without that bit are skipped.
func parseProcARP(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		if first { // header
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		flags, err := parseHexByte(fields[2])
		if err != nil || flags&0x2 == 0 {
			continue
		}
		mac := NormalizeMAC(fields[3])
		if mac == "" || mac == "00:00:00:00:00:00" {
			continue
		}
		if ip := canonicalIP(fields[0]); ip != "" {
			out[ip] = mac
		}
	}
	return out, sc.Err()
}

// macOSARPLine matches "? (192.168.2.4) at 0:e:10:19:44:8a on bridge100 …".
// An incomplete entry reads "at (incomplete)" and does not match.
var macOSARPLine = regexp.MustCompile(`\(([0-9.]+)\) at ([0-9a-fA-F]{1,2}(?::[0-9a-fA-F]{1,2}){5})\b`)

// readARPTableCommand is the development fallback for hosts without procfs.
func readARPTableCommand() (map[string]string, error) {
	raw, err := exec.Command("arp", "-an").Output()
	if err != nil {
		return nil, fmt.Errorf("arp -an: %w", err)
	}
	out := make(map[string]string)
	for _, m := range macOSARPLine.FindAllStringSubmatch(string(raw), -1) {
		if mac := NormalizeMAC(m[2]); mac != "" {
			if ip := canonicalIP(m[1]); ip != "" {
				out[ip] = mac
			}
		}
	}
	return out, nil
}

// readDefaultGateways returns the gateway address of every default route.
//
// /proc/net/route stores addresses as little-endian hex, so 010011AC is
// 172.17.0.1 — the shape of a Docker bridge gateway, which is precisely the
// address this exists to recognise. Returns nothing on a host without procfs;
// the caller treats that as "no gateway known".
func readDefaultGateways(dir string) ([]string, error) {
	f, err := os.Open(filepath.Join(dir, "route"))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var gws []string
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		if ip := parseLittleEndianHexIP(fields[2]); ip != "" && ip != "0.0.0.0" {
			gws = append(gws, ip)
		}
	}
	return gws, sc.Err()
}

func parseLittleEndianHexIP(s string) string {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 4 {
		return ""
	}
	var v4 [4]byte
	binary.BigEndian.PutUint32(v4[:], binary.LittleEndian.Uint32(b))
	return net.IP(v4[:]).String()
}

func parseHexByte(s string) (uint64, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	b, err := hex.DecodeString(padEven(s))
	if err != nil || len(b) == 0 {
		return 0, fmt.Errorf("device: not a hex flag: %q", s)
	}
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v, nil
}

func padEven(s string) string {
	if len(s)%2 == 1 {
		return "0" + s
	}
	return s
}
