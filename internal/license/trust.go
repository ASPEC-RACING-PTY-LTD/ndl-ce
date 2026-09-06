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

func AllowDevTrust() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("NODAL_EE_ALLOW_DEV_TRUST")))
	if v == "" {
		v = strings.ToLower(strings.TrimSpace(os.Getenv("NDL_EE_ALLOW_DEV_KEYS")))
	}
	return v == "1" || v == "true" || v == "yes"
}

func ResolveTrustDir(dir string) string {
	if strings.TrimSpace(dir) != "" {
		return dir
	}
	if env := strings.TrimSpace(os.Getenv("NODAL_EE_TRUST_DIR")); env != "" {
		return env
	}
	return TrustDir
}

func LoadTrust(dir string) TrustBundle {
	out := TrustBundle{}
	if AllowDevTrust() {
		out = DevTrust()
	}
	dir = ResolveTrustDir(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		applyRevokedKeys(out, dir)
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
	applyRevokedKeys(out, dir)
	return out
}

func applyRevokedKeys(out TrustBundle, dir string) {
	revoked := loadRevokedLines(dir, "revoked")
	for id := range out {
		if _, ok := revoked[strings.ToLower(id)]; ok {
			delete(out, id)
		}
	}
}

func DigestRevoked(dir, digest string) bool {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if digest == "" {
		return false
	}
	_, ok := loadRevokedLines(ResolveTrustDir(dir), "revoked-artifacts")[digest]
	return ok
}

func loadRevokedLines(dir, name string) map[string]struct{} {
	out := map[string]struct{}{}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[strings.ToLower(line)] = struct{}{}
	}
	return out
}
