package migration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndpointIsLocalHost(t *testing.T) {
	t.Parallel()
	facts := LocalHostFacts{Hostnames: []string{"pve", "192.168.1.10"}, HasPVE: false}
	if !EndpointIsLocalHost("https://127.0.0.1:8006", facts) || !EndpointIsLocalHost("https://localhost:8006", facts) {
		t.Fatal("loopback")
	}
	if !EndpointIsLocalHost("https://pve:8006", facts) || !EndpointIsLocalHost("https://192.168.1.10:8006", facts) {
		t.Fatal("hostname or ip")
	}
	if EndpointIsLocalHost("https://other.example:8006", facts) {
		t.Fatal("remote")
	}
	if !EndpointIsLocalHost("https://other.example:8006", LocalHostFacts{HasPVE: true}) {
		t.Fatal("pve node")
	}
}

func isolateLXCHost(t *testing.T, root string) {
	t.Helper()
	prev := lxcHostRoot
	lxcHostRoot = root
	t.Cleanup(func() { lxcHostRoot = prev })
}

func TestResolveLXCRootfsHostPath(t *testing.T) {
	dir := t.TempDir()
	isolateLXCHost(t, filepath.Join(dir, "no-lxc"))
	zfs := filepath.Join(dir, "rpool", "data", "subvol-104-disk-0")
	plantPopulatedRootfs(t, zfs)
	images := filepath.Join(dir, "vz", "images", "104", "subvol-104-disk-0")
	plantPopulatedRootfs(t, images)
	exists := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	got, kind, ok := ResolveLXCRootfsHostPath("local-zfs:subvol-104-disk-0", []map[string]any{
		{"storage": "local-zfs", "type": "zfspool", "pool": filepath.Join(dir, "rpool", "data")},
	}, exists)
	if !ok || kind != "zfspool" || got != zfs {
		t.Fatalf("zfs %v %s %s", ok, kind, got)
	}
	got, kind, ok = ResolveLXCRootfsHostPath("local:subvol-104-disk-0", []map[string]any{
		{"storage": "local", "type": "dir", "path": filepath.Join(dir, "vz")},
	}, exists)
	if !ok || kind != "dir" || got != images {
		t.Fatalf("dir %v %s %s", ok, kind, got)
	}
	_, _, ok = ResolveLXCRootfsHostPath("local-lvm:subvol-104-disk-0", []map[string]any{
		{"storage": "local-lvm", "type": "lvmthin", "vgname": "pve"},
	}, exists)
	if ok {
		t.Fatal("missing lvm must not resolve")
	}
}

func TestResolveLXCRootfsPrefersPopulatedLXCMount(t *testing.T) {
	dir := t.TempDir()
	emptyZFS := filepath.Join(dir, "rpool", "data", "subvol-121-disk-0")
	if err := os.MkdirAll(emptyZFS, 0o750); err != nil {
		t.Fatal(err)
	}
	plantSkeletonRootfs(t, emptyZFS)
	lxcRoot := filepath.Join(dir, "lxc")
	populated := filepath.Join(lxcRoot, "121", "rootfs")
	plantPopulatedRootfs(t, populated)
	isolateLXCHost(t, lxcRoot)
	exists := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	got, kind, ok := ResolveLXCRootfsHostPath("local-zfs:subvol-121-disk-0", []map[string]any{
		{"storage": "local-zfs", "type": "zfspool", "pool": filepath.Join(dir, "rpool", "data")},
	}, exists)
	if !ok || kind != "zfspool" || got != populated {
		t.Fatalf("must prefer populated LXC mount, got %v %s %s", ok, kind, got)
	}
}

func TestResolveLXCRootfsIgnoresEmptyZFSDataset(t *testing.T) {
	dir := t.TempDir()
	isolateLXCHost(t, filepath.Join(dir, "no-lxc"))
	emptyZFS := filepath.Join(dir, "rpool", "data", "subvol-104-disk-0")
	if err := os.MkdirAll(emptyZFS, 0o750); err != nil {
		t.Fatal(err)
	}
	exists := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	got, _, ok := ResolveLXCRootfsHostPath("local-zfs:subvol-104-disk-0", []map[string]any{
		{"storage": "local-zfs", "type": "zfspool", "pool": filepath.Join(dir, "rpool", "data")},
	}, exists)
	if ok {
		t.Fatalf("empty ZFS dataset must not resolve: %s", got)
	}
}

