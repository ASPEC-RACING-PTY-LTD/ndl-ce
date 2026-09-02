package cluster

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIssueNodeDoesNotEmbedCAKey(t *testing.T) {
	dir := t.TempDir()
	ca := CA{Dir: dir}
	now := time.Now().UTC()
	certPEM, keyPEM, err := ca.IssueNode("11111111-2222-3333-4444-555555555555", now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(certPEM), "PRIVATE KEY") {
		t.Fatal("node cert must not include a private key")
	}
	caKey, err := os.ReadFile(filepath.Join(dir, CAKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(certPEM), string(caKey)) || strings.Contains(string(keyPEM), string(caKey)) {
		t.Fatal("cluster CA private key must not appear in issued material")
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("node cert pem")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.IsCA {
		t.Fatal("node cert must not be a CA")
	}
	caCertPEM, err := ca.CertPEM()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(caCertPEM), "PRIVATE KEY") {
		t.Fatal("CA cert PEM must not include the key")
	}
	info, err := os.Stat(filepath.Join(dir, CAKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("ca.key mode %o", info.Mode().Perm())
	}
	if len(cert.URIs) != 1 || cert.URIs[0].String() != "spiffe://no-dal/node/11111111-2222-3333-4444-555555555555" {
		t.Fatalf("SPIFFE-shaped URI SAN %v", cert.URIs)
	}
	if err := ca.VerifyClientCerts([][]byte{cert.Raw}); err != nil {
		t.Fatal(err)
	}
	if err := ca.RevokeNode("11111111-2222-3333-4444-555555555555"); err != nil {
		t.Fatal(err)
	}
	if err := ca.VerifyClientCerts([][]byte{cert.Raw}); err == nil {
		t.Fatal("revoked serial must fail closed")
	}
}

func TestPairingTokenIsSingleUseMarker(t *testing.T) {
	ca := CA{Dir: t.TempDir()}
	if ca.PairingUsed("peer-1") {
		t.Fatal("unused")
	}
	if err := ca.MarkPairingUsed("peer-1"); err != nil {
		t.Fatal(err)
	}
	if !ca.PairingUsed("peer-1") {
		t.Fatal("consumed pairing token must stay used")
	}
}

func TestUniqueNodeNameDoesNotUseHostnameAsID(t *testing.T) {
	taken := map[string]struct{}{"box-b": {}}
	name := UniqueNodeName("box-b.example.com", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", taken)
	if name == "box-b" {
		t.Fatal("colliding hostname must be suffixed")
	}
	if !strings.HasPrefix(name, "box-b-") {
		t.Fatalf("got %s", name)
	}
	if UniqueNodeName("box-b", "different-id", nil) == UniqueNodeName("box-b", "other-id", nil) {
		// same locator is fine when unused; identity is the UUID, not this name.
	}
}
