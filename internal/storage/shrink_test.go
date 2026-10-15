package storage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// shrinkFixture creates an 8 GiB directory container root and returns a request
// for it, the image path, and the command log. used is what statfs reports.
func shrinkFixture(t *testing.T, used int64, fail func(name string, args []string) error) (Directory, CreateVolumeRequest, PoolHint, string, *[]string) {
	t.Helper()
	d, base := fixtureDir(t, "", false, 20<<30)
	ran := &[]string{}
	d.Run = func(_ context.Context, name string, args ...string) error {
		*ran = append(*ran, name+" "+strings.Join(args, " "))
		if fail != nil {
			return fail(name, args)
		}
		return nil
	}
	poolID := uuid.NewString()
	root := base + "/pool"
	if _, err := d.CreatePool(context.Background(), CreatePoolRequest{PoolID: poolID, RootPath: root, Create: true}, nil); err != nil {
		t.Fatal(err)
	}
	req := CreateVolumeRequest{
		VolumeID: uuid.NewString(), PoolID: poolID, RootPath: root, Class: ClassContainerRoot,
		Size: 8 << 30, Format: FormatDirectory, Owner: VolumeOwnerName, OwnerKind: VolumeKindOperator,
	}
	hint := PoolHint{PoolID: poolID, RootPath: root}
	if _, err := d.CreateVolume(context.Background(), req, hint); err != nil {
		t.Fatal(err)
	}
	poolStat := d.Host.StatFS
	d.Host.StatFS = func(p string) (FSStat, error) {
		if strings.Contains(p, ClassContainerRoot) {
			blocks := uint64(8 << 30 / 4096)
			return FSStat{BlockSize: 4096, Blocks: blocks, BlocksFree: blocks - uint64(used/4096), Dev: 3}, nil
		}
		return poolStat(p)
	}
	img := filepath.Join(filepath.FromSlash(root), "volumes", ClassContainerRoot, req.VolumeID) + VolumeSizeExt
	*ran = nil
	return d, req, hint, img, ran
}

func stubExt4Bytes(t *testing.T, fn func(string) (int64, error)) {
	t.Helper()
	prev := ext4Bytes
	ext4Bytes = fn
	t.Cleanup(func() { ext4Bytes = prev })
}

func imageSize(t *testing.T, img string) int64 {
	t.Helper()
	st, err := os.Stat(img)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

func indexOf(ran []string, bin string) int {
	for i, c := range ran {
		if strings.HasPrefix(c, bin+" ") {
			return i
		}
	}
	return -1
}

func TestDirectoryShrinkRunsCheckedSequence(t *testing.T) {
	d, req, hint, img, ran := shrinkFixture(t, 1<<30, nil)
	stubExt4Bytes(t, func(string) (int64, error) { return 4 << 30, nil })
	req.Size = 4 << 30
	if err := d.ResizeVolume(context.Background(), req, hint); err != nil {
		t.Fatal(err)
	}
	if got := imageSize(t, img); got != 4<<30 {
		t.Fatalf("image size %d, want %d", got, int64(4<<30))
	}
	um, fsck, rs, mnt := indexOf(*ran, BinUmount), indexOf(*ran, BinE2fsck), indexOf(*ran, BinResize2fs), len(*ran)-1
	if um < 0 || fsck < um || rs < fsck || !strings.HasPrefix((*ran)[mnt], BinMount+" ") {
		t.Fatalf("shrink must umount, fsck, resize2fs, then remount: %v", *ran)
	}
	if !strings.HasSuffix((*ran)[rs], " 4194304K") {
		t.Fatalf("resize2fs must target the new size: %s", (*ran)[rs])
	}
	if strings.Count(strings.Join(*ran, "\n"), BinE2fsck) != 2 {
		t.Fatalf("shrink must fsck before and after: %v", *ran)
	}
}

func TestDirectoryShrinkRefusedWhileRunning(t *testing.T) {
	d, req, hint, img, ran := shrinkFixture(t, 1<<30, nil)
	req.Size, req.Live = 4<<30, true
	if err := d.ResizeVolume(context.Background(), req, hint); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("live shrink must be refused: %v", err)
	}
	for _, bin := range []string{BinUmount, BinE2fsck, BinResize2fs} {
		if indexOf(*ran, bin) >= 0 {
			t.Fatalf("refused shrink must not touch the disk: %v", *ran)
		}
	}
	if imageSize(t, img) != 8<<30 {
		t.Fatal("refused shrink must not truncate the image")
	}
}

