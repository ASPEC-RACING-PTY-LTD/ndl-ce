package lxc

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
)

// Mount is a host folder bind-mounted into a system container.
//
// Managing mounts only edits the container's mount list. It never stops,
// restarts or rebuilds the container, and never deletes, formats or
// re-owns existing data. Removing a mount removes the config line only;
// the host folder and its files stay where they are.
type Mount struct {
	// Source is the absolute host folder.
	Source string `json:"source"`
	// Target is the absolute path inside the container.
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
	// Create asks the agent to create Source as a new empty folder owned by
	// the container's root user. An existing folder is never re-owned.
	Create bool `json:"create,omitempty"`
	// PoolID and Label are display metadata from the control plane.
	PoolID string `json:"pool_id,omitempty"`
	Label  string `json:"label,omitempty"`
	// MountPoint records that Source was its own filesystem (a ZFS dataset
	// or a pool image) when it was attached. The container then refuses to
	// start while it is not mounted, so writes cannot land on the host disk.
	MountPoint bool `json:"mount_point,omitempty"`
}

// MaxMounts bounds the mount list of one container.
const MaxMounts = 32

// deniedMountSources are host trees that must never be shared into a guest.
var deniedMountSources = []string{
	"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32",
	"/proc", "/root", "/run", "/sbin", "/sys", "/usr",
	"/var/lib/lxc", "/var/lib/ndl/lxc", "/var/lib/ndl/secrets", "/var/lib/ndl/control",
}

// deniedMountTargets are container paths a bind mount must not cover.
var deniedMountTargets = []string{"/proc", "/sys", "/dev", "/run", "/boot", "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64"}

func cleanMountPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("mount paths must be absolute")
	}
	if strings.ContainsAny(p, " \t\n\r\x00\\,=#") {
		return "", fmt.Errorf("mount path %q contains a character LXC cannot use", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", fmt.Errorf("mount path %q must not contain ..", p)
		}
	}
	return path.Clean(p), nil
}

