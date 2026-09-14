// Package cdc implements FastCDC content-defined chunking.
//
// Content-defined chunking places chunk boundaries based on the data itself
// rather than at fixed offsets. When bytes are inserted or removed in the
// middle of a stream, boundaries after the edit realign to the same content,
// so unchanged regions keep producing identical chunks. That property is what
// makes deduplication effective across successive backups of a mutating file.
//
// The algorithm is FastCDC (Xia et al., "FastCDC: a Fast and Efficient
// Content-Defined Chunking Approach for Data Deduplication", USENIX ATC 2016)
// with normalized chunking: a stricter mask is used before the average size to
// discourage short chunks and a looser mask after it to discourage long ones,
// which tightens the chunk-size distribution around the target average.
//
// The gear table is derived deterministically from a fixed seed so that chunk
// boundaries are stable across processes and releases. Changing the table or
// the parameters is a repository-format change and must be versioned.
package cdc

import (
	"io"
	"math/bits"
)

// Algorithm identifies the chunker parameters embedded in a repository so a
// reader can reject data produced by an incompatible chunking configuration.
const Algorithm = "fastcdc-v1"

// Default chunk-size bounds. Averages around 1 MiB keep the chunk index small
// while still catching localized edits. Callers may override for benchmarking.
const (
	DefaultMin = 256 << 10
	DefaultAvg = 1 << 20
	DefaultMax = 4 << 20
)

// Config bounds chunk sizes. Min <= Avg <= Max and all must be positive.
type Config struct {
	Min int
	Avg int
	Max int
}

// DefaultConfig returns the standard parameters.
func DefaultConfig() Config {
	return Config{Min: DefaultMin, Avg: DefaultAvg, Max: DefaultMax}
}

func (c Config) normalized() Config {
	if c.Min <= 0 {
		c.Min = DefaultMin
	}
	if c.Avg <= 0 {
		c.Avg = DefaultAvg
	}
	if c.Max <= 0 {
		c.Max = DefaultMax
	}
	if c.Avg < c.Min {
		c.Avg = c.Min
	}
	if c.Max < c.Avg {
		c.Max = c.Avg
	}
	return c
}

// Chunker finds content-defined cut points using the gear-hash rolling sum.
type Chunker struct {
	cfg   Config
	maskS uint64
	maskL uint64
}

// New builds a chunker. Normalization derives two masks around the average:
// maskS (more set bits) is applied before the average length to make an early
// boundary less likely, and maskL (fewer set bits) after it to make a late
// boundary more likely.
func New(cfg Config) *Chunker {
	cfg = cfg.normalized()
	avgBits := uint(bits.Len(uint(cfg.Avg)) - 1)
	// Normalization level 2: +/- 2 bits around the average.
	sBits := avgBits + 2
	lBits := avgBits - 2
	if avgBits < 2 {
		lBits = 0
	}
	return &Chunker{
		cfg:   cfg,
		maskS: spreadMask(sBits),
		maskL: spreadMask(lBits),
	}
}

// Config returns the effective (normalized) parameters.
func (c *Chunker) Config() Config { return c.cfg }

// cutpoint returns the length of the next chunk within data. data is assumed to
// be clamped to at most Max by the caller. It never returns more than len(data)
// and, unless data is the final short remainder, never less than Min.
func (c *Chunker) cutpoint(data []byte) int {
	n := len(data)
	if n <= c.cfg.Min {
		return n
	}
	if n > c.cfg.Max {
		n = c.cfg.Max
	}
	normal := c.cfg.Avg
	if normal > n {
		normal = n
	}
	var fp uint64
	i := c.cfg.Min
	for ; i < normal; i++ {
		fp = (fp << 1) + gear[data[i]]
		if fp&c.maskS == 0 {
			return i
		}
	}
	for ; i < n; i++ {
		fp = (fp << 1) + gear[data[i]]
		if fp&c.maskL == 0 {
			return i
		}
	}
	return i
}

// Split reads r fully and calls emit for each content-defined chunk in order.
// Memory use is bounded to roughly 2*Max regardless of input size. The slice
// passed to emit is only valid for the duration of the call; copy it to retain.
func (c *Chunker) Split(r io.Reader, emit func(chunk []byte) error) error {
	buf := make([]byte, 0, c.cfg.Max*2)
	read := make([]byte, 64<<10)
	eof := false
	for {
		for len(buf) < c.cfg.Max && !eof {
			nr, err := r.Read(read)
			if nr > 0 {
				buf = append(buf, read[:nr]...)
			}
			if err == io.EOF {
				eof = true
			} else if err != nil {
				return err
			}
		}
		if len(buf) == 0 {
			return nil
		}
		cp := c.cutpoint(buf)
		if cp <= 0 {
			cp = len(buf)
		}
		if err := emit(buf[:cp]); err != nil {
			return err
		}
		rest := copy(buf, buf[cp:])
		buf = buf[:rest]
		if len(buf) == 0 && eof {
			return nil
		}
	}
}

// spreadMask returns a 64-bit mask with the requested number of set bits spread
// across the high portion of the word, matching FastCDC's use of a sparse mask.
func spreadMask(bitsSet uint) uint64 {
	if bitsSet == 0 {
		return 0
	}
	if bitsSet >= 64 {
		return ^uint64(0)
	}
	// Place set bits in the upper region to reduce correlation with the
	// low-order rolling-hash bits.
	var m uint64
	pos := 63
	for i := uint(0); i < bitsSet; i++ {
		m |= uint64(1) << uint(pos)
		pos -= 2
		if pos < 0 {
			pos = 62
		}
	}
	return m
}
