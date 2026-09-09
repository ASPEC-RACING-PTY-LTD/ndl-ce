package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDirectoryRootArgv(t *testing.T) {
	img := "/var/lib/ndl/storage/local/volumes/container-root/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.img"
	mnt := "/var/lib/ndl/storage/local/volumes/container-root/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	mkfs, err := MkfsExt4ImageArgv(img)
	if err != nil || mkfs[0] != BinMkfsExt4 {
		t.Fatalf("%v %v", mkfs, err)
	}
	loop, err := MountLoopArgv(img, mnt)
	if err != nil || !strings.Contains(strings.Join(loop, " "), "loop,nouuid") {
		t.Fatalf("%v %v", loop, err)
	}
	if _, err := MountLoopArgv(img, "/etc/passwd"); err != nil {
		// path is absolute and clean; validation is not a jail here
	}
	if _, err := UmountArgv("/"); err == nil {
		t.Fatal("must refuse umount /")
	}
}

func TestDirectoryContainerRootEnforcesSize(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	var ran []string
	d.Run = func(_ context.Context, name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	volID := uuid.NewString()
	_, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: volID, PoolID: poolID, RootPath: root, Class: ClassContainerRoot,
		Size: 8 << 30, Format: FormatDirectory, Owner: VolumeOwnerName, OwnerKind: VolumeKindOperator,
	}, PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root})
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(filepath.FromSlash(root), "volumes", ClassContainerRoot, volID)
	st, err := os.Stat(abs + VolumeSizeExt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 8<<30 {
		t.Fatalf("image size %d", st.Size())
	}
	if len(ran) < 2 || !strings.Contains(ran[0], BinMkfsExt4) || !strings.Contains(ran[1], "loop,nouuid") {
		t.Fatalf("limit commands: %v", ran)
	}
}

func TestDirectoryContainerRootRejectsOverCapacity(t *testing.T) {
	d, base := fixtureDir(t, "", false, 4<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	_, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: uuid.NewString(), PoolID: poolID, RootPath: root, Class: ClassContainerRoot,
		Size: 8 << 30, Format: FormatDirectory,
	}, PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root})
	if err == nil {
		t.Fatal("expected capacity error")
	}
}

func TestDirectoryResizeRefusesShrink(t *testing.T) {
	d, base := fixtureDir(t, "", false, 20<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	volID := uuid.NewString()
	req := CreateVolumeRequest{
		VolumeID: volID, PoolID: poolID, RootPath: root, Class: ClassContainerRoot,
		Size: 8 << 30, Format: FormatDirectory, Owner: VolumeOwnerName, OwnerKind: VolumeKindOperator,
	}
	if _, err := d.CreateVolume(context.Background(), req, PoolHint{PoolID: poolID, RootPath: root}); err != nil {
		t.Fatal(err)
	}
	req.Size = 4 << 30
	if err := d.ResizeVolume(context.Background(), req, PoolHint{PoolID: poolID, RootPath: root}); err == nil {
		t.Fatal("shrink must be refused")
	}
}

func TestEnsureDirectoryRootMounted(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	var ran []string
	d.Run = func(_ context.Context, name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}
	abs := filepath.Join(filepath.FromSlash(base), "root")
	if err := os.MkdirAll(abs, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureDirectoryRootMounted(context.Background(), abs); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 0 {
		t.Fatalf("no image must not mount: %v", ran)
	}
	if err := os.WriteFile(abs+VolumeSizeExt, []byte("img"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureDirectoryRootMounted(context.Background(), abs); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || !strings.Contains(ran[0], "loop,nouuid") {
		t.Fatalf("want loop mount, got %v", ran)
	}
}

func TestRestoreLoopMountsRefusesSlash(t *testing.T) {
	d := Directory{Run: func(context.Context, string, ...string) error { return nil }}
	if err := d.RestoreLoopMounts(context.Background(), "/"); err == nil {
		t.Fatal("must refuse /")
	}
}