func TestDirectoryShrinkRefusesTooLittleHeadroom(t *testing.T) {
	d, req, hint, img, ran := shrinkFixture(t, 5<<30, nil)
	req.Size = 5<<30 + 512<<20
	err := d.ResizeVolume(context.Background(), req, hint)
	if err == nil || !strings.Contains(err.Error(), "cannot shrink below") {
		t.Fatalf("shrink below data plus headroom must be refused: %v", err)
	}
	if indexOf(*ran, BinUmount) >= 0 || imageSize(t, img) != 8<<30 {
		t.Fatalf("refused shrink must not unmount or truncate: %v", *ran)
	}
}

func TestDirectoryShrinkBusyDiskIsUntouched(t *testing.T) {
	d, req, hint, img, ran := shrinkFixture(t, 1<<30, func(name string, _ []string) error {
		if name == BinUmount {
			return fmt.Errorf("target is busy")
		}
		return nil
	})
	req.Size = 4 << 30
	if err := d.ResizeVolume(context.Background(), req, hint); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("busy disk must be refused: %v", err)
	}
	if indexOf(*ran, BinResize2fs) >= 0 || imageSize(t, img) != 8<<30 {
		t.Fatalf("busy disk must not be resized: %v", *ran)
	}
}

func TestDirectoryShrinkFailuresKeepImageAndRemount(t *testing.T) {
	cases := map[string]struct {
		fail  func(string, []string) error
		ext4  int64
		match string
	}{
		"fsck fails": {fail: func(n string, _ []string) error {
			if n == BinE2fsck {
				return fmt.Errorf("bad superblock")
			}
			return nil
		}, ext4: 4 << 30, match: "nothing was changed"},
		"resize2fs fails": {fail: func(n string, _ []string) error {
			if n == BinResize2fs {
				return fmt.Errorf("no space")
			}
			return nil
		}, ext4: 4 << 30, match: "keeps its old size"},
		"filesystem did not shrink": {ext4: 8 << 30, match: "keeps its old size"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d, req, hint, img, ran := shrinkFixture(t, 1<<30, tc.fail)
			stubExt4Bytes(t, func(string) (int64, error) { return tc.ext4, nil })
			req.Size = 4 << 30
			err := d.ResizeVolume(context.Background(), req, hint)
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("want error containing %q, got %v", tc.match, err)
			}
			if got := imageSize(t, img); got != 8<<30 {
				t.Fatalf("image must keep its size on failure, got %d", got)
			}
			if last := (*ran)[len(*ran)-1]; !strings.HasPrefix(last, BinMount+" ") {
				t.Fatalf("disk must be remounted after a failed shrink: %v", *ran)
			}
		})
	}
}

func TestShrinkFloor(t *testing.T) {
	for _, tc := range []struct{ used, want int64 }{
		{0, MinRootBytes},
		{1 << 30, 2 << 30},
		{20 << 30, 22 << 30},
	} {
		if got := ShrinkFloor(tc.used); got != tc.want {
			t.Fatalf("ShrinkFloor(%d) = %d, want %d", tc.used, got, tc.want)
		}
	}
}

