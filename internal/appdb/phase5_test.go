package appdb

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

func TestReconcileWorkloadsMarksUnavailableWithoutDelete(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	nodeID := uuid.NewString()
	id := uuid.NewString()
	_ = m.CreateCluster(context.Background(), Cluster{ID: clusterID, Name: "c"})
	_ = m.CreateWorkload(context.Background(), Workload{
		ID: id, ClusterID: clusterID, NodeID: nodeID, OwnerNodeID: nodeID, DesiredNodeID: nodeID,
		Name: "ct", Kind: lxc.KindSystemContainer, Status: lxc.StatusRunning,
		ImagePin: "alpine/3.21/amd64/default", DesiredPower: "running",
	})
	items, _ := m.ListWorkloads(context.Background(), clusterID)
	unavail, recovered, err := ReconcileWorkloads(context.Background(), m, clusterID, items, ObservationEmpty())
	if err != nil {
		t.Fatal(err)
	}
	if len(unavail) != 1 || len(recovered) != 0 {
		t.Fatalf("unavail=%v recovered=%v", unavail, recovered)
	}
	got, _ := m.GetWorkload(context.Background(), clusterID, id)
	if got == nil || got.Status != lxc.StatusUnavailable {
		t.Fatalf("%+v", got)
	}
}

func ObservationEmpty() lxc.Observation {
	return lxc.Observation{}
}

func TestGetWorkloadByNameAndIdempotency(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	id := uuid.NewString()
	_ = m.CreateCluster(context.Background(), Cluster{ID: clusterID, Name: "c"})
	_ = m.CreateWorkload(context.Background(), Workload{
		ID: id, ClusterID: clusterID, Name: "alpine-a", Kind: lxc.KindSystemContainer,
		Status: lxc.StatusStopped, ImagePin: "alpine/3.21/amd64/default", IdempotencyKey: "create-1",
	})
	_ = m.UpsertOperation(context.Background(), Operation{
		ID: uuid.NewString(), ClusterID: clusterID, Kind: "workload.create",
		State: "succeeded", IdempotencyKey: "create-1", Message: `{"workload_id":"` + id + `","volume_id":"` + uuid.NewString() + `"}`,
	})
	byName, err := m.GetWorkloadByName(context.Background(), clusterID, "alpine-a")
	if err != nil || byName == nil || byName.ID != id {
		t.Fatalf("by name %+v %v", byName, err)
	}
	byKey, err := m.GetWorkloadByIdempotency(context.Background(), clusterID, "create-1")
	if err != nil || byKey == nil || byKey.ID != id {
		t.Fatalf("by key %+v %v", byKey, err)
	}
}
