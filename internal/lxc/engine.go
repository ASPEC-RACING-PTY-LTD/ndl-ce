package lxc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Engine drives liblxc through typed argv and written config files.
// systemd starts the CT. The agent does not hold the process.
type Engine struct {
	DataDir      string
	LXCPath      string
	WorkloadsDir string
	CacheDir     string
	ImageBase    string
	HTTP         HTTPDoer
	Run          Runner
	Now          func() time.Time
	SkipHostCmds bool
	FakeUnpack   bool
}

func (e *Engine) dataDir() string {
	if e.DataDir != "" {
		return e.DataDir
	}
	return defaultDataDir
}

func (e *Engine) lxcPath() string {
	if e.LXCPath != "" {
		return e.LXCPath
	}
	if e.DataDir != "" {
		return filepath.Join(e.DataDir, "runtime", "lxc")
	}
	return defaultRuntimeLXC
}

func (e *Engine) workloadsDir() string {
	if e.WorkloadsDir != "" {
		return e.WorkloadsDir
	}
	if e.DataDir != "" {
		return filepath.Join(e.DataDir, "workloads")
	}
	return defaultWorkloads
}

func (e *Engine) cacheDir() string {
	if e.CacheDir != "" {
		return e.CacheDir
	}
	if e.DataDir != "" {
		return filepath.Join(e.DataDir, "cache", "lxc-images")
	}
	return defaultImageCache
}

func normalizeSpec(spec Spec) (Spec, error) {
	if _, err := uuid.Parse(strings.TrimSpace(spec.WorkloadID)); err != nil {
		return Spec{}, fmt.Errorf("workload_id must be a UUID")
	}
	if !spec.SkipImage {
		if err := ValidatePin(spec.ImagePin); err != nil {
			return Spec{}, err
		}
	} else if spec.ImagePin == "" {
		spec.ImagePin = "imported"
	}
	if strings.TrimSpace(spec.RootfsPath) == "" {
		return Spec{}, fmt.Errorf("rootfs_path is required")
	}
	if spec.CPUs < 1 {
		spec.CPUs = DefaultCPUs
	}
	if spec.MemoryBytes < 1 {
		spec.MemoryBytes = DefaultMemoryBytes
	}
	if spec.UIDMap == "" {
		spec.UIDMap = DefaultUIDMap
	}
	if spec.GIDMap == "" {
		spec.GIDMap = DefaultGIDMap
	}
	if spec.MAC == "" {
		spec.MAC = MACFromUUID(spec.WorkloadID)
	}
	if spec.Name == "" {
		spec.Name = spec.WorkloadID
	}
	return spec, nil
}

// Create writes LXC config and last-applied, unpacks the image, and enables the unit.
// Replay of the same workload_id returns the original volume_id.
func (e *Engine) Create(ctx context.Context, spec Spec) (Result, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return Result{}, err
	}
	if prev, err := e.readApplied(spec.WorkloadID); err == nil && prev.Spec.WorkloadID == spec.WorkloadID {
		spec.VolumeID = prev.Spec.VolumeID
		spec.RootfsPath = prev.Spec.RootfsPath
		if spec.MAC == "" {
			spec.MAC = prev.Spec.MAC
		}
		if err := e.prepareRootfs(spec); err != nil {
			return Result{}, err
		}
		if specChanged(prev.Spec, spec) {
			if err := e.writeConfig(spec); err != nil {
				return Result{}, err
			}
			if err := e.writeApplied(spec, prev.ImageVerified, prev.ImageSHA256); err != nil {
				return Result{}, err
			}
		}
		if err := e.enableUnit(ctx, spec.WorkloadID); err != nil {
			return Result{}, err
		}
		if err := e.Start(ctx, spec.WorkloadID); err != nil {
			return Result{}, err
		}
		return Result{
			WorkloadID: prev.Spec.WorkloadID, VolumeID: prev.Spec.VolumeID,
			RootfsPath: prev.Spec.RootfsPath, MAC: spec.MAC,
			ImageVerified: prev.ImageVerified, ImageSHA256: prev.ImageSHA256,
			Status: StatusRunning,
		}, nil
	}
	if err := os.MkdirAll(filepath.Dir(e.configPath(spec.WorkloadID)), 0o750); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(spec.RootfsPath, 0o750); err != nil {
		return Result{}, err
	}
	var verified bool
	var sha string
	if spec.SkipImage {
		if err := writeRootfsMarker(spec.RootfsPath); err != nil {
			return Result{}, err
		}
	} else {
		var err error
		verified, sha, err = e.fetchAndUnpack(ctx, spec.ImagePin, spec.RootfsPath)
		if err != nil {
			return Result{}, err
		}
	}
	if err := e.writeConfig(spec); err != nil {
		return Result{}, err
	}
	if err := e.prepareRootfs(spec); err != nil {
		return Result{}, err
	}
	if err := e.writeApplied(spec, verified, sha); err != nil {
		return Result{}, err
	}
	if err := e.enableUnit(ctx, spec.WorkloadID); err != nil {
		return Result{}, err
	}
	status := StatusStopped
	if !spec.NoStart {
		if err := e.Start(ctx, spec.WorkloadID); err != nil {
			return Result{}, err
		}
		status = StatusRunning
	}
	return Result{
		WorkloadID: spec.WorkloadID, VolumeID: spec.VolumeID, RootfsPath: spec.RootfsPath,
		MAC: spec.MAC, ImageVerified: verified, ImageSHA256: sha, Status: status,
	}, nil
}

