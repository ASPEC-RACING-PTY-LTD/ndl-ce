package auth

import (
	"sync"
	"time"
)

const (
	maxFailures = 8
	window      = 15 * time.Minute
	lockFor     = 15 * time.Minute
)

// Lockout is an in-process brute-force gate.
type Lockout struct {
	mu   sync.Mutex
	seen map[string]*bucket
}

type bucket struct {
	fails  int
	first  time.Time
	locked time.Time
}

// NewLockout returns an empty lockout table.
func NewLockout() *Lockout {
	return &Lockout{seen: map[string]*bucket{}}
}

// Check returns an error if key is locked.
func (l *Lockout) Check(key string, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.seen[key]
	if b == nil {
		return nil
	}
	if !b.locked.IsZero() && now.Before(b.locked) {
		return ErrLocked
	}
	if !b.first.IsZero() && now.Sub(b.first) > window {
		delete(l.seen, key)
	}
	return nil
}

// Fail records a failed attempt.
func (l *Lockout) Fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.seen[key]
	if b == nil || (!b.first.IsZero() && now.Sub(b.first) > window) {
		b = &bucket{first: now}
		l.seen[key] = b
	}
	b.fails++
	if b.fails >= maxFailures {
		b.locked = now.Add(lockFor)
	}
}

// Success clears the key.
func (l *Lockout) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, key)
}

// ErrLocked is returned when login is temporarily blocked.
var ErrLocked = errLocked{}

type errLocked struct{}

func (errLocked) Error() string { return "too many failed login attempts" }

// ClearSuffix clears every key ending in suffix (for example "|alice" for
// all of a user's login buckets) and returns how many it cleared.
func (l *Lockout) ClearSuffix(suffix string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for k := range l.seen {
		if len(k) >= len(suffix) && k[len(k)-len(suffix):] == suffix {
			delete(l.seen, k)
			n++
		}
	}
	return n
}
