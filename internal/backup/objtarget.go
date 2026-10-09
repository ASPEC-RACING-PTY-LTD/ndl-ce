package backup

import (
	"context"
	"fmt"

	"github.com/no-dal/ndl-ce/internal/objstore"
)

// TransportTarget adapts the existing object-store Transport (Cloudflare R2 and
// other S3-compatible backends) to the engine's Target interface. Pack objects
// are already compressed and authenticated-encrypted by the engine before they
// reach here, so this adapter uploads opaque bytes. The underlying S3 transport
// selects multipart upload for large packs automatically, which is where R2
// multipart parallelism and resumability apply; small pack objects use a single
// parallel PUT. No plaintext or key material passes through this layer.
type TransportTarget struct {
	T      objstore.Transport
	Bucket string
}

var _ Target = TransportTarget{}

func (t TransportTarget) Put(ctx context.Context, key string, data []byte) error {
	return t.T.Put(ctx, t.Bucket, key, data)
}

func (t TransportTarget) Head(ctx context.Context, key string) (bool, int64, error) {
	return t.T.Head(ctx, t.Bucket, key)
}

func (t TransportTarget) Get(ctx context.Context, key string) ([]byte, error) {
	return t.T.Get(ctx, t.Bucket, key)
}

// Delete removes one object.
func (t TransportTarget) Delete(ctx context.Context, key string) error {
	return t.T.Delete(ctx, t.Bucket, key)
}

// List enumerates objects under prefix when the transport supports listing.
func (t TransportTarget) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	l, ok := t.T.(objstore.Lister)
	if !ok {
		return nil, fmt.Errorf("this object store transport cannot list objects")
	}
	entries, err := l.List(ctx, t.Bucket, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, ObjectInfo{Key: e.Key, Size: e.Size})
	}
	return out, nil
}
