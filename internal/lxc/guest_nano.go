package lxc

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Host paths for the default guest editor. Tests replace them.
var (
	hostNanoBin   = "/usr/bin/nano"
	hostRnanoBin  = "/usr/bin/rnano"
	hostNanorc    = "/etc/nanorc"
	hostNanoShare = "/usr/share/nano"
	hostNanoLibs  = []string{
		"/lib/x86_64-linux-gnu/libncursesw.so.6",
		"/lib/x86_64-linux-gnu/libtinfo.so.6",
		"/usr/lib/x86_64-linux-gnu/libncursesw.so.6",
		"/usr/lib/x86_64-linux-gnu/libtinfo.so.6",
	}
)

// ensureGuestNano copies the host nano editor into the guest rootfs when the
// guest does not already have it. Missing host nano is a no-op so tests and
// non-Debian hosts still provision. An existing guest nano is not overwritten.
func ensureGuestNano(rootfs string) error {
	if strings.TrimSpace(rootfs) == "" {
		return nil
	}
	if _, err := os.Lstat(hostNanoBin); err != nil {
		return nil
	}
	if err := copyPathIfMissing(hostNanoBin, filepath.Join(rootfs, "usr", "bin", "nano")); err != nil {
		return err
	}
	if err := copyPathIfMissing(hostRnanoBin, filepath.Join(rootfs, "usr", "bin", "rnano")); err != nil {
		return err
	}
	if err := copyPathIfMissing(hostNanorc, filepath.Join(rootfs, "etc", "nanorc")); err != nil {
		return err
	}
	if err := copyPathIfMissing(hostNanoShare, filepath.Join(rootfs, "usr", "share", "nano")); err != nil {
		return err
	}
	for _, lib := range hostNanoLibs {
		rel := strings.TrimPrefix(lib, "/")
		if rel == lib {
			continue
		}
		if err := copyPathIfMissing(lib, filepath.Join(rootfs, rel)); err != nil {
			return err
		}
	}
	return nil
}

func copyPathIfMissing(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return nil
	}
	if _, err := os.Lstat(dst); err == nil {
		if st.IsDir() && st.Mode()&os.ModeSymlink == 0 {
			return copyDirMissing(src, dst)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, dst); err != nil {
			return err
		}
		if filepath.IsAbs(target) {
			return nil
		}
		return copyPathIfMissing(filepath.Join(filepath.Dir(src), target), filepath.Join(filepath.Dir(dst), target))
	}
	if st.IsDir() {
		return copyDirMissing(src, dst)
	}
	return copyRegularFile(src, dst, st.Mode())
}

func copyDirMissing(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if err := copyPathIfMissing(filepath.Join(src, ent.Name()), filepath.Join(dst, ent.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyRegularFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}
	return nil
}
