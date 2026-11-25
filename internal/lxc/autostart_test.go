package lxc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestObserveReportsBootStart(t *testing.T) {
	e := testEngine(t)
	e.UnitDir = filepath.Join(t.TempDir(), "units")
	ctx := context.Background()
	on, off := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	for _, id := range []string{on, off} {
		if _, err := e.Create(ctx, Spec{WorkloadID: id, Name: "ct-" + id[:4], ImagePin: "alpine/3.21/amd64/default", VolumeID: id, RootfsPath: filepath.Join(t.TempDir(), "rootfs"), NoStart: true}); err != nil {
			t.Fatal(err)
		}
	}
	wants := filepath.Join(e.UnitDir, "nodal-workloads.target.wants")
	if err := os.MkdirAll(wants, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/lib/systemd/system/nodal-ct@.service", filepath.Join(wants, unitName(on))); err != nil {
		t.Fatal(err)
	}
	obs, err := e.Observe(ctx, []Hint{{WorkloadID: on, Kind: KindSystemContainer}, {WorkloadID: off, Kind: KindSystemContainer}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*bool{}
	for _, o := range obs.Workloads {
		got[o.WorkloadID] = o.Autostart
	}
	if got[on] == nil || !*got[on] {
		t.Fatalf("enabled unit must report autostart=true: %v", got[on])
	}
	if got[off] == nil || *got[off] {
		t.Fatalf("unit without a link must report autostart=false: %v", got[off])
	}
}

func TestObserveSkipsBootStartWhenNotChecked(t *testing.T) {
	e := testEngine(t) // SkipHostCmds and no UnitDir: the host is not looked at
	id := "33333333-3333-4333-8333-333333333333"
	if _, err := e.Create(context.Background(), Spec{WorkloadID: id, Name: "ct", ImagePin: "alpine/3.21/amd64/default", VolumeID: id, RootfsPath: filepath.Join(t.TempDir(), "rootfs"), NoStart: true}); err != nil {
		t.Fatal(err)
	}
	obs, _ := e.Observe(context.Background(), []Hint{{WorkloadID: id, Kind: KindSystemContainer}})
	if len(obs.Workloads) != 1 || obs.Workloads[0].Autostart != nil {
		t.Fatalf("autostart must be unknown (nil): %+v", obs.Workloads)
	}
}
