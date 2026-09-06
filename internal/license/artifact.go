package license

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const ArtifactManifestVersion = "ndl-ee-artifact-v1"

// ArtifactManifest is the CE copy of the EE signed artifact contract.
// Community Edition never imports ndl-ee.
type ArtifactManifest struct {
	Version     string `json:"version"`
	Name        string `json:"name"`
	Package     string `json:"package"`
	SHA256      string `json:"sha256"`
	KeyID       string `json:"key_id"`
	Channel     string `json:"channel"`
	CECompatMin string `json:"ce_compat_min"`
	CECompatMax string `json:"ce_compat_max"`
	CreatedAt   string `json:"created_at"`
	Signature   string `json:"signature,omitempty"`
}

func ArtifactDigest(blob []byte) string {
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}

func VerifyArtifact(m ArtifactManifest, blob []byte, trust TrustBundle) error {
	if m.Version != ArtifactManifestVersion {
		return fmt.Errorf("unsupported artifact manifest")
	}
	if ArtifactDigest(blob) != strings.ToLower(m.SHA256) {
		return fmt.Errorf("artifact digest does not match manifest")
	}
	pub, ok := trust[m.KeyID]
	if !ok || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("artifact key_id is not trusted")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("artifact signature is invalid")
	}
	copyDoc := m
	copyDoc.Signature = ""
	body, err := json.Marshal(copyDoc)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, body, sig) {
		return fmt.Errorf("artifact signature does not match manifest")
	}
	if DigestRevoked("", m.SHA256) {
		return fmt.Errorf("artifact digest is revoked")
	}
	return nil
}
