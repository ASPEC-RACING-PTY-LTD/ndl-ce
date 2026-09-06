package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTrustOmitsDevUnlessAllowed(t *testing.T) {
	t.Setenv("NODAL_EE_ALLOW_DEV_TRUST", "")
	t.Setenv("NDL_EE_ALLOW_DEV_KEYS", "")
	if AllowDevTrust() {
		t.Fatal("dev trust must be off")
	}
	got := LoadTrust(t.TempDir())
	if _, ok := got[DevKeyID]; ok {
		t.Fatal("dev key must not be trusted by default")
	}
}

func TestVerifyArtifact(t *testing.T) {
	signer, trust, err := NewEphemeralSigner("ee-prod")
	if err != nil {
		t.Fatal(err)
	}
	blob := []byte("ndl-ee package bytes")
	man := ArtifactManifest{
		Version: ArtifactManifestVersion, Name: "ndl-ee", Package: "1.0.0",
		SHA256: ArtifactDigest(blob), KeyID: signer.KeyID, Channel: "enterprise",
		CECompatMin: "1.0.0", CreatedAt: "2026-09-06T12:00:00Z",
	}
	unsigned := man
	body, err := json.Marshal(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	man.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(signer.Private, body))
	if err := VerifyArtifact(man, blob, trust); err != nil {
		t.Fatal(err)
	}
	man.SHA256 = "00"
	if err := VerifyArtifact(man, blob, trust); err == nil {
		t.Fatal("digest")
	}
}

func TestLoadTrustReadsPubFiles(t *testing.T) {
	t.Setenv("NODAL_EE_ALLOW_DEV_TRUST", "")
	t.Setenv("NDL_EE_ALLOW_DEV_KEYS", "")
	_, trust, err := NewEphemeralSigner("ee-prod")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pub := trust["ee-prod"]
	if err := os.WriteFile(filepath.Join(dir, "ee-prod.pub"), []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadTrust(dir)
	if _, ok := got["ee-prod"]; !ok {
		t.Fatal("missing prod key")
	}
}

func TestLoadTrustHonorsRevokedKeys(t *testing.T) {
	t.Setenv("NODAL_EE_ALLOW_DEV_TRUST", "")
	t.Setenv("NDL_EE_ALLOW_DEV_KEYS", "")
	_, trust, err := NewEphemeralSigner("ee-prod")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pub := trust["ee-prod"]
	if err := os.WriteFile(filepath.Join(dir, "ee-prod.pub"), []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "revoked"), []byte("ee-prod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadTrust(dir)
	if _, ok := got["ee-prod"]; ok {
		t.Fatal("revoked key still trusted")
	}
}
