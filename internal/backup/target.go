package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Target is a remote backup destination. It is intentionally generic so that
// Cloudflare R2, other S3-compatible stores, or a local target can all satisfy
// it. Objects are immutable packs and manifests keyed by string. Large packs
// are uploaded by the adapter using multipart where appropriate; this interface
// stays byte-oriented and bounded because packs are size-capped.
type Target interface {
	Put(ctx context.Context, key string, data []byte) error
	Head(ctx context.Context, key string) (exists bool, size int64, err error)
	Get(ctx context.Context, key string) ([]byte, error)
}

// LocalTarget stores objects under a directory. It stands in for a remote store
// in development and tests, and is a legitimate local backup target.
type LocalTarget struct {
	Root string
}

func (t LocalTarget) path(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	return filepath.Join(t.Root, clean), nil
}

func (t LocalTarget) Put(_ context.Context, key string, data []byte) error {
	p, err := t.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	return writeFileAtomic(p, data)
}

func (t LocalTarget) Head(_ context.Context, key string) (bool, int64, error) {
	p, err := t.path(key)
	if err != nil {
		return false, 0, err
	}
	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}
	return true, info.Size(), nil
}

func (t LocalTarget) Get(_ context.Context, key string) ([]byte, error) {
	p, err := t.path(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// MemTarget is an in-process target for tests. It can simulate latency and a
// number of transient failures per key to exercise retry and resume paths.
type MemTarget struct {
	mu        sync.Mutex
	objects   map[string][]byte
	Delay     time.Duration
	failuresK map[string]int // remaining forced failures per key
	Puts      int
	InFlight  int
	PeakPuts  int
}

// NewMemTarget builds an empty in-memory target.
func NewMemTarget() *MemTarget {
	return &MemTarget{objects: map[string][]byte{}, failuresK: map[string]int{}}
}

// FailFor forces the next n Put calls for key to fail transiently.
func (m *MemTarget) FailFor(key string, n int) {
	m.mu.Lock()
	m.failuresK[key] = n
	m.mu.Unlock()
}

func (m *MemTarget) Put(ctx context.Context, key string, data []byte) error {
	m.mu.Lock()
	m.InFlight++
	if m.InFlight > m.PeakPuts {
		m.PeakPuts = m.InFlight
	}
	delay := m.Delay
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.InFlight--
		m.mu.Unlock()
	}()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if left := m.failuresK[key]; left > 0 {
		m.failuresK[key] = left - 1
		return fmt.Errorf("simulated transient failure for %s", key)
	}
	m.objects[key] = append([]byte(nil), data...)
	m.Puts++
	return nil
}

func (m *MemTarget) Head(_ context.Context, key string) (bool, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[key]
	if !ok {
		return false, 0, nil
	}
	return true, int64(len(b)), nil
}

// PutCount returns the number of successful puts (thread-safe).
func (m *MemTarget) PutCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Puts
}

// PeakConcurrency returns the peak number of concurrent puts (thread-safe).
func (m *MemTarget) PeakConcurrency() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.PeakPuts
}

func (m *MemTarget) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("object %s not found", key)
	}
	return append([]byte(nil), b...), nil
}
