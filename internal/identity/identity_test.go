package identity

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/cluster"
)

func TestClusterAndNodeStable(t *testing.T) {
	f := Files{Dir: t.TempDir()}
	if err := f.SaveCluster("c1"); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveCluster("c1"); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveCluster("c2"); err == nil {
		t.Fatal("must not replace cluster_id")
	}
	if err := f.SaveNode("n1", "c1"); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveNode("n1", "c1"); err != nil {
		t.Fatal(err)
	}
	id, err := f.LoadCluster()
	if err != nil || id != "c1" {
		t.Fatal(id, err)
	}
}

func TestSaveJoinMaterialWritesNodeKey0600(t *testing.T) {
	f := Files{Dir: t.TempDir()}
	if err := f.SaveJoinMaterial("c1", "n1", []byte("CA"), []byte("CERT"), []byte("KEY")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(f.Dir, "node.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("node.key mode %o", info.Mode().Perm())
	}
	nodeID, clusterID, err := f.LoadNode()
	if err != nil || nodeID != "n1" || clusterID != "c1" {
		t.Fatal(nodeID, clusterID, err)
	}
}

func TestLoadClientTLSUsesJoinMaterial(t *testing.T) {
	f := Files{Dir: t.TempDir()}
	cfg, err := f.LoadClientTLS()
	if err != nil || cfg != nil {
		t.Fatalf("missing material %v %v", cfg, err)
	}
	ca := cluster.CA{Dir: t.TempDir()}
	certPEM, keyPEM, err := ca.IssueNode("n1", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	caPEM, err := ca.CertPEM()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SaveJoinMaterial("c1", "n1", caPEM, certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	cfg, err = f.LoadClientTLS()
	if err != nil || cfg == nil || len(cfg.Certificates) != 1 || cfg.RootCAs == nil {
		t.Fatalf("join mTLS material %+v %v", cfg, err)
	}
	sys, err := x509.SystemCertPool()
	if err != nil || sys == nil || len(sys.Subjects()) == 0 {
		t.Skip("no system roots in this environment")
	}
	if len(cfg.RootCAs.Subjects()) < len(sys.Subjects()) {
		t.Fatal("join mTLS must keep system roots for northbound HTTPS")
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("node cert pem")
	}
	nodeCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nodeCert.Verify(x509.VerifyOptions{
		Roots:     cfg.RootCAs,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("cluster CA must still verify node certs: %v", err)
	}
}
