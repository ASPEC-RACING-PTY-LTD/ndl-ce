package appdb

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestListStorageCatalogOrdersByCreatedAtID(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	stamp := time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC)
	later := stamp.Add(time.Second)
	lowID := "00000000-0000-4000-8000-000000000001"
	highID := "ffffffff-ffff-4fff-8fff-ffffffffffff"

	if err := m.CreateStoragePool(context.Background(), StoragePool{
		ID: highID, ClusterID: clusterID, Name: "late-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateStoragePool(context.Background(), StoragePool{
		ID: uuid.NewString(), ClusterID: clusterID, Name: "later", CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateStoragePool(context.Background(), StoragePool{
		ID: lowID, ClusterID: clusterID, Name: "early-id", CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	pools, err := m.ListStoragePools(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 3 || pools[0].ID != lowID || pools[1].ID != highID || !pools[2].CreatedAt.Equal(later) {
		t.Fatalf("GET /storage/pools must be created_at then id: %+v", pools)
	}

	poolID := lowID
	if err := m.CreateVolume(context.Background(), Volume{
		ID: highID, ClusterID: clusterID, PoolID: poolID, Class: storage.ClassVMDisk, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateVolume(context.Background(), Volume{
		ID: uuid.NewString(), ClusterID: clusterID, PoolID: poolID, Class: storage.ClassVMDisk, CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateVolume(context.Background(), Volume{
		ID: "00000000-0000-4000-8000-00000000000a", ClusterID: clusterID, PoolID: poolID, Class: storage.ClassVMDisk, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	vols, err := m.ListVolumes(context.Background(), clusterID, poolID)
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 3 || vols[0].ID != "00000000-0000-4000-8000-00000000000a" || vols[1].ID != highID || !vols[2].CreatedAt.Equal(later) {
		t.Fatalf("GET /storage/volumes must be created_at then id: %+v", vols)
	}

	if err := m.CreateLibraryItem(context.Background(), LibraryItem{
		ID: highID, ClusterID: clusterID, PoolID: poolID, Kind: storage.LibraryISO, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateLibraryItem(context.Background(), LibraryItem{
		ID: uuid.NewString(), ClusterID: clusterID, PoolID: poolID, Kind: storage.LibraryISO, CreatedAt: later,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateLibraryItem(context.Background(), LibraryItem{
		ID: "00000000-0000-4000-8000-00000000000b", ClusterID: clusterID, PoolID: poolID, Kind: storage.LibraryISO, CreatedAt: stamp,
	}); err != nil {
		t.Fatal(err)
	}
	imgs, err := m.ListLibraryItems(context.Background(), clusterID, poolID)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 3 || imgs[0].ID != "00000000-0000-4000-8000-00000000000b" || imgs[1].ID != highID || !imgs[2].CreatedAt.Equal(later) {
		t.Fatalf("GET /storage/images must be created_at then id: %+v", imgs)
	}
}
