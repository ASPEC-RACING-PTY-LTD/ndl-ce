package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DevKeyID  = "ee-dev-2026"
	DevPublic = "TNkNiVMKebLEU8iv8xwY39KhX3RdXPlzOrfDUABX8lk="
	TrustDir  = "/etc/ndl/ee-trust"
)

type TrustBundle map[string]ed25519.PublicKey

type Signer struct {
	KeyID   string
	Private ed25519.PrivateKey
}

func ParsePublic(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key is invalid")
	}
	return ed25519.PublicKey(raw), nil
}

func (s Signer) Sign(d Document) (Document, error) {
	if len(s.Private) != ed25519.PrivateKeySize {
		return Document{}, fmt.Errorf("signing key is invalid")
	}
	d.KeyID = s.KeyID
	d.WorkloadsStopped = false
	if d.Version == "" {
		d.Version = DocumentVersion
	}
	body, err := CanonicalJSON(d)
	if err != nil {
		return Document{}, err
	}
	d.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(s.Private, body))
	return d, nil
}

func Verify(d Document, trust TrustBundle) error {
	if err := d.ValidateShape(); err != nil {
		return err
	}
	if strings.TrimSpace(d.Signature) == "" {
		return fmt.Errorf("entitlement is not signed")
	}
	pub, ok := trust[d.KeyID]
	if !ok || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("entitlement key_id is not trusted")
	}
	sig, err := base64.StdEncoding.DecodeString(d.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("entitlement signature is invalid")
	}
	body, err := CanonicalJSON(d)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, body, sig) {
		return fmt.Errorf("entitlement signature does not match payload")
	}
	return nil
}

func DevTrust() TrustBundle {
	pub, err := ParsePublic(DevPublic)
	if err != nil {
		return TrustBundle{}
	}
	return TrustBundle{DevKeyID: pub}
}

func NewEphemeralSigner(keyID string) (Signer, TrustBundle, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Signer{}, nil, err
	}
	if keyID == "" {
		keyID = "ee-test"
	}
	return Signer{KeyID: keyID, Private: priv}, TrustBundle{keyID: pub}, nil
}

func LoadTrust(dir string) TrustBundle {
	out := DevTrust()
	if dir == "" {
		if env := strings.TrimSpace(os.Getenv("NODAL_EE_TRUST_DIR")); env != "" {
			dir = env
		} else {
			dir = TrustDir
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".pub") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		pub, err := ParsePublic(string(raw))
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(name, ".pub")
		out[id] = pub
	}
	return out
}
