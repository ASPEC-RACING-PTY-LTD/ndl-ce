package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storscan"
)

// localScan runs the real scanner in-process, standing in for the agent.
type localScan struct{ m *storscan.Manager }

func (l localScan) StorageScan(_ context.Context, action string, spec agentrpc.StorageScanSpec) (agentrpc.StorageScanResult, error) {
	var out agentrpc.StorageScanResult
	switch action {
	case "scan-start":
		s, err := l.m.Start(spec.Root, spec.OneFS)
		if err != nil {
			return out, err
		}
		out.Scan, _, _ = l.m.Get(s.ID)
	case "scan-status":
		st, tree, _ := l.m.Get(spec.ID)
		out.Scan = st
		if st.State != storscan.StateRunning && tree != nil {
			n, err := storscan.Find(tree, spec.Path)
			if err != nil {
				return out, err
			}
			sh := storscan.Shallow(n)
			out.Node = &sh
		}
	case "delete":
		st, _, _ := l.m.Get(spec.ID)
		out.Scan = st
		out.Deleted = storscan.Delete(st.Root, spec.Items, spec.Protect, spec.Keep, func() ([]string, error) { return []string{"/"}, nil })
	}
	return out, nil
}

func TestStorageManagerScansExplainsAndDeletesSafely(t *testing.T) {
	s, mem, token := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	root := t.TempDir()
	poolID, volID, wid := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_ = mem.CreateStoragePool(ctx, appdb.StoragePool{ID: poolID, ClusterID: cluster.ID, Name: "local", BackendType: "directory", RootPath: root})
	_ = mem.CreateVolume(ctx, appdb.Volume{ID: volID, ClusterID: cluster.ID, PoolID: poolID, Class: "container-root", BackendRef: "volumes/container-root/" + volID})
	_ = mem.CreateWorkload(ctx, appdb.Workload{ID: wid, ClusterID: cluster.ID, Name: "VaultDock", Kind: lxc.KindSystemContainer})
	_ = mem.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wid, VolumeID: volID, Role: "root"})
	img := filepath.Join(root, "volumes", "container-root", volID+".img")
	_ = os.MkdirAll(filepath.Dir(img), 0o755)
	_ = os.WriteFile(img, make([]byte, 64<<10), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "leftovers"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "leftovers", "old.tar.gz"), make([]byte, 128<<10), 0o644)
	s.Scan = localScan{m: &storscan.Manager{}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	admin := claimAdmin(t, ts, token)
	call := func(cookie, method, path, body string) (int, map[string]any, string) {
		res := authedJSON(t, ts, method, path, cookie, body, nil)
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return res.StatusCode, out, string(raw)
	}
	code, out, raw := call(admin, "GET", "/api/v1/storage/scan/targets", "")
	if code != 200 || !strings.Contains(raw, root) || out["can_delete"] != true {
		t.Fatalf("targets %d %s", code, raw)
	}
	if code, _, _ = call(admin, "POST", "/api/v1/storage/scan", `{"path":"relative/path"}`); code != 400 {
		t.Fatal("only absolute paths in the host root or a pool may be scanned")
	}
	code, out, raw = call(admin, "POST", "/api/v1/storage/scan", `{"path":"`+root+`"}`)
	if code != 202 {
		t.Fatalf("start %d %s", code, raw)
	}
	id := out["id"].(string)
	for i := 0; i < 200; i++ {
		_, out, _ = call(admin, "GET", "/api/v1/storage/scan/"+id, "")
		if out["scan"].(map[string]any)["state"] != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, _, raw = call(admin, "GET", "/api/v1/storage/scan/"+id+"?path=volumes/container-root", "")
	if !strings.Contains(raw, "The disk of system-container VaultDock") || !strings.Contains(raw, `"confidence":"known"`) {
		t.Fatalf("a workload disk must be named: %s", raw)
	}
	_, _, raw = call(admin, "GET", "/api/v1/storage/scan/"+id+"?path=leftovers", "")
	if !strings.Contains(raw, `"name":"old.tar.gz"`) || !strings.Contains(raw, `"confidence":"likely"`) {
		t.Fatalf("each item must be explained: %s", raw)
	}
	items := `{"items":["leftovers/old.tar.gz","volumes/container-root/` + volID + `.img","volumes"],"confirm":"delete 3"}`
	if code, _, _ = call(admin, "POST", "/api/v1/storage/scan/"+id+"/delete", `{"items":["leftovers"],"confirm":"yes"}`); code != 400 {
		t.Fatal("the count must be typed to confirm")
	}
	viewer := loginRole(t, ts, mem, "scanview", rbac.Viewer)
	operator := loginRole(t, ts, mem, "scanop", rbac.Operator)
	if code, _, _ = call(viewer, "GET", "/api/v1/storage/scan/"+id, ""); code != 200 {
		t.Fatal("viewers can browse a scan")
	}
	if code, _, _ = call(viewer, "POST", "/api/v1/storage/scan", `{"path":"`+root+`"}`); code != 403 {
		t.Fatal("viewers cannot start scans")
	}
	if code, _, _ = call(operator, "POST", "/api/v1/storage/scan/"+id+"/delete", items); code != 403 {
		t.Fatal("deleting host files is admin-only by default")
	}
	code, out, raw = call(admin, "POST", "/api/v1/storage/scan/"+id+"/delete", items)
	if code != 200 || out["deleted"] != float64(1) {
		t.Fatalf("delete %d %s", code, raw)
	}
	if _, err := os.Stat(filepath.Join(root, "leftovers", "old.tar.gz")); !os.IsNotExist(err) {
		t.Fatal("the archive must be gone")
	}
	if _, err := os.Stat(img); err != nil || !strings.Contains(raw, "protected") {
		t.Fatalf("a workload disk must never be deleted: %s", raw)
	}
}
