package ratelimit

import (
	"sync"
	"time"
)

// Limits holds the three windows enforced for every key (usually a client IP).
// A zero or negative value disables that window.
type Limits struct {
	Minute int
	Hour   int
	Day    int
}

func (l Limits) at(i int) int {
	switch i {
	case 0:
		return l.Minute
	case 1:
		return l.Hour
	default:
		return l.Day
	}
}

var scopes = [3]time.Duration{time.Minute, time.Hour, 24 * time.Hour}

const maxKeys = 100_000

type entry struct {
	start [3]int64
	count [3]int
}

// Limiter is a fixed-window counter kept entirely in memory. It is intentionally
// simple: no Redis, no database, and a restart only resets the current window.
type Limiter struct {
	limits  Limits
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]*entry
}

func New(limits Limits) *Limiter {
	return NewWithClock(limits, time.Now)
}

func NewWithClock(limits Limits, now func() time.Time) *Limiter {
	return &Limiter{limits: limits, now: now, entries: make(map[string]*entry)}
}

// Allow records one hit for key and reports whether it fits inside every window.
// When it does not, the returned duration is how long the caller should wait.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now().Unix()

	l.mu.Lock()
	defer l.mu.Unlock()

	e := l.entries[key]
	if e == nil {
		if len(l.entries) >= maxKeys {
			l.evictLocked()
		}
		e = &entry{}
		l.entries[key] = e
	}

	for i, size := range scopes {
		seconds := int64(size / time.Second)
		start := now / seconds * seconds
		if e.start[i] != start {
			e.start[i] = start
			e.count[i] = 0
		}
	}

	for i, size := range scopes {
		limit := l.limits.at(i)
		if limit <= 0 {
			continue
		}
		if e.count[i] >= limit {
			seconds := int64(size / time.Second)
			wait := time.Duration(e.start[i]+seconds-now) * time.Second
			if wait < time.Second {
				wait = time.Second
			}
			return false, wait
		}
	}

	for i := range scopes {
		if l.limits.at(i) > 0 {
			e.count[i]++
		}
	}
	return true, 0
}

// Cleanup drops keys that have been idle for more than a day. Call it
// periodically from a background goroutine.
func (l *Limiter) Cleanup() {
	now := l.now().Unix()
	day := int64(24 * time.Hour / time.Second)

	l.mu.Lock()
	defer l.mu.Unlock()
	for key, e := range l.entries {
		if now-e.start[2] > day*2 {
			delete(l.entries, key)
		}
	}
}

func (l *Limiter) evictLocked() {
	// Drop roughly a tenth of the map. Good enough: the map only fills up when
	// somebody is deliberately cycling source addresses.
	target := len(l.entries) / 10
	if target < 1 {
		target = 1
	}
	for key := range l.entries {
		delete(l.entries, key)
		target--
		if target == 0 {
			return
		}
	}
}
