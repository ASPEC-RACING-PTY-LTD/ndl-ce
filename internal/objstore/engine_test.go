package objstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptRoundTripAndRefusesWrongKey(t *testing.T) {
	_, key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("nodal-backup-plain")
	blob, err := Encrypt(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(blob, []byte(Magic)) {
		t.Fatalf("missing magic: %q", blob[:4])
	}
	got, err := Decrypt(blob, key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip %q", got)
	}
	wrong := append([]byte(nil), key...)
	wrong[0] ^= 0xff
	if _, err := Decrypt(blob, wrong); err == nil {
		t.Fatal("wrong key must fail")
	}
}

func TestEnginePutGetEncryptedObject(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "disk.qcow2")
	dst := filepath.Join(dir, "restored.qcow2")
	if err := os.WriteFile(src, []byte("qcow-fixture-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	mem := NewMemoryTransport()
	eng := &Engine{Transport: mem}
	put, err := eng.Do(t.Context(), Request{
		Action: ActionPut, Provider: KindR2, Bucket: "backups", Key: "w1/a1.qcow2.ndl",
		SourcePath: src, EncryptionKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !put.Encrypted || put.PlaintextSize != 18 {
		t.Fatalf("put %+v", put)
	}
	cipher := mem.Ciphertext("backups", "w1/a1.qcow2.ndl")
	if !bytes.HasPrefix(cipher, []byte(Magic)) {
		t.Fatal("stored object must be encrypted NDLE")
	}
	if bytes.Contains(cipher, []byte("qcow-fixture-bytes")) {
		t.Fatal("plaintext must not appear in the bucket")
	}
	got, err := eng.Do(t.Context(), Request{
		Action: ActionGet, Provider: KindR2, Bucket: "backups", Key: "w1/a1.qcow2.ndl",
		DestPath: dst, EncryptionKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.PlaintextSHA256 != put.PlaintextSHA256 {
		t.Fatalf("checksum mismatch %s %s", got.PlaintextSHA256, put.PlaintextSHA256)
	}
	raw, _ := os.ReadFile(dst)
	if string(raw) != "qcow-fixture-bytes" {
		t.Fatalf("restored %q", raw)
	}
}

func TestEngineRefusesMissingEncryptionKey(t *testing.T) {
	eng := &Engine{Transport: NewMemoryTransport()}
	_, err := eng.Do(t.Context(), Request{
		Action: ActionPut, Bucket: "b", Key: "k", SourcePath: "/tmp/x",
	})
	if err == nil || !strings.Contains(err.Error(), "encryption") {
		t.Fatalf("got %v", err)
	}
}

func TestSkipNetworkWithoutTransportIsUnavailable(t *testing.T) {
	eng := &Engine{SkipNetwork: true}
	res, err := eng.Do(t.Context(), Request{
		Action: ActionPut, Bucket: "b", Key: "k.ndl", SourcePath: "/tmp/missing",
		EncryptionKey: make([]byte, KeySize),
	})
	if err == nil {
		t.Fatal("skip-network put must not invent success")
	}
	if res.Status == "available" || res.Encrypted {
		t.Fatalf("honest unavailable required: %+v", res)
	}
}

func TestNoCheckBucketDoesNotFakeAvailable(t *testing.T) {
	eng := &Engine{Transport: NewMemoryTransport()}
	res, err := eng.Do(t.Context(), Request{
		Action: ActionHead, Bucket: "b", Key: "missing", NoCheckBucket: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == "available" {
		t.Fatal("no_check_bucket must not invent available")
	}
}

func TestSecondPutCanTransferLessWhenPlaintextShrinks(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "stream")
	_, key, _ := GenerateKey()
	mem := NewMemoryTransport()
	eng := &Engine{Transport: mem}
	if err := os.WriteFile(src, bytes.Repeat([]byte("A"), 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	full, err := eng.Do(t.Context(), Request{
		Action: ActionPut, Bucket: "b", Key: "full.ndl", SourcePath: src, EncryptionKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, bytes.Repeat([]byte("B"), 256), 0o600); err != nil {
		t.Fatal(err)
	}
	inc, err := eng.Do(t.Context(), Request{
		Action: ActionPut, Bucket: "b", Key: "inc.ndl", SourcePath: src, EncryptionKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if inc.TransferredBytes >= full.TransferredBytes {
		t.Fatalf("incremental should transfer less: full=%d inc=%d", full.TransferredBytes, inc.TransferredBytes)
	}
}
