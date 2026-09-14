package backup

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// Chunk framing: one algorithm byte followed by the payload. Compression is
// applied only when it actually shrinks the chunk, so incompressible data is
// not inflated. The frame is what gets encrypted, so the algorithm choice is
// authenticated along with the data.
const (
	frameRaw  byte = 0
	frameGzip byte = 1
)

func compressChunk(plain []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(frameGzip)
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := gz.Write(plain); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if buf.Len() >= len(plain)+1 {
		raw := make([]byte, 0, len(plain)+1)
		raw = append(raw, frameRaw)
		raw = append(raw, plain...)
		return raw, nil
	}
	return buf.Bytes(), nil
}

func decompressChunk(frame []byte) ([]byte, error) {
	if len(frame) == 0 {
		return nil, fmt.Errorf("empty chunk frame")
	}
	switch frame[0] {
	case frameRaw:
		return append([]byte(nil), frame[1:]...), nil
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
