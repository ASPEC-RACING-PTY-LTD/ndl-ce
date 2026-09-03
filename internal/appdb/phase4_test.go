package appdb

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

func TestReconcileNetworksMarksUnavailableWithoutDelete(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	nodeID := uuid.NewString()
	netID := uuid.NewString()
	_ = m.CreateCluster(context.Background(), Cluster{ID: clusterID, Name: "c"})
	_ = m.CreateNetwork(context.Background(), Network{
		ID: netID, ClusterID: clusterID, NodeID: nodeID, Name: "iso",
		Kind: ndnet.KindIsolated, Status: ndnet.StatusAvailable, BridgeName: "ndldeadbeef",
	})
	unavail, recovered, err := ReconcileNetworks(context.Background(), m, clusterID, mustListNets(t, m, clusterID), ndnet.Observation{})
	if err != nil {
		t.Fatal(err)
	}
	if len(unavail) != 1 || len(recovered) != 0 {
		t.Fatalf("unavail=%v recovered=%v", unavail, recovered)
	}
	got, _ := m.GetNetwork(context.Background(), clusterID, netID)
	if got == nil || got.Status != ndnet.StatusUnavailable {
		t.Fatalf("%+v", got)
	}
}

func mustListNets(t *testing.T, m *Memory, clusterID string) []Network {
	t.Helper()
	items, err := m.ListNetworks(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestListReservationsOrdersByCreatedAtID(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	netID := uuid.NewString()
	stamp := time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC)
	later := stamp.Add(time.Second)
	lowID := "00000000-0000-4000-8000-000000000001"
	highID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	if err := m.CreateReservation(context.Background(), DHCPReservation{
		ID: highID, ClusterID: clusterID, NetworkID: netID, MAC: "aa:aa:aa:aa:aa:01", IPv4: "10.64.0.10", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateReservation(context.Background(), DHCPReservation{
		ID: uuid.NewString(), ClusterID: clusterID, NetworkID: netID, MAC: "aa:aa:aa:aa:aa:02", IPv4: "10.64.0.11", CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateReservation(context.Background(), DHCPReservation{
		ID: lowID, ClusterID: clusterID, NetworkID: netID, MAC: "aa:aa:aa:aa:aa:03", IPv4: "10.64.0.12", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListReservations(context.Background(), clusterID, netID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len %d", len(got))
	}
	if got[0].ID != lowID || got[1].ID != highID || !got[2].CreatedAt.Equal(later) {
		t.Fatalf("GET reservations must be created_at then id: %+v", got)
	}
}
