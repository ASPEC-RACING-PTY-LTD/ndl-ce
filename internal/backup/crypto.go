package backup

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// KeyID identifies a chunk by a keyed hash of its plaintext. Using an HMAC keyed
// by a per-repository secret (rather than a bare SHA-256 of the plaintext) still
// deduplicates within a repository but does not let an outside party confirm
// that a known file is present by matching a public content hash.
type KeyID [32]byte

// Keys holds the derived subkeys for one repository. The master key never
// leaves memory here; subkeys are derived with HKDF using distinct labels so a
// weakness in one use cannot be leveraged against another.
type Keys struct {
	master  []byte
	idKey   []byte // HMAC key for chunk identity
	encKey  []byte // XChaCha20-Poly1305 key for chunk/manifest sealing
	nonceKl []byte // key for deriving deterministic per-chunk nonces
}

// NewKeys derives the repository subkeys from a 32-byte master secret.
func NewKeys(master []byte) (*Keys, error) {
	if len(master) < 32 {
		return nil, fmt.Errorf("repository master key must be at least 32 bytes")
	}
	k := &Keys{master: append([]byte(nil), master...)}
	var err error
	if k.idKey, err = derive(master, "ndl-backup/v1/chunk-id"); err != nil {
		return nil, err
	}
	if k.encKey, err = derive(master, "ndl-backup/v1/chunk-enc"); err != nil {
		return nil, err
	}
	if k.nonceKl, err = derive(master, "ndl-backup/v1/chunk-nonce"); err != nil {
		return nil, err
	}
	return k, nil
}

// GenerateMaster returns a fresh random 32-byte master key.
func GenerateMaster() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func derive(master []byte, label string) ([]byte, error) {
	r := hkdf.New(sha256.New, master, nil, []byte(label))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ID returns the keyed chunk identity for plaintext.
func (k *Keys) ID(plain []byte) KeyID {
	m := hmac.New(sha256.New, k.idKey)
	m.Write(plain)
	var id KeyID
	copy(id[:], m.Sum(nil))
	return id
}

// Seal encrypts and authenticates one chunk. The nonce is derived
// deterministically from the chunk identity: because each unique plaintext is
// stored exactly once, a unique plaintext always maps to a unique nonce, which
// keeps deduplication working while remaining nonce-safe. The chunk id is bound
// as associated data so a sealed chunk cannot be silently relabeled.
func (k *Keys) Seal(id KeyID, plain []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(k.encKey)
	if err != nil {
		return nil, err
	}
	nonce := k.nonce(id[:])
	return aead.Seal(nil, nonce, plain, id[:]), nil
}

// Open decrypts and authenticates a sealed chunk previously produced by Seal.
func (k *Keys) Open(id KeyID, sealed []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(k.encKey)
	if err != nil {
		return nil, err
	}
	nonce := k.nonce(id[:])
	return aead.Open(nil, nonce, sealed, id[:])
}

// SealBytes seals arbitrary data (a manifest or index) under a label-scoped
// nonce derived from the data's own keyed hash, returning nonce||ciphertext.
func (k *Keys) SealBytes(label string, plain []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(k.encKey)
	if err != nil {
		return nil, err
	}
	m := hmac.New(sha256.New, k.nonceKl)
	m.Write([]byte(label))
	m.Write(plain)
	nonce := m.Sum(nil)[:chacha20poly1305.NonceSizeX]
	out := append([]byte(nil), nonce...)
	return aead.Seal(out, nonce, plain, []byte(label)), nil
}

// OpenBytes reverses SealBytes.
func (k *Keys) OpenBytes(label string, blob []byte) ([]byte, error) {
	if len(blob) < chacha20poly1305.NonceSizeX {
		return nil, fmt.Errorf("sealed blob is too short")
	}
	aead, err := chacha20poly1305.NewX(k.encKey)
	if err != nil {
		return nil, err
	}
	nonce := blob[:chacha20poly1305.NonceSizeX]
	return aead.Open(nil, nonce, blob[chacha20poly1305.NonceSizeX:], []byte(label))
}

func (k *Keys) nonce(id []byte) []byte {
	m := hmac.New(sha256.New, k.nonceKl)
	m.Write(id)
	return m.Sum(nil)[:chacha20poly1305.NonceSizeX]
}
