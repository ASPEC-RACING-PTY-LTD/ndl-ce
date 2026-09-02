package objstore

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// Encrypt compresses then AES-256-GCM seals plaintext. The key never leaves the caller.
func Encrypt(plaintext, key []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("encryption key must be %d bytes", KeySize)
	}
	var compressed bytes.Buffer
	gz, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := gz.Write(plaintext); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, compressed.Bytes(), []byte(Magic))
	out := make([]byte, 0, HeaderSize+len(sealed))
	out = append(out, Magic...)
	out = append(out, Version)
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// Decrypt opens an NDLE blob and gunzips the plaintext.
func Decrypt(blob, key []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("encryption key must be %d bytes", KeySize)
	}
	if len(blob) < HeaderSize {
		return nil, fmt.Errorf("encrypted object is truncated")
	}
	if string(blob[:4]) != Magic {
		return nil, fmt.Errorf("object is not a No-dal encrypted backup")
	}
	if blob[4] != Version {
		return nil, fmt.Errorf("unsupported encryption version")
	}
	nonce := blob[5:HeaderSize]
	sealed := blob[HeaderSize:]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	compressed, err := gcm.Open(nil, nonce, sealed, []byte(Magic))
	if err != nil {
		return nil, fmt.Errorf("decrypt failed")
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	return io.ReadAll(gz)
}

// SHA256Hex checksums plaintext for the backup catalog.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ParseKey accepts 32 raw bytes or 64 hex characters.
func ParseKey(raw string) ([]byte, error) {
	raw = string(bytes.TrimSpace([]byte(raw)))
	if raw == "" {
		return nil, fmt.Errorf("encryption key is required")
	}
	if len(raw) == KeySize {
		return []byte(raw), nil
	}
	if len(raw) == KeySize*2 {
		out, err := hex.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("encryption key is not hex")
		}
		return out, nil
	}
	return nil, fmt.Errorf("encryption key must be 32 bytes or 64 hex characters")
}

// GenerateKey returns a 32-byte key as lowercase hex (stored in secrets).
func GenerateKey() (string, []byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(key), key, nil
}