func underAny(p string, roots []string) bool {
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

// NormalizeMounts validates a mount list and returns it cleaned.
func NormalizeMounts(in []Mount) ([]Mount, error) {
	if len(in) > MaxMounts {
		return nil, fmt.Errorf("a container can have at most %d mounts", MaxMounts)
	}
	out := make([]Mount, 0, len(in))
	targets := map[string]bool{}
	for _, m := range in {
		src, err := cleanMountPath(m.Source)
		if err != nil {
			return nil, err
		}
		dst, err := cleanMountPath(m.Target)
		if err != nil {
			return nil, err
		}
		if src == "/" || underAny(src, deniedMountSources) {
			return nil, fmt.Errorf("host folder %s cannot be mounted into a container", src)
		}
		if dst == "/" || underAny(dst, deniedMountTargets) {
			return nil, fmt.Errorf("container path %s is reserved", dst)
		}
		if targets[dst] {
			return nil, fmt.Errorf("container path %s is used by two mounts", dst)
		}
		targets[dst] = true
		m.Source, m.Target = src, dst
		m.Label = strings.TrimSpace(m.Label)
		out = append(out, m)
	}
	return out, nil
}

// renderMounts writes one lxc.mount.entry per mount. create=dir makes the
// mount point inside the container; it does not touch the host folder.
func renderMounts(spec Spec) string {
	var b strings.Builder
	for _, m := range spec.Mounts {
		opts := "bind,create=dir"
		if m.ReadOnly {
			opts += ",ro"
		}
		fmt.Fprintf(&b, "lxc.mount.entry = %s %s none %s 0 0\n", m.Source, strings.TrimPrefix(m.Target, "/"), opts)
	}
	return b.String()
}

// MappedRootUID is the host UID of root inside the container (0 when the
// container is privileged).
func MappedRootUID(spec Spec) int {
	if spec.Privileged {
		return 0
	}
	return mapBase(spec.UIDMap, DefaultUIDMap)
}

func mappedRootGID(spec Spec) int {
	if spec.Privileged {
		return 0
	}
	return mapBase(spec.GIDMap, DefaultGIDMap)
}

// mapBase reads the host id from an "u 0 <host> <count>" idmap line.
func mapBase(line, fallback string) int {
	for _, l := range []string{line, fallback} {
		f := strings.Fields(l)
		if len(f) == 4 && f[1] == "0" {
			if n, err := strconv.Atoi(f[2]); err == nil && n >= 0 {
				return n
			}
		}
	}
	return 0
}

// Mounts returns a container's mount list.
func (e *Engine) Mounts(id string) (Result, error) {
	applied, err := e.readApplied(id)
	if err != nil {
		return Result{}, fmt.Errorf("system container last-applied is missing: %w", err)
	}
	return Result{
		WorkloadID: id, Mounts: append([]Mount{}, applied.Spec.Mounts...),
		MappedRootUID: MappedRootUID(applied.Spec),
	}, nil
}

// SetMounts replaces a container's mount list. It writes the container
// config only: a running container picks the change up on its next start,
// and RestartRequired says so. New folders requested with Create are made
// empty and owned by the container's root user; existing folders and
// their contents are never changed.
func (e *Engine) SetMounts(id string, mounts []Mount) (Result, error) {
	applied, err := e.readApplied(id)
	if err != nil {
		return Result{}, fmt.Errorf("system container last-applied is missing: %w", err)
	}
	clean, err := NormalizeMounts(mounts)
	if err != nil {
		return Result{}, err
	}
	for i, m := range clean {
		st, err := os.Stat(m.Source)
		switch {
		case err == nil && !st.IsDir():
			return Result{}, fmt.Errorf("host path %s is not a folder", m.Source)
		case err == nil:
		case os.IsNotExist(err) && m.Create:
			if err := os.MkdirAll(m.Source, 0o750); err != nil {
				return Result{}, fmt.Errorf("could not create %s: %w", m.Source, err)
			}
		case os.IsNotExist(err):
			return Result{}, fmt.Errorf("host folder %s does not exist", m.Source)
		default:
			return Result{}, err
		}
		// Ownership is only set on a new, empty folder. A folder with any
		// files in it is never re-owned.
		if m.Create && !e.SkipHostCmds && folderEmpty(m.Source) {
			if err := os.Chown(m.Source, MappedRootUID(applied.Spec), mappedRootGID(applied.Spec)); err != nil {
				return Result{}, fmt.Errorf("could not give the container ownership of %s: %w", m.Source, err)
			}
		}
		clean[i].Create = false
		if !clean[i].MountPoint {
			clean[i].MountPoint = isMountPoint(clean[i].Source)
		}
	}
	before := renderMounts(applied.Spec)
	applied.Spec.Mounts = clean
	if err := e.writeConfig(applied.Spec); err != nil {
		return Result{}, err
	}
	if err := e.writeApplied(applied.Spec, applied.ImageVerified, applied.ImageSHA256); err != nil {
		return Result{}, err
	}
	return Result{
		WorkloadID: id, Mounts: append([]Mount{}, clean...),
		MappedRootUID:   MappedRootUID(applied.Spec),
		RestartRequired: before != renderMounts(applied.Spec) && e.AlreadyRunning(context.Background(), id),
	}, nil
}

// isMountPoint reports whether p is the root of a mounted filesystem.
func isMountPoint(p string) bool {
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	parent, err := os.Stat(path.Dir(p))
	if err != nil {
		return false
	}
	return deviceOf(st) != deviceOf(parent)
}

// CheckMountsReady fails when a mount that was its own filesystem is not
// mounted, instead of letting the container write to the host disk.
func CheckMountsReady(spec Spec) error {
	for _, m := range spec.Mounts {
		if _, err := os.Stat(m.Source); err != nil {
			return fmt.Errorf("storage folder %s for %s is missing", m.Source, m.Target)
		}
		if m.MountPoint && !isMountPoint(m.Source) {
			return fmt.Errorf("storage for %s is not mounted at %s; mount the pool, then start the container", m.Target, m.Source)
		}
	}
	return nil
}

func folderEmpty(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	// A fresh ext4 image holds only lost+found.
	names, _ := f.Readdirnames(3)
	for _, n := range names {
		if n != "lost+found" {
			return false
		}
	}
	return true
}
