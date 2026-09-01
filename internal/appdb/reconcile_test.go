package appdb

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestReconcileStorageMarksUnavailableWithoutDelete(t *testing.T) {
	m := NewMemory()
	cluster := uuid.NewString()
	node := uuid.NewString()
	poolID := uuid.NewString()
	volID := uuid.NewString()
	itemID := uuid.NewString()
	_ = m.CreateCluster(context.Background(), Cluster{ID: cluster, Name: "local"})
	_ = m.CreateStoragePool(context.Background(), StoragePool{
		ID: poolID, ClusterID: cluster, NodeID: node, Name: "local",
		BackendType: storage.BackendDirectory, Status: storage.StatusAvailable, RootPath: "/mnt/data",
	})
	_ = m.CreateVolume(context.Background(), Volume{
		ID: volID, ClusterID: cluster, NodeID: node, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		SizeBytes: 1 << 30, Status: storage.StatusAvailable, BackendType: storage.BackendDirectory,
		BackendRef: "volumes/vm-disk/" + volID + ".qcow2", CreatedAt: time.Now().UTC(),
	})
	_ = m.CreateLibraryItem(context.Background(), LibraryItem{
		ID: itemID, ClusterID: cluster, NodeID: node, PoolID: poolID, Kind: storage.LibraryISO,
		DisplayName: "a.iso", BackendRef: "library/iso/" + itemID + ".iso", SizeBytes: 12,
		ChecksumSHA256: "aa", Status: storage.StatusAvailable,
	})
	pools, _ := m.ListStoragePools(context.Background(), cluster)
	unavail, recovered, err := ReconcileStorage(context.Background(), m, cluster, pools, storage.Observation{
		Pools: []storage.ObservedPool{{
			PoolID: poolID, Status: storage.StatusUnavailable, Reason: "pool path is missing",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unavail) != 1 || len(recovered) != 0 {
		t.Fatalf("unavail=%v recovered=%v", unavail, recovered)
	}
	got, _ := m.GetStoragePool(context.Background(), cluster, poolID)
	if got == nil || got.Status != storage.StatusUnavailable {
		t.Fatal("pool must remain and be unavailable")
	}
	if got.UsableBytes != nil {
		t.Fatal("unavailable pool must not report zero usable")
	}
	vol, _ := m.GetVolume(context.Background(), cluster, volID)
	if vol == nil || vol.Status != storage.StatusUnavailable {
		t.Fatal("volume row must remain unavailable")
	}
	item, _ := m.GetLibraryItem(context.Background(), cluster, itemID)
	if item == nil || item.Status != storage.StatusUnavailable {
		t.Fatal("library row must remain unavailable")
	}
}
