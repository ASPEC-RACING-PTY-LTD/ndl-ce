package migration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var rootfsInitRel = []string{
	"sbin/init",
	"usr/sbin/init",
	"usr/lib/systemd/systemd",
	"lib/systemd/systemd",
}

var rootfsSkeletonNames = map[string]bool{
	"dev":            true,
	"etc":            true,
	"proc":           true,
	"sys":            true,
	".ndl-rootfs-ok": true,
	"lost+found":     true,
}

var rootfsUserspaceNames = []string{"bin", "sbin", "usr", "lib", "lib64"}

// FindRootfsInit returns the first executable init path under root, following
// a relative symlink such as /sbin/init -> /lib/systemd/systemd.
func FindRootfsInit(root string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	for _, rel := range rootfsInitRel {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if isExecutableInit(p) {
			return p
		}
	}
	return ""
}

func isExecutableInit(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	return st.Mode()&0o111 != 0
}

func rootfsLooksSkeleton(root string) bool {
	ents, err := os.ReadDir(root)
	if err != nil || len(ents) == 0 {
		return true
	}
	for _, e := range ents {
		if !rootfsSkeletonNames[e.Name()] {
			return false
		}
	}
	return true
}

func rootfsHasUserspaceTree(root string) bool {
	for _, name := range rootfsUserspaceNames {
		st, err := os.Lstat(filepath.Join(root, name))
		if err != nil {
			continue
		}
		if st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// VerifyCopiedRootfs fails unless dest has an executable init and nontrivial
// transferred content. A CreateCT skeleton (dev/etc/proc/sys plus
// .ndl-rootfs-ok) must not be treated as a completed Local Host copy.
func VerifyCopiedRootfs(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("rootfs path is empty")
	}
	if FindRootfsInit(root) == "" {
		return fmt.Errorf("destination rootfs has no executable init (sbin/init or systemd)")
	}
	if rootfsLooksSkeleton(root) {
		return fmt.Errorf("destination rootfs is a skeleton (dev/etc/proc/sys) without transferred content")
	}
	if !rootfsHasUserspaceTree(root) {
		return fmt.Errorf("destination rootfs content is trivial")
	}
	return nil
}

// RequirePopulatedLXCRootfs is the source-side check used before a Local Host
// copy. Empty ZFS mountpoints and CreateCT skeletons are refused.
func RequirePopulatedLXCRootfs(root string) error {
	return VerifyCopiedRootfs(root)
}

// ObservedContainerTransfer returns transfer_complete and configuration_verified
// only after dest contains a real rootfs. Callers must not record those levels
// when this returns an error.
func ObservedContainerTransfer(root string, startAfter bool) ([]string, error) {
	if err := VerifyCopiedRootfs(root); err != nil {
		return nil, err
	}
	out := []string{VerifyTransfer, VerifyConfig}
	if startAfter {
		out = append(out, VerifyBoot)
	}
	return out, nil
}