// Start starts nodal-ct@<uuid> via systemd.
func (e *Engine) Start(ctx context.Context, id string) error {
	e.ensureAppliedTraverse(id)
	_, err := e.run(ctx, BinSystemctl, "start", unitName(id))
	return err
}

// Stop stops the CT unit. The process is not held by the agent.
func (e *Engine) Stop(ctx context.Context, id string) error {
	_, err := e.run(ctx, BinSystemctl, "stop", unitName(id))
	return err
}

// Restart restarts the CT unit.
func (e *Engine) Restart(ctx context.Context, id string) error {
	e.ensureAppliedTraverse(id)
	_, err := e.run(ctx, BinSystemctl, "restart", unitName(id))
	return err
}

func (e *Engine) ensureAppliedTraverse(id string) {
	applied, err := e.readApplied(id)
	if err != nil {
		return
	}
	if !applied.Spec.Privileged {
		_ = ensureTraverse(applied.Spec.RootfsPath)
	}
	_ = ensureTraverse(e.configPath(id))
}

// Delete stops the unit and removes runtime files. Desired rows stay in the store.
func (e *Engine) Delete(ctx context.Context, id string) error {
	_ = e.Stop(ctx, id)
	_, _ = e.run(ctx, BinSystemctl, "disable", unitName(id))
	_ = os.RemoveAll(filepath.Join(e.lxcPath(), id))
	_ = os.RemoveAll(filepath.Join(e.workloadsDir(), id))
	return nil
}

// Clone copies rootfs and writes a new identity. The clone UUID is caller-supplied.
func (e *Engine) Clone(ctx context.Context, req LifecycleRequest) (Result, error) {
	if _, err := uuid.Parse(strings.TrimSpace(req.CloneID)); err != nil {
		return Result{}, fmt.Errorf("clone_id must be a UUID")
	}
	if req.CloneID == req.WorkloadID {
		return Result{}, fmt.Errorf("clone_id must be a new UUID")
	}
	src, err := e.readApplied(req.WorkloadID)
	if err != nil {
		return Result{}, fmt.Errorf("source workload is unavailable")
	}
	dst := src.Spec
	dst.WorkloadID = req.CloneID
	if req.CloneName != "" {
		dst.Name = req.CloneName
	} else {
		dst.Name = src.Spec.Name + "-clone"
	}
	if req.CloneVolumeID != "" {
		dst.VolumeID = req.CloneVolumeID
	}
	if req.CloneRootfsPath != "" {
		dst.RootfsPath = req.CloneRootfsPath
	}
	if req.CloneMAC != "" {
		dst.MAC = req.CloneMAC
	} else {
		dst.MAC = MACFromUUID(dst.WorkloadID)
	}
	if err := os.MkdirAll(dst.RootfsPath, 0o750); err != nil {
		return Result{}, err
	}
	if err := e.copyRootfs(ctx, src.Spec.RootfsPath, dst.RootfsPath); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(e.configPath(dst.WorkloadID)), 0o750); err != nil {
		return Result{}, err
	}
	if err := e.writeConfig(dst); err != nil {
		return Result{}, err
	}
	if err := e.writeApplied(dst, src.ImageVerified, src.ImageSHA256); err != nil {
		return Result{}, err
	}
	return Result{
		WorkloadID: dst.WorkloadID, VolumeID: dst.VolumeID, RootfsPath: dst.RootfsPath,
		MAC: dst.MAC, ImageVerified: src.ImageVerified, ImageSHA256: src.ImageSHA256,
		Status: StatusStopped,
	}, nil
}

