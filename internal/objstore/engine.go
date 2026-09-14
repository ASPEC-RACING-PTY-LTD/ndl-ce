package objstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/backuppack"
	"github.com/no-dal/ndl-ce/internal/ctbackup"
)

// Transport puts and gets opaque object bytes. The engine encrypts before Put.
type Transport interface {
	Put(ctx context.Context, bucket, object string, body []byte) error
	Get(ctx context.Context, bucket, object string) ([]byte, error)
	Head(ctx context.Context, bucket, object string) (exists bool, size int64, err error)
	Delete(ctx context.Context, bucket, object string) error
}

// Engine encrypts-before-upload. SSE is never treated as sufficient.
type Engine struct {
	Transport   Transport
	SkipNetwork bool
}

func (e *Engine) transport() Transport {
	if e != nil && e.Transport != nil {
		return e.Transport
	}
	return nil
}

func (e *Engine) Do(ctx context.Context, req Request) (Result, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" {
		action = ActionPut
	}
	p := strings.ToLower(strings.TrimSpace(req.Provider))
	if p != "" && !IsObjectKind(p) {
		return Result{}, fmt.Errorf("provider must be s3, r2, aws, b2, or minio")
	}
	switch action {
	case ActionPut:
		return e.put(ctx, req)
	case ActionGet:
		return e.get(ctx, req)
	case ActionHead:
		return e.head(ctx, req)
	case ActionDel:
		return e.del(ctx, req)
	case ActionPutPack:
		return e.putPack(ctx, req)
	case ActionGetPack:
		return e.getPack(ctx, req)
	case ActionDelPack:
		return e.delPack(ctx, req)
	case ActionTest:
		return e.test(ctx, req)
	default:
		return Result{}, fmt.Errorf("unsupported object action")
	}
}

