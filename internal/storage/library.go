package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"

	"github.com/google/uuid"
)

type uploadSession struct {
	itemID     string
	poolID     string
	root       string
	kind       string
	display    string
	tmp        string
	file       *os.File
	hash       hash256
	written    int64
	maxBytes   int64
	finalized  bool
	rejectSums map[string]bool
}

type hash256 struct {
	h interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
	}
}

func newHash() hash256 {
	return hash256{h: sha256.New()}
}

func (s hash256) Write(p []byte) (int, error) { return s.h.Write(p) }
func (s hash256) Sum() string                 { return hex.EncodeToString(s.h.Sum(nil)) }

// Uploads holds in-progress library writes owned by this agent process.
type Uploads struct {
	mu   sync.Mutex
	byID map[string]*uploadSession
	Dir  Directory
}

func (u *Uploads) get(id string) (*uploadSession, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.byID == nil {
		return nil, false
	}
	s, ok := u.byID[id]
	return s, ok
}

func (u *Uploads) put(s *uploadSession) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.byID == nil {
		u.byID = map[string]*uploadSession{}
	}
	u.byID[s.itemID] = s
}

func (u *Uploads) drop(id string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.byID, id)
}

// Begin opens an agent-owned temp file under the pool.
func (u *Uploads) Begin(hint PoolHint, req BeginUploadRequest) error {
	if !ValidLibraryKind(req.Kind) {
		return fmt.Errorf("%w: unknown library kind", ErrInvalidUpload)
	}
	if _, err := uuid.Parse(req.ItemID); err != nil {
		return fmt.Errorf("item_id must be a UUID")
	}
	pool, err := u.Dir.AssertWritablePool(hint)
	if err != nil {
		return err
	}
	max := req.MaxBytes
	if max <= 0 || max > DefaultLibraryMax {
		max = DefaultLibraryMax
	}
	if pool.Capacity.UsableBytes != nil && *pool.Capacity.UsableBytes < MinPoolFreeBytes {
		return ErrCapacity
	}
	rel := tmpRel(req.ItemID)
	abs, err := JoinUnder(pool.RootPath, rel)
	if err != nil {
		return err
	}
	h := u.Dir.host()
	if err := h.MkdirAll(path.Dir(abs), 0o750); err != nil {
		return err
	}
	f, err := h.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	reject := map[string]bool{}
	for _, sum := range req.RejectChecksums {
		if sum != "" {
			reject[strings.ToLower(sum)] = true
		}
	}
	u.put(&uploadSession{
		itemID:     req.ItemID,
		poolID:     req.PoolID,
		root:       pool.RootPath,
		kind:       req.Kind,
		display:    DisplayName(req.DisplayName),
		tmp:        abs,
		file:       f,
		hash:       newHash(),
		maxBytes:   max,
		rejectSums: reject,
	})
	return nil
}

// Write appends a chunk. The whole ISO is never loaded into RAM.
func (u *Uploads) Write(_ context.Context, itemID string, chunk []byte) error {
	s, ok := u.get(itemID)
	if !ok {
		return fmt.Errorf("%w: upload is not open", ErrInvalidUpload)
	}
	if s.finalized {
		return fmt.Errorf("%w: upload already finalized", ErrInvalidUpload)
	}
	if int64(len(chunk)) > s.maxBytes-s.written {
		u.Abort(itemID)
		return fmt.Errorf("%w: upload exceeds size limit", ErrInvalidUpload)
	}
	n, err := s.file.Write(chunk)
	if err != nil {
		u.Abort(itemID)
		if isNoSpace(err) {
			return fmt.Errorf("%w: filesystem is full", ErrCapacity)
		}
		return err
	}
	if _, err := s.hash.Write(chunk[:n]); err != nil {
		u.Abort(itemID)
		return err
	}
	s.written += int64(n)
	return nil
}

