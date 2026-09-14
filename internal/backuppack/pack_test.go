package backuppack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSanitizeAndUniqueNames(t *testing.T) {
	if got := SanitizeName("SoundDock"); got != "SoundDock" {
		t.Fatalf("sanitize %s", got)
	}
	if got := SanitizeName("Sound Dock / 1"); got != "Sound-Dock-1" {
		t.Fatalf("sanitize %s", got)
	}
	if got := SanitizeName("..."); got != "workload" {
		t.Fatalf("empty %s", got)
	}
	a := UniqueName("SoundDock", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", []string{"Other"})
	if a != "SoundDock" {
		t.Fatalf("unique unique %s", a)
	}
	b := UniqueName("SoundDock", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", []string{"SoundDock"})
	if b != "SoundDock-bbbbbbbb" {
		t.Fatalf("duplicate %s", b)
	}
}

func TestObjectPrefixHumanReadable(t *testing.T) {
	p := ObjectPrefix("", "SoundDock", "2026-09-12T05-40-00Z")
	if p != "backups/SoundDock/2026-09-12T05-40-00Z" {
		t.Fatalf("prefix %s", p)
	}
	if ManifestKey(p) != "backups/SoundDock/2026-09-12T05-40-00Z/manifest.json" {
		t.Fatalf("manifest %s", ManifestKey(p))
	}
	p2 := ObjectPrefix("acct1", "SoundDock", "2026-09-12T05-40-00Z")
	if p2 != "acct1/backups/SoundDock/2026-09-12T05-40-00Z" {
		t.Fatalf("repo prefix %s", p2)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	prev := ChunkSize
	ChunkSize = 32
	t.Cleanup(func() { ChunkSize = prev })
	payload := bytes.Repeat([]byte("ndl-pack-payload-"), 20)
	repo := &MemRepo{MaxPut: MaxLiveBytes()}
	wrapCalls := 0
	wrap := func(plain []byte) ([]byte, error) {
		wrapCalls++
		if len(plain) > wrapLimit() {
			t.Fatalf("wrap received %d bytes", len(plain))
		}
		return GzipWrap(plain)
	}
	meta, err := Write(context.Background(), repo, "backups/SoundDock/2026-09-12T05-40-00Z", bytes.NewReader(payload), []byte(`{"workload_id":"wl-1"}`), wrap, Manifest{
		WorkloadID: "wl-1", WorkloadName: "SoundDock", ArtifactID: "art-1", PayloadKind: PayloadTar,
	})
	if err != nil {
		t.Fatal(err)
	}
	if meta.WorkloadID != "wl-1" {
		t.Fatalf("identity %s", meta.WorkloadID)
	}
	if len(meta.Chunks) < 2 {
		t.Fatalf("expected multiple chunks %+v", meta.Chunks)
	}
	var out bytes.Buffer
	got, err := Reconstruct(context.Background(), repo, meta.ObjectPrefix, &out, GzipUnwrap)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), payload) {
		t.Fatalf("payload mismatch %d %d", out.Len(), len(payload))
	}
	if got.PayloadSHA256 != sha256Hex(payload) {
		t.Fatalf("checksum %s", got.PayloadSHA256)
	}
	cfg, err := ReadConfig(context.Background(), repo, meta.ObjectPrefix, GzipUnwrap)
	if err != nil || !bytes.Contains(cfg, []byte("wl-1")) {
		t.Fatalf("config %s %v", cfg, err)
	}
	if wrapCalls < 3 {
		t.Fatalf("wrap calls %d", wrapCalls)
	}
}

func TestWriteStaysInsideMemoryEnvelope(t *testing.T) {
	prev := ChunkSize
	ChunkSize = 1 << 20
	t.Cleanup(func() { ChunkSize = prev })
	const payloadSize = 32 << 20 // 32MiB logical payload, not held in one buffer by the packer
	repo := &MemRepo{MaxPut: MaxLiveBytes()}
	r := io.LimitReader(neverEnding('A'), payloadSize)
	peak := 0
	wrap := func(plain []byte) ([]byte, error) {
		if len(plain) > peak {
			peak = len(plain)
		}
		if len(plain) > ChunkSize {
			t.Fatalf("packer buffered %d", len(plain))
		}
		out := make([]byte, len(plain))
		copy(out, plain)
		return out, nil
	}
	meta, err := Write(context.Background(), repo, "backups/env/stamp", r, nil, wrap, Manifest{WorkloadID: "wl", PayloadKind: PayloadTar})
	if err != nil {
		t.Fatal(err)
	}
	if meta.PayloadSize != payloadSize {
		t.Fatalf("size %d", meta.PayloadSize)
	}
	if peak > ChunkSize {
		t.Fatalf("peak wrap %d", peak)
	}
	if len(repo.Objects) < 2 {
		t.Fatalf("objects %d", len(repo.Objects))
	}
}

func TestRenameDoesNotChangeHistoricalPrefix(t *testing.T) {
	prefix := ObjectPrefix("", "SoundDock", "2026-09-12T05-40-00Z")
	renamed := UniqueName("SoundDock-renamed", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", nil)
	if strings.Contains(prefix, renamed) {
		t.Fatal("historical prefix must stay the name used at backup time")
	}
	if filepath.Base(filepath.Dir(prefix)) != "SoundDock" {
		t.Fatalf("historical name %s", prefix)
	}
}

func TestRejectOversizedWrap(t *testing.T) {
	prev := ChunkSize
	ChunkSize = 16
	t.Cleanup(func() { ChunkSize = prev })
	if _, err := GzipWrap(bytes.Repeat([]byte("x"), wrapLimit()+1)); err == nil {
		t.Fatal("gzip wrap must refuse oversized plaintext")
	}
}

func TestUniqueNameKeepsReadablePrefix(t *testing.T) {
	dup := UniqueName("SoundDock", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", []string{"SoundDock"})
	p := ObjectPrefix("", dup, "2026-09-12T05-40-00Z")
	if p != "backups/SoundDock-bbbbbbbb/2026-09-12T05-40-00Z" {
		t.Fatalf("duplicate prefix %s", p)
	}
}

type neverEnding byte

func (n neverEnding) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(n)
	}
	return len(p), nil
}

func TestPayloadChecksum(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	_ = sum
}

func TestWriteUploadsChunksInParallel(t *testing.T) {
	prevChunk := ChunkSize
	prevConc := UploadConcurrency
	ChunkSize = 32
	UploadConcurrency = 4
	t.Cleanup(func() {
		ChunkSize = prevChunk
		UploadConcurrency = prevConc
	})
	repo := &MemRepo{MaxPut: MaxLiveBytes(), Delay: 20 * time.Millisecond}
	payload := bytes.Repeat([]byte("n"), 32*8)
	meta, err := Write(context.Background(), repo, "backups/parallel/stamp", bytes.NewReader(payload), nil, GzipWrap, Manifest{
		WorkloadID: "wl", PayloadKind: PayloadTar,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Chunks) != 8 {
		t.Fatalf("chunks %d", len(meta.Chunks))
	}
	if repo.PeakPuts < 2 {
		t.Fatalf("expected overlapping PUTs, peak %d", repo.PeakPuts)
	}
	if repo.PeakPuts > UploadConcurrency {
		t.Fatalf("peak PUTs %d exceeds bound %d", repo.PeakPuts, UploadConcurrency)
	}
}