func (e *Engine) put(ctx context.Context, req Request) (Result, error) {
	if err := validateObjectIdentity(req); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(req.SourcePath) == "" {
		return Result{}, fmt.Errorf("source_path is required")
	}
	if len(req.EncryptionKey) != KeySize {
		return Result{}, fmt.Errorf("client-side encryption key is required; bucket SSE is not sufficient")
	}
	if e != nil && e.SkipNetwork && e.transport() == nil {
		return Result{Key: req.Key, Status: StatusUnavailable, Reason: "object transport is not configured"}, fmt.Errorf("object transport is unavailable")
	}
	tr := e.transport()
	if tr == nil {
		return Result{}, fmt.Errorf("object transport is unavailable")
	}
	f, err := os.Open(req.SourcePath)
	if err != nil {
		return Result{}, fmt.Errorf("read backup source: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Result{}, fmt.Errorf("read backup source: %w", err)
	}
	if st.Size() > int64(PartSize) {
		return Result{}, fmt.Errorf("source exceeds %d byte single-object envelope; use put-pack", PartSize)
	}
	plain := make([]byte, st.Size())
	if _, err := io.ReadFull(f, plain); err != nil {
		return Result{}, fmt.Errorf("read backup source: %w", err)
	}
	cipher, err := Encrypt(plain, req.EncryptionKey)
	if err != nil {
		return Result{}, err
	}
	if err := tr.Put(ctx, req.Bucket, req.Key, cipher); err != nil {
		return Result{}, err
	}
	return Result{
		Key: req.Key, PlaintextSHA256: SHA256Hex(plain), PlaintextSize: int64(len(plain)),
		TransferredBytes: int64(len(cipher)), Encrypted: true, Status: StatusAvailable, AppliedAt: time.Now().UTC(),
	}, nil
}

func (e *Engine) get(ctx context.Context, req Request) (Result, error) {
	if err := validateObjectIdentity(req); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(req.DestPath) == "" {
		return Result{}, fmt.Errorf("dest_path is required")
	}
	if len(req.EncryptionKey) != KeySize {
		return Result{}, fmt.Errorf("client-side encryption key is required")
	}
	tr := e.transport()
	if tr == nil {
		return Result{}, fmt.Errorf("object transport is unavailable")
	}
	blob, err := tr.Get(ctx, req.Bucket, req.Key)
	if err != nil {
		return Result{}, err
	}
	plain, err := Decrypt(blob, req.EncryptionKey)
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(req.DestPath, plain, 0o640); err != nil {
		return Result{}, err
	}
	return Result{
		Key: req.Key, PlaintextSHA256: SHA256Hex(plain), PlaintextSize: int64(len(plain)),
		TransferredBytes: int64(len(blob)), Encrypted: true, Status: StatusAvailable,
	}, nil
}

func (e *Engine) head(ctx context.Context, req Request) (Result, error) {
	if err := validateObjectIdentity(req); err != nil {
		return Result{}, err
	}
	if req.NoCheckBucket {
		return Result{Key: req.Key, Status: StatusUntested, Reason: "no_check_bucket skips listing and HeadBucket"}, nil
	}
	tr := e.transport()
	if tr == nil {
		return Result{Status: StatusUnavailable, Reason: "object transport is unavailable"}, nil
	}
	exists, size, err := tr.Head(ctx, req.Bucket, req.Key)
	if err != nil {
		return Result{Status: Classify(err), Reason: "object head failed"}, nil
	}
	if !exists {
		return Result{Key: req.Key, Status: StatusUntested, TransferredBytes: size}, nil
	}
	return Result{Key: req.Key, Status: StatusAvailable, TransferredBytes: size, Encrypted: true}, nil
}

func (e *Engine) del(ctx context.Context, req Request) (Result, error) {
	if err := validateObjectIdentity(req); err != nil {
		return Result{}, err
	}
	tr := e.transport()
	if tr == nil {
		return Result{}, fmt.Errorf("object transport is unavailable")
	}
	if err := tr.Delete(ctx, req.Bucket, req.Key); err != nil {
		return Result{}, err
	}
	return Result{Key: req.Key, Status: StatusAvailable}, nil
}

func (e *Engine) putPack(ctx context.Context, req Request) (Result, error) {
	if err := validateObjectIdentity(req); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(req.SourcePath) == "" {
		return Result{}, fmt.Errorf("source_path is required")
	}
	if len(req.EncryptionKey) != KeySize {
		return Result{}, fmt.Errorf("client-side encryption key is required; bucket SSE is not sufficient")
	}
	tr := e.transport()
	if tr == nil {
		return Result{}, fmt.Errorf("object transport is unavailable")
	}
	payload, err := openPackPayload(ctx, req.SourcePath)
	if err != nil {
		return Result{}, err
	}
	defer payload.Close()
	config := readPackConfig(req)
	wrap := func(plain []byte) ([]byte, error) {
		return Encrypt(plain, req.EncryptionKey)
	}
	repo := Repo{T: tr, Bucket: req.Bucket}
	defer repo.CloseIdle()
	meta, err := backuppack.Write(ctx, repo, req.Key, payload, config, wrap, backuppack.Manifest{
		PayloadKind: payloadKind(req.SourcePath), ObjectPrefix: req.Key,
	})
	if err != nil {
		return Result{Key: req.Key, Status: Classify(err)}, err
	}
	var transferred int64
	for _, c := range meta.Chunks {
		transferred += c.CipherSize
	}
	return Result{
		Key: req.Key, PlaintextSHA256: meta.PayloadSHA256, PlaintextSize: meta.PayloadSize,
		TransferredBytes: transferred, Encrypted: true, Status: StatusAvailable, AppliedAt: time.Now().UTC(),
	}, nil
}

func (e *Engine) getPack(ctx context.Context, req Request) (Result, error) {
	if err := validateObjectIdentity(req); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(req.DestPath) == "" {
		return Result{}, fmt.Errorf("dest_path is required")
	}
	if len(req.EncryptionKey) != KeySize {
		return Result{}, fmt.Errorf("client-side encryption key is required")
	}
	tr := e.transport()
	if tr == nil {
		return Result{}, fmt.Errorf("object transport is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(req.DestPath), 0o750); err != nil {
		return Result{}, err
	}
	f, err := os.Create(req.DestPath)
	if err != nil {
		return Result{}, err
	}
	open := func(blob []byte) ([]byte, error) { return Decrypt(blob, req.EncryptionKey) }
	meta, err := backuppack.Reconstruct(ctx, Repo{T: tr, Bucket: req.Bucket}, req.Key, f, open)
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(req.DestPath)
		return Result{}, err
	}
	if closeErr != nil {
		_ = os.Remove(req.DestPath)
		return Result{}, closeErr
	}
	if cfg, err := backuppack.ReadConfig(ctx, Repo{T: tr, Bucket: req.Bucket}, req.Key, open); err == nil && len(cfg) > 0 {
		_ = os.WriteFile(req.DestPath+".ndl-meta.json", cfg, 0o600)
	}
	var transferred int64
	for _, c := range meta.Chunks {
		transferred += c.CipherSize
	}
	return Result{
		Key: req.Key, PlaintextSHA256: meta.PayloadSHA256, PlaintextSize: meta.PayloadSize,
		TransferredBytes: transferred, Encrypted: true, Status: StatusAvailable,
	}, nil
}

func (e *Engine) delPack(ctx context.Context, req Request) (Result, error) {
	if err := validateObjectIdentity(req); err != nil {
		return Result{}, err
	}
	tr := e.transport()
	if tr == nil {
		return Result{}, fmt.Errorf("object transport is unavailable")
	}
	repo := Repo{T: tr, Bucket: req.Bucket}
	open := func(blob []byte) ([]byte, error) {
		if len(req.EncryptionKey) == KeySize {
			return Decrypt(blob, req.EncryptionKey)
		}
		return backuppack.GzipUnwrap(blob)
	}
	meta, err := backuppack.ReadManifest(ctx, repo, req.Key, open)
	if err == nil {
		for _, c := range meta.Chunks {
			_ = tr.Delete(ctx, req.Bucket, backuppack.Join(req.Key, c.Name))
		}
	}
	_ = tr.Delete(ctx, req.Bucket, backuppack.ManifestKey(req.Key))
	_ = tr.Delete(ctx, req.Bucket, backuppack.ConfigKey(req.Key))
	_ = tr.Delete(ctx, req.Bucket, backuppack.ChecksumsKey(req.Key))
	return Result{Key: req.Key, Status: StatusAvailable}, nil
}

func (e *Engine) test(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Bucket) == "" {
		return Result{}, fmt.Errorf("bucket is required")
	}
	if len(req.EncryptionKey) != KeySize {
		return Result{}, fmt.Errorf("client-side encryption key is required")
	}
	tr := e.transport()
	if tr == nil {
		return Result{Status: StatusUnavailable, Reason: "object transport is unavailable"}, fmt.Errorf("object transport is unavailable")
	}
	prefix := strings.Trim(strings.TrimSpace(req.Key), "/")
	key := backuppack.Join(prefix, ".ndl-probe/"+uuid.NewString())
	plain := []byte("ndl-probe")
	cipher, err := Encrypt(plain, req.EncryptionKey)
	if err != nil {
		return Result{}, err
	}
	if err := tr.Put(ctx, req.Bucket, key, cipher); err != nil {
		return Result{Key: key, Status: Classify(err)}, err
	}
	got, err := tr.Get(ctx, req.Bucket, key)
	if err != nil {
		_ = tr.Delete(ctx, req.Bucket, key)
		return Result{Key: key, Status: Classify(err)}, err
	}
	out, err := Decrypt(got, req.EncryptionKey)
	if err != nil || !bytes.Equal(out, plain) {
		_ = tr.Delete(ctx, req.Bucket, key)
		return Result{Key: key, Status: StatusDegraded}, fmt.Errorf("probe object round-trip failed")
	}
	if err := tr.Delete(ctx, req.Bucket, key); err != nil {
		return Result{Key: key, Status: StatusDegraded}, err
	}
	return Result{Key: key, Encrypted: true, Status: StatusAvailable, AppliedAt: time.Now().UTC()}, nil
}

