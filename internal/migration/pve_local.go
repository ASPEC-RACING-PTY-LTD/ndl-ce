package migration

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// lxcHostRoot is the Proxmox LXC mount parent. Tests replace it so an empty
// ZFS dataset path cannot win over a populated /var/lib/lxc/{vmid}/rootfs.
var lxcHostRoot = "/var/lib/lxc"

// LocalHostFacts describes this machine for same-host Proxmox detection.
type LocalHostFacts struct {
	Hostnames []string
	HasPVE    bool
	Exists    func(string) bool
}

func realLocalHostFacts() LocalHostFacts {
	var names []string
	if h, err := os.Hostname(); err == nil && h != "" {
		names = append(names, h)
		if i := strings.Index(h, "."); i > 0 {
			names = append(names, h[:i])
		}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err != nil || ip == nil || ip.IsLoopback() {
				continue
			}
			names = append(names, ip.String())
		}
	}
	_, err := os.Stat("/etc/pve")
	return LocalHostFacts{
		Hostnames: names,
		HasPVE:    err == nil,
		Exists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
	}
}

var localHostFacts = realLocalHostFacts

// EndpointIsLocalHost reports whether the Proxmox API points at this machine.
func EndpointIsLocalHost(endpoint string, facts LocalHostFacts) bool {
	host := endpointHost(endpoint)
	if host == "" {
		return false
	}
	if host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost") {
		return true
	}
	for _, n := range facts.Hostnames {
		if strings.EqualFold(host, n) {
			return true
		}
	}
	return facts.HasPVE
}

func endpointHost(endpoint string) string {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Hostname())
}

// ResolveLXCRootfsHostPath maps a Proxmox LXC volid onto a local path.
func ResolveLXCRootfsHostPath(volid string, storages []map[string]any, exists func(string) bool) (string, string, bool) {
	if exists == nil {
		exists = func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		}
	}
	store, name := pveVolParts(volid)
	if store == "" || name == "" {
		return "", "", false
	}
	row := storageRowByName(storages, store)
	kind := strings.ToLower(fmt.Sprint(row["type"]))
	vmid := volVMID(name)
	var firstDev string
	for _, cand := range lxcRootfsCandidates(row, store, name, vmid) {
		if !exists(cand) {
			continue
		}
		if VerifyCopiedRootfs(cand) == nil {
			return cand, kind, true
		}
		if firstDev == "" && pathIsBlockDevice(cand) {
			firstDev = cand
		}
	}
	if firstDev != "" {
		return firstDev, kind, true
	}
	return "", kind, false
}

func pathIsBlockDevice(p string) bool {
	st, err := os.Lstat(p)
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeDevice != 0
}

func pveVolParts(volid string) (store, name string) {
	id := PVEVolumeID(volid)
	if i := strings.Index(id, ":"); i >= 0 {
		return id[:i], id[i+1:]
	}
	return "", id
}

func storageRowByName(storages []map[string]any, name string) map[string]any {
	for _, s := range storages {
		if fmt.Sprint(s["storage"]) == name {
			return s
		}
	}
	return map[string]any{}
}

func volVMID(name string) string {
	if i := strings.Index(name, "/"); i > 0 {
		return name[:i]
	}
	for _, p := range strings.Split(name, "-") {
		if p == "" {
			continue
		}
		ok := true
		for _, c := range p {
			if c < '0' || c > '9' {
				ok = false
				break
			}
		}
		if ok && p[0] != '0' {
			return p
		}
	}
	return ""
}

func lxcRootfsCandidates(row map[string]any, store, name, vmid string) []string {
	kind := strings.ToLower(fmt.Sprint(row["type"]))
	base, _ := row["path"].(string)
	pool, _ := row["pool"].(string)
	vg, _ := row["vgname"].(string)
	var out []string
	switch kind {
	case "dir", "btrfs", "nfs", "cifs", "cephfs":
		if base != "" {
			out = append(out, filepath.Join(base, name))
			if vmid != "" {
				out = append(out, filepath.Join(base, "images", vmid, filepath.Base(name)))
				out = append(out, filepath.Join(base, "images", vmid, "rootfs"))
				out = append(out, filepath.Join(base, "private", vmid))
			}
		}
	case "zfspool":
		if pool != "" {
			out = append(out, filepath.Join("/", pool, name))
			out = append(out, "/"+strings.TrimPrefix(pool, "/")+"/"+name)
		}
		if base != "" {
			out = append(out, filepath.Join(base, name))
		}
	case "lvmthin", "lvm":
		if vg != "" {
			out = append(out, "/dev/"+vg+"/"+name)
			out = append(out, lvmMapperPath(vg, name))
		}
	}
	if vmid != "" {
		out = append([]string{filepath.Join(lxcHostRoot, vmid, "rootfs")}, out...)
	}
	_ = store
	return uniqueNonEmpty(out)
}

