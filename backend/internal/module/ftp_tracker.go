package module

import "sync"

// ftpTrackers holds every module that wants to be notified of successful FTP
// uploads. It is populated once at startup and consulted again whenever the FTP
// server is (re)started from the admin UI — a UI-triggered restart builds a
// fresh ingestion.Server that would otherwise lose its file-received hook,
// silently breaking nihon-kohden ECTP FILE|ENDS verification.
var (
	ftpTrackersMu sync.RWMutex
	ftpTrackers   []FTPFileTracker
)

// RegisterFTPFileTracker records t so the FTP file-received hook can be rebuilt
// for it on every server (re)start. Safe for concurrent use.
func RegisterFTPFileTracker(t FTPFileTracker) {
	ftpTrackersMu.Lock()
	defer ftpTrackersMu.Unlock()
	ftpTrackers = append(ftpTrackers, t)
}

// FTPFileTrackers returns a snapshot of all registered FTP file trackers.
func FTPFileTrackers() []FTPFileTracker {
	ftpTrackersMu.RLock()
	defer ftpTrackersMu.RUnlock()
	out := make([]FTPFileTracker, len(ftpTrackers))
	copy(out, ftpTrackers)
	return out
}
