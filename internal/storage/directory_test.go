package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func fixtureDir(t *testing.T, mounts string, rootBacked bool, usable int64) (Directory, string) {
	t.Helper()
	if filepath.Separator != '/' {
		t.Skip("Directory driver tests require a POSIX host")
	}
	root := filepath.ToSlash(t.TempDir())
	h := liveHost()
	h.ReadMounts = func() (string, error) {
		mp := "/"
		uuid := "ROOTFS"
		if !rootBacked {
			mp = root
			uuid = "DATAFS"
		}
		return "36 1 8:1 / " + mp + " rw - ext4 /dev/sda1 rw,uuid=" + uuid + "\n" + mounts, nil
	}
	h.StatFS = func(p string) (FSStat, error) {
		dev := uint64(1)
		if !rootBacked && p != "/" {
			dev = 2
		}
		return FSStat{BlockSize: 4096, Blocks: 100000, BlocksFree: uint64(usable / 4096), BlocksAvail: uint64(usable / 4096), Dev: dev}, nil
	}
	h.LookupUUID = func(string) string { return "" }
	h.SetXattr = func(path, name, value string) error {
		return os.WriteFile(path+".xattr-"+name, []byte(value), 0o600)
	}
	h.GetXattr = func(path, name string) (string, error) {
		b, err := os.ReadFile(path + ".xattr-" + name)
		if err != nil {
			if os.IsNotExist(err) {
				return "", errMissingXattr
			}
			return "", err
		}
		return string(b), nil
	}
	h.QEMU = func(_ context.Context, argv []string) (string, string, error) {
		if len(argv) >= 3 && argv[1] == "info" {
			return `{"virtual-size":2147483648,"actual-size":196608,"format":"qcow2"}`, "", nil
		}
		if len(argv) < 6 || argv[1] != "create" {
			return "", "bad argv", errors.New("bad argv")
		}
		return "", "", os.WriteFile(argv[4], []byte("qcow2-fixture"), 0o640)
	}
	return Directory{Host: h, AllowTestPrefix: root}, root
}

var errMissingXattr = errors.New("missing xattr")

