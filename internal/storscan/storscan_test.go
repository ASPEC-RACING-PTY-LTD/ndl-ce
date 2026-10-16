package storscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitScan(t *testing.T, m *Manager, id string) (Status, *Node) {
	t.Helper()
	for i := 0; i < 500; i++ {
		st, tree, _ := m.Get(id)
		if st.State != StateRunning {
			return st, tree
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("scan did not finish")
	return Status{}, nil
}

func TestScanSumsSortsAndCountsHardLinksOnce(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "big", "a.bin"), 300<<10)
	writeFile(t, filepath.Join(root, "big", "nested", "b.bin"), 200<<10)
	writeFile(t, filepath.Join(root, "small", "c.txt"), 8<<10)
	if err := os.Link(filepath.Join(root, "big", "a.bin"), filepath.Join(root, "small", "hardlink.bin")); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink("/etc", filepath.Join(root, "link-to-etc"))
	m := &Manager{}
	s, err := m.Start(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(root, true); err == nil {
		t.Fatal("only one scan may run at a time")
	}
	st, tree := waitScan(t, m, s.ID)
	if st.State != StateDone || tree == nil {
		t.Fatalf("scan %+v", st)
	}
	if tree.Children[0].Name != "big" || tree.Children[0].Size < 500<<10 {
		t.Fatalf("largest first: %+v", tree.Children[0])
	}
	small, err := Find(tree, "small")
	if err != nil {
		t.Fatal(err)
	}
	if small.Size >= 300<<10 {
		t.Fatalf("a hard link must not be counted twice: %d", small.Size)
	}
	link, _ := Find(tree, "link-to-etc")
	if link == nil || !link.Symlink || link.Size != 0 {
		t.Fatalf("symlinks are not followed: %+v", link)
	}
	if tree.Files != 5 || tree.Dirs != 3 {
		t.Fatalf("counts files=%d dirs=%d", tree.Files, tree.Dirs)
	}
	sh := Shallow(tree)
	for _, c := range sh.Children {
		if c.Name == "big" && (!c.HasChildren || len(c.Children) != 0) {
			t.Fatalf("shallow copies omit grandchildren: %+v", c)
		}
	}
}

func TestPruneRollsUpSmallItems(t *testing.T) {
	var kids []*Node
	for i := 0; i < keepChildren+25; i++ {
		kids = append(kids, &Node{Name: "f", Size: int64(i)})
	}
	kept, other := prune(kids)
	if len(kept) != keepChildren || other == nil || other.Count != 25 || kept[0].Size != int64(keepChildren+24) {
		t.Fatalf("kept %d other %+v", len(kept), other)
	}
}

func TestDeleteRefusesEscapesProtectedAndMounts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "junk", "old.log"), 4096)
	writeFile(t, filepath.Join(root, "keep", "db.sql"), 4096)
	writeFile(t, filepath.Join(root, "mnt", "inside", "data"), 4096)
	writeFile(t, filepath.Join(root, "pool", "leftover.bin"), 4096)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret"), 10)
	_ = os.Symlink(outside, filepath.Join(root, "escape"))
	mounts := func() ([]string, error) { return []string{"/", filepath.Join(root, "mnt", "inside")}, nil }
	res := Delete(root, []string{"junk", "../" + filepath.Base(outside), "escape/secret", "keep", "mnt", "", "escape", "pool", "pool/leftover.bin"}, []string{filepath.Join(root, "keep")}, []string{filepath.Join(root, "pool")}, mounts)
	byPath := map[string]DeleteResult{}
	for _, r := range res {
		byPath[r.Path] = r
	}
	if !byPath["junk"].Deleted {
		t.Fatalf("junk: %+v", byPath["junk"])
	}
	if byPath["pool"].Deleted || !byPath["pool/leftover.bin"].Deleted {
		t.Fatalf("a kept folder stays but its contents may go: %+v %+v", byPath["pool"], byPath["pool/leftover.bin"])
	}
	for _, p := range []string{"escape/secret", "keep", "mnt", ""} {
		if byPath[p].Deleted || byPath[p].Error == "" {
			t.Fatalf("%q must be refused: %+v", p, byPath[p])
		}
	}
	if !strings.Contains(byPath["mnt"].Error, "mounted") || !strings.Contains(byPath["escape/secret"].Error, "link") {
		t.Fatalf("reasons: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatal("a file outside the root was reached")
	}
	// "../x" is cleaned inside the root, and the symlink itself may go but not its target.
	if !byPath["escape"].Deleted {
		t.Fatalf("the link itself can be removed: %+v", byPath["escape"])
	}
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatal("removing a symlink must not touch its target")
	}
	if _, err := Resolve("/", "etc/passwd", nil, nil); err == nil {
		t.Fatal("system files are protected")
	}
	if _, err := Resolve("/var", "lib", nil, nil); err == nil {
		t.Fatal("a folder containing protected paths cannot be deleted")
	}
}

func TestClassifyIsHonestAboutWhatItKnows(t *testing.T) {
	now := time.Now()
	known := []Known{{Path: "/var/lib/ndl/storage/local/volumes/container-root/abc.img", Purpose: "Disk of container VaultDock", Protected: true}}
	cases := []struct {
		path, purpose, importance, confidence string
		dir                                   bool
	}{
		{"/var/lib/ndl/storage/local/volumes/container-root/abc.img", "Disk of container VaultDock", ImportanceCritical, ConfidenceKnown, false},
		{"/var/lib/ndl/storage/local/volumes", "Contains Disk of container VaultDock", ImportanceCritical, ConfidenceKnown, true},
		{"/var/cache/apt/archives/x.deb", "Downloaded package files", ImportanceLow, ConfidenceLikely, false},
		{"/var/log/syslog.3.gz", "Rotated old log file", ImportanceLow, ConfidenceLikely, false},
		{"/var/log/syslog", "Log files", ImportanceReview, ConfidenceLikely, false},
		{"/var/lib/docker/overlay2", "Docker images, layers and container files", ImportanceImportant, ConfidenceLikely, true},
		{"/data/mystery.bin", "Unknown", ImportanceReview, ConfidenceUnknown, false},
		{"/etc/hosts", "Operating system files", ImportanceCritical, ConfidenceLikely, false},
	}
	for _, c := range cases {
		got := Classify(c.path, &Node{Dir: c.dir, MTime: now.Add(-48 * time.Hour).Unix()}, known, now)
		if got.Purpose != c.purpose || got.Importance != c.importance || got.Confidence != c.confidence {
			t.Fatalf("%s: %+v", c.path, got)
		}
	}
	fresh := Classify("/tmp/upload.part", &Node{MTime: now.Unix()}, nil, now)
	if fresh.Importance != ImportanceReview {
		t.Fatalf("recently changed temporary files need review: %+v", fresh)
	}
}

func TestSecretFoldersAreNotListed(t *testing.T) {
	for _, p := range []string{"/var/lib/ndl/secrets", "/var/lib/ndl/secrets/x", "/root/.ssh", "/etc/ssh/ssh_host_ed25519_key"} {
		if !Secret(p) {
			t.Fatalf("%s must be secret", p)
		}
	}
	for _, p := range []string{"/root", "/var/lib/ndl", "/etc", "/var/log"} {
		if Secret(p) {
			t.Fatalf("%s only contains secrets and may be listed", p)
		}
	}
}
