package objstore

import (
	"context"
	"fmt"
)

// Repo adapts a Transport to backuppack.Repository for one bucket.
type Repo struct {
	T      Transport
	Bucket string
}

func (r Repo) Put(ctx context.Context, key string, body []byte) error {
	if r.T == nil {
		return fmt.Errorf("object transport is unavailable")
	}
	return r.T.Put(ctx, r.Bucket, key, body)
}

func (r Repo) Get(ctx context.Context, key string) ([]byte, error) {
	if r.T == nil {
		return nil, fmt.Errorf("object transport is unavailable")
	}
	return r.T.Get(ctx, r.Bucket, key)
}

func (r Repo) Head(ctx context.Context, key string) (bool, int64, error) {
	if r.T == nil {
		return false, 0, fmt.Errorf("object transport is unavailable")
	}
	return r.T.Head(ctx, r.Bucket, key)
}

func (r Repo) Delete(ctx context.Context, key string) error {
	if r.T == nil {
		return fmt.Errorf("object transport is unavailable")
	}
	return r.T.Delete(ctx, r.Bucket, key)
}

func (r Repo) CloseIdle() {
	if c, ok := r.T.(interface{ CloseIdle() }); ok {
		c.CloseIdle()
	}
}
