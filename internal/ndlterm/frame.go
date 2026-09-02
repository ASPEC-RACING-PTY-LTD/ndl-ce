package ndlterm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// nodal.term.v1 binary frames. Never query-string tickets.
const (
	TypeInput        byte = 1
	TypeOutput       byte = 2
	TypeResize       byte = 3
	TypePing         byte = 4
	TypePong         byte = 5
	TypeCWD          byte = 6
	TypeError        byte = 7
	TypeSessionEnded byte = 8
)

const maxPayload = 1 << 20

// Frame is one protocol message.
type Frame struct {
	Type    byte
	Payload []byte
}

func Write(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > maxPayload {
		return fmt.Errorf("term frame too large")
	}
	var hdr [5]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

func Read(r io.Reader) (Frame, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxPayload {
		return Frame{}, fmt.Errorf("term frame too large")
	}
	payload := make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Type: hdr[0], Payload: payload}, nil
}

func ResizePayload(rows, cols uint16) []byte {
	var b [4]byte
	binary.BigEndian.PutUint16(b[0:], rows)
	binary.BigEndian.PutUint16(b[2:], cols)
	return b[:]
}

func Encode(typ byte, payload []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := Write(&buf, typ, payload); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func Decode(p []byte) (Frame, error) {
	return Read(bytes.NewReader(p))
}

func ParseResize(p []byte) (rows, cols uint16, err error) {
	if len(p) < 4 {
		return 0, 0, fmt.Errorf("resize payload is short")
	}
	return binary.BigEndian.Uint16(p[0:2]), binary.BigEndian.Uint16(p[2:4]), nil
}
