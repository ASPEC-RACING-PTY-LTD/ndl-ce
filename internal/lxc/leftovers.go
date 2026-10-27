package lxc

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Leftover is system container state on this node. The control plane
// decides which of them no longer belong to a workload; the node cannot
// tell, because a deleted workload's last-applied state may still be here.
type Leftover struct {
	ID          string    `json:"id"`
	Name        string    `json:"name,omitempty"`
	UnitEnabled bool      `json:"unit_enabled"`
	UnitActive  bool      `json:"unit_active"`
	HasConfig   bool      `json:"has_config"`
	HasState    bool      `json:"has_state"`
	Rootfs      string    `json:"rootfs,omitempty"`
	RootfsBytes int64     `json:"rootfs_bytes,omitempty"`
	RootfsMount bool      `json:"rootfs_mount,omitempty"`
	Bridge      string    `json:"bridge,omitempty"`
	Modified    time.Time `json:"modified"`
}

// LeftoverRemoved reports one removal. Rootfs is left in place when it is
// its own mounted filesystem (a pool image or dataset): the control plane
// destroys it through its storage volume.
type LeftoverRemoved struct {
	ID          string `json:"id"`
	Rootfs      string `json:"rootfs,omitempty"`
	RootfsKept  bool   `json:"rootfs_kept,omitempty"`
	KeptReason  string `json:"kept_reason,omitempty"`
	RemovedData bool   `json:"removed_data,omitempty"`
}

func (e *Engine) unitDir() string {
	if e.UnitDir != "" {
		return e.UnitDir
	}
	return "/etc/systemd/system"
}

// ListContainerState lists every system container this node has state for:
// an LXC config folder or an enabled nodal-ct@ unit.
func (e *Engine) ListContainerState(ctx context.Context) ([]Leftover, error) {
	ids := map[string]bool{}
	ents, err := os.ReadDir(e.lxcPath())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, ent := range ents {
		if _, err := uuid.Parse(ent.Name()); err == nil && ent.IsDir() {
			ids[ent.Name()] = true
		}
	}
	enabled := map[string]bool{}
	wants, _ := filepath.Glob(filepath.Join(e.unitDir(), "*.wants", CTUnitPrefix+"*.service"))
	for _, link := range wants {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(link), CTUnitPrefix), ".service")
		if _, err := uuid.Parse(id); err == nil {
			ids[id] = true
			enabled[id] = true
		}
	}
	out := make([]Leftover, 0, len(ids))
	for id := range ids {
		l := Leftover{ID: id, UnitEnabled: enabled[id]}
		if st, err := os.Stat(e.configPath(id)); err == nil {
			l.HasConfig, l.Modified = true, st.ModTime().UTC()
		}
		// The agent rewrites LXC config on every start, so the container's
		// own state file dates it better.
		if st, err := os.Stat(e.lastAppliedPath(id)); err == nil {
			l.HasState, l.Modified = true, st.ModTime().UTC()
		}
		if applied, err := e.readApplied(id); err == nil {
			l.Name, l.Rootfs, l.Bridge = applied.Spec.Name, applied.Spec.RootfsPath, applied.Spec.BridgeName
		}
		if l.Rootfs == "" {
			l.Rootfs = rootfsFromConfig(e.configPath(id))
		}
		if l.Rootfs != "" {
			l.RootfsMount = isMountPoint(l.Rootfs)
		}
		out = append(out, l)
	}
	if !e.SkipHostCmds {
		for i := range out {
			b, _ := e.run(ctx, BinSystemctl, "is-active", unitName(out[i].ID))
			out[i].UnitActive = strings.TrimSpace(string(b)) == "active"
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SizeLeftovers fills RootfsBytes, giving up on one tree after budget.
func SizeLeftovers(ctx context.Context, items []Leftover, budget time.Duration) {
	for i := range items {
		if items[i].Rootfs == "" {
			continue
		}
		tctx, cancel := context.WithTimeout(ctx, budget)
		items[i].RootfsBytes = treeBytes(tctx, items[i].Rootfs)
		cancel()
	}
}

func treeBytes(ctx context.Context, root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// RemoveLeftover stops and disables a container's unit and removes its LXC
// config, last-applied state and, when it is a plain folder no other
// container uses, its root filesystem. The caller has checked that no
// workload owns id.
func (e *Engine) RemoveLeftover(ctx context.Context, id string) (LeftoverRemoved, error) {
	if _, err := uuid.Parse(id); err != nil {
		return LeftoverRemoved{}, fmt.Errorf("container id must be a UUID")
	}
	out := LeftoverRemoved{ID: id}
	all, err := e.ListContainerState(ctx)
	if err != nil {
		return out, err
	}
	var self *Leftover
	usedBy := map[string]string{}
	for i, l := range all {
		if l.ID == id {
			self = &all[i]
			continue
		}
		if l.Rootfs != "" {
			usedBy[filepath.Clean(l.Rootfs)] = l.ID
		}
	}
	if self == nil {
		return out, fmt.Errorf("no container state for %s on this node", id)
	}
	if err := e.Delete(ctx, id); err != nil {
		return out, err
	}
	out.Rootfs = self.Rootfs
	switch root := filepath.Clean(self.Rootfs); {
	case self.Rootfs == "":
	case !leftoverRootfsAllowed(root):
		out.RootfsKept, out.KeptReason = true, "the root filesystem is outside No-dal's container storage"
	case usedBy[root] != "":
		out.RootfsKept, out.KeptReason = true, "container "+usedBy[root]+" uses the same root filesystem"
	case isMountPoint(root):
		out.RootfsKept, out.KeptReason = true, "the root filesystem is its own mounted volume"
	default:
		if err := os.RemoveAll(root); err != nil {
			return out, fmt.Errorf("removing %s: %w", root, err)
		}
		out.RemovedData = true
	}
	return out, nil
}

// leftoverRootfsAllowed limits deletion to container root folders in a
// No-dal storage pool.
func leftoverRootfsAllowed(root string) bool {
	if !filepath.IsAbs(root) || strings.Contains(root, "..") {
		return false
	}
	return strings.Contains(root, "/volumes/container-root/") && filepath.Base(filepath.Dir(root)) == "container-root"
}

func rootfsFromConfig(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "lxc.rootfs.path" {
			v = strings.TrimSpace(v)
			return strings.TrimPrefix(v, "dir:")
		}
	}
	return ""
}
