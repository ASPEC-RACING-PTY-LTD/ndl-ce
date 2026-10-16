package backup

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Chunk framing: one algorithm byte followed by the payload. Compression is
// applied only when it actually shrinks the chunk, so incompressible data is
// not inflated. The frame is what gets encrypted, so the algorithm choice is
// authenticated along with the data. Chunk ids are keyed hashes of the
// plaintext, so changing the level never breaks deduplication.
const (
	frameRaw  byte = 0
	frameGzip byte = 1 // written by older releases; still read
	frameZstd byte = 2
)

// Compression levels follow zstd's 1 (fastest) to 22 (smallest) scale.
// klauspost/compress maps them onto its encoder modes; levels from about 10
// up use its strongest mode. 0 means DefaultCompressionLevel; -1 stores
// chunks uncompressed.
const (
	DefaultCompressionLevel = 19
	MaxCompressionLevel     = 22
	CompressionOff          = -1
)

// minSaving is the share a compressed chunk must save to be kept compressed;
// below it the raw chunk is stored and decoding costs nothing.
const minSaving = 0.02

var (
	encMu    sync.Mutex
	encoders = map[zstd.EncoderLevel]*zstd.Encoder{}
	decOnce  sync.Once
	decoder  *zstd.Decoder
	decErr   error
)

func zstdEncoder(level int) (*zstd.Encoder, error) {
	el := zstd.EncoderLevelFromZstd(level)
	encMu.Lock()
	defer encMu.Unlock()
	if e := encoders[el]; e != nil {
		return e, nil
	}
	// EncodeAll is safe for concurrent use; one encoder per level is shared.
	e, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(el), zstd.WithEncoderCRC(false), zstd.WithLowerEncoderMem(true))
	if err != nil {
		return nil, err
	}
	encoders[el] = e
	return e, nil
}

func zstdDecoder() (*zstd.Decoder, error) {
	decOnce.Do(func() {
		decoder, decErr = zstd.NewReader(nil, zstd.WithDecoderConcurrency(0))
	})
	return decoder, decErr
}

// NormalizeCompressionLevel clamps a configured level.
func NormalizeCompressionLevel(level int) int {
	switch {
	case level == 0:
		return DefaultCompressionLevel
	case level < 0:
		return CompressionOff
	case level > MaxCompressionLevel:
		return MaxCompressionLevel
	}
	return level
}

func rawFrame(plain []byte) []byte {
	raw := make([]byte, 0, len(plain)+1)
	raw = append(raw, frameRaw)
	return append(raw, plain...)
}

// compressChunk frames one chunk. level is a zstd level; skip stores the
// chunk raw (already-compressed file types).
func compressChunk(plain []byte, level int, skip bool) ([]byte, error) {
	level = NormalizeCompressionLevel(level)
	if skip || level == CompressionOff || len(plain) < 64 {
		return rawFrame(plain), nil
	}
	enc, err := zstdEncoder(level)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 1, len(plain)/2+64)
	out[0] = frameZstd
	out = enc.EncodeAll(plain, out)
	if float64(len(out)) > float64(len(plain))*(1-minSaving) {
		return rawFrame(plain), nil
	}
	return out, nil
}

func decompressChunk(frame []byte) ([]byte, error) {
	if len(frame) == 0 {
		return nil, fmt.Errorf("empty chunk frame")
	}
	switch frame[0] {
	case frameRaw:
		return append([]byte(nil), frame[1:]...), nil
	case frameZstd:
		dec, err := zstdDecoder()
		if err != nil {
			return nil, err
		}
		return dec.DecodeAll(frame[1:], nil)
	case frameGzip:
		gz, err := gzip.NewReader(bytes.NewReader(frame[1:]))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		return io.ReadAll(gz)
	default:
		return nil, fmt.Errorf("unknown chunk frame %d", frame[0])
	}
}

// precompressed lists file types whose content is already compressed;
// compressing them again costs CPU and saves nothing.
var precompressed = map[string]bool{
	".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".txz": true, ".zst": true, ".zstd": true, ".lz4": true,
	".lzma": true, ".7z": true, ".zip": true, ".rar": true, ".jar": true, ".war": true, ".apk": true, ".deb": true,
	".rpm": true, ".whl": true, ".br": true, ".sz": true, ".snappy": true,
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true, ".heic": true, ".heif": true,
	".mp3": true, ".m4a": true, ".aac": true, ".ogg": true, ".opus": true, ".flac": true,
	".mp4": true, ".m4v": true, ".mkv": true, ".webm": true, ".mov": true, ".avi": true,
	".woff": true, ".woff2": true, ".pdf": true, ".docx": true, ".xlsx": true, ".pptx": true, ".odt": true,
}

// isPrecompressed reports whether a path's type is already compressed.
func isPrecompressed(path string) bool {
	return precompressed[strings.ToLower(filepath.Ext(path))]
}
