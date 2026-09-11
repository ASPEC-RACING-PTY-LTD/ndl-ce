package storage

import (
	"context"
	"os"
	"os/exec"
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
	joined := strings.Join(loop, " ")
	if err != nil || !strings.Contains(joined, "-o loop ") || strings.Contains(joined, "nouuid") {
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
	if len(ran) < 2 || !strings.Contains(ran[0], BinMkfsExt4) || !strings.Contains(ran[1], "-o loop ") || strings.Contains(ran[1], "nouuid") {
		t.Fatalf("limit commands: %v", ran)
	}
}

func TestDirectoryContainerRootAllowsSparseOverCapacity(t *testing.T) {
	d, base := fixtureDir(t, "", false, 4<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	_, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: uuid.NewString(), PoolID: poolID, RootPath: root, Class: ClassContainerRoot,
		Size: 8 << 30, Format: FormatDirectory, Owner: VolumeOwnerName, OwnerKind: VolumeKindOperator,
	}, PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root})
	if err != nil {
		t.Fatalf("sparse 8 GiB root on 4 GiB free must be admitted: %v", err)
	}
}

func TestDirectoryContainerRootRejectsPhysicallyExhausted(t *testing.T) {
	d, base := fixtureDir(t, "", false, 20<<20)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	d.Host.StatFS = func(string) (FSStat, error) {
		return FSStat{BlockSize: 4096, Blocks: 100000, BlocksFree: 2048, BlocksAvail: 2048, Dev: 2}, nil
	}
	_, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: uuid.NewString(), PoolID: poolID, RootPath: root, Class: ClassContainerRoot,
		Size: 8 << 30, Format: FormatDirectory, Owner: VolumeOwnerName, OwnerKind: VolumeKindOperator,
	}, PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root})
	if err == nil {
		t.Fatal("expected capacity error when physical free is below the safety floor")
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

func TestDirectoryResizeRunsE2fsck(t *testing.T) {
	d, base := fixtureDir(t, "", false, 20<<30)
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
	req := CreateVolumeRequest{
		VolumeID: volID, PoolID: poolID, RootPath: root, Class: ClassContainerRoot,
		Size: 8 << 30, Format: FormatDirectory, Owner: VolumeOwnerName, OwnerKind: VolumeKindOperator,
	}
	if _, err := d.CreateVolume(context.Background(), req, PoolHint{PoolID: poolID, RootPath: root}); err != nil {
		t.Fatal(err)
	}
	ran = nil
	req.Size = 10 << 30
	if err := d.ResizeVolume(context.Background(), req, PoolHint{PoolID: poolID, RootPath: root}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(ran, "\n")
	if !strings.Contains(joined, BinE2fsck) || !strings.Contains(joined, BinResize2fs) {
		t.Fatalf("grow must fsck then resize: %v", ran)
	}
	e2 := -1
	rs := -1
	for i, c := range ran {
		if strings.Contains(c, BinE2fsck) {
			e2 = i
		}
		if strings.Contains(c, BinResize2fs) {
			rs = i
		}
	}
	if e2 < 0 || rs < 0 || e2 > rs {
		t.Fatalf("e2fsck must run before resize2fs: %v", ran)
	}
}

func TestDirectoryResizeSameSizeStillFscks(t *testing.T) {
	d, base := fixtureDir(t, "", false, 20<<30)
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
	req := CreateVolumeRequest{
		VolumeID: volID, PoolID: poolID, RootPath: root, Class: ClassContainerRoot,
		Size: 8 << 30, Format: FormatDirectory, Owner: VolumeOwnerName, OwnerKind: VolumeKindOperator,
	}
	if _, err := d.CreateVolume(context.Background(), req, PoolHint{PoolID: poolID, RootPath: root}); err != nil {
		t.Fatal(err)
	}
	ran = nil
	if err := d.ResizeVolume(context.Background(), req, PoolHint{PoolID: poolID, RootPath: root}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(ran, "\n")
	if !strings.Contains(joined, BinE2fsck) || !strings.Contains(joined, BinResize2fs) {
		t.Fatalf("same-size grow must still complete the filesystem: %v", ran)
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
	if len(ran) != 1 || !strings.Contains(ran[0], "-o loop ") || strings.Contains(ran[0], "nouuid") {
		t.Fatalf("want loop mount, got %v", ran)
	}
}

func TestRestoreLoopMountsRefusesSlash(t *testing.T) {
	d := Directory{Run: func(context.Context, string, ...string) error { return nil }}
	if err := d.RestoreLoopMounts(context.Background(), "/"); err == nil {
		t.Fatal("must refuse /")
	}
}

func TestMountLoopExt4AcceptsLoopAndRejectsNouuid(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to loop-mount")
	}
	if _, err := os.Stat(BinMkfsExt4); err != nil {
		t.Skip("mkfs.ext4 is not installed")
	}
	dir := t.TempDir()
	img := filepath.Join(dir, "root.img")
	mnt := filepath.Join(dir, "mnt")
	if err := os.Mkdir(mnt, 0o750); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64 << 20); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(BinMkfsExt4, "-F", "-q", img).CombinedOutput(); err != nil {
		t.Fatalf("mkfs: %s %v", out, err)
	}
	if out, err := exec.Command(BinMount, "-o", "loop,nouuid", img, mnt).CombinedOutput(); err == nil {
		_ = exec.Command(BinUmount, mnt).Run()
		t.Fatal("ext4 must reject nouuid")
	} else if !strings.Contains(string(out), "nouuid") && !strings.Contains(string(out), "Unknown parameter") {
		t.Fatalf("unexpected nouuid failure: %s %v", out, err)
	}
	argv, err := MountLoopArgv(img, mnt)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("loop mount: %s %v", out, err)
	}
	if err := exec.Command(BinUmount, mnt).Run(); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryLiveGrowDoesNotUnmount(t *testing.T) {
	d, base := fixtureDir(t, "", false, 20<<30)
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
	req := CreateVolumeRequest{
		VolumeID: volID, PoolID: poolID, RootPath: root, Class: ClassContainerRoot,
		Size: 8 << 30, Format: FormatDirectory, Owner: VolumeOwnerName, OwnerKind: VolumeKindOperator,
	}
	if _, err := d.CreateVolume(context.Background(), req, PoolHint{PoolID: poolID, RootPath: root}); err != nil {
		t.Fatal(err)
	}
	ran = nil
	req.Size = 10 << 30
	req.Live = true
	if err := d.ResizeVolume(context.Background(), req, PoolHint{PoolID: poolID, RootPath: root}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(ran, "\n")
	if strings.Contains(joined, BinUmount) {
		t.Fatalf("live grow must not unmount: %v", ran)
	}
	if strings.Contains(joined, BinE2fsck) {
		t.Fatalf("live grow must not fsck: %v", ran)
	}
	if !strings.Contains(joined, BinResize2fs) {
		t.Fatalf("live grow should expand the mounted filesystem: %v", ran)
	}
}