// Finish checksums, validates, and atomically publishes the library object.
func (u *Uploads) Finish(_ context.Context, req FinishUploadRequest) (UploadResult, error) {
	s, ok := u.get(req.ItemID)
	if !ok {
		return UploadResult{}, fmt.Errorf("%w: upload is not open", ErrInvalidUpload)
	}
	if s.written <= 0 {
		u.Abort(req.ItemID)
		return UploadResult{}, fmt.Errorf("%w: empty upload", ErrInvalidUpload)
	}
	if err := s.file.Sync(); err != nil {
		u.Abort(req.ItemID)
		return UploadResult{}, err
	}
	sum := s.hash.Sum()
	if s.rejectSums[strings.ToLower(sum)] {
		u.Abort(req.ItemID)
		return UploadResult{}, fmt.Errorf("%w: duplicate checksum", ErrDuplicate)
	}
	if req.ExpectedSHA256 != "" && !strings.EqualFold(req.ExpectedSHA256, sum) {
		u.Abort(req.ItemID)
		return UploadResult{}, fmt.Errorf("%w: checksum mismatch", ErrInvalidUpload)
	}
	if err := validateUpload(s.kind, s.tmp, s.written); err != nil {
		u.Abort(req.ItemID)
		return UploadResult{}, err
	}
	rel := libraryRel(s.kind, s.itemID, s.display)
	dest, err := JoinUnder(s.root, rel)
	if err != nil {
		u.Abort(req.ItemID)
		return UploadResult{}, err
	}
	if err := u.Dir.refuseEscape(s.root, dest); err != nil {
		u.Abort(req.ItemID)
		return UploadResult{}, err
	}
	h := u.Dir.host()
	if err := h.MkdirAll(path.Dir(dest), 0o750); err != nil {
		u.Abort(req.ItemID)
		return UploadResult{}, err
	}
	if _, err := h.Stat(dest); err == nil {
		u.Abort(req.ItemID)
		return UploadResult{}, ErrDuplicate
	}
	_ = s.file.Close()
	s.file = nil
	if err := h.Rename(s.tmp, dest); err != nil {
		_ = h.Remove(s.tmp)
		u.drop(req.ItemID)
		if isNoSpace(err) {
			return UploadResult{}, fmt.Errorf("%w: filesystem is full", ErrCapacity)
		}
		return UploadResult{}, err
	}
	s.finalized = true
	u.drop(req.ItemID)
	return UploadResult{
		ItemID:      s.itemID,
		PoolID:      s.poolID,
		Kind:        s.kind,
		DisplayName: s.display,
		BackendRef:  rel,
		SizeBytes:   s.written,
		SHA256:      sum,
	}, nil
}

// Abort deletes the No-dal-owned temp file only.
func (u *Uploads) Abort(itemID string) {
	s, ok := u.get(itemID)
	if !ok {
		return
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	if s.tmp != "" {
		_ = u.Dir.host().Remove(s.tmp)
	}
	u.drop(itemID)
}

func isNoSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC)
}

func validateUpload(kind, path string, size int64) error {
	if size <= 0 {
		return fmt.Errorf("%w: empty upload", ErrInvalidUpload)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 16)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if looksLikeTextDocument(head) {
		return fmt.Errorf("%w: content is not media", ErrInvalidUpload)
	}
	if kind == LibraryISO {
		if !isoLooksPlausible(f, size) {
			return fmt.Errorf("%w: file is not a plausible ISO", ErrInvalidUpload)
		}
	}
	if kind == LibraryCloudImage && n >= 4 && bytes.Equal(head[:4], []byte("QFI\xfb")) {
		return nil
	}
	return nil
}

func looksLikeTextDocument(head []byte) bool {
	s := strings.ToLower(string(bytes.TrimSpace(head)))
	return strings.HasPrefix(s, "<!doctype") || strings.HasPrefix(s, "<html") || strings.HasPrefix(s, "<?xml")
}

func isoLooksPlausible(f *os.File, size int64) bool {
	if size < 32768+6 {
		return false
	}
	buf := make([]byte, 5)
	if _, err := f.ReadAt(buf, 32769); err != nil {
		return false
	}
	return string(buf) == "CD001"
}

// Stream copies r into the open upload without buffering the whole object.
func (u *Uploads) Stream(ctx context.Context, itemID string, r io.Reader) error {
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			u.Abort(itemID)
			return err
		}
		n, err := r.Read(buf)
		if n > 0 {
			if werr := u.Write(ctx, itemID, buf[:n]); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			u.Abort(itemID)
			return err
		}
	}
}
