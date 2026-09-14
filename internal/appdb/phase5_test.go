package appdb

import (
	"context"
	"testing"
	"time"

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

func TestListWorkloadDisksOrdersBySlotCreatedAtID(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	workloadID := uuid.NewString()
	stamp := time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC)
	later := stamp.Add(time.Second)
	lowID := "00000000-0000-4000-8000-000000000001"
	highID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	if err := m.CreateWorkloadDisk(context.Background(), WorkloadDisk{
		ID: highID, ClusterID: clusterID, WorkloadID: workloadID, VolumeID: uuid.NewString(),
		Role: "data", Slot: 0, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateWorkloadDisk(context.Background(), WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: workloadID, VolumeID: uuid.NewString(),
		Role: "data", Slot: 1, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateWorkloadDisk(context.Background(), WorkloadDisk{
		ID: lowID, ClusterID: clusterID, WorkloadID: workloadID, VolumeID: uuid.NewString(),
		Role: "boot", Slot: 0, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateWorkloadDisk(context.Background(), WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: workloadID, VolumeID: uuid.NewString(),
		Role: "data", Slot: 1, CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListWorkloadDisks(context.Background(), clusterID, workloadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("len %d", len(got))
	}
	if got[0].ID != lowID || got[0].Slot != 0 {
		t.Fatalf("slot 0 must sort by id when created_at ties: %+v", got)
	}
	if got[1].ID != highID || got[1].Slot != 0 {
		t.Fatalf("same slot later id: %+v", got)
	}
	if got[2].Slot != 1 || got[2].CreatedAt.Equal(later) {
		t.Fatalf("slot 1 earlier created_at: %+v", got[2])
	}
	if got[3].Slot != 1 || !got[3].CreatedAt.Equal(later) {
		t.Fatalf("slot 1 later created_at: %+v", got[3])
	}
}

func TestListWorkloadsOrdersByCreatedAtID(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	stamp := time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC)
	later := stamp.Add(time.Second)
	lowID := "00000000-0000-4000-8000-000000000001"
	highID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	if err := m.CreateWorkload(context.Background(), Workload{
		ID: highID, ClusterID: clusterID, Name: "late-id", Kind: lxc.KindSystemContainer, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateWorkload(context.Background(), Workload{
		ID: uuid.NewString(), ClusterID: clusterID, Name: "later", Kind: lxc.KindSystemContainer, CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateWorkload(context.Background(), Workload{
		ID: lowID, ClusterID: clusterID, Name: "early-id", Kind: lxc.KindSystemContainer, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListWorkloads(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != lowID || got[1].ID != highID || !got[2].CreatedAt.Equal(later) {
		t.Fatalf("GET /workloads must be created_at then id: %+v", got)
	}
}
