package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

func TestPickRestoreNetworkSkipsProductionLAN(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(t.Context())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	lanID := uuid.NewString()
	isoID := uuid.NewString()
	if err := mem.CreateNetwork(context.Background(), appdb.Network{
		ID: lanID, ClusterID: cluster.ID, NodeID: node.ID, Name: "lan",
		Kind: "lan-bridge", Status: ndnet.StatusAvailable, BridgeName: "ndl685ff937",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateNetwork(context.Background(), appdb.Network{
		ID: isoID, ClusterID: cluster.ID, NodeID: node.ID, Name: "cert-iso",
		Kind: ndnet.KindIsolatedNAT, Status: ndnet.StatusAvailable, BridgeName: "ndlcertiso",
	}); err != nil {
		t.Fatal(err)
	}
	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: node.ID, Name: "gone", Kind: "system-container"}
	if err := mem.CreateWorkload(context.Background(), wl); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateWorkloadNIC(context.Background(), appdb.WorkloadNIC{
		ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wl.ID, NetworkID: lanID,
	}); err != nil {
		t.Fatal(err)
	}
	netID, bridge, err := s.pickRestoreNetwork(context.Background(), cluster.ID, &wl)
	if err != nil || netID != isoID || bridge != "ndlcertiso" {
		t.Fatalf("restore must use isolated-nat not lan: %s %s %v", netID, bridge, err)
	}
}

func TestBootableCTArtifactPrefersFull(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(t.Context())
	wlID := uuid.NewString()
	smart := appdb.BackupArtifact{
		ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wlID,
		CaptureMode: appdb.BackupCaptureSmart, Format: "tar.zst", CreatedAt: time.Unix(100, 0).UTC(),
	}
	full := appdb.BackupArtifact{
		ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wlID,
		CaptureMode: appdb.BackupCaptureFull, Format: "tar.zst", CreatedAt: time.Unix(50, 0).UTC(),
	}
	if err := mem.CreateBackupArtifact(context.Background(), smart); err != nil {
		t.Fatal(err)
	}
	if err := mem.CreateBackupArtifact(context.Background(), full); err != nil {
		t.Fatal(err)
	}
	got := s.bootableCTArtifact(context.Background(), cluster.ID, smart)
	if got.ID != full.ID {
		t.Fatalf("restore-as-new must prefer the full artifact, got %s", got.ID)
	}
}
