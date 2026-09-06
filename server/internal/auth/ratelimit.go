package auth

import (
	"sync"
	"time"
)

// RateLimiter is the in-memory login rate limiter from docs/backend.md
// section 2: a sliding window of failed attempts per key (login+IP), with
// a temporary block once the window's attempt budget is exhausted. No
// external cache (Redis is explicitly out of scope) — just a mutex-guarded
// map, pruned lazily as it's used.
type RateLimiter struct {
	mu          sync.Mutex
	failures    map[string][]time.Time
	blockedTill map[string]time.Time

	maxAttempts int
	window      time.Duration
	blockFor    time.Duration
}

// NewRateLimiter builds a RateLimiter that blocks a key for blockFor once
// it accumulates maxAttempts failures within window.
func NewRateLimiter(maxAttempts int, window, blockFor time.Duration) *RateLimiter {
	return &RateLimiter{
		failures:    map[string][]time.Time{},
		blockedTill: map[string]time.Time{},
		maxAttempts: maxAttempts,
		window:      window,
		blockFor:    blockFor,
	}
}

// Allow reports whether key (typically "login|ip") may attempt a login
// right now. If blocked, ok is false and retryAfter is how much longer
// the block lasts.
func (l *RateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if until, blocked := l.blockedTill[key]; blocked {
		if now.Before(until) {
			return false, until.Sub(now)
		}
		delete(l.blockedTill, key)
		delete(l.failures, key)
	}
	return true, 0
}

// RecordFailure records a failed login attempt for key, blocking it if
// this pushes it over maxAttempts within window.
func (l *RateLimiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	attempts := pruneOlderThan(l.failures[key], now.Add(-l.window))
	attempts = append(attempts, now)
	l.failures[key] = attempts

	if len(attempts) >= l.maxAttempts {
		l.blockedTill[key] = now.Add(l.blockFor)
	}
}

// RecordSuccess clears any failure history for key (a successful login
// resets the window).
func (l *RateLimiter) RecordSuccess(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
	delete(l.blockedTill, key)
}

func pruneOlderThan(times []time.Time, cutoff time.Time) []time.Time {
	out := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}
