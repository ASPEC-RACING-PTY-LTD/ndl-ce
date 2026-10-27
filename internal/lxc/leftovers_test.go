package lxc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLeftovers(t *testing.T) {
	root := t.TempDir()
	e := &Engine{
		LXCPath: filepath.Join(root, "lxc"), WorkloadsDir: filepath.Join(root, "workloads"),
		UnitDir: filepath.Join(root, "units"), SkipHostCmds: true,
	}
	pool := filepath.Join(root, "pool", "volumes", "container-root")
	mk := func(id, vol string, enabled bool) string {
		rootfs := filepath.Join(pool, vol)
		_ = os.MkdirAll(filepath.Join(rootfs, "etc"), 0o750)
		_ = os.WriteFile(filepath.Join(rootfs, "etc", "hostname"), []byte("old\n"), 0o640)
		_ = os.MkdirAll(filepath.Join(e.LXCPath, id), 0o750)
		_ = os.WriteFile(filepath.Join(e.LXCPath, id, "config"), []byte("lxc.rootfs.path = dir:"+rootfs+"\n"), 0o640)
		_ = os.MkdirAll(filepath.Join(e.WorkloadsDir, id), 0o750)
		raw, _ := json.Marshal(Applied{Spec: Spec{WorkloadID: id, Name: "old-" + id[:4], RootfsPath: rootfs, BridgeName: "ndlgone"}})
		_ = os.WriteFile(filepath.Join(e.WorkloadsDir, id, "last-applied.json"), raw, 0o640)
		if enabled {
			_ = os.MkdirAll(filepath.Join(e.UnitDir, "multi-user.target.wants"), 0o750)
			_ = os.Symlink("/lib/systemd/system/nodal-ct@.service", filepath.Join(e.UnitDir, "multi-user.target.wants", CTUnitPrefix+id+".service"))
		}
		return rootfs
	}
	gone := "2bfff17a-0f19-4f3b-96a7-ba94fad9f0d1"
	live := "9de638b0-74d4-4202-9e9b-1cf89b9c0f2f"
	goneRoot := mk(gone, "f67be82e-c14b-4f43-97e5-cf48cdf8d9ab", true)
	liveRoot := mk(live, "e667d00f-e3e9-40f4-92b4-64bbf1fea0b0", true)
	// An enabled unit with no other state is listed too.
	unitOnly := "3193d754-6f83-4c13-806e-70a3fb0f0249"
	_ = os.Symlink("/lib/systemd/system/nodal-ct@.service", filepath.Join(e.UnitDir, "multi-user.target.wants", CTUnitPrefix+unitOnly+".service"))

	items, err := e.ListContainerState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items %+v", items)
	}
	byID := map[string]Leftover{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if g := byID[gone]; !g.UnitEnabled || !g.HasConfig || !g.HasState || g.Rootfs != goneRoot || g.Name != "old-2bff" || g.Bridge != "ndlgone" {
		t.Fatalf("gone %+v", g)
	}
	if u := byID[unitOnly]; !u.UnitEnabled || u.HasConfig || u.HasState {
		t.Fatalf("unit only %+v", u)
	}
	SizeLeftovers(context.Background(), items, 0)
	sized := []Leftover{byID[gone]}
	SizeLeftovers(context.Background(), sized, 5e9)
	if sized[0].RootfsBytes != 4 {
		t.Fatalf("size %d", sized[0].RootfsBytes)
	}

	res, err := e.RemoveLeftover(context.Background(), gone)
	if err != nil {
		t.Fatal(err)
	}
	if !res.RemovedData || res.RootfsKept {
		t.Fatalf("result %+v", res)
	}
	for _, p := range []string{goneRoot, filepath.Join(e.LXCPath, gone), filepath.Join(e.WorkloadsDir, gone)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	if _, err := os.Stat(liveRoot); err != nil {
		t.Fatal("another container's root was touched")
	}

	// A root another container shares, or one outside container storage,
	// is kept.
	shared := "3553a544-faa3-4e60-9ae9-7784e2f52fde"
	mk(shared, "e667d00f-e3e9-40f4-92b4-64bbf1fea0b0", false)
	if res, err := e.RemoveLeftover(context.Background(), shared); err != nil || !res.RootfsKept || res.RemovedData {
		t.Fatalf("shared root %+v %v", res, err)
	}
	if _, err := os.Stat(liveRoot); err != nil {
		t.Fatal("a shared root was removed")
	}
	outside := "3625cf60-16a0-406c-9976-492dfd6871cb"
	_ = os.MkdirAll(filepath.Join(e.LXCPath, outside), 0o750)
	_ = os.WriteFile(filepath.Join(e.LXCPath, outside, "config"), []byte("lxc.rootfs.path = dir:"+root+"\n"), 0o640)
	if res, err := e.RemoveLeftover(context.Background(), outside); err != nil || !res.RootfsKept {
		t.Fatalf("outside root %+v %v", res, err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("a root outside container storage was removed")
	}
	if _, err := e.RemoveLeftover(context.Background(), "not-a-uuid"); err == nil {
		t.Fatal("a bad id was accepted")
	}
}
