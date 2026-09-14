package backup

import (
	"context"

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
