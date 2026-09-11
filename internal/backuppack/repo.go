package backuppack

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Repository stores pack objects. Puts are bounded: callers never pass a
// whole-workload buffer, only one chunk or a small JSON document.
type Repository interface {
	Put(ctx context.Context, key string, body []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Head(ctx context.Context, key string) (exists bool, size int64, err error)
	Delete(ctx context.Context, key string) error
}

// DirRepo writes the pack tree under Root. Keys are relative paths.
type DirRepo struct {
	Root string
}

func (d DirRepo) resolve(key string) (string, error) {
	root := filepath.Clean(d.Root)
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("pack root is not an absolute path")
	}
	key = strings.Trim(strings.TrimSpace(key), "/")
	if key == "" || strings.Contains(key, "..") {
		return "", fmt.Errorf("pack object key is invalid")
	}
	p := filepath.Join(root, filepath.FromSlash(key))
	rel, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("pack object key is invalid")
	}
	return p, nil
}

func (d DirRepo) Put(_ context.Context, key string, body []byte) error {
	p, err := d.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	return os.WriteFile(p, body, 0o640)
}

func (d DirRepo) Get(_ context.Context, key string) ([]byte, error) {
	p, err := d.resolve(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

func (d DirRepo) Head(_ context.Context, key string) (bool, int64, error) {
	p, err := d.resolve(key)
	if err != nil {
		return false, 0, err
	}
	st, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}
	return true, st.Size(), nil
}

func (d DirRepo) Delete(_ context.Context, key string) error {
	p, err := d.resolve(key)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// MemRepo is an in-process pack store for tests. Each object is one chunk.
type MemRepo struct {
	Objects map[string][]byte
	MaxPut  int
}

func (m *MemRepo) Put(_ context.Context, key string, body []byte) error {
	if m.Objects == nil {
		m.Objects = map[string][]byte{}
	}
	if m.MaxPut > 0 && len(body) > m.MaxPut {
		return fmt.Errorf("put exceeds memory envelope: %d > %d", len(body), m.MaxPut)
	}
	cp := append([]byte(nil), body...)
	m.Objects[key] = cp
	return nil
}

func (m *MemRepo) Get(_ context.Context, key string) ([]byte, error) {
	body, ok := m.Objects[key]
	if !ok {
		return nil, fmt.Errorf("object not found")
	}
	return append([]byte(nil), body...), nil
}

func (m *MemRepo) Head(_ context.Context, key string) (bool, int64, error) {
	body, ok := m.Objects[key]
	if !ok {
		return false, 0, nil
	}
	return true, int64(len(body)), nil
}

func (m *MemRepo) Delete(_ context.Context, key string) error {
	delete(m.Objects, key)
	return nil
}

// LimitedWriter rejects writes that would exceed N bytes of live buffer.
type LimitedWriter struct {
	N    int
	used int
	W    io.Writer
}

func (l *LimitedWriter) Write(p []byte) (int, error) {
	l.used += len(p)
	if l.N > 0 && l.used > l.N {
		return 0, fmt.Errorf("write exceeds memory envelope")
	}
	if l.W == nil {
		return len(p), nil
	}
	return l.W.Write(p)
}