func specChanged(prev, next Spec) bool {
	return prev.CPUs != next.CPUs || prev.MemoryBytes != next.MemoryBytes ||
		prev.BridgeName != next.BridgeName || prev.Privileged != next.Privileged ||
		prev.UIDMap != next.UIDMap || prev.GIDMap != next.GIDMap || prev.Name != next.Name ||
		strings.Join(prev.GPUDevices, "\n") != strings.Join(next.GPUDevices, "\n")
}

func (e *Engine) prepareRootfs(spec Spec) error {
	if e.SkipHostCmds || e.FakeUnpack {
		return nil
	}
	if err := validateRootfsPath(spec.RootfsPath); err != nil {
		return err
	}
	if err := ensureGuestDHCP(spec.RootfsPath); err != nil {
		return err
	}
	if spec.Privileged {
		return nil
	}
	if err := remapRootfs(spec.RootfsPath, hostMapStart(spec.UIDMap), hostMapStart(spec.GIDMap)); err != nil {
		return err
	}
	if err := ensureTraverse(spec.RootfsPath); err != nil {
		return err
	}
	return ensureTraverse(e.configPath(spec.WorkloadID))
}

func ensureTraverse(rootfs string) error {
	p := filepath.Clean(rootfs)
	for {
		st, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) {
				next := filepath.Dir(p)
				if next == p {
					return nil
				}
				p = next
				continue
			}
			return err
		}
		if mode := st.Mode(); mode&0o111 != 0o111 {
			if err := os.Chmod(p, mode|0o111); err != nil {
				return fmt.Errorf("rootfs traverse %s: %w", p, err)
			}
		}
		next := filepath.Dir(p)
		if next == p {
			return nil
		}
		p = next
	}
}

func hostMapStart(mapping string) int {
	fields := strings.Fields(mapping)
	if len(fields) >= 3 {
		n, err := strconv.Atoi(fields[2])
		if err == nil && n > 0 {
			return n
		}
	}
	return 100000
}

func validateRootfsPath(p string) error {
	if p == "" || !filepath.IsAbs(p) || p != filepath.Clean(p) || strings.Contains(p, "..") {
		return fmt.Errorf("rootfs_path is not a clean absolute path")
	}
	return nil
}