func TestCreatePoolSafeDirectory(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	root := base + "/pool"
	res, err := d.CreatePool(context.Background(), CreatePoolRequest{
		PoolID: uuid.NewString(), RootPath: root, Create: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == StatusUnavailable {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(filepath.FromSlash(root), MarkerFile)); err != nil {
		t.Fatal("marker")
	}
}

func TestCreatePoolRejectsForbidden(t *testing.T) {
	d, _ := fixtureDir(t, "", true, 10<<30)
	for _, p := range []string{"/", "/etc", "/usr", "/boot", "/tmp"} {
		if _, err := d.CreatePool(context.Background(), CreatePoolRequest{
			PoolID: uuid.NewString(), RootPath: p, Create: true,
		}, nil); err == nil {
			t.Fatalf("accepted %s", p)
		}
	}
}

func TestCreatePoolRejectsOverlap(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{
		PoolID: uuid.NewString(), RootPath: root, Create: true,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{
		PoolID: uuid.NewString(), RootPath: root + "/child", Create: true,
	}, []string{root}); err == nil {
		t.Fatal("overlap")
	}
}

func TestCreatePoolRootBackedWarning(t *testing.T) {
	d, base := fixtureDir(t, "", true, 10<<30)
	res, err := d.CreatePool(context.Background(), CreatePoolRequest{
		PoolID: uuid.NewString(), RootPath: base + "/onroot", Create: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(res.Warnings, WarnRootFilesystem) {
		t.Fatalf("missing root warning: %+v", res)
	}
	if !strings.Contains(strings.Join(res.WarningText, " "), "host root filesystem") {
		t.Fatal(res.WarningText)
	}
}

func TestRootBackedWhenBindSharesRootDev(t *testing.T) {
	d, base := fixtureDir(t, "", true, 10<<30)
	d.Host.ReadMounts = func() (string, error) {
		return "36 1 8:1 / / rw - ext4 /dev/sda1 rw,uuid=ROOTFS\n36 2 8:1 / " + base + " rw - ext4 /dev/sda1 rw,uuid=ROOTFS\n", nil
	}
	res, err := d.CreatePool(context.Background(), CreatePoolRequest{
		PoolID: uuid.NewString(), RootPath: base + "/onroot", Create: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(res.Warnings, WarnRootFilesystem) {
		t.Fatalf("bind on root FS must warn: %+v", res)
	}
}

func TestCreateVolumeRejectsLayoutSymlinkEscape(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	outside := base + "/outside"
	if err := os.MkdirAll(filepath.FromSlash(outside), 0o750); err != nil {
		t.Fatal(err)
	}
	volDir := filepath.Join(filepath.FromSlash(root), "volumes")
	if err := os.RemoveAll(volDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.FromSlash(outside), volDir); err != nil {
		t.Fatal(err)
	}
	_, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: uuid.NewString(), PoolID: poolID, RootPath: root, Class: ClassVMDisk, Size: 1 << 30,
	}, PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root})
	if err == nil || !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("want symlink escape, got %v", err)
	}
}

func TestCreatePoolSeparateMountNotRootBacked(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	res, err := d.CreatePool(context.Background(), CreatePoolRequest{
		PoolID: uuid.NewString(), RootPath: base + "/data", Create: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if contains(res.Warnings, WarnRootFilesystem) {
		t.Fatalf("false root warning: %+v", res)
	}
}

func TestSharedFilesystemWarning(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	d.Host.ReadMounts = func() (string, error) {
		return "36 1 8:1 / " + base + " rw - nfs4 192.168.1.5:/export rw,uuid=NFS1\n", nil
	}
	res, err := d.CreatePool(context.Background(), CreatePoolRequest{
		PoolID: uuid.NewString(), RootPath: base + "/share", Create: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(res.Warnings, WarnSharedFilesystem) {
		t.Fatalf("%+v", res)
	}
	if res.Capabilities.IncrementalSend {
		t.Fatal("shared dir is still Directory")
	}
}

func TestMissingPoolUnavailableKeepsHint(t *testing.T) {
	d, _ := fixtureDir(t, "", false, 10<<30)
	obs := d.Observe([]PoolHint{{
		PoolID: "11111111-1111-1111-1111-111111111111", BackendType: BackendDirectory,
		RootPath: "/no/such/ndl-pool-path",
	}})
	if len(obs.Pools) != 1 || obs.Pools[0].Status != StatusUnavailable {
		t.Fatalf("%+v", obs)
	}
	if obs.Pools[0].Capacity.UsableBytes != nil {
		t.Fatal("unavailable must not report zero capacity as usable")
	}
}

func TestMountDisappearanceBlocksWrites(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	id := uuid.NewString()
	root := base + "/mnt"
	res, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: id, RootPath: root, Create: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Backing.RootBacked {
		t.Fatal("setup should be a separate mount")
	}
	expected := res.Backing
	d.Host.ReadMounts = func() (string, error) {
		return "36 1 8:1 / / rw - ext4 /dev/sda1 rw,uuid=ROOTFS\n", nil
	}
	d.Host.StatFS = func(string) (FSStat, error) {
		return FSStat{BlockSize: 4096, Blocks: 100000, BlocksAvail: 20000, Dev: 1}, nil
	}
	hint := PoolHint{PoolID: id, BackendType: BackendDirectory, RootPath: root, Backing: expected}
	obs := d.Observe([]PoolHint{hint})
	if obs.Pools[0].Status != StatusUnavailable {
		t.Fatalf("naked mountpoint must be unavailable: %+v", obs.Pools[0])
	}
	if _, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: uuid.NewString(), PoolID: id, RootPath: root, Class: ClassVMDisk, Size: 1 << 30,
	}, hint); err == nil {
		t.Fatal("must not write into naked mountpoint")
	}
}

func TestVolumeUUIDAndBackendRef(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	volID := uuid.NewString()
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	out, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: volID, PoolID: poolID, RootPath: root, Class: ClassVMDisk, Size: 2 << 30,
	}, hint)
	if err != nil {
		t.Fatal(err)
	}
	if out.Handle.VolumeID != volID {
		t.Fatal("uuid identity")
	}
	if out.Handle.BackendRef != "volumes/vm-disk/"+volID+".qcow2" {
		t.Fatalf("backend_ref %s", out.Handle.BackendRef)
	}
	if _, err := os.Stat(filepath.Join(filepath.FromSlash(root), filepath.FromSlash(out.Handle.BackendRef))); err != nil {
		t.Fatal(err)
	}
	if out.XattrState != XattrOK {
		t.Fatalf("xattr %s", out.XattrState)
	}
	obs := d.Observe([]PoolHint{hint})
	if len(obs.Pools) != 1 || obs.Pools[0].Capacity.ProvisionedBytes == nil || *obs.Pools[0].Capacity.ProvisionedBytes < 2<<30 {
		t.Fatalf("provisioned after volume: %+v", obs.Pools[0].Capacity)
	}
	if obs.Pools[0].Capacity.AllocatedBytes == nil {
		t.Fatal("allocated must be reported")
	}
	if *obs.Pools[0].Capacity.AllocatedBytes == *obs.Pools[0].Capacity.ProvisionedBytes {
		t.Fatal("sparse qcow2 must not report allocated == provisioned")
	}
}

func TestVolumeRejectsBadSizeFormatDuplicate(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	if _, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: uuid.NewString(), PoolID: poolID, Class: ClassVMDisk, Size: 0,
	}, hint); err == nil {
		t.Fatal("zero size")
	}
	if _, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: uuid.NewString(), PoolID: poolID, Class: ClassVMDisk, Size: 1 << 30, Format: "vmdk",
	}, hint); err == nil {
		t.Fatal("format")
	}
	id := uuid.NewString()
	if _, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: id, PoolID: poolID, Class: ClassVMDisk, Size: 1 << 30,
	}, hint); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: id, PoolID: poolID, Class: ClassVMDisk, Size: 1 << 30,
	}, hint); err == nil {
		t.Fatal("duplicate")
	}
}