// The superblock reader is what stops a truncate from cutting off data, so
// check it against a real filesystem shrunk by the real tools.
func TestExt4FilesystemBytesAfterRealResize2fs(t *testing.T) {
	for _, bin := range []string{BinMkfsExt4, BinE2fsck, BinResize2fs} {
		if _, err := os.Stat(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	img := filepath.Join(t.TempDir(), "root.img")
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2 << 30); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	run := func(argv ...string) {
		t.Helper()
		if err := LiveRun(context.Background(), argv[0], argv[1:]...); err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
	}
	mkfs, _ := MkfsExt4ImageArgv(img)
	run(mkfs...)
	if got, err := ext4ImageBytes(img); err != nil || got != 2<<30 {
		t.Fatalf("fresh filesystem: %d %v", got, err)
	}
	fsck, _ := E2fsckImageArgv(img)
	run(fsck...)
	rs, _ := Resize2fsToArgv(img, 1<<30)
	run(rs...)
	if got, err := ext4ImageBytes(img); err != nil || got != 1<<30 {
		t.Fatalf("shrunk filesystem: %d %v", got, err)
	}
	if out, err := exec.Command(BinE2fsck, "-f", "-n", img).CombinedOutput(); err != nil {
		t.Fatalf("filesystem must be clean after shrink: %v %s", err, out)
	}
}

func TestExt4FilesystemBytesRejectsNonExt4(t *testing.T) {
	img := filepath.Join(t.TempDir(), "blank.img")
	if err := os.WriteFile(img, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ext4ImageBytes(img); err == nil {
		t.Fatal("a blank image must not parse as ext4")
	}
}

func zfsShrinkEngine(sizes string, ran *[]string) ZFSEngine {
	return ZFSEngine{Installed: boolPtr(true), Run: func(_ context.Context, argv []string) (string, error) {
		*ran = append(*ran, strings.Join(argv, " "))
		if argv[1] == "get" {
			return sizes, nil
		}
		return "", nil
	}}
}

func TestZFSShrinkLiveWithHeadroom(t *testing.T) {
	var ran []string
	e := zfsShrinkEngine("8589934592\n4294967296\n", &ran)
	res, err := e.resizeVolume(t.Context(), ZFSOp{Name: "storage", VolumeID: uuid.NewString(), Class: ClassContainerRoot, SizeBytes: 6 << 30})
	if err != nil || res.Status != StatusAvailable {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(ran[len(ran)-1], "refquota=6442450944") {
		t.Fatalf("shrink must lower refquota: %v", ran)
	}
}

func TestZFSShrinkRefusesTooLittleHeadroom(t *testing.T) {
	var ran []string
	e := zfsShrinkEngine("8589934592\n5368709120\n", &ran)
	res, err := e.resizeVolume(t.Context(), ZFSOp{Name: "storage", VolumeID: uuid.NewString(), Class: ClassContainerRoot, SizeBytes: 5<<30 + 512<<20})
	if err != nil || res.Status != StatusFailed || !strings.Contains(res.Reason, "cannot shrink below") {
		t.Fatalf("%+v %v", res, err)
	}
	for _, c := range ran {
		if strings.Contains(c, " set ") {
			t.Fatalf("refused shrink must not set refquota: %v", ran)
		}
	}
}

func TestZFSSettingFirstLimitChecksUsage(t *testing.T) {
	var ran []string
	e := zfsShrinkEngine("0\n5368709120\n", &ran)
	res, _ := e.resizeVolume(t.Context(), ZFSOp{Name: "storage", VolumeID: uuid.NewString(), Class: ClassContainerRoot, SizeBytes: 5 << 30})
	if res.Status != StatusFailed {
		t.Fatalf("a limit below current usage must be refused: %+v", res)
	}
}

func TestZFSGrowStillWorks(t *testing.T) {
	var ran []string
	e := zfsShrinkEngine("8589934592\n7516192768\n", &ran)
	res, err := e.resizeVolume(t.Context(), ZFSOp{Name: "storage", VolumeID: uuid.NewString(), Class: ClassContainerRoot, SizeBytes: 16 << 30})
	if err != nil || res.Status != StatusAvailable || !strings.Contains(ran[len(ran)-1], "refquota=17179869184") {
		t.Fatalf("%+v %v %v", res, err, ran)
	}
}
