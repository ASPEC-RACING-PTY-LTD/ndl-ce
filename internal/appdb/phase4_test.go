package appdb

import (
	"context"
	"strings"
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
	unavail, recovered, degraded, err := ReconcileNetworks(context.Background(), m, clusterID, mustListNets(t, m, clusterID), ndnet.Observation{})
	if err != nil {
		t.Fatal(err)
	}
	if len(unavail) != 1 || len(recovered) != 0 || len(degraded) != 0 {
		t.Fatalf("unavail=%v recovered=%v degraded=%v", unavail, recovered, degraded)
	}
	got, _ := m.GetNetwork(context.Background(), clusterID, netID)
	if got == nil || got.Status != ndnet.StatusUnavailable {
		t.Fatalf("%+v", got)
	}
}

func TestReconcileNetworksMarksDegradedFromAvailable(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	nodeID := uuid.NewString()
	netID := uuid.NewString()
	_ = m.CreateCluster(context.Background(), Cluster{ID: clusterID, Name: "c"})
	_ = m.CreateNetwork(context.Background(), Network{
		ID: netID, ClusterID: clusterID, NodeID: nodeID, Name: "iso",
		Kind: ndnet.KindIsolated, Status: ndnet.StatusAvailable, BridgeName: "ndl67639c6a",
	})
	_, _, degraded, err := ReconcileNetworks(context.Background(), m, clusterID, mustListNets(t, m, clusterID), ndnet.Observation{
		Networks: []ndnet.ObservedNetwork{{
			NetworkID: netID, Status: ndnet.StatusWarning, Warnings: []string{"isolated bridge is present but not configured"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(degraded) != 1 || degraded[0] != netID {
		t.Fatalf("degraded=%v", degraded)
	}
	got, _ := m.GetNetwork(context.Background(), clusterID, netID)
	if got == nil || got.Status != ndnet.StatusWarning {
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

func TestListNetworksOrdersByCreatedAtID(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	stamp := time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC)
	later := stamp.Add(time.Second)
	lowID := "00000000-0000-4000-8000-000000000001"
	highID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	if err := m.CreateNetwork(context.Background(), Network{
		ID: highID, ClusterID: clusterID, Name: "late-id", Kind: ndnet.KindIsolated, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNetwork(context.Background(), Network{
		ID: uuid.NewString(), ClusterID: clusterID, Name: "later", Kind: ndnet.KindIsolated, CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNetwork(context.Background(), Network{
		ID: lowID, ClusterID: clusterID, Name: "early-id", Kind: ndnet.KindIsolated, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListNetworks(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != lowID || got[1].ID != highID || !got[2].CreatedAt.Equal(later) {
		t.Fatalf("GET /networks items must be created_at then id: %+v", got)
	}
}

func TestDeleteNetworkRemovesIsolatedAndKeepsOthers(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	keep := uuid.NewString()
	drop := uuid.NewString()
	if err := m.CreateNetwork(context.Background(), Network{
		ID: drop, ClusterID: clusterID, Name: "drop", Kind: ndnet.KindIsolatedNAT, IPv4CIDR: "10.77.21.0/24",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNetwork(context.Background(), Network{
		ID: keep, ClusterID: clusterID, Name: "keep", Kind: ndnet.KindIsolatedNAT, IPv4CIDR: "10.77.22.0/24",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateAddress(context.Background(), Address{
		ID: uuid.NewString(), ClusterID: clusterID, NetworkID: drop, Family: "ipv4", CIDR: "10.77.21.1/24", Role: "gateway",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateReservation(context.Background(), DHCPReservation{
		ID: uuid.NewString(), ClusterID: clusterID, NetworkID: drop, MAC: "aa:aa:aa:aa:aa:01", IPv4: "10.77.21.10",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteNetwork(context.Background(), clusterID, drop); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.GetNetwork(context.Background(), clusterID, drop); got != nil {
		t.Fatalf("deleted network still present: %+v", got)
	}
	if got, _ := m.GetNetwork(context.Background(), clusterID, keep); got == nil {
		t.Fatal("unrelated network was deleted")
	}
	addrs, err := m.ListAddresses(context.Background(), clusterID, drop)
	if err != nil || len(addrs) != 0 {
		t.Fatalf("addresses leftover: %v %v", addrs, err)
	}
}

func TestDeleteNetworkRefusesAttachedNIC(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	netID := uuid.NewString()
	if err := m.CreateNetwork(context.Background(), Network{
		ID: netID, ClusterID: clusterID, Name: "busy", Kind: ndnet.KindIsolated,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateWorkloadNIC(context.Background(), WorkloadNIC{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: uuid.NewString(), NetworkID: netID, MAC: "aa:aa:aa:aa:aa:02",
	}); err != nil {
		t.Fatal(err)
	}
	err := m.DeleteNetwork(context.Background(), clusterID, netID)
	if err == nil || !strings.Contains(err.Error(), "still attached") {
		t.Fatalf("attached NIC: %v", err)
	}
}