func TestInsufficientCapacity(t *testing.T) {
	d, base := fixtureDir(t, "", false, 4096)
	_, err := d.CreatePool(context.Background(), CreatePoolRequest{
		PoolID: uuid.NewString(), RootPath: base + "/tiny", Create: true,
	}, nil)
	if err == nil {
		t.Fatal("tiny filesystem")
	}
}

func TestXattrMismatchAndUnsupported(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	volID := uuid.NewString()
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	out, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: volID, PoolID: poolID, Class: ClassVMDisk, Size: 1 << 30,
	}, hint)
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(filepath.FromSlash(root), filepath.FromSlash(out.Handle.BackendRef))
	_ = os.WriteFile(abs+".xattr-"+XattrVolumeID, []byte("other-id"), 0o600)
	state := d.readVolumeXattr(filepath.ToSlash(abs), volID)
	if state != XattrMismatch {
		t.Fatalf("mismatch=%s", state)
	}
	d.Host.GetXattr = func(string, string) (string, error) { return "", errMissingXattr }
	if d.readVolumeXattr(abs, volID) != XattrMissing && classifyXattrErr(errMissingXattr) != XattrMissing {
		if got := d.readVolumeXattr(filepath.ToSlash(abs), volID); got != XattrInaccessible && got != XattrMissing {
			t.Fatalf("missing=%s", got)
		}
	}
	d.Host.SetXattr = func(string, string, string) error { return errors.New("ENOTSUP") }
	d.Host.GetXattr = func(string, string) (string, error) { return "", errors.New("ENOTSUP") }
	out2, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: uuid.NewString(), PoolID: poolID, Class: ClassVMDisk, Size: 1 << 30,
	}, hint)
	if err != nil {
		t.Fatal(err)
	}
	if out2.XattrState == XattrOK {
		t.Fatal("unsupported xattr must be explicit")
	}
}

func TestPartialCreateCleanup(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	d.Host.QEMU = func(_ context.Context, argv []string) (string, string, error) {
		_ = os.WriteFile(argv[4], []byte("partial"), 0o640)
		return "", "boom", errors.New("boom")
	}
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	_, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: uuid.NewString(), PoolID: poolID, Class: ClassVMDisk, Size: 1 << 30,
	}, hint)
	if err == nil {
		t.Fatal("expected failure")
	}
	ents, _ := os.ReadDir(filepath.Join(filepath.FromSlash(root), "volumes", "vm-disk"))
	if len(ents) != 0 {
		t.Fatalf("leaked %v", ents)
	}
}

func TestObserveDoesNotDropRows(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	hint := PoolHint{PoolID: poolID, BackendType: BackendDirectory, RootPath: root}
	if _, err := d.CreateVolume(context.Background(), CreateVolumeRequest{
		VolumeID: uuid.NewString(), PoolID: poolID, Class: ClassVMDisk, Size: 1 << 30,
	}, hint); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(filepath.FromSlash(root))
	obs := d.Observe([]PoolHint{hint})
	if obs.Pools[0].Status != StatusUnavailable {
		t.Fatal(obs.Pools[0].Status)
	}
}

func TestMarkerRoundTrip(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	id := uuid.NewString()
	res, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: id, RootPath: base + "/p", Create: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.FromSlash(res.RootPath), MarkerFile))
	if err != nil {
		t.Fatal(err)
	}
	var m PoolMarker
	if err := json.Unmarshal(raw, &m); err != nil || m.PoolID != id {
		t.Fatalf("%s %v", raw, err)
	}
}

func TestUnwritablePool(t *testing.T) {
	d, base := fixtureDir(t, "", false, 10<<30)
	d.Host.WriteFile = func(name string, data []byte, perm os.FileMode) error {
		if strings.Contains(name, ".ndl-write-probe") || strings.Contains(name, MarkerFile) && !strings.Contains(name, "xattr") {
			if strings.Contains(name, ".ndl-write-probe") {
				return errors.New("ro")
			}
		}
		return os.WriteFile(name, data, perm)
	}
	_, err := d.CreatePool(context.Background(), CreatePoolRequest{
		PoolID: uuid.NewString(), RootPath: base + "/ro", Create: true,
	}, nil)
	if err == nil {
		t.Fatal("unwritable")
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
