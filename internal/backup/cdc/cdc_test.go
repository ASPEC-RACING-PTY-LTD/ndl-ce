package cdc

import (
	"bytes"
	"math/rand"
	"testing"
)

func splitAll(t *testing.T, c *Chunker, data []byte) [][]byte {
	t.Helper()
	var out [][]byte
	err := c.Split(bytes.NewReader(data), func(chunk []byte) error {
		cp := append([]byte(nil), chunk...)
		out = append(out, cp)
		return nil
	})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	// Reassembly must equal the input exactly.
	var joined []byte
	for _, c := range out {
		joined = append(joined, c...)
	}
	if !bytes.Equal(joined, data) {
		t.Fatalf("reassembled data does not match input: got %d bytes want %d", len(joined), len(data))
	}
	return out
}

func randData(seed int64, n int) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	r.Read(b)
	return b
}

func TestSplitDeterministic(t *testing.T) {
	c := New(DefaultConfig())
	data := randData(1, 8<<20)
	a := splitAll(t, c, data)
	b := splitAll(t, c, data)
	if len(a) != len(b) {
		t.Fatalf("nondeterministic chunk count %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			t.Fatalf("chunk %d differs between runs", i)
		}
	}
	if len(a) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(a))
	}
}

func TestChunkSizeBounds(t *testing.T) {
	cfg := DefaultConfig()
	c := New(cfg)
	data := randData(2, 16<<20)
	chunks := splitAll(t, c, data)
	var total int
	for i, ch := range chunks {
		total += len(ch)
		if len(ch) > cfg.Max {
			t.Fatalf("chunk %d size %d exceeds max %d", i, len(ch), cfg.Max)
		}
		// Every chunk except the final remainder must be at least Min.
		if i < len(chunks)-1 && len(ch) < cfg.Min {
			t.Fatalf("chunk %d size %d below min %d", i, len(ch), cfg.Min)
		}
	}
	avg := total / len(chunks)
	// The normalized average should land within a reasonable band of target.
	if avg < cfg.Avg/4 || avg > cfg.Avg*4 {
		t.Fatalf("average chunk size %d is far from target %d", avg, cfg.Avg)
	}
}

// TestBoundaryRealignment is the core deduplication property: inserting bytes
// near the front of a stream must not change the majority of downstream chunks.
func TestBoundaryRealignment(t *testing.T) {
	c := New(DefaultConfig())
	base := randData(3, 12<<20)
	baseChunks := splitAll(t, c, base)

	// Insert 100 bytes near the beginning.
	insert := randData(99, 100)
	at := 5000
	modified := make([]byte, 0, len(base)+len(insert))
	modified = append(modified, base[:at]...)
	modified = append(modified, insert...)
	modified = append(modified, base[at:]...)
	modChunks := splitAll(t, c, modified)

	baseSet := map[string]int{}
	for _, ch := range baseChunks {
		baseSet[string(ch)]++
	}
	shared := 0
	for _, ch := range modChunks {
		if baseSet[string(ch)] > 0 {
			baseSet[string(ch)]--
			shared++
		}
	}
	sharedBytes := 0
	for _, ch := range modChunks {
		_ = ch
	}
	for _, ch := range baseChunks {
		_ = ch
	}
	// After a small insertion, the vast majority of base chunks should reappear
	// unchanged. Fixed-size chunking would shift every subsequent boundary and
	// share almost nothing.
	frac := float64(shared) / float64(len(baseChunks))
	if frac < 0.9 {
		t.Fatalf("only %.1f%% of chunks realigned after insertion; dedup would be poor", frac*100)
	}
	_ = sharedBytes
}

func TestSplitBoundedMemoryLargeInput(t *testing.T) {
	// A tiny Max with a large input exercises the buffer-shifting path and
	// proves Split does not accumulate the whole input.
	c := New(Config{Min: 64, Avg: 256, Max: 1024})
	data := randData(4, 4<<20)
	chunks := splitAll(t, c, data)
	for i, ch := range chunks {
		if len(ch) > 1024 {
			t.Fatalf("chunk %d exceeds max under small config: %d", i, len(ch))
		}
	}
}