func openPackPayload(ctx context.Context, src string) (io.ReadCloser, error) {
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return os.Open(src)
	}
	tree := src
	if st, err := os.Stat(filepath.Join(src, "tree")); err == nil && st.IsDir() {
		tree = filepath.Join(src, "tree")
	}
	return ctbackup.StreamTar(ctx, tree)
}

func readPackConfig(req Request) []byte {
	candidates := []string{req.DestPath, filepath.Join(req.SourcePath, backuppack.ConfigName), req.SourcePath + ".ndl-meta.json"}
	for _, p := range candidates {
		if strings.TrimSpace(p) == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err == nil && len(bytes.TrimSpace(b)) > 0 {
			return b
		}
	}
	return nil
}

func payloadKind(src string) string {
	switch {
	case strings.HasSuffix(src, ".zfs"):
		return backuppack.PayloadZFS
	case strings.HasSuffix(src, ".qcow2"):
		return backuppack.PayloadQCOW2
	default:
		return backuppack.PayloadTar
	}
}

func validateObjectIdentity(req Request) error {
	if strings.TrimSpace(req.Bucket) == "" {
		return fmt.Errorf("bucket is required")
	}
	if strings.Contains(req.Bucket, "/") || strings.Contains(req.Bucket, "..") {
		return fmt.Errorf("bucket is invalid")
	}
	if strings.TrimSpace(req.Key) == "" {
		return fmt.Errorf("object key is required")
	}
	if strings.Contains(req.Key, "..") {
		return fmt.Errorf("object key is invalid")
	}
	return nil
}

// ObjectKey builds a legacy single-object artifact key. New backups use PackPrefix.
func ObjectKey(prefix, artifactID, format string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	name := strings.TrimSpace(artifactID) + ".ndl"
	if format != "" {
		name = strings.TrimSpace(artifactID) + "." + strings.TrimSpace(format) + ".ndl"
	}
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

// Locator is the inspectable object locator stored on the artifact. It is not a filesystem path.
func Locator(bucket, key string) string {
	return "s3://" + strings.TrimSpace(bucket) + "/" + strings.TrimSpace(key)
}
