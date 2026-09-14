package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestDirectoryThinOvercommitThirtySparseRoots(t *testing.T) {
	const (
		physicalTotal = int64(1) << 40
		physicalUsed  = int64(200) << 30
		volSize       = int64(50) << 30
		count         = 30
	)
	d, base := fixtureDir(t, "", false, 10<<30)
	usable := physicalTotal - physicalUsed
	d.Host.StatFS = func(p string) (FSStat, error) {
		dev := uint64(2)
		if p == "/" {
			dev = 1
		}
		return FSStat{
			BlockSize:   4096,
			Blocks:      uint64(physicalTotal / 4096),
			BlocksFree:  uint64(usable / 4096),
			BlocksAvail: uint64(usable / 4096),
			Dev:         dev,
		}, nil
	}
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	for i := 0; i < count; i++ {
		_, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
			VolumeID: uuid.NewString(), PoolID: poolID, RootPath: root, Class: ClassContainerRoot,
			Size: volSize, Format: FormatDirectory, Owner: VolumeOwnerName, OwnerKind: VolumeKindOperator,
		}, hint)
		if err != nil {
			t.Fatalf("sparse volume %d: %v", i, err)
		}
	}
	obs := d.Observe([]PoolHint{hint})
	if len(obs.Pools) != 1 {
		t.Fatalf("pools %d", len(obs.Pools))
	}
	p := obs.Pools[0]
	if p.Status == StatusUnavailable {
		t.Fatalf("overcommit must not make the pool unavailable: %+v", p)
	}
	wantProv := volSize * int64(count)
	if p.Capacity.ProvisionedBytes == nil || *p.Capacity.ProvisionedBytes != wantProv {
		t.Fatalf("provisioned %v want %d", p.Capacity.ProvisionedBytes, wantProv)
	}
	if p.Capacity.AllocatedBytes == nil {
		t.Fatal("allocated must be reported")
	}
	if *p.Capacity.AllocatedBytes >= wantProv/10 {
		t.Fatalf("allocated %d must stay far below 1.5 TiB provisioned", *p.Capacity.AllocatedBytes)
	}
	used := PhysicalUsedBytes(p.Capacity)
	if used == nil || *used != physicalUsed {
		t.Fatalf("pool used %v want %d (StatFS, not logical provisioned)", used, physicalUsed)
	}
	if p.Capacity.UsableBytes == nil || *p.Capacity.UsableBytes != usable {
		t.Fatalf("usable %v want %d", p.Capacity.UsableBytes, usable)
	}
	if !containsWarning(p.Warnings, WarnThinOvercommit) {
		t.Fatalf("want thin_overcommit warning, got %v", p.Warnings)
	}
	if len(obs.Volumes) != count {
		t.Fatalf("volumes %d", len(obs.Volumes))
	}
	for _, v := range obs.Volumes {
		if v.Provisioned != volSize {
			t.Fatalf("volume provisioned %d", v.Provisioned)
		}
		if v.Allocated >= volSize/10 {
			t.Fatalf("volume allocated %d must be sparse", v.Allocated)
		}
		img := filepath.Join(filepath.FromSlash(root), "volumes", ClassContainerRoot, v.VolumeID+VolumeSizeExt)
		st, err := os.Stat(img)
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() != volSize {
			t.Fatalf("image logical size %d", st.Size())
		}
	}
}

func TestDirectoryGrowChangesLogicalSizeNotAllocated(t *testing.T) {
	d, base := fixtureDir(t, "", false, 4<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	volID := uuid.NewString()
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	req := CreateVolumeRequest{
		VolumeID: volID, PoolID: poolID, RootPath: root, Class: ClassContainerRoot,
		Size: 8 << 30, Format: FormatDirectory, Owner: VolumeOwnerName, OwnerKind: VolumeKindOperator,
	}
	if _, err := d.CreateVolume(context.Background(), req, hint); err != nil {
		t.Fatal(err)
	}
	before := d.Observe([]PoolHint{hint})
	var allocBefore int64
	if before.Pools[0].Capacity.AllocatedBytes != nil {
		allocBefore = *before.Pools[0].Capacity.AllocatedBytes
	}
	req.Size = 20 << 30
	if err := d.ResizeVolume(context.Background(), req, hint); err != nil {
		t.Fatal(err)
	}
	after := d.Observe([]PoolHint{hint})
	if after.Pools[0].Capacity.ProvisionedBytes == nil || *after.Pools[0].Capacity.ProvisionedBytes != 20<<30 {
		t.Fatalf("provisioned after grow: %+v", after.Pools[0].Capacity)
	}
	if after.Pools[0].Capacity.AllocatedBytes == nil {
		t.Fatal("allocated after grow")
	}
	if *after.Pools[0].Capacity.AllocatedBytes > allocBefore+(1<<20) {
		t.Fatalf("grow must not pretend new logical bytes were consumed: before %d after %d", allocBefore, *after.Pools[0].Capacity.AllocatedBytes)
	}
}

func TestAdmitPhysicalFree(t *testing.T) {
	ok := int64(1 << 30)
	if err := AdmitPhysicalFree(&ok); err != nil {
		t.Fatal(err)
	}
	low := int64(MinPoolFreeBytes - 1)
	if err := AdmitPhysicalFree(&low); err == nil {
		t.Fatal("want ErrCapacity")
	}
	if err := AdmitPhysicalFree(nil); err != nil {
		t.Fatal(err)
	}
}

func TestApplyCapacityWarningsDoNotUnavailable(t *testing.T) {
	total := int64(1 << 40)
	usable := int64(800 << 30)
	obs := ObservedPool{
		Status:   StatusAvailable,
		Capacity: Capacity{TotalBytes: &total, UsableBytes: &usable},
	}
	applyCapacityWarnings(&obs, 3<<40)
	if obs.Status != StatusWarning {
		t.Fatalf("status %s", obs.Status)
	}
	if !containsWarning(obs.Warnings, WarnThinOvercommit) {
		t.Fatal(obs.Warnings)
	}
}
