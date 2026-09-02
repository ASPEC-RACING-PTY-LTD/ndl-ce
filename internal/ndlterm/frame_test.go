package ndlterm

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, TypeOutput, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	f, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != TypeOutput || string(f.Payload) != "hello" {
		t.Fatalf("%+v", f)
	}
}

func TestEncodeDecode(t *testing.T) {
	raw, err := Encode(TypeCWD, []byte("/root"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Decode(raw)
	if err != nil || f.Type != TypeCWD || string(f.Payload) != "/root" {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestResizePayload(t *testing.T) {
	p := ResizePayload(24, 80)
	r, c, err := ParseResize(p)
	if err != nil || r != 24 || c != 80 {
		t.Fatalf("%d %d %v", r, c, err)
	}
}
