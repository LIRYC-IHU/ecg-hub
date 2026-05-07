package metrics

import (
	"sync"
	"time"
)

const maxErrors = 50

type ErrorEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Method     string    `json:"method"`
	Route      string    `json:"route"`
	Status     int       `json:"status"`
	Error      string    `json:"error,omitempty"`
	RequestURI string    `json:"request_uri"`
	UserID     string    `json:"user_id,omitempty"`
	Duration   float64   `json:"duration_ms"`
}

var (
	errorsMu  sync.RWMutex
	errorRing []ErrorEntry
)

func RecordError(entry ErrorEntry) {
	errorsMu.Lock()
	defer errorsMu.Unlock()
	errorRing = append(errorRing, entry)
	if len(errorRing) > maxErrors {
		errorRing = errorRing[len(errorRing)-maxErrors:]
	}
}

func RecentErrors(limit int) []ErrorEntry {
	errorsMu.RLock()
	defer errorsMu.RUnlock()
	if limit <= 0 || limit > len(errorRing) {
		limit = len(errorRing)
	}
	result := make([]ErrorEntry, limit)
	copy(result, errorRing[len(errorRing)-limit:])
	// Reverse: most recent first
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}
