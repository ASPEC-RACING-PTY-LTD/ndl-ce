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
	// Kinds a backup cannot copy are never attempted.
	oci := uuid.NewString()
	_ = mem.CreateWorkload(ctx, appdb.Workload{ID: oci, ClusterID: cluster.ID, Name: "savelocker", Kind: "oci"})
	row, _ = mem.GetBackupPolicy(ctx, cluster.ID, id)
	if covered, _ := s.policyWorkloadIDs(ctx, cluster.ID, *row); slices.Contains(covered, oci) {
		t.Fatal("an OCI container must not be part of All workloads")
	}
	// A new workload waits for approval, then is covered without editing
	// the policy.
	newID := uuid.NewString()
	_ = mem.CreateWorkload(ctx, appdb.Workload{ID: newID, ClusterID: cluster.ID, Name: "new", Kind: "system-container"})
	row, _ = mem.GetBackupPolicy(ctx, cluster.ID, id)
	if covered, _ := s.policyWorkloadIDs(ctx, cluster.ID, *row); slices.Contains(covered, newID) {
		t.Fatal("a new workload waits for approval")
	}
	code, list := jsonCall(t, ts, cookie, "GET", "/api/v1/notifications", "")
	asked := false
	for _, it := range list["items"].([]any) {
		n := it.(map[string]any)
		if n["kind"] == noteBackupApproval && n["subject_id"] == newID && n["approval"] == appdb.BackupApprovalPending {
			asked = true
		}
	}
	if code != http.StatusOK || !asked {
		t.Fatalf("the bell asks about the new workload: %d %v", code, list)
	}
	if code, out := jsonCall(t, ts, cookie, "POST", "/api/v1/backups/approvals/"+newID, `{"decision":"approve"}`); code != http.StatusOK || out["state"] != appdb.BackupApprovalApproved {
		t.Fatalf("approve %d %v", code, out)
	}
	if code, _ := jsonCall(t, ts, cookie, "POST", "/api/v1/backups/approvals/"+newID, `{"decision":"deny"}`); code != http.StatusConflict {
		t.Fatalf("a decided workload cannot be decided again: %d", code)
	}
	if covered, _ := s.policyWorkloadIDs(ctx, cluster.ID, *row); !slices.Contains(covered, newID) {
		t.Fatal("an approved workload is covered")
	}
	// Denying one leaves it out of the policy, visibly.
	deniedID := uuid.NewString()
	_ = mem.CreateWorkload(ctx, appdb.Workload{ID: deniedID, ClusterID: cluster.ID, Name: "denied", Kind: "system-container"})
	s.syncBackupApprovals(ctx, cluster.ID)
	if code, out := jsonCall(t, ts, cookie, "POST", "/api/v1/backups/approvals/"+deniedID, `{"decision":"deny"}`); code != http.StatusOK || out["state"] != appdb.BackupApprovalDenied {
		t.Fatalf("deny %d %v", code, out)
	}
	row, _ = mem.GetBackupPolicy(ctx, cluster.ID, id)
	if !slices.Contains(row.ExcludedWorkloadIDs, deniedID) {
		t.Fatalf("a denied workload joins the Leave out list: %v", row.ExcludedWorkloadIDs)
	}
	if covered, _ := s.policyWorkloadIDs(ctx, cluster.ID, *row); slices.Contains(covered, deniedID) {
		t.Fatal("a denied workload is not covered")
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
