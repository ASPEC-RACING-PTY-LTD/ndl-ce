package lxc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeMountsRefusesSystemPaths(t *testing.T) {
	ok := []Mount{{Source: "/srv/media/", Target: "/mnt/media"}}
	got, err := NormalizeMounts(ok)
	if err != nil || got[0].Source != "/srv/media" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range [][]Mount{
		{{Source: "/", Target: "/mnt/x"}},
		{{Source: "/etc", Target: "/mnt/x"}},
		{{Source: "/var/lib/ndl/secrets/x", Target: "/mnt/x"}},
		{{Source: "relative", Target: "/mnt/x"}},
		{{Source: "/srv/a/../../etc", Target: "/mnt/x"}},
		{{Source: "/srv/a b", Target: "/mnt/x"}},
		{{Source: "/srv/a", Target: "/"}},
		{{Source: "/srv/a", Target: "/proc/x"}},
		{{Source: "/srv/a", Target: "/mnt/x"}, {Source: "/srv/b", Target: "/mnt/x"}},
	} {
		if _, err := NormalizeMounts(bad); err == nil {
			t.Fatalf("mount list must be refused: %+v", bad)
		}
	}
}

func TestSetMountsWritesBindEntriesAndKeepsData(t *testing.T) {
	e := testEngine(t)
	id := uuid.NewString()
	if _, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "media", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: uuid.NewString(), RootfsPath: filepath.Join(e.DataDir, "rootfs", id), BridgeName: "br0",
	}); err != nil {
		t.Fatal(err)
	}
	host := t.TempDir()
	existing := filepath.Join(host, "existing")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "movie.mkv"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(host, "pool", "data", id, "media")

	res, err := e.SetMounts(id, []Mount{
		{Source: created, Target: "/mnt/media", Create: true, PoolID: "p1", Label: "Media"},
		{Source: existing, Target: "/mnt/library", ReadOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Mounts) != 2 || res.Mounts[0].Create || res.MappedRootUID != 100000 {
		t.Fatalf("%+v", res)
	}
	if st, err := os.Stat(created); err != nil || !st.IsDir() {
		t.Fatalf("pool folder must be created: %v", err)
	}
	cfg, _ := os.ReadFile(e.configPath(id))
	for _, want := range []string{
		"lxc.mount.entry = " + created + " mnt/media none bind,create=dir 0 0",
		"lxc.mount.entry = " + existing + " mnt/library none bind,create=dir,ro 0 0",
	} {
		if !strings.Contains(string(cfg), want) {
			t.Fatalf("config missing %q:\n%s", want, cfg)
		}
	}

	got, err := e.Mounts(id)
	if err != nil || len(got.Mounts) != 2 || got.Mounts[0].Label != "Media" {
		t.Fatalf("%+v %v", got, err)
	}

	// Removing a mount drops the config line and leaves the folder and files.
	if _, err := e.SetMounts(id, []Mount{{Source: created, Target: "/mnt/media"}}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = os.ReadFile(e.configPath(id))
	if strings.Contains(string(cfg), "mnt/library") {
		t.Fatalf("removed mount still configured:\n%s", cfg)
	}
	if b, err := os.ReadFile(filepath.Join(existing, "movie.mkv")); err != nil || string(b) != "data" {
		t.Fatalf("unmounting must never touch data: %q %v", b, err)
	}

	// A folder that does not exist is refused unless No-dal is asked to create it.
	if _, err := e.SetMounts(id, []Mount{{Source: filepath.Join(host, "missing"), Target: "/mnt/x"}}); err == nil {
		t.Fatal("missing host folder must be refused")
	}

	// Saving the container again from the control plane keeps its mounts.
	applied, _ := e.readApplied(id)
	spec := applied.Spec
	spec.Mounts = nil
	spec.NoStart = true
	if _, err := e.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Mounts(id); len(got.Mounts) != 1 {
		t.Fatalf("mounts must survive a spec save: %+v", got)
	}
}

// TUN is kept in the spec, so every config written later still has it.
func TestSetTUNWritesTheDeviceAndSurvivesRewrites(t *testing.T) {
	e := testEngine(t)
	id := uuid.NewString()
	if _, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "velvet", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: uuid.NewString(), RootfsPath: filepath.Join(e.DataDir, "rootfs", id), BridgeName: "br0",
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"lxc.mount.entry = /dev/net/tun dev/net/tun none bind,optional,create=file 0 0",
		"lxc.cgroup2.devices.allow = c 10:200 rwm",
	}
	has := func() (int, string) {
		cfg, _ := os.ReadFile(e.configPath(id))
		n := 0
		for _, w := range want {
			if strings.Contains(string(cfg), w) {
				n++
			}
		}
		return n, string(cfg)
	}
	if n, _ := has(); n != 0 {
		t.Fatal("TUN must be off by default")
	}
	res, err := e.SetTUN(context.Background(), id, true)
	if err != nil || !res.TUN {
		t.Fatalf("%+v %v", res, err)
	}
	if n, cfg := has(); n != 2 {
		t.Fatalf("config missing TUN lines:\n%s", cfg)
	}
	// Another config rewrite (a mount change) keeps it.
	if _, err := e.SetMounts(id, nil); err != nil {
		t.Fatal(err)
	}
	if n, cfg := has(); n != 2 {
		t.Fatalf("TUN lost on rewrite:\n%s", cfg)
	}
	if got, _ := e.Mounts(id); !got.TUN {
		t.Fatal("mounts must report TUN")
	}
	if _, err := e.SetTUN(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	if n, cfg := has(); n != 0 {
		t.Fatalf("TUN must be removed:\n%s", cfg)
	}
}
