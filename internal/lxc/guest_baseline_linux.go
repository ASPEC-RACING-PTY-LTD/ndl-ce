//go:build linux

package lxc

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func withCreateUmask(mask int, fn func() error) error {
	old := unix.Umask(mask)
	defer unix.Umask(old)
	return fn()
}

func sanitizeRootfs(rootfs string, spec Spec) error {
	uid, gid := 0, 0
	if !spec.Privileged {
		uid = hostMapStart(spec.UIDMap)
		gid = hostMapStart(spec.GIDMap)
		if uid == 0 {
			uid = 100000
		}
		if gid == 0 {
			gid = 100000
		}
	}
	type dirSpec struct {
		rel  string
		mode os.FileMode
		uid  int
		gid  int
	}
	dirs := []dirSpec{
		{".", 0o755, uid, gid},
		{"etc", 0o755, uid, gid},
		{"root", 0o700, uid, gid},
		{"tmp", 0o1777, uid, gid},
		{"var", 0o755, uid, gid},
		{"var/tmp", 0o1777, uid, gid},
		{"var/lib", 0o755, uid, gid},
		{"var/lib/apt", 0o755, uid, gid},
		{"var/lib/dpkg", 0o755, uid, gid},
		{"etc/apt", 0o755, uid, gid},
	}
	for _, d := range dirs {
		p := filepath.Join(rootfs, d.rel)
		if d.rel == "." {
			p = rootfs
		}
		st, err := os.Lstat(p)
		if err != nil {
			if os.IsNotExist(err) {
				if d.rel == "tmp" || d.rel == "var/tmp" || d.rel == "root" {
					if err := os.MkdirAll(p, d.mode); err != nil {
						return err
					}
					if err := os.Chown(p, d.uid, d.gid); err != nil {
						return fmt.Errorf("chown %s: %w", d.rel, err)
					}
					if err := os.Chmod(p, d.mode); err != nil {
						return err
					}
				}
				continue
			}
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			if int(sys.Uid) != d.uid || int(sys.Gid) != d.gid {
				if err := os.Lchown(p, d.uid, d.gid); err != nil {
					return fmt.Errorf("chown %s: %w", d.rel, err)
				}
			}
		}
		want := d.mode
		if d.rel == "tmp" || d.rel == "var/tmp" {
			if err := os.Chmod(p, want); err != nil {
				return fmt.Errorf("chmod %s: %w", d.rel, err)
			}
			continue
		}
		mode := st.Mode().Perm()
		if mode&0o111 != 0o111 || mode&0o004 != 0o004 {
			if err := os.Chmod(p, mode|0o755); err != nil {
				return fmt.Errorf("chmod %s: %w", d.rel, err)
			}
		}
	}
	return nil
}
