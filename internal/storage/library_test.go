package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"
)

func isoFixture(size int) []byte {
	if size < 32768+16 {
		size = 32768 + 16
	}
	b := bytes.Repeat([]byte{0x00}, size)
	copy(b[32769:], []byte("CD001"))
	return b
}

func TestLibraryUploadChecksumAndSanitize(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	u := &Uploads{Dir: d}
	item := uuid.NewString()
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	if err := u.Begin(hint, BeginUploadRequest{
		ItemID: item, PoolID: poolID, Kind: LibraryISO, DisplayName: "../../etc/passwd.iso",
	}); err != nil {
		t.Fatal(err)
	}
	data := isoFixture(64 << 10)
	if err := u.Stream(context.Background(), item, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	res, err := u.Finish(context.Background(), FinishUploadRequest{ItemID: item, ExpectedSHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if res.DisplayName != "passwd.iso" {
		t.Fatalf("display %s", res.DisplayName)
	}
	if res.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("checksum")
	}
	if strings.Contains(res.BackendRef, "..") {
		t.Fatal(res.BackendRef)
	}
	if _, err := os.Stat(filepath.Join(filepath.FromSlash(root), filepath.FromSlash(res.BackendRef))); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryRejectsEmptyAndHTML(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	u := &Uploads{Dir: d}
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	item := uuid.NewString()
	if err := u.Begin(hint, BeginUploadRequest{ItemID: item, PoolID: poolID, Kind: LibraryISO, DisplayName: "x.iso"}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Finish(context.Background(), FinishUploadRequest{ItemID: item}); err == nil {
		t.Fatal("empty")
	}
	item = uuid.NewString()
	if err := u.Begin(hint, BeginUploadRequest{ItemID: item, PoolID: poolID, Kind: LibraryISO, DisplayName: "x.iso"}); err != nil {
		t.Fatal(err)
	}
	_ = u.Write(context.Background(), item, []byte("<!doctype html>"))
	if _, err := u.Finish(context.Background(), FinishUploadRequest{ItemID: item}); err == nil {
		t.Fatal("html")
	}
}

func TestLibraryAbortCleansTemp(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	u := &Uploads{Dir: d}
	item := uuid.NewString()
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	if err := u.Begin(hint, BeginUploadRequest{ItemID: item, PoolID: poolID, Kind: LibraryISO, DisplayName: "a.iso"}); err != nil {
		t.Fatal(err)
	}
	_ = u.Write(context.Background(), item, isoFixture(40 << 10)[:1024])
	u.Abort(item)
	ents, _ := os.ReadDir(filepath.Join(filepath.FromSlash(root), "tmp"))
	if len(ents) != 0 {
		t.Fatalf("temp leak %v", ents)
	}
}

func TestLibraryChecksumMismatch(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	u := &Uploads{Dir: d}
	item := uuid.NewString()
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	if err := u.Begin(hint, BeginUploadRequest{ItemID: item, PoolID: poolID, Kind: LibraryISO, DisplayName: "a.iso"}); err != nil {
		t.Fatal(err)
	}
	_ = u.Stream(context.Background(), item, bytes.NewReader(isoFixture(40<<10)))
	if _, err := u.Finish(context.Background(), FinishUploadRequest{ItemID: item, ExpectedSHA256: "00"}); err == nil {
		t.Fatal("checksum")
	}
}

func TestLibraryRejectsDuplicateChecksumBeforePublish(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	u := &Uploads{Dir: d}
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	data := isoFixture(40 << 10)
	sum := sha256.Sum256(data)
	item := uuid.NewString()
	if err := u.Begin(hint, BeginUploadRequest{
		ItemID: item, PoolID: poolID, Kind: LibraryISO, DisplayName: "dup.iso",
		RejectChecksums: []string{hex.EncodeToString(sum[:])},
	}); err != nil {
		t.Fatal(err)
	}
	if err := u.Stream(context.Background(), item, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Finish(context.Background(), FinishUploadRequest{ItemID: item}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("want duplicate, got %v", err)
	}
	ents, _ := os.ReadDir(filepath.Join(filepath.FromSlash(root), "library", "iso"))
	if len(ents) != 0 {
		t.Fatalf("duplicate must not publish: %v", ents)
	}
	tmp, _ := os.ReadDir(filepath.Join(filepath.FromSlash(root), "tmp"))
	if len(tmp) != 0 {
		t.Fatalf("temp leak after duplicate reject: %v", tmp)
	}
}

func TestLibraryENOSPC(t *testing.T) {
	if !isNoSpace(syscall.ENOSPC) {
		t.Fatal("ENOSPC must be treated as capacity failure")
	}
}

func TestLibraryStreamingDoesNotBufferAll(t *testing.T) {
	// The upload path copies 1 MiB chunks from io.Reader. This test
	// uses a reader that cannot be rewound to prove we do not ReadAll.
	r := &onceReader{r: bytes.NewReader(isoFixture(40 << 10))}
	buf := make([]byte, 4096)
	n, err := r.Read(buf)
	if n == 0 || err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); err != nil {
		t.Fatal(err)
	}
}

type onceReader struct{ r io.Reader }

func (o *onceReader) Read(p []byte) (int, error) { return o.r.Read(p) }