func lvmMapperPath(vg, lv string) string {
	esc := strings.ReplaceAll(vg, "-", "--") + "-" + strings.ReplaceAll(lv, "-", "--")
	return "/dev/mapper/" + esc
}

// DetectLocalLXC reports a same-host stopped-capable LXC rootfs path.
func DetectLocalLXC(endpoint string, storages []map[string]any, root *Artifact, running bool) (path string, sameHost bool, ready bool) {
	facts := localHostFacts()
	if !EndpointIsLocalHost(endpoint, facts) {
		return "", false, false
	}
	if root == nil || root.Path == "" {
		return "", true, false
	}
	path, _, ok := ResolveLXCRootfsHostPath(root.Path, storages, facts.Exists)
	if !ok {
		return "", true, false
	}
	return path, true, !running
}

func LXCRootfsLocalStopReason(name, store, kind string) string {
	if store == "" {
		store = "rootfs"
	}
	if kind == "" {
		kind = "unknown"
	}
	guest := strings.TrimSpace(name)
	if guest == "" {
		guest = "the container"
	}
	return "LXC rootfs on " + store + " (" + kind + ") is on this host. Stop " + guest + " on Proxmox to use Local Host Migration. No-dal will not stop it."
}

func ValidateLocalMigrationPath(p string) error {
	if p == "" || !strings.HasPrefix(p, "/") {
		return fmt.Errorf("local rootfs path must be absolute")
	}
	if strings.Contains(p, "..") || strings.ContainsAny(p, ",=\n\r\x00;$") {
		return fmt.Errorf("local rootfs path is invalid")
	}
	clean := filepath.Clean(p)
	if clean != p {
		return fmt.Errorf("local rootfs path is not clean")
	}
	denied := []string{
		"/etc", "/root", "/home", "/boot", "/usr", "/proc", "/sys", "/dev/shm",
		"/var/lib/ndl/secrets", "/var/lib/ndl/certs", "/var/lib/postgresql",
	}
	for _, d := range denied {
		if clean == d || strings.HasPrefix(clean, d+"/") {
			return fmt.Errorf("local rootfs path is not an allowed Proxmox volume location")
		}
	}
	if strings.HasPrefix(clean, "/dev/") {
		return nil
	}
	if strings.HasPrefix(clean, "/var/lib/vz/") || strings.HasPrefix(clean, "/var/lib/lxc/") {
		return nil
	}
	if strings.Contains(clean, "/subvol-") || strings.Contains(clean, "/vm-") {
		return nil
	}
	return fmt.Errorf("local rootfs path is not an allowed Proxmox volume location")
}

var (
	mountROFn = mountRO
	unmountFn = func(p string) error { return unix.Unmount(p, unix.MNT_DETACH) }
)

// CopyLocalRootfs copies a local LXC rootfs tree into dest. Dest must be a
// No-dal staging or storage path. Source data is read-only: the copy never
// chmod, chown, or otherwise mutates the source. A block device is mounted
// read-only for the copy and then unmounted. Dest ownership, mode, POSIX
// ACLs, user xattrs, and relative symlinks are restored from the source.
// An empty or skeleton source is an error; success requires dest to contain
// an executable init and nontrivial transferred content.
func CopyLocalRootfs(src, dest string) error {
	if err := ValidateHostPath(dest); err != nil {
		return err
	}
	if err := ValidateHostPath(src); err != nil {
		if err := ValidateLocalMigrationPath(src); err != nil {
			return err
		}
	}
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("local LXC rootfs is not readable: %w", err)
	}
	if info.IsDir() {
		return copyRootfsTree(src, dest)
	}
	if info.Mode()&os.ModeDevice != 0 {
		mnt := dest + ".mnt"
		if err := os.MkdirAll(mnt, 0o750); err != nil {
			return err
		}
		if err := mountROFn(src, mnt); err != nil {
			return err
		}
		defer func() { _ = unmountFn(mnt) }()
		return copyRootfsTree(mnt, dest)
	}
	return fmt.Errorf("local LXC rootfs is not a directory or mountable volume")
}

