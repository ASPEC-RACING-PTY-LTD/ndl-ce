package objstore

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MemoryTransport is an in-process bucket for tests. It stores ciphertext as uploaded.
type MemoryTransport struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func NewMemoryTransport() *MemoryTransport {
	return &MemoryTransport{objects: map[string][]byte{}}
}

func (m *MemoryTransport) key(bucket, object string) string {
	return bucket + "/" + object
}

func (m *MemoryTransport) Put(_ context.Context, bucket, object string, body []byte) error {
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(object) == "" {
		return fmt.Errorf("bucket and key are required")
	}
	cp := append([]byte(nil), body...)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objects == nil {
		m.objects = map[string][]byte{}
	}
	m.objects[m.key(bucket, object)] = cp
	return nil
}

func (m *MemoryTransport) Get(_ context.Context, bucket, object string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	body, ok := m.objects[m.key(bucket, object)]
	if !ok {
		return nil, fmt.Errorf("object not found")
	}
	return append([]byte(nil), body...), nil
}

func (m *MemoryTransport) Head(_ context.Context, bucket, object string) (bool, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	body, ok := m.objects[m.key(bucket, object)]
	if !ok {
		return false, 0, nil
	}
	return true, int64(len(body)), nil
}

func (m *MemoryTransport) Delete(_ context.Context, bucket, object string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, m.key(bucket, object))
	return nil
}

func (m *MemoryTransport) Ciphertext(bucket, object string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.objects[m.key(bucket, object)]...)
}
