package storage

import "testing"

func TestZFSDestroySnapshotArgvOnlyTargetsSnapshots(t *testing.T) {
	argv, err := ZFSDestroySnapshotArgv("tank/vol", "ndl-v2-abc")
	if err != nil || len(argv) != 3 || argv[2] != "tank/vol@ndl-v2-abc" {
		t.Fatalf("argv %v %v", argv, err)
	}
	for _, snap := range []string{"", "a b", "x/y", "a@b", "a,b", "a%b"} {
		if _, err := ZFSDestroySnapshotArgv("tank/vol", snap); err == nil {
			t.Fatalf("%q must be refused", snap)
		}
	}
	if _, err := ZFSDestroySnapshotArgv("", "s"); err == nil {
		t.Fatal("an empty dataset must be refused")
	}
}
