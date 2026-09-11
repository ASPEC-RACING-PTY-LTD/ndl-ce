package backuppack

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// ChunkSize is the maximum plaintext bytes held for one pack chunk. Encoding,
// encryption, and upload must not retain more than a small multiple of this.
var ChunkSize = 4 << 20

// UploadConcurrency is the number of in-flight chunk PUTs or HEADs for one pack.
// Fleet backups stay single-workload; this only overlaps object RPCs.
var UploadConcurrency = 4

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
	workers := UploadConcurrency
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var mu sync.Mutex
	fail := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			wg.Wait()
			return Manifest{}, err
		}
		n, readErr := io.ReadFull(payload, plainBuf)
		if n > 0 {
			chunk := append([]byte(nil), plainBuf[:n]...)
			if _, err := payloadHash.Write(chunk); err != nil {
				wg.Wait()
				return Manifest{}, err
			}
			sealed, err := wrap(chunk)
			if err != nil {
				wg.Wait()
				return Manifest{}, err
			}
			name := fmt.Sprintf("%s/%06d", ChunkDir, index)
			rec := Chunk{
				Index: index, Name: name,
				PlainSHA256: sha256Hex(chunk), CipherSHA256: sha256Hex(sealed),
				PlainSize: int64(n), CipherSize: int64(len(sealed)),
			}
			total += int64(n)
			index++
			sem <- struct{}{}
			wg.Add(1)
			go func(rec Chunk, sealed []byte) {
				defer wg.Done()
				defer func() { <-sem }()
				if err := repo.Put(ctx, Join(prefix, rec.Name), sealed); err != nil {
					fail(err)
					return
				}
				mu.Lock()
				meta.Chunks = append(meta.Chunks, rec)
				mu.Unlock()
			}(rec, sealed)
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			wg.Wait()
			return Manifest{}, readErr
		}
		select {
		case err := <-errCh:
			wg.Wait()
			return Manifest{}, err
		default:
		}
	}
	wg.Wait()
	select {
	case err := <-errCh:
		return Manifest{}, err
	default:
	}
	sort.Slice(meta.Chunks, func(i, j int) bool { return meta.Chunks[i].Index < meta.Chunks[j].Index })
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
	workers := UploadConcurrency
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for _, c := range meta.Chunks {
		sem <- struct{}{}
		wg.Add(1)
		go func(c Chunk) {
			defer wg.Done()
			defer func() { <-sem }()
			ok, size, err := repo.Head(ctx, Join(prefix, c.Name))
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			if !ok {
				select {
				case errCh <- fmt.Errorf("chunk %s is missing", c.Name):
				default:
				}
				return
			}
			if size != c.CipherSize {
				select {
				case errCh <- fmt.Errorf("chunk %s size %d != %d", c.Name, size, c.CipherSize):
				default:
				}
			}
		}(c)
	}
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
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
