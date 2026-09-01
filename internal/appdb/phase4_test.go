package appdb

import (
	"context"
	"testing"

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
