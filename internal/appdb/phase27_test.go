package appdb

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestListNetworkCatalogOrdersByCreatedAtID(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	stamp := time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC)
	later := stamp.Add(time.Second)
	lowID := "00000000-0000-4000-8000-000000000001"
	highID := "ffffffff-ffff-4fff-8fff-ffffffffffff"

	if err := m.CreateNetworkVLAN(context.Background(), NetworkVLAN{
		ID: highID, ClusterID: clusterID, Name: "late-id", VID: 10, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNetworkVLAN(context.Background(), NetworkVLAN{
		ID: uuid.NewString(), ClusterID: clusterID, Name: "later", VID: 20, CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNetworkVLAN(context.Background(), NetworkVLAN{
		ID: lowID, ClusterID: clusterID, Name: "early-id", VID: 30, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	vlans, err := m.ListNetworkVLANs(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(vlans) != 3 || vlans[0].ID != lowID || vlans[1].ID != highID || !vlans[2].CreatedAt.Equal(later) {
		t.Fatalf("GET /networks vlans must be created_at then id: %+v", vlans)
	}

	if err := m.CreateNetworkBond(context.Background(), NetworkBond{
		ID: highID, ClusterID: clusterID, Name: "late-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNetworkBond(context.Background(), NetworkBond{
		ID: uuid.NewString(), ClusterID: clusterID, Name: "later", CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNetworkBond(context.Background(), NetworkBond{
		ID: lowID, ClusterID: clusterID, Name: "early-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	bonds, err := m.ListNetworkBonds(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bonds) != 3 || bonds[0].ID != lowID || bonds[1].ID != highID || !bonds[2].CreatedAt.Equal(later) {
		t.Fatalf("GET /networks bonds must be created_at then id: %+v", bonds)
	}

	if err := m.CreateNetworkPolicy(context.Background(), NetworkPolicy{
		ID: highID, ClusterID: clusterID, Name: "late-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNetworkPolicy(context.Background(), NetworkPolicy{
		ID: uuid.NewString(), ClusterID: clusterID, Name: "later", CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNetworkPolicy(context.Background(), NetworkPolicy{
		ID: lowID, ClusterID: clusterID, Name: "early-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	policies, err := m.ListNetworkPolicies(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 3 || policies[0].ID != lowID || policies[1].ID != highID || !policies[2].CreatedAt.Equal(later) {
		t.Fatalf("GET /networks policies must be created_at then id: %+v", policies)
	}

	if err := m.CreateNetworkOverlay(context.Background(), NetworkOverlay{
		ID: highID, ClusterID: clusterID, Name: "late-id", VNI: 10, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNetworkOverlay(context.Background(), NetworkOverlay{
		ID: uuid.NewString(), ClusterID: clusterID, Name: "later", VNI: 20, CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNetworkOverlay(context.Background(), NetworkOverlay{
		ID: lowID, ClusterID: clusterID, Name: "early-id", VNI: 30, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	overlays, err := m.ListNetworkOverlays(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(overlays) != 3 || overlays[0].ID != lowID || overlays[1].ID != highID || !overlays[2].CreatedAt.Equal(later) {
		t.Fatalf("GET /networks overlays must be created_at then id: %+v", overlays)
	}
}
