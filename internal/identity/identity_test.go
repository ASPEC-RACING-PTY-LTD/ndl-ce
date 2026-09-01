package identity

import "testing"

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
