package objstore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
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
		return Result{Key: req.Key, Status: "unavailable", Reason: "object transport is not configured"}, fmt.Errorf("object transport is unavailable")
	}
	tr := e.transport()
	if tr == nil {
		return Result{}, fmt.Errorf("object transport is unavailable")
	}
	plain, err := os.ReadFile(req.SourcePath)
	if err != nil {
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
		TransferredBytes: int64(len(cipher)), Encrypted: true, Status: "available", AppliedAt: time.Now().UTC(),
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
		TransferredBytes: int64(len(blob)), Encrypted: true, Status: "available",
	}, nil
}

func (e *Engine) head(ctx context.Context, req Request) (Result, error) {
	if err := validateObjectIdentity(req); err != nil {
		return Result{}, err
	}
	if req.NoCheckBucket {
		return Result{Key: req.Key, Status: "not_configured", Reason: "no_check_bucket skips listing and HeadBucket"}, nil
	}
	tr := e.transport()
	if tr == nil {
		return Result{Status: "unavailable", Reason: "object transport is unavailable"}, nil
	}
	exists, size, err := tr.Head(ctx, req.Bucket, req.Key)
	if err != nil {
		return Result{Status: "unavailable", Reason: err.Error()}, nil
	}
	if !exists {
		return Result{Key: req.Key, Status: "not_configured", TransferredBytes: size}, nil
	}
	return Result{Key: req.Key, Status: "available", TransferredBytes: size, Encrypted: true}, nil
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
	return Result{Key: req.Key, Status: "available"}, nil
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

// ObjectKey builds a stable artifact key under an optional prefix.
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
