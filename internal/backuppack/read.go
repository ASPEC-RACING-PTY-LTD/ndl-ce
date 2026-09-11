package backuppack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
)

// OpenFunc opens one sealed chunk or sidecar.
type OpenFunc func(blob []byte) ([]byte, error)

// ReadManifest loads and verifies the committed pack record.
func ReadManifest(ctx context.Context, repo Repository, prefix string, open OpenFunc) (Manifest, error) {
	raw, err := repo.Get(ctx, ManifestKey(prefix))
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	plain, err := openBlob(raw, open)
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	var meta Manifest
	if err := json.Unmarshal(plain, &meta); err != nil {
		return Manifest{}, fmt.Errorf("manifest is not valid JSON")
	}
	if meta.Version != Version || meta.Format != FormatNDLB {
		return Manifest{}, fmt.Errorf("unsupported backup pack version")
	}
	if meta.PayloadSHA256 == "" {
		return Manifest{}, fmt.Errorf("manifest is missing payload checksum")
	}
	return meta, nil
}

// ReadConfig returns the sealed config document, or nil when absent.
func ReadConfig(ctx context.Context, repo Repository, prefix string, open OpenFunc) ([]byte, error) {
	raw, err := repo.Get(ctx, ConfigKey(prefix))
	if err != nil {
		return nil, nil
	}
	return openBlob(raw, open)
}

// Reconstruct writes the concatenated payload to dest and verifies checksums.
func Reconstruct(ctx context.Context, repo Repository, prefix string, dest io.Writer, open OpenFunc) (Manifest, error) {
	meta, err := ReadManifest(ctx, repo, prefix, open)
	if err != nil {
		return Manifest{}, err
	}
	if open == nil {
		open = GzipUnwrap
	}
	h := sha256.New()
	var n int64
	for _, c := range meta.Chunks {
		raw, err := repo.Get(ctx, Join(prefix, c.Name))
		if err != nil {
			return Manifest{}, fmt.Errorf("chunk %s: %w", c.Name, err)
		}
		if int64(len(raw)) != c.CipherSize {
			return Manifest{}, fmt.Errorf("chunk %s ciphertext size mismatch", c.Name)
		}
		plain, err := open(raw)
		if err != nil {
			return Manifest{}, fmt.Errorf("chunk %s: %w", c.Name, err)
		}
		if int64(len(plain)) != c.PlainSize {
			return Manifest{}, fmt.Errorf("chunk %s plaintext size mismatch", c.Name)
		}
		if sha256Hex(plain) != c.PlainSHA256 {
			return Manifest{}, fmt.Errorf("chunk %s checksum mismatch", c.Name)
		}
		if _, err := dest.Write(plain); err != nil {
			return Manifest{}, err
		}
		if _, err := h.Write(plain); err != nil {
			return Manifest{}, err
		}
		n += int64(len(plain))
	}
	sum := fmt.Sprintf("%x", h.Sum(nil))
	if n != meta.PayloadSize || sum != meta.PayloadSHA256 {
		return Manifest{}, fmt.Errorf("payload checksum mismatch")
	}
	return meta, nil
}

func openBlob(raw []byte, open OpenFunc) ([]byte, error) {
	if open != nil {
		return open(raw)
	}
	if IsGzip(raw) {
		return GzipUnwrap(raw)
	}
	if bytes.HasPrefix(raw, []byte("{")) {
		return raw, nil
	}
	return GzipUnwrap(raw)
}
