package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func TestDeleteRefusesResourcesInUse(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	admin := claimAdmin(t, ts, token)
	ctx := context.Background()
	c, _ := mem.GetCluster(ctx)
	cid := c.ID
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(ctx, appdb.Node{ID: nodeID, ClusterID: cid, Name: "n"})

	used := uuid.NewString()
	empty := uuid.NewString()
	for _, id := range []string{used, empty} {
		if err := mem.CreateStoragePool(ctx, appdb.StoragePool{ID: id, ClusterID: cid, NodeID: nodeID, Name: id[:6], BackendType: "directory", Status: "available"}); err != nil {
			t.Fatal(err)
		}
	}
	volID := uuid.NewString()
	if err := mem.CreateVolume(ctx, appdb.Volume{ID: volID, ClusterID: cid, NodeID: nodeID, PoolID: used, Class: "vm-disk", Status: "available"}); err != nil {
		t.Fatal(err)
	}

	expect := func(method, path string, want int, contains string) {
		t.Helper()
		res := doCookie(t, ts, admin, method, path, "")
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != want || !strings.Contains(string(body), contains) {
			t.Fatalf("%s %s = %d %s, want %d containing %q", method, path, res.StatusCode, body, want, contains)
		}
	}

	expect("DELETE", "/api/v1/storage/pools/"+used, http.StatusConflict, "1 volume is still on this pool")
	expect("DELETE", "/api/v1/storage/pools/"+empty, http.StatusNoContent, "")
	expect("DELETE", "/api/v1/storage/pools/"+empty, http.StatusNotFound, "")

	targetID := uuid.NewString()
	if err := mem.CreateBackupTarget(ctx, appdb.BackupTarget{ID: targetID, ClusterID: cid, Name: "nas", Kind: appdb.BackupLocal, Locator: "/var/lib/ndl/backups/nas"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateBackupPolicy(ctx, appdb.BackupPolicy{ID: uuid.NewString(), ClusterID: cid, Name: "nightly", TargetID: targetID}); err != nil {
		t.Fatal(err)
	}
	expect("DELETE", "/api/v1/backups/targets/"+targetID, http.StatusConflict, "nightly")

	groupID := uuid.NewString()
	if err := mem.CreateGroup(ctx, appdb.Group{ID: groupID, ClusterID: cid, Name: "ops"}); err != nil {
		t.Fatal(err)
	}
	expect("DELETE", "/api/v1/groups/"+groupID, http.StatusNoContent, "")
}

func TestDeleteRequiresPermission(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)
	res := doCookie(t, ts, view, "DELETE", "/api/v1/storage/pools/"+uuid.NewString(), "")
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer delete pool = %d", res.StatusCode)
	}
}
