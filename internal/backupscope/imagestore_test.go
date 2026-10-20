package backupscope

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/no-dal/ndl-ce/internal/backup"
)

func TestSmartAlwaysSkipsImageStores(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"var/lib/containerd/io.containerd.content.v1.content/blobs/sha256/a":                        "layer",
		"var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/1/fs/bin/x":            "layer",
		"var/lib/docker/overlay2/abc/diff/x":                                                        "layer",
		"var/lib/docker/volumes/db/_data/pg":                                                        "data",
		"srv/docker/overlay2/abc/diff/x":                                                            "layer",
		"srv/docker/containers/c1/config.json":                                                      "c",
		"srv/docker/volumes/app/_data/keep":                                                         "data",
		"home/u/.local/share/docker/overlay2/abc/x":                                                 "layer",
		"home/u/.local/share/docker/containers/c/x":                                                 "c",
		"home/u/.local/share/docker/volumes/v/_data/keep":                                           "data",
		"var/lib/containers/storage/storage.lock":                                                   "",
		"var/lib/containers/storage/overlay/abc/diff/x":                                             "layer",
		"var/lib/containers/storage/volumes/pv/_data/keep":                                          "data",
		"var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/1/x": "layer",
		"opt/app/docker/image/readme.txt":                                                           "not an image store",
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Even a selection that picks every item cannot bring images back.
	prev, err := Discover(Options{Root: root, Mode: ModeSmart})
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, it := range prev.Items {
		all = append(all, it.ID)
	}
	prev, err = Discover(Options{Root: root, Mode: ModeSmart, Selection: Selection{Selected: all}})
	if err != nil {
		t.Fatal(err)
	}
	incl, excl := PlanCapture(prev)
	for rel, body := range files {
		got := backup.Captured(root, rel, incl, excl)
		want := body != "layer"
		if body == "" || body == "c" {
			continue
		}
		if got != want {
			t.Errorf("%s captured=%v, want %v (incl %v, excl %v)", rel, got, want, incl, excl)
		}
	}
}
