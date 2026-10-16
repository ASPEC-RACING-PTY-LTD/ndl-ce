package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestAllWorkloadsPolicyLeavesOutItsExclusions(t *testing.T) {
	s, mem, token := testServer(t)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	var ids []string
	for _, n := range []string{"rsdws", "viewdock", "codehold"} {
		id := uuid.NewString()
		_ = mem.CreateWorkload(ctx, appdb.Workload{ID: id, ClusterID: cluster.ID, Name: n, Kind: "system-container"})
		ids = append(ids, id)
	}
	tgt := uuid.NewString()
	_ = mem.CreateBackupTarget(ctx, appdb.BackupTarget{ID: tgt, ClusterID: cluster.ID, Name: "r2", Kind: "r2", Bucket: "b", CreatedAt: time.Now()}, "k", "")

	code, pol := jsonCall(t, ts, cookie, "POST", "/api/v1/backups/policies",
		`{"name":"all","scope":"all","target_id":"`+tgt+`","capture_mode":"smart","excluded_workload_ids":["`+ids[0]+`"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create %d %v", code, pol)
	}
	id := pol["id"].(string)
	row, _ := mem.GetBackupPolicy(ctx, cluster.ID, id)
	covered, _ := s.policyWorkloadIDs(ctx, cluster.ID, *row)
	if slices.Contains(covered, ids[0]) || len(covered) != 2 {
		t.Fatalf("rsdws must be left out: %v", covered)
	}

	// Pausing sends the policy without exclusions: they stay.
	code, pol = jsonCall(t, ts, cookie, "PATCH", "/api/v1/backups/policies/"+id,
		`{"name":"all","scope":"all","target_id":"`+tgt+`","capture_mode":"smart","paused":true}`)
	if code != http.StatusOK || len(pol["excluded_workload_ids"].([]any)) != 1 {
		t.Fatalf("omitted exclusions are kept: %d %v", code, pol)
	}
	// A new workload is covered without editing the policy.
	newID := uuid.NewString()
	_ = mem.CreateWorkload(ctx, appdb.Workload{ID: newID, ClusterID: cluster.ID, Name: "new", Kind: "system-container"})
	row, _ = mem.GetBackupPolicy(ctx, cluster.ID, id)
	if covered, _ := s.policyWorkloadIDs(ctx, cluster.ID, *row); !slices.Contains(covered, newID) {
		t.Fatal("new workloads are covered")
	}
	// Switching to selected workloads drops the exclusions.
	code, pol = jsonCall(t, ts, cookie, "PATCH", "/api/v1/backups/policies/"+id,
		`{"name":"all","scope":"selected","workload_ids":["`+ids[1]+`"],"target_id":"`+tgt+`","capture_mode":"smart","excluded_workload_ids":["`+ids[0]+`"]}`)
	if code != http.StatusOK || len(pol["excluded_workload_ids"].([]any)) != 0 {
		t.Fatalf("exclusions only apply to all: %d %v", code, pol)
	}
	if code, _ := jsonCall(t, ts, cookie, "POST", "/api/v1/backups/policies",
		`{"name":"x","scope":"all","target_id":"`+tgt+`","excluded_workload_ids":["`+uuid.NewString()+`"]}`); code != http.StatusNotFound {
		t.Fatalf("unknown excluded workload %d", code)
	}
}
