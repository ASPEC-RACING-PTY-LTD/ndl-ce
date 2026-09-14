package ndnet

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// GenerateWGKey returns a WireGuard Curve25519 keypair as standard base64.
// The private key is a secret. Callers must not put it in JSON list bodies
// or systemd unit/netdev files.
func GenerateWGKey() (private, public string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()), base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// PublicFromPrivate derives the WireGuard public key.
func PublicFromPrivate(privB64 string) (string, error) {
	raw, err := decodeWGKey(privB64)
	if err != nil {
		return "", err
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", fmt.Errorf("wireguard private key is invalid")
	}
	return base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

func decodeWGKey(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("wireguard key must be 32-byte base64")
	}
	return raw, nil
}

// ValidWGPublicKey reports whether s is a 32-byte standard-base64 key.
func ValidWGPublicKey(s string) bool {
	_, err := decodeWGKey(s)
	return err == nil
}
