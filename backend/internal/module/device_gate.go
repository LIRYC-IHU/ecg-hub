package module

import (
	"context"
	"sync"

	"github.com/LIRYC-IHU/ecg-hub/internal/device"
)

// DeviceGate decides whether the hardware behind an incoming connection may
// ingest. Implemented by device.Gate.
type DeviceGate interface {
	Identify(remoteAddr, source string) device.Identity
	Decide(ctx context.Context, id device.Identity) device.Decision
	// Recheck re-evaluates a device without recording another contact, for
	// callers that hold a session open across several files.
	Recheck(ctx context.Context, id device.Identity) device.Decision
}

// Modules that open their own listener — nihon-kohden's ECTP server — need the
// whitelist, and Module.Start takes only a *config.Config. Rather than widen
// that signature for every module, main registers the gate here once at startup
// and a module reads it when it binds, the same way FTP file trackers are
// shared (see ftp_tracker.go).
var (
	deviceGateMu sync.RWMutex
	deviceGate   DeviceGate
)

// SetDeviceGate records the gate modules should consult. Safe for concurrent
// use; called once from main before modules start.
func SetDeviceGate(g DeviceGate) {
	deviceGateMu.Lock()
	defer deviceGateMu.Unlock()
	deviceGate = g
}

// ActiveDeviceGate returns the registered gate, or nil when no whitelist is
// wired — in which case a module allows every device, as before the whitelist
// existed.
func ActiveDeviceGate() DeviceGate {
	deviceGateMu.RLock()
	defer deviceGateMu.RUnlock()
	return deviceGate
}
