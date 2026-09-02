package storetrust

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const AlgorithmEd25519 = "ed25519"

// KeyPair is a cluster Store signing key. The private half never leaves secrets storage.
type KeyPair struct {
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
}

// Generate creates an Ed25519 Store signing key.
func Generate() (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Public: pub, Private: priv}, nil
}

// PayloadSHA256 is the fail-closed digest of the stored manifest bytes.
func PayloadSHA256(manifest []byte) string {
	sum := sha256.Sum256(manifest)
	return hex.EncodeToString(sum[:])
}

// Sign returns a base64 Ed25519 signature over the exact manifest bytes.
func Sign(priv ed25519.PrivateKey, manifest []byte) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("signing key is invalid")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifest)), nil
}

// Verify fails closed when the signature does not match the stored bytes.
func Verify(pub ed25519.PublicKey, manifest []byte, sigB64 string) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("public key is invalid")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("signature does not match manifest; tamper fails closed")
	}
	if !ed25519.Verify(pub, manifest, sig) {
		return fmt.Errorf("signature does not match manifest; tamper fails closed")
	}
	return nil
}

// EncodePublic is standard base64 of the 32-byte public key.
func EncodePublic(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// EncodePrivate is standard base64 of the 64-byte private key.
func EncodePrivate(priv ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(priv)
}

// ParsePublic decodes a Store public key.
func ParsePublic(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key is invalid")
	}
	return ed25519.PublicKey(raw), nil
}

// ParsePrivate decodes a Store private key from secrets storage.
func ParsePrivate(b64 string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("signing key is invalid")
	}
	return ed25519.PrivateKey(raw), nil
}
