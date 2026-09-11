package backuppack

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// ChunkSize is the maximum plaintext bytes held for one pack chunk. Encoding,
// encryption, and upload must not retain more than a small multiple of this.
var ChunkSize = 4 << 20

// MaxLiveBytes is the documented RAM envelope for one pack worker: one
// plaintext chunk, one ciphertext buffer, and JSON sidecars. It is not a
// cgroup limit; tests assert puts and wrap calls stay inside it.
func MaxLiveBytes() int {
	return (3 * ChunkSize) + (1 << 20)
}

// Write chunks payload into prefix, then commits checksums and the manifest
// last. wrap must refuse plaintext larger than ChunkSize.
func Write(ctx context.Context, repo Repository, prefix string, payload io.Reader, config []byte, wrap WrapFunc, meta Manifest) (Manifest, error) {
	if repo == nil {
		return Manifest{}, fmt.Errorf("pack repository is required")
	}
	if payload == nil {
		return Manifest{}, fmt.Errorf("payload is required")
	}
	if wrap == nil {
		wrap = GzipWrap
	}
	meta.Version = Version
	meta.Format = FormatNDLB
	if meta.PayloadKind == "" {
		meta.PayloadKind = PayloadTar
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now().UTC()
	}
	meta.ChunkSize = ChunkSize
	meta.ObjectPrefix = prefix
	meta.Chunks = nil

	fillIdentityFromConfig(&meta, config)
	if len(config) > 0 {
		if err := CheckSidecarSize(len(config)); err != nil {
			return Manifest{}, fmt.Errorf("config: %w", err)
		}
		sealed, err := wrap(config)
		if err != nil {
			return Manifest{}, err
		}
		if err := repo.Put(ctx, ConfigKey(prefix), sealed); err != nil {
			return Manifest{}, err
		}
		meta.ConfigSHA256 = sha256Hex(config)
	}

	plainBuf := make([]byte, ChunkSize)
	payloadHash := sha256.New()
	index := 0
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		n, readErr := io.ReadFull(payload, plainBuf)
		if n > 0 {
			chunk := plainBuf[:n]
			if _, err := payloadHash.Write(chunk); err != nil {
				return Manifest{}, err
			}
			sealed, err := wrap(chunk)
			if err != nil {
				return Manifest{}, err
			}
			name := fmt.Sprintf("%s/%06d", ChunkDir, index)
			if err := repo.Put(ctx, Join(prefix, name), sealed); err != nil {
				return Manifest{}, err
			}
			meta.Chunks = append(meta.Chunks, Chunk{
				Index: index, Name: name,
				PlainSHA256: sha256Hex(chunk), CipherSHA256: sha256Hex(sealed),
				PlainSize: int64(n), CipherSize: int64(len(sealed)),
			})
			total += int64(n)
			index++
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return Manifest{}, readErr
		}
	}
	meta.PayloadSize = total
	meta.PayloadSHA256 = fmt.Sprintf("%x", payloadHash.Sum(nil))
	meta.Encrypted = wrap != nil

	sums, err := marshalJSON(Checksums{
		PayloadSHA256: meta.PayloadSHA256, PayloadSize: total, Chunks: meta.Chunks,
	})
	if err != nil {
		return Manifest{}, err
	}
	if err := CheckSidecarSize(len(sums)); err != nil {
		return Manifest{}, fmt.Errorf("checksums: %w", err)
	}
	sealedSums, err := wrap(sums)
	if err != nil {
		return Manifest{}, err
	}
	if err := repo.Put(ctx, ChecksumsKey(prefix), sealedSums); err != nil {
		return Manifest{}, err
	}

	raw, err := marshalJSON(meta)
	if err != nil {
		return Manifest{}, err
	}
	if err := CheckSidecarSize(len(raw)); err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	sealedMan, err := wrap(raw)
	if err != nil {
		return Manifest{}, err
	}
	if err := repo.Put(ctx, ManifestKey(prefix), sealedMan); err != nil {
		return Manifest{}, err
	}
	if err := verifyPack(ctx, repo, prefix, meta); err != nil {
		return Manifest{}, err
	}
	return meta, nil
}

func verifyPack(ctx context.Context, repo Repository, prefix string, meta Manifest) error {
	exists, _, err := repo.Head(ctx, ManifestKey(prefix))
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("manifest was not committed")
	}
	for _, c := range meta.Chunks {
		ok, size, err := repo.Head(ctx, Join(prefix, c.Name))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("chunk %s is missing", c.Name)
		}
		if size != c.CipherSize {
			return fmt.Errorf("chunk %s size %d != %d", c.Name, size, c.CipherSize)
		}
	}
	if len(meta.Chunks) == 0 && meta.PayloadSize != 0 {
		return fmt.Errorf("manifest payload size does not match chunks")
	}
	return nil
}

func fillIdentityFromConfig(meta *Manifest, config []byte) {
	if meta == nil || len(config) == 0 {
		return
	}
	var cfg struct {
		WorkloadID string `json:"workload_id"`
		Name       string `json:"name"`
		Hostname   string `json:"hostname"`
	}
	if json.Unmarshal(config, &cfg) != nil {
		return
	}
	if meta.WorkloadID == "" {
		meta.WorkloadID = strings.TrimSpace(cfg.WorkloadID)
	}
	if meta.WorkloadName == "" {
		meta.WorkloadName = strings.TrimSpace(cfg.Name)
		if meta.WorkloadName == "" {
			meta.WorkloadName = strings.TrimSpace(cfg.Hostname)
		}
	}
}