func mountRO(dev, dest string) error {
	for _, fsType := range []string{"ext4", "xfs", "btrfs"} {
		if err := unix.Mount(dev, dest, fsType, unix.MS_RDONLY, ""); err == nil {
			return nil
		}
	}
	return fmt.Errorf("cannot mount LXC volume %s read-only. Use a temporary vzdump, or mount the volume read-only first", dev)
}

func copyRootfsTree(src, dest string) error {
	src = filepath.Clean(src)
	if err := RequirePopulatedLXCRootfs(src); err != nil {
		return fmt.Errorf("local LXC source rootfs is empty or incomplete: %w", err)
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return copyDestMeta(path, dest, false)
		}
		target, err := RelJail(dest, filepath.ToSlash(rel))
		if err != nil {
			return nil
		}
		return copyRootfsPath(path, target, dest, d)
	})
	if err != nil {
		return err
	}
	if err := VerifyCopiedRootfs(dest); err != nil {
		return fmt.Errorf("local LXC copy produced an empty or incomplete rootfs: %w", err)
	}
	return nil
}

func symlinkStaysInRoot(destRoot, destPath, link string) bool {
	if filepath.IsAbs(link) {
		return false
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(destPath), link))
	root := filepath.Clean(destRoot)
	return resolved == root || strings.HasPrefix(resolved, root+string(os.PathSeparator))
}

func copyRootfsPath(src, dest, destRoot string, d os.DirEntry) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	mode := info.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		link, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if !symlinkStaysInRoot(destRoot, dest, link) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return err
		}
		_ = os.Remove(dest)
		if err := os.Symlink(link, dest); err != nil {
			return err
		}
		return copyDestMeta(src, dest, true)
	case mode.IsDir():
		if err := os.MkdirAll(dest, 0o750); err != nil {
			return err
		}
		return copyDestMeta(src, dest, false)
	case mode.IsRegular():
		if err := copyRegularFile(src, dest); err != nil {
			return err
		}
		return copyDestMeta(src, dest, false)
	default:
		_ = d
		return nil
	}
}

func copyRegularFile(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func copyDestMeta(src, dest string, symlink bool) error {
	var st unix.Stat_t
	if err := unix.Lstat(src, &st); err != nil {
		return err
	}
	if err := unix.Lchown(dest, int(st.Uid), int(st.Gid)); err != nil && !errors.Is(err, unix.EPERM) {
		return fmt.Errorf("preserve owner %s: %w", dest, err)
	}
	if !symlink {
		if err := unix.Chmod(dest, uint32(st.Mode&0o7777)); err != nil && !errors.Is(err, unix.EPERM) {
			return fmt.Errorf("preserve mode %s: %w", dest, err)
		}
	}
	return copyAllowedXattrs(src, dest)
}

func allowedCopyXattr(name string) bool {
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, "=\x00") {
		return false
	}
	if strings.HasPrefix(name, "user.") {
		return true
	}
	return name == "system.posix_acl_access" || name == "system.posix_acl_default"
}

func copyAllowedXattrs(src, dest string) error {
	sz, err := unix.Llistxattr(src, nil)
	if err != nil || sz == 0 {
		return nil
	}
	buf := make([]byte, sz)
	n, err := unix.Llistxattr(src, buf)
	if err != nil || n == 0 {
		return nil
	}
	for _, name := range strings.Split(string(buf[:n]), "\x00") {
		if !allowedCopyXattr(name) {
			continue
		}
		vsz, err := unix.Lgetxattr(src, name, nil)
		if err != nil || vsz == 0 {
			continue
		}
		val := make([]byte, vsz)
		got, err := unix.Lgetxattr(src, name, val)
		if err != nil || got == 0 {
			continue
		}
		_ = unix.Lsetxattr(dest, name, val[:got], 0)
	}
	return nil
}