func remapRootfs(rootfs string, uid, gid int) error {
	return filepath.Walk(rootfs, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

func ensureGuestDHCP(rootfs string) error {
	ifaces := filepath.Join(rootfs, "etc", "network", "interfaces")
	if b, err := os.ReadFile(ifaces); err == nil && bytes.Contains(bytes.ToLower(b), []byte("dhcp")) {
		return nil
	}
	netd := filepath.Join(rootfs, "etc", "systemd", "network")
	if entries, err := os.ReadDir(netd); err == nil {
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(netd, e.Name()))
			if bytes.Contains(bytes.ToLower(b), []byte("dhcp")) {
				return nil
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(ifaces), 0o755); err != nil {
		return err
	}
	const body = "auto lo\niface lo inet loopback\n\nauto eth0\niface eth0 inet dhcp\n"
	return os.WriteFile(ifaces, []byte(body), 0o644)
}

func (e *Engine) copyRootfs(ctx context.Context, src, dst string) error {
	if e.SkipHostCmds || e.FakeUnpack {
		return writeRootfsMarker(dst)
	}
	_, err := e.run(ctx, BinCP, "-a", src+"/.", dst)
	return err
}

func (e *Engine) enableUnit(ctx context.Context, id string) error {
	_, err := e.run(ctx, BinSystemctl, "enable", unitName(id))
	return err
}

// Lifecycle dispatches a typed action.
func (e *Engine) Lifecycle(ctx context.Context, req LifecycleRequest) (Result, error) {
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "start":
		if err := e.Start(ctx, req.WorkloadID); err != nil {
			return Result{}, err
		}
		return Result{WorkloadID: req.WorkloadID, Status: StatusRunning}, nil
	case "stop":
		if err := e.Stop(ctx, req.WorkloadID); err != nil {
			return Result{}, err
		}
		return Result{WorkloadID: req.WorkloadID, Status: StatusStopped}, nil
	case "restart":
		if err := e.Restart(ctx, req.WorkloadID); err != nil {
			return Result{}, err
		}
		return Result{WorkloadID: req.WorkloadID, Status: StatusRunning}, nil
	case "delete":
		if err := e.Delete(ctx, req.WorkloadID); err != nil {
			return Result{}, err
		}
		return Result{WorkloadID: req.WorkloadID, Status: StatusUnavailable}, nil
	case "clone":
		return e.Clone(ctx, req)
	default:
		return Result{}, fmt.Errorf("unknown lifecycle action %q", req.Action)
	}
}

// Observe reports pid, unit_active, and honest status. Missing is unavailable.
func (e *Engine) Observe(ctx context.Context, hints []Hint) (Observation, error) {
	obs := Observation{ObservedAt: e.now(), Workloads: make([]Observed, 0, len(hints))}
	for _, h := range hints {
		obs.Workloads = append(obs.Workloads, e.observeOne(ctx, h))
	}
	return obs, nil
}

func (e *Engine) observeOne(ctx context.Context, h Hint) Observed {
	out := Observed{
		WorkloadID:      h.WorkloadID,
		Kind:            h.Kind,
		Status:          StatusUnavailable,
		Reason:          "workload was not observed",
		MigrateReady:    false,
		MigrateBlockers: []string{"live migrate of system containers is post-1.0"},
		ObservedAt:      e.now(),
	}
	if out.Kind == "" {
		out.Kind = KindSystemContainer
	}
	applied, err := e.readApplied(h.WorkloadID)
	cfgMissing := false
	if _, statErr := os.Stat(e.configPath(h.WorkloadID)); statErr != nil {
		cfgMissing = true
	}
	if err != nil && cfgMissing {
		return out
	}
	out.Reason = ""
	out.Status = StatusStopped
	if err == nil {
		out.ImageVerified = applied.ImageVerified
		out.MAC = applied.Spec.MAC
	}
	if e.SkipHostCmds {
		return out
	}
	active, unitErr := e.unitActive(ctx, h.WorkloadID)
	out.UnitActive = active
	pid, ipv4, infoErr := e.lxcInfo(ctx, h.WorkloadID)
	out.PID = pid
	out.IPv4 = ipv4
	if unitErr != nil && infoErr != nil && cfgMissing {
		out.Status = StatusUnavailable
		out.Reason = "workload was not observed"
		return out
	}
	if active && pid > 0 {
		out.Status = StatusRunning
		return out
	}
	if active && pid == 0 {
		out.Status = StatusChecking
		out.Reason = "unit is active; pid is not reported"
		return out
	}
	if unitState, _ := e.unitState(ctx, h.WorkloadID); unitState == "failed" {
		out.Status = StatusFailed
		out.Reason = "nodal-ct unit failed"
		return out
	}
	out.Status = StatusStopped
	return out
}

func (e *Engine) unitActive(ctx context.Context, id string) (bool, error) {
	state, err := e.unitState(ctx, id)
	return state == "active", err
}

func (e *Engine) unitState(ctx context.Context, id string) (string, error) {
	out, err := e.run(ctx, BinSystemctl, "is-active", unitName(id))
	state := strings.TrimSpace(string(out))
	if state == "" && err != nil {
		return "inactive", err
	}
	return state, nil
}

func (e *Engine) lxcInfo(ctx context.Context, id string) (pid int, ipv4 string, err error) {
	out, err := e.run(ctx, BinLXCInfo, "-P", e.lxcPath(), "-n", id)
	if err != nil {
		return 0, "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "pid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				pid, _ = strconv.Atoi(fields[len(fields)-1])
			}
		}
		if strings.HasPrefix(lower, "ip:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				cand := fields[len(fields)-1]
				if strings.Contains(cand, ".") {
					ipv4 = cand
				}
			}
		}
	}
	return pid, ipv4, nil
}
