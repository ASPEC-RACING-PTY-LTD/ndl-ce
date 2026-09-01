package identity

import (
	"os"
	"path/filepath"
	"testing"
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
