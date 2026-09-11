package backuppack

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

const gzipMagic = "\x1f\x8b"

// SidecarMax is the RAM envelope for manifest, config, and checksum JSON.
// Payload chunks stay at ChunkSize; sidecars may be larger but never workload-scale.
const SidecarMax = 1 << 20

// WrapFunc seals one bounded chunk. Implementations must refuse oversized
// plaintext so a backup cannot expand into a workload-sized RAM buffer.
type WrapFunc func(plain []byte) ([]byte, error)

// GzipWrap compresses one bounded buffer without encryption. Payload chunks
// must stay at ChunkSize; JSON sidecars may use wrapLimit().
func GzipWrap(plain []byte) ([]byte, error) {
	if err := CheckPlainSize(len(plain)); err != nil {
		if err := CheckSidecarSize(len(plain)); err != nil {
			return nil, err
		}
	}
	return gzipBytes(plain)
}

// GzipUnwrap expands one GzipWrap chunk.
func GzipUnwrap(blob []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	limit := wrapLimit()
	plain, err := io.ReadAll(io.LimitReader(gz, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(plain) > limit {
		return nil, fmt.Errorf("chunk plaintext %d exceeds %d byte envelope", len(plain), limit)
	}
	return plain, nil
}

func IsGzip(blob []byte) bool {
	return len(blob) >= 2 && string(blob[:2]) == gzipMagic
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func marshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

func CheckPlainSize(n int) error {
	if n < 0 {
		return fmt.Errorf("chunk is invalid")
	}
	if n > ChunkSize {
		return fmt.Errorf("chunk plaintext %d exceeds %d byte envelope", n, ChunkSize)
	}
	return nil
}

func wrapLimit() int {
	if ChunkSize > SidecarMax {
		return ChunkSize
	}
	return SidecarMax
}

func CheckSidecarSize(n int) error {
	if n < 0 {
		return fmt.Errorf("sidecar is invalid")
	}
	lim := wrapLimit()
	if n > lim {
		return fmt.Errorf("sidecar %d exceeds %d byte envelope", n, lim)
	}
	return nil
}

func gzipBytes(plain []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := gz.Write(plain); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
