package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMergeOpMessageKeepsIDsAndError(t *testing.T) {
	prev := `{"workload_id":"w1","volume_id":"v1"}`
	got := mergeOpMessage(prev, "failed_precondition: tar chmod", "failed")
	if !strings.Contains(got, `"workload_id":"w1"`) || !strings.Contains(got, "tar chmod") {
		t.Fatalf("%s", got)
	}
	if !looksLikeCreateIDs(got) {
		t.Fatal("retry planner must still see create ids")
	}
}

func TestFinishOpPersistsAfterCanceledContext(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	op := s.startOp(ctx, cluster.ID, "", "pool.create", "validating", 10)
	cancel()
	s.finishOp(ctx, op, "succeeded", "directory pool created", 100)
	ops, _ := mem.ListOperations(context.Background(), cluster.ID, 10)
	if len(ops) != 1 || ops[0].State != "succeeded" || ops[0].Message != "directory pool created" {
		t.Fatalf("%+v", ops)
	}
	if ops[0].Progress == nil || *ops[0].Progress != 100 || ops[0].Stage != "done" {
		t.Fatalf("%+v", ops[0])
	}
	if ops[0].UpdatedAt.Before(ops[0].CreatedAt) {
		t.Fatal("updated_at must move forward")
	}
}

func TestFinishOpFailedWorkloadKeepsError(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	op := s.startOpKeyed(context.Background(), cluster.ID, "", "workload.create", "creating", "k1", mustCreateMsg(createIDs{WorkloadID: "w1", VolumeID: "v1"}), 20)
	s.finishOp(context.Background(), op, "failed", "tar: Cannot change mode", 0)
	got, _ := mem.GetOperationByIdempotency(context.Background(), cluster.ID, "k1")
	if got == nil || got.State != "failed" || !strings.Contains(got.Message, "Cannot change mode") {
		t.Fatalf("%+v", got)
	}
	if time.Since(got.UpdatedAt) > time.Minute {
		t.Fatalf("timestamp %v", got.UpdatedAt)
	}
}
