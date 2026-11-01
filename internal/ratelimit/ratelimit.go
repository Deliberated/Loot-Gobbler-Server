// Package ratelimit counts events per key in a sliding time window (e.g. failed logins per IP).
package ratelimit

import (
	"sync"
	"time"
)

// Limiter allows at most Max recorded events per key within Window. Counts live in memory and reset on restart.
type Limiter struct {
	Max    int
	Window time.Duration
	Now    func() time.Time // replaceable in tests

	mu   sync.Mutex
	hits map[string][]time.Time
}

func New(max int, window time.Duration) *Limiter {
	return &Limiter{Max: max, Window: window, Now: time.Now, hits: map[string][]time.Time{}}
}

// Check reports whether key is still below the limit. If not, retryAfter says when the oldest event expires.
func (l *Limiter) Check(key string) (retryAfter time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	hits := l.prune(key, now)
	if len(hits) < l.Max {
		return 0, true
	}
	return hits[0].Add(l.Window).Sub(now), false
}

// Record counts one event for key.
func (l *Limiter) Record(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	l.hits[key] = append(l.prune(key, now), now)
}

// Reset forgets all events for key.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}

// Sweep drops keys whose events have all expired, so the map does not grow forever.
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	for key := range l.hits {
		l.prune(key, now)
	}
}

func (l *Limiter) prune(key string, now time.Time) []time.Time {
	hits := l.hits[key]
	cutoff := now.Add(-l.Window)
	i := 0
	for i < len(hits) && !hits[i].After(cutoff) {
		i++
	}
	hits = hits[i:]
	if len(hits) == 0 {
		delete(l.hits, key)
		return nil
	}
	l.hits[key] = hits
	return hits
}
