package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestClearActivityLogKeepsRunningTasksAndRecordsItself(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	old := time.Now().UTC().Add(-90 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-time.Hour)
	for _, at := range []time.Time{old, recent} {
		_ = mem.InsertAudit(context.Background(), appdb.AuditEvent{ID: uuid.NewString(), ClusterID: cluster.ID, Action: "x.y", Result: "ok", CreatedAt: at})
	}
	ops := []appdb.Operation{
		{ID: uuid.NewString(), ClusterID: cluster.ID, Kind: "pool.create", State: "succeeded", CreatedAt: old, UpdatedAt: old},
		{ID: uuid.NewString(), ClusterID: cluster.ID, Kind: "workload.create", State: appdb.OpStateRunning, CreatedAt: old, UpdatedAt: old},
		{ID: uuid.NewString(), ClusterID: cluster.ID, Kind: "inventory.refresh", State: "succeeded", CreatedAt: recent, UpdatedAt: recent},
	}
	for _, op := range ops {
		if err := mem.UpsertOperation(context.Background(), op); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"before":"` + time.Now().UTC().Add(-30*24*time.Hour).Format(time.RFC3339) + `","audit":true,"tasks":true}`
	if code, _ := provCall(t, ts, cookie, "POST", "/audit/clear", body, ""); code != http.StatusConflict {
		t.Fatalf("clearing needs confirmation, got %d", code)
	}
	code, out := provCall(t, ts, cookie, "POST", "/audit/clear", body, clearLogConfirm)
	if code != http.StatusOK || out["audit_deleted"] != float64(1) || out["tasks_deleted"] != float64(1) {
		t.Fatalf("clear %d %v", code, out)
	}
	left, _ := mem.ListOperations(context.Background(), cluster.ID, 100)
	running := false
	for _, op := range left {
		if op.State == appdb.OpStateRunning {
			running = true
		}
	}
	if !running {
		t.Fatal("running tasks must never be cleared")
	}
	cleared := false
	for _, e := range mem.Audits() {
		if e.Action == "audit.clear" {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("the clear must be recorded in the audit log")
	}
	code, out = provCall(t, ts, cookie, "POST", "/audit/clear", `{"all":true,"tasks":true}`, clearLogConfirm)
	if code != http.StatusOK || out["tasks_deleted"] != float64(1) {
		t.Fatalf("clear all tasks %d %v", code, out)
	}
}
