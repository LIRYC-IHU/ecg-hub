package handlers

import (
	"log/slog"
	"sync"
	"time"
)

// loginAttempts tracks consecutive failures and the active lockout window for one key.
type loginAttempts struct {
	failures    int
	lockedUntil time.Time
}

// LoginThrottle is an in-memory brute-force guard for the login endpoint.
//
// Keys are expected to combine client IP and username (e.g. "1.2.3.4|alice"),
// so a single attacker IP cannot lock a victim's account from arbitrary sources
// (avoids an account-lockout DoS) while still slowing credential stuffing.
//
// In-memory state is per-instance: for a multi-replica deployment this throttle
// is best-effort and should be complemented by the IP-based rate limiter at the
// edge (nginx) or a shared store. See EPIC-011 Story 11.4.
type LoginThrottle struct {
	mu          sync.Mutex
	byKey       map[string]*loginAttempts
	maxFailures int
	lockout     time.Duration
}

// NewLoginThrottle builds a throttle that locks a key for lockout duration after
// maxFailures consecutive failures.
func NewLoginThrottle(maxFailures int, lockout time.Duration) *LoginThrottle {
	return &LoginThrottle{
		byKey:       make(map[string]*loginAttempts),
		maxFailures: maxFailures,
		lockout:     lockout,
	}
}

// Locked reports whether key is currently locked out and the remaining duration.
func (t *LoginThrottle) Locked(key string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a := t.byKey[key]
	if a == nil {
		return false, 0
	}
	if remaining := time.Until(a.lockedUntil); remaining > 0 {
		return true, remaining
	}
	return false, 0
}

// Fail records a failed attempt and locks the key once the threshold is reached.
func (t *LoginThrottle) Fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a := t.byKey[key]
	if a == nil {
		a = &loginAttempts{}
		t.byKey[key] = a
	}
	// If a previous lock has expired, start a fresh failure window.
	if a.failures >= t.maxFailures && time.Now().After(a.lockedUntil) {
		a.failures = 0
	}
	a.failures++
	if a.failures >= t.maxFailures {
		a.lockedUntil = time.Now().Add(t.lockout)
		slog.Warn("auth: account locked after repeated failed logins",
			"key", key, "failures", a.failures, "lockout", t.lockout.String())
	}
}

// Reset clears the failure counter for key — call on a successful login.
func (t *LoginThrottle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.byKey, key)
}
