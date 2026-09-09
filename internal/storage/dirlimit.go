package storage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
)

const (
	BinMkfsExt4   = "/usr/sbin/mkfs.ext4"
	BinMount      = "/usr/bin/mount"
	BinUmount     = "/usr/bin/umount"
	BinE2fsck     = "/usr/sbin/e2fsck"
	BinResize2fs  = "/usr/sbin/resize2fs"
	VolumeSizeExt = ".img"
)

// CommandRunner executes a validated argv. Tests replace it.
type CommandRunner func(ctx context.Context, name string, args ...string) error

func allowedLimitBin(name string) bool {
	switch name {
	case BinMkfsExt4, BinMount, BinUmount, BinE2fsck, BinResize2fs:
		return true
	default:
		return false
	}
}

// LiveRun is the production runner for directory rootfs images.
func LiveRun(ctx context.Context, name string, args ...string) error {
	if !allowedLimitBin(name) {
		return fmt.Errorf("refusing unlisted binary %s", name)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if name == BinE2fsck {
			if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() <= 1 {
				return nil
			}
		}
		if len(out) == 0 {
			return err
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d Directory) runLimit(ctx context.Context, name string, args ...string) error {
	if !allowedLimitBin(name) {
		return fmt.Errorf("refusing unlisted binary %s", name)
	}
	if d.Run != nil {
		return d.Run(ctx, name, args...)
	}
	return fmt.Errorf("directory filesystem limit runner is unavailable")
}

func directoryRootImage(abs string) string {
	return abs + VolumeSizeExt
}

func MkfsExt4ImageArgv(img string) ([]string, error) {
	img = path.Clean(img)
	if img == "" || img != path.Clean(img) || strings.Contains(img, "..") || !strings.HasPrefix(img, "/") {
		return nil, fmt.Errorf("rootfs image path is invalid")
	}
	return []string{BinMkfsExt4, "-F", "-q", img}, nil
}

func MountLoopArgv(img, mount string) ([]string, error) {
	img = path.Clean(img)
	mount = path.Clean(mount)
	if img == "" || mount == "" || strings.Contains(img, "..") || strings.Contains(mount, "..") {
		return nil, fmt.Errorf("loop mount locators are invalid")
	}
	if !strings.HasPrefix(img, "/") || !strings.HasPrefix(mount, "/") {
		return nil, fmt.Errorf("loop mount locators must be absolute")
	}
	// loop is a mount(8) userspace option. Do not pass filesystem-specific
	// flags such as nouuid (XFS). Debian 13 util-linux feeds remaining -o
	// values to fsconfig(), and ext4 rejects unknown parameters.
	return []string{BinMount, "-o", "loop", img, mount}, nil
}

func UmountArgv(mount string) ([]string, error) {
	mount = path.Clean(mount)
	if mount == "" || mount == "/" || strings.Contains(mount, "..") || !strings.HasPrefix(mount, "/") {
		return nil, fmt.Errorf("umount path is invalid")
	}
	return []string{BinUmount, mount}, nil
}

func Resize2fsArgv(img string) ([]string, error) {
	img = path.Clean(img)
	if img == "" || strings.Contains(img, "..") || !strings.HasPrefix(img, "/") {
		return nil, fmt.Errorf("resize2fs path is invalid")
	}
	return []string{BinResize2fs, img}, nil
}

func E2fsckImageArgv(img string) ([]string, error) {
	img = path.Clean(img)
	if img == "" || strings.Contains(img, "..") || !strings.HasPrefix(img, "/") {
		return nil, fmt.Errorf("e2fsck path is invalid")
	}
	return []string{BinE2fsck, "-f", "-p", img}, nil
}

func (d Directory) enforceContainerRootSize(ctx context.Context, abs string, size int64) error {
	if size < MinRootBytes {
		return ErrInvalidSize
	}
	img := directoryRootImage(abs)
	f, err := os.OpenFile(img, os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		_ = os.Remove(img)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(img)
		return err
	}
	if d.Run == nil {
		return nil
	}
	mkfs, err := MkfsExt4ImageArgv(img)
	if err != nil {
		_ = os.Remove(img)
		return err
	}
	if err := d.runLimit(ctx, mkfs[0], mkfs[1:]...); err != nil {
		_ = os.Remove(img)
		return err
	}
	mnt, err := MountLoopArgv(img, abs)
	if err != nil {
		_ = os.Remove(img)
		return err
	}
	if err := d.runLimit(ctx, mnt[0], mnt[1:]...); err != nil {
		_ = os.Remove(img)
		return err
	}
	return nil
}

func (d Directory) unmountContainerRoot(ctx context.Context, abs string) {
	if d.Run == nil {
		return
	}
	if argv, err := UmountArgv(abs); err == nil {
		_ = d.runLimit(ctx, argv[0], argv[1:]...)
	}
}

// ResizeVolume grows a directory-backed container root. Shrink is refused.
func (d Directory) ResizeVolume(ctx context.Context, req CreateVolumeRequest, hint PoolHint) error {
	if req.Class != ClassContainerRoot {
		return fmt.Errorf("directory resize is only implemented for container roots")
	}
	if req.Size < MinRootBytes || req.Size > MaxVolumeBytes {
		return ErrInvalidSize
	}
	if hint.RootPath == "" {
		hint.RootPath = req.RootPath
	}
	rel := strings.TrimSpace(req.BackendRef)
	if rel == "" {
		rel = volumeRel(req.Class, req.VolumeID, FormatDirectory)
	}
	abs, err := JoinUnder(hint.RootPath, rel)
	if err != nil {
		return err
	}
	if err := d.refuseEscape(hint.RootPath, abs); err != nil {
		return err
	}
	img := directoryRootImage(abs)
	st, err := os.Stat(img)
	if err != nil {
		return fmt.Errorf("container root image is missing")
	}
	if req.Size < st.Size() {
		return fmt.Errorf("shrinking a container disk is not supported")
	}
	d.unmountContainerRoot(ctx, abs)
	if req.Size > st.Size() {
		f, err := os.OpenFile(img, os.O_RDWR, 0o640)
		if err != nil {
			return err
		}
		if err := f.Truncate(req.Size); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	if d.Run != nil {
		fsck, err := E2fsckImageArgv(img)
		if err != nil {
			return err
		}
		if err := d.runLimit(ctx, fsck[0], fsck[1:]...); err != nil {
			return err
		}
		resize, err := Resize2fsArgv(img)
		if err != nil {
			return err
		}
		if err := d.runLimit(ctx, resize[0], resize[1:]...); err != nil {
			return err
		}
		mnt, err := MountLoopArgv(img, abs)
		if err != nil {
			return err
		}
		if err := d.runLimit(ctx, mnt[0], mnt[1:]...); err != nil {
			return err
		}
	}
	return nil
}

func (d Directory) restoreContainerRoots(root string) {
	if d.Run == nil {
		return
	}
	dir := path.Join(root, "volumes", ClassContainerRoot)
	ents, err := d.host().ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, VolumeSizeExt) {
			continue
		}
		abs := path.Join(dir, strings.TrimSuffix(name, VolumeSizeExt))
		_ = d.EnsureDirectoryRootMounted(context.Background(), abs)
	}
}

// RestoreLoopMounts remounts bounded Directory container-root images under storageRoot.
func (d Directory) RestoreLoopMounts(ctx context.Context, storageRoot string) error {
	storageRoot = path.Clean(storageRoot)
	if storageRoot == "" || storageRoot == "/" {
		return fmt.Errorf("storage root is invalid")
	}
	ents, err := os.ReadDir(storageRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		d.restoreContainerRoots(path.Join(storageRoot, e.Name()))
	}
	return nil
}

// EnsureDirectoryRootMounted loop-mounts a sized container-root image onto abs.
// A missing image is treated as a legacy unbounded directory and is left alone.
func (d Directory) EnsureDirectoryRootMounted(ctx context.Context, abs string) error {
	abs = path.Clean(abs)
	if abs == "" || abs == "/" || strings.Contains(abs, "..") || !strings.HasPrefix(abs, "/") {
		return fmt.Errorf("container root path is invalid")
	}
	img := directoryRootImage(abs)
	if _, err := os.Stat(img); err != nil {
		return nil
	}
	if d.containerRootMounted(abs) {
		return nil
	}
	if d.Run == nil {
		return fmt.Errorf("container root image is present but not mounted")
	}
	mnt, err := MountLoopArgv(img, abs)
	if err != nil {
		return err
	}
	return d.runLimit(ctx, mnt[0], mnt[1:]...)
}

func (d Directory) containerRootMounted(abs string) bool {
	text, err := d.host().ReadMounts()
	if err != nil {
		return false
	}
	cover, ok := CoveringMount(abs, ParseMountinfo(text))
	return ok && cover.MountPoint == abs
}
