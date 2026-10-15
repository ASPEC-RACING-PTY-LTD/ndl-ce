package cloudflare

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultKeyPath holds the key that encrypts stored API tokens. Only the
// control plane user can read it, so a database dump alone reveals nothing.
const DefaultKeyPath = "/var/lib/ndl/control/keys/cloudflare.key"

const sealPrefix = "ndlcf1:"

// Sealer encrypts secrets with AES-256-GCM. Each value is bound to an ID
// (associated data) so a ciphertext cannot be moved to another record.
type Sealer struct {
	Path string

	once sync.Once
	key  []byte
	err  error
}

func (s *Sealer) load() ([]byte, error) {
	s.once.Do(func() {
		path := s.Path
		if path == "" {
			path = DefaultKeyPath
		}
		s.key, s.err = loadOrCreateKey(path)
	})
	return s.key, s.err
}

func loadOrCreateKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		key, derr := hex.DecodeString(strings.TrimSpace(string(raw)))
		if derr != nil || len(key) != 32 {
			return nil, fmt.Errorf("the Cloudflare key file %s is damaged", path)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read Cloudflare key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Cloudflare key directory: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadOrCreateKey(path)
		}
		return nil, fmt.Errorf("write Cloudflare key: %w", err)
	}
	if _, err := f.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *Sealer) gcm() (cipher.AEAD, error) {
	key, err := s.load()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext bound to id.
func (s *Sealer) Seal(id, plaintext string) (string, error) {
	g, err := s.gcm()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := g.Seal(nonce, nonce, []byte(plaintext), []byte(id))
	return sealPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a value sealed for id.
func (s *Sealer) Open(id, sealed string) (string, error) {
	if !strings.HasPrefix(sealed, sealPrefix) {
		return "", fmt.Errorf("stored credential is not encrypted with this version")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, sealPrefix))
	if err != nil {
		return "", fmt.Errorf("stored credential is damaged")
	}
	g, err := s.gcm()
	if err != nil {
		return "", err
	}
	if len(raw) < g.NonceSize() {
		return "", fmt.Errorf("stored credential is damaged")
	}
	plain, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte(id))
	if err != nil {
		return "", fmt.Errorf("stored credential cannot be decrypted; the key file may have changed")
	}
	return string(plain), nil
}

// Last4 is the only part of a token ever shown.
func Last4(token string) string {
	t := strings.TrimSpace(token)
	if len(t) <= 4 {
		return strings.Repeat("•", len(t))
	}
	return t[len(t)-4:]
}
