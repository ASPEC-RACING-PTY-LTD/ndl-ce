package platformbackup

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// MemoryStore is an in-memory Store for tests.
type MemoryStore struct {
	mu   sync.Mutex
	Objs map[string][]byte
	// FailPut, when set, fails puts of matching keys.
	FailPut func(key string) error
	// Corrupt, when set, alters what Get returns for matching keys.
	Corrupt func(key string, body []byte) []byte
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{Objs: map[string][]byte{}} }

func (m *MemoryStore) Put(_ context.Context, key string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailPut != nil {
		if err := m.FailPut(key); err != nil {
			return err
		}
	}
	m.Objs[key] = append([]byte(nil), body...)
	return nil
}

func (m *MemoryStore) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Objs[key]
	if !ok {
		return nil, ErrNotFound
	}
	out := append([]byte(nil), b...)
	if m.Corrupt != nil {
		out = m.Corrupt(key, out)
	}
	return out, nil
}

func (m *MemoryStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Objs, key)
	return nil
}

func (m *MemoryStore) List(_ context.Context, prefix string) ([]Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Object
	for k, v := range m.Objs {
		if strings.HasPrefix(k, prefix) {
			out = append(out, Object{Key: k, Size: int64(len(v))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