func TestCopyLocalRootfsTree(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	plantPopulatedRootfs(t, src)
	dest := filepath.Join(t.TempDir(), "rootfs")
	if err := CopyLocalRootfs(src, dest); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dest, "etc", "hosts"))
	if err != nil || !strings.Contains(string(body), "localhost") {
		t.Fatalf("copied %v %s", err, body)
	}
	if err := VerifyCopiedRootfs(dest); err != nil {
		t.Fatal(err)
	}
}

func TestCopyLocalRootfsKeepsDotDotFilenames(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	plantPopulatedRootfs(t, src)
	if err := os.WriteFile(filepath.Join(src, "etc", "issue..old"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "rootfs")
	if err := CopyLocalRootfs(src, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "etc", "issue..old")); err != nil {
		t.Fatal(err)
	}
}

func TestEnrichLXCPrefersLocalHostWhenStopped(t *testing.T) {
	dir := t.TempDir()
	isolateLXCHost(t, filepath.Join(dir, "no-lxc"))
	root := filepath.Join(dir, "rpool", "data", "subvol-104-disk-0")
	plantPopulatedRootfs(t, root)
	old := localHostFacts
	localHostFacts = func() LocalHostFacts {
		return LocalHostFacts{
			Hostnames: []string{"pve"},
			HasPVE:    true,
			Exists: func(p string) bool {
				_, err := os.Stat(p)
				return err == nil
			},
		}
	}
	defer func() { localHostFacts = old }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/lxc/104/config"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"hostname": "SoundDock", "cores": 4.0, "memory": 8192.0,
				"rootfs": "local-zfs:subvol-104-disk-0,size=120G",
				"net0":   "name=eth0,bridge=vmbr0",
			}})
		case strings.Contains(r.URL.Path, "/snapshot"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		}
	}))
	defer ts.Close()
	c := &PVEClient{Base: ts.URL, Token: "t", Insecure: true, Client: ts.Client()}
	w := DiscoveredWorkload{SourceID: "pve/104", Name: "SoundDock", Kind: KindContainer, Running: false}
	EnrichPVEWorkload(c, &w, []map[string]any{
		{"storage": "local-zfs", "type": "zfspool", "content": "rootdir,images", "pool": filepath.Join(dir, "rpool", "data")},
		{"storage": "local", "type": "dir", "content": "backup,iso"},
	}, nil)
	if !w.LocalHost || w.TempBackup || w.LocalRootfsPath != root {
		t.Fatalf("local %+v", w)
	}
	hasLocal := false
	for _, cap := range w.Caps {
		if cap == ModeLocal {
			hasLocal = true
		}
	}
	if !hasLocal {
		t.Fatalf("caps %v", w.Caps)
	}
	mode, f := SuggestModeForStrategy(w, StrategyConsistent)
	if mode != ModeLocal || f != nil {
		t.Fatalf("suggest %s %+v", mode, f)
	}
	plan, err := BuildPlan("j1", AdapterProxmox, []DiscoveredWorkload{w}, []string{w.SourceID}, nil, map[string]Manifest{
		w.SourceID: {Kind: KindContainer, Container: &ContainerSection{Rootfs: &Artifact{Path: "local-zfs:subvol-104-disk-0", Format: "dir"}}},
	}, Mapping{}, nil, nil, false, nil)
	if err != nil || plan.Items[0].Mode != ModeLocal || !plan.Items[0].LocalHost || plan.Items[0].Compatibility == CompatBlocked {
		t.Fatalf("plan %v %+v", err, plan)
	}
	found := false
	for _, finding := range plan.Items[0].Findings {
		if strings.Contains(finding.Message, "Local Host") {
			found = true
		}
	}
	if !found {
		t.Fatalf("finding %+v", plan.Items[0].Findings)
	}
}
