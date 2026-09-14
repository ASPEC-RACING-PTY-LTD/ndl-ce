package appdb

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestListNodeGroupsOrdersByNameID(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	lowID := "00000000-0000-4000-8000-000000000001"
	highID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	if err := m.CreateNodeGroup(context.Background(), NodeGroup{
		ID: highID, ClusterID: clusterID, Name: "beta",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNodeGroup(context.Background(), NodeGroup{
		ID: uuid.NewString(), ClusterID: clusterID, Name: "zeta",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateNodeGroup(context.Background(), NodeGroup{
		ID: lowID, ClusterID: clusterID, Name: "alpha",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListNodeGroups(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "alpha" || got[1].Name != "beta" || got[2].Name != "zeta" {
		t.Fatalf("GET /node-groups must be name then id: %+v", got)
	}
}

func TestListWGPeersOrdersByCreatedAtID(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	stamp := time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC)
	later := stamp.Add(time.Second)
	lowID := "00000000-0000-4000-8000-000000000001"
	highID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	if err := m.CreateWGPeer(context.Background(), WGPeer{
		ID: highID, ClusterID: clusterID, Name: "late-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateWGPeer(context.Background(), WGPeer{
		ID: uuid.NewString(), ClusterID: clusterID, Name: "later", CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateWGPeer(context.Background(), WGPeer{
		ID: lowID, ClusterID: clusterID, Name: "early-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListWGPeers(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != lowID || got[1].ID != highID || !got[2].CreatedAt.Equal(later) {
		t.Fatalf("GET /cluster/wg must be created_at then id: %+v", got)
	}
}

func TestListRemainingCatalogOrdersByCreatedAtID(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	stamp := time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC)
	later := stamp.Add(time.Second)
	lowID := "00000000-0000-4000-8000-000000000001"
	highID := "ffffffff-ffff-4fff-8fff-ffffffffffff"

	if err := m.CreateRemoteNode(context.Background(), RemoteNode{
		ID: highID, ClusterID: clusterID, Name: "late-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateRemoteNode(context.Background(), RemoteNode{
		ID: uuid.NewString(), ClusterID: clusterID, Name: "later", CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateRemoteNode(context.Background(), RemoteNode{
		ID: lowID, ClusterID: clusterID, Name: "early-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	nodes, err := m.ListRemoteNodes(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 || nodes[0].ID != lowID || nodes[1].ID != highID || !nodes[2].CreatedAt.Equal(later) {
		t.Fatalf("GET remote nodes must be created_at then id: %+v", nodes)
	}

	if err := m.CreateDistributedOSD(context.Background(), DistributedOSD{
		ID: highID, ClusterID: clusterID, Disk: "late", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateDistributedOSD(context.Background(), DistributedOSD{
		ID: uuid.NewString(), ClusterID: clusterID, Disk: "later", CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateDistributedOSD(context.Background(), DistributedOSD{
		ID: lowID, ClusterID: clusterID, Disk: "early", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	osds, err := m.ListDistributedOSDs(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(osds) != 3 || osds[0].ID != lowID || osds[1].ID != highID || !osds[2].CreatedAt.Equal(later) {
		t.Fatalf("GET OSDs must be created_at then id: %+v", osds)
	}

	if err := m.CreateMigrationSource(context.Background(), MigrationSource{
		ID: highID, ClusterID: clusterID, Label: "late-id", Adapter: "pve", CreatedAt: stamp,
	}, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateMigrationSource(context.Background(), MigrationSource{
		ID: uuid.NewString(), ClusterID: clusterID, Label: "later", Adapter: "pve", CreatedAt: later,
	}, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateMigrationSource(context.Background(), MigrationSource{
		ID: lowID, ClusterID: clusterID, Label: "early-id", Adapter: "pve", CreatedAt: stamp,
	}, "", "", nil); err != nil {
		t.Fatal(err)
	}
	srcs, err := m.ListMigrationSources(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 3 || srcs[0].ID != lowID || srcs[1].ID != highID || !srcs[2].CreatedAt.Equal(later) {
		t.Fatalf("GET migration sources must be created_at then id: %+v", srcs)
	}

	if err := m.CreateServicePrincipal(context.Background(), ServicePrincipal{
		ID: highID, ClusterID: clusterID, UserID: uuid.NewString(), Name: "late-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateServicePrincipal(context.Background(), ServicePrincipal{
		ID: uuid.NewString(), ClusterID: clusterID, UserID: uuid.NewString(), Name: "later", CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateServicePrincipal(context.Background(), ServicePrincipal{
		ID: lowID, ClusterID: clusterID, UserID: uuid.NewString(), Name: "early-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	sps, err := m.ListServicePrincipals(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sps) != 3 || sps[0].ID != lowID || sps[1].ID != highID || !sps[2].CreatedAt.Equal(later) {
		t.Fatalf("GET service principals must be created_at then id: %+v", sps)
	}
}
