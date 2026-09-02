package hostos

import (
	"context"
	"fmt"
	"strings"

	"github.com/no-dal/ndl-ce/internal/hostos/debian"
)

const (
	ChannelStable         = debian.ChannelStable
	UpdateFeatureInstall  = debian.UpdateFeatureInstall
	UpdateFeatureRemove   = debian.UpdateFeatureRemove
	UpdateK8sRuntimeStart = debian.UpdateK8sRuntimeStart
	UpdateK8sRuntimeStop  = debian.UpdateK8sRuntimeStop
	UpdateOSDStart        = debian.UpdateOSDStart
	UpdateOSDStop         = debian.UpdateOSDStop
	// UpdateUnsupportedReason is the honest public reason when the host adapter cannot run.
	UpdateUnsupportedReason = debian.UnsupportedHost
	// StoreCompatDetail is the Phase 12 preflight note. Store manifests are Phase 36.
	StoreCompatDetail = "Store packages are declarative manifests. Helper scripts are rejected. Compatibility checks declared CPU, RAM, and optional GPU."
)

// PackageNames are the only names the public update contract may mention.
var PackageNames = debian.PackageNames

// FeaturePackageNames are optional Phase 35 modules, never core Depends.
var FeaturePackageNames = debian.FeaturePackageNames

// UpdateRequest is a typed host-platform update action. It is not a shell string.
type UpdateRequest struct {
	Action       string
	Channel      string
	PackageName  string
	Version      string
	DryRun       bool
	CheckpointID string
}

// PackageStatus is one control-plane package as observed by the host adapter.
type PackageStatus struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// PreviewItem is one dry-run package change.
type PreviewItem struct {
	Name             string `json:"name"`
	CurrentVersion   string `json:"current_version"`
	CandidateVersion string `json:"candidate_version"`
	Action           string `json:"action"`
}

// PreflightCheck is one honest preflight row.
type PreflightCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// UpdateResult is host-platform-neutral. Public JSON must not contain apt or dpkg verbs.
type UpdateResult struct {
	Supported       bool             `json:"supported"`
	Reason          string           `json:"reason,omitempty"`
	Action          string           `json:"action"`
	Channel         string           `json:"channel"`
	DryRun          bool             `json:"dry_run"`
	Status          string           `json:"status"`
	Packages        []PackageStatus  `json:"packages,omitempty"`
	Items           []PreviewItem    `json:"items,omitempty"`
	Changelog       string           `json:"changelog,omitempty"`
	Checks          []PreflightCheck `json:"checks,omitempty"`
	KernelOK        bool             `json:"kernel_ok"`
	ZFSOK           bool             `json:"zfs_ok"`
	NvidiaOK        bool             `json:"nvidia_ok"`
	PreflightOK     bool             `json:"preflight_ok"`
	CheckpointID    string           `json:"checkpoint_id,omitempty"`
	Locator         string           `json:"locator,omitempty"`
	PostgresDump    bool             `json:"postgres_dump"`
	Version         string           `json:"version,omitempty"`
	PreviousVersion string           `json:"previous_version,omitempty"`
}

// ExecFunc runs one validated argv list. argv[0] is an absolute binary path.
type ExecFunc func(ctx context.Context, argv []string) (stdout string, err error)

// Debian13Amd64 reports whether p is the Phase 12 update adapter host.
func Debian13Amd64(p Platform) bool {
	return debian.Is(p.ID, p.VersionID) && p.Architecture == "amd64"
}

// EvaluateUpdate returns honest unsupported results without running a package manager.
func EvaluateUpdate(p Platform, req UpdateRequest) UpdateResult {
	channel := strings.TrimSpace(req.Channel)
	if channel == "" {
		channel = ChannelStable
	}
	action := strings.TrimSpace(req.Action)
	res := UpdateResult{
		Action:   action,
		Channel:  channel,
		DryRun:   req.DryRun,
		Status:   "unsupported",
		Packages: unsupportedPackages(),
	}
	if !Lookup(p).Qualified() {
		res.Reason = UpdateUnsupportedReason
		res.Items = unsupportedItems()
		res.Checks = unsupportedChecks()
		res.Changelog = "Changelog is not reported on an unsupported host."
		return res
	}
	res.Supported = true
	res.Reason = "Debian 13 amd64 uses the signed nodal repository."
	res.Status = "succeeded"
	res.Packages = notReportedPackages()
	return res
}

func unsupportedPackages() []PackageStatus {
	out := make([]PackageStatus, 0, len(PackageNames))
	for _, name := range PackageNames {
		out = append(out, PackageStatus{Name: name, Status: "unsupported"})
	}
	return out
}

func notReportedPackages() []PackageStatus {
	out := make([]PackageStatus, 0, len(PackageNames))
	for _, name := range PackageNames {
		out = append(out, PackageStatus{Name: name, Status: "not_reported"})
	}
	return out
}

func unsupportedItems() []PreviewItem {
	out := make([]PreviewItem, 0, len(PackageNames))
	for _, name := range PackageNames {
		out = append(out, PreviewItem{Name: name, Action: "unsupported"})
	}
	return out
}

func unsupportedChecks() []PreflightCheck {
	return []PreflightCheck{
		{Name: "kernel", Status: "unsupported", Detail: UpdateUnsupportedReason},
		{Name: "zfs", Status: "unsupported", Detail: UpdateUnsupportedReason},
		{Name: "nvidia", Status: "unsupported", Detail: UpdateUnsupportedReason},
		{Name: "store_compatibility", Status: "unsupported", Detail: StoreCompatDetail},
	}
}

// RunUpdate executes typed Debian argv when the host is Debian 13 amd64.
// exec may be nil when the caller only wants the plan (tests / SkipHostCmds).
func RunUpdate(ctx context.Context, p Platform, req UpdateRequest, exec ExecFunc) (UpdateResult, error) {
	res := EvaluateUpdate(p, req)
	if !res.Supported {
		return res, nil
	}
	switch strings.TrimSpace(req.Action) {
	case debian.UpdateStatus, "":
		return runStatus(ctx, res, exec)
	case debian.UpdateCheck:
		return runCheck(ctx, res, exec)
	case debian.UpdatePreflight:
		return runPreflight(res), nil
	case debian.UpdateCheckpoint:
		return runCheckpoint(ctx, req, res, exec)
	case debian.UpdateApply:
		return runApply(ctx, req, res, exec)
	case debian.UpdateRollback:
		return runRollback(ctx, req, res, exec)
	case debian.UpdateFeatureInstall:
		return runFeaturePackage(ctx, req, res, exec, false)
	case debian.UpdateFeatureRemove:
		return runFeaturePackage(ctx, req, res, exec, true)
	case debian.UpdateK8sRuntimeStart:
		return runK8sRuntime(ctx, res, exec, true)
	case debian.UpdateK8sRuntimeStop:
		return runK8sRuntime(ctx, res, exec, false)
	case debian.UpdateOSDStart:
		return runOSDRuntime(ctx, res, exec, true)
	case debian.UpdateOSDStop:
		return runOSDRuntime(ctx, res, exec, false)
	default:
		res.Supported = false
		res.Status = "failed"
		res.Reason = "update action is unknown"
		return res, fmt.Errorf("update action is unknown")
	}
}

func runStatus(ctx context.Context, res UpdateResult, exec ExecFunc) (UpdateResult, error) {
	res.DryRun = true
	if exec == nil {
		return res, nil
	}
	pkgs, _, ver := readPolicies(ctx, exec)
	res.Packages = pkgs
	res.Version = ver
	return res, nil
}

func runCheck(ctx context.Context, res UpdateResult, exec ExecFunc) (UpdateResult, error) {
	res.DryRun = true
	res.Changelog = "Changelog is not reported until the signed repository returns one."
	if exec == nil {
		res.Items = holdItems(res.Packages)
		return res, nil
	}
	if _, err := exec(ctx, debian.CheckArgv()); err != nil {
		res.Status = "failed"
		res.Reason = "package index refresh failed"
		return res, nil
	}
	pkgs, items, ver := readPolicies(ctx, exec)
	res.Packages = pkgs
	res.Items = items
	res.Version = ver
	if logOut, err := exec(ctx, debian.ChangelogArgv()); err == nil {
		if trimmed := strings.TrimSpace(logOut); trimmed != "" {
			res.Changelog = trimChangelog(trimmed)
		}
	}
	return res, nil
}

func readPolicies(ctx context.Context, exec ExecFunc) ([]PackageStatus, []PreviewItem, string) {
	pkgs := make([]PackageStatus, 0, len(PackageNames))
	items := make([]PreviewItem, 0, len(PackageNames))
	var controlVer string
	for _, name := range PackageNames {
		argv, err := debian.PolicyArgv(name)
		if err != nil {
			continue
		}
		out, err := exec(ctx, argv)
		if err != nil {
			pkgs = append(pkgs, PackageStatus{Name: name, Status: "not_reported"})
			items = append(items, PreviewItem{Name: name, Action: "hold"})
			continue
		}
		pol := debian.ParsePolicy(out)
		st := "current"
		act := "hold"
		if pol.Installed == "" || pol.Installed == "(none)" {
			st = "not_configured"
			act = "unsupported"
		} else if pol.Candidate != "" && pol.Candidate != pol.Installed {
			st = "update_available"
			act = "upgrade"
		}
		if name == "ndl-control" {
			controlVer = pol.Installed
		}
		pkgs = append(pkgs, PackageStatus{Name: name, Version: pol.Installed, Status: st})
		items = append(items, PreviewItem{
			Name: name, CurrentVersion: pol.Installed, CandidateVersion: pol.Candidate, Action: act,
		})
	}
	return pkgs, items, controlVer
}

func holdItems(pkgs []PackageStatus) []PreviewItem {
	out := make([]PreviewItem, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, PreviewItem{Name: p.Name, CurrentVersion: p.Version, Action: "hold"})
	}
	return out
}

func runPreflight(res UpdateResult) UpdateResult {
	k, z, n := debian.ObserveRuntime()
	kernel := PreflightCheck{Name: "kernel", Status: "ok", Detail: "Kernel version is readable."}
	if !k {
		kernel = PreflightCheck{Name: "kernel", Status: "failed", Detail: "Kernel version is unreadable."}
	}
	zfs := PreflightCheck{Name: "zfs", Status: "warning", Detail: "ZFS module is not loaded. Directory storage remains first-class."}
	if z {
		zfs = PreflightCheck{Name: "zfs", Status: "ok", Detail: "ZFS module is present."}
	}
	nv := PreflightCheck{Name: "nvidia", Status: "warning", Detail: "NVIDIA runtime is not present. GPU assignment is a later phase."}
	if n {
		nv = PreflightCheck{Name: "nvidia", Status: "ok", Detail: "NVIDIA runtime is present."}
	}
	store := PreflightCheck{Name: "store_compatibility", Status: "ok", Detail: StoreCompatDetail}
	res.Checks = []PreflightCheck{kernel, zfs, nv, store}
	res.KernelOK = k
	res.ZFSOK = z
	res.NvidiaOK = n
	res.PreflightOK = k
	if k {
		res.Status = "succeeded"
	} else {
		res.Status = "failed"
	}
	return res
}

func runCheckpoint(ctx context.Context, req UpdateRequest, res UpdateResult, exec ExecFunc) (UpdateResult, error) {
	id := strings.TrimSpace(req.CheckpointID)
	if id == "" {
		res.Status = "failed"
		res.Reason = "checkpoint id is required"
		return res, nil
	}
	locator := debian.CheckpointTarPath(id)
	res.CheckpointID = id
	res.Locator = locator
	res.PostgresDump = false
	if exec == nil {
		res.Status = "succeeded"
		res.Reason = "Checkpoint argv was planned. Host commands were not run."
		return res, nil
	}
	if _, err := exec(ctx, debian.MkdirCheckpointArgv()); err != nil {
		res.Status = "failed"
		res.Reason = "checkpoint directory could not be created"
		return res, nil
	}
	tarArgv, err := debian.CheckpointTarArgv(locator)
	if err != nil {
		res.Status = "failed"
		res.Reason = "checkpoint locator is invalid"
		return res, nil
	}
	if _, err := exec(ctx, tarArgv); err != nil {
		res.Status = "failed"
		res.Reason = "control-plane checkpoint failed"
		return res, nil
	}
	dumpPath := debian.CheckpointDumpPath(id)
	dumpArgv, err := debian.PgDumpArgv(dumpPath)
	if err != nil {
		res.Status = "failed"
		res.Reason = "dump locator is invalid"
		return res, nil
	}
	if _, err := exec(ctx, dumpArgv); err != nil {
		res.Status = "failed"
		res.Reason = "PostgreSQL dump failed"
		res.PostgresDump = false
		return res, nil
	}
	res.PostgresDump = true
	res.Status = "succeeded"
	return res, nil
}

func runApply(ctx context.Context, req UpdateRequest, res UpdateResult, exec ExecFunc) (UpdateResult, error) {
	res.DryRun = req.DryRun
	res.Packages = []PackageStatus{{Name: "nodal", Status: "not_reported"}}
	if exec == nil {
		res.Status = "succeeded"
		res.Reason = "Package apply argv was planned. Host commands were not run."
		return res, nil
	}
	if _, err := exec(ctx, debian.ApplyArgv(req.DryRun)); err != nil {
		res.Status = "failed"
		res.Reason = "control-plane package apply failed"
		return res, nil
	}
	res.Status = "succeeded"
	res.Reason = "Control-plane packages applied through the signed repository."
	return res, nil
}

func runRollback(ctx context.Context, req UpdateRequest, res UpdateResult, exec ExecFunc) (UpdateResult, error) {
	res.DryRun = req.DryRun
	argv, err := debian.RollbackControlArgv(req.Version, req.DryRun)
	if err != nil {
		res.Status = "failed"
		res.Reason = "previous ndl-control version is not recorded"
		return res, nil
	}
	res.PreviousVersion = req.Version
	res.Packages = []PackageStatus{{Name: "ndl-control", Version: req.Version, Status: "not_reported"}}
	if exec == nil {
		res.Status = "succeeded"
		res.Reason = "Package rollback argv was planned. Host commands were not run."
		return res, nil
	}
	if _, err := exec(ctx, argv); err != nil {
		res.Status = "failed"
		res.Reason = "control-plane package rollback failed"
		return res, nil
	}
	res.Status = "succeeded"
	res.Reason = "ndl-control was rolled back through the signed repository."
	return res, nil
}

func runK8sRuntime(ctx context.Context, res UpdateResult, exec ExecFunc, start bool) (UpdateResult, error) {
	argv := debian.K8sRuntimeArgv(start)
	if exec == nil {
		res.Status = "succeeded"
		res.Reason = "Kubernetes runtime argv was planned. Host commands were not run. kubelet is not started."
		return res, nil
	}
	if _, err := exec(ctx, argv); err != nil {
		res.Status = "failed"
		if start {
			res.Reason = "kubelet start failed"
		} else {
			res.Reason = "kubelet stop failed"
		}
		return res, nil
	}
	if start {
		res.Reason = "kubelet start was requested via systemd"
	} else {
		res.Reason = "kubelet stop was requested via systemd. Virtual machines were not stopped."
	}
	return res, nil
}

func runOSDRuntime(ctx context.Context, res UpdateResult, exec ExecFunc, start bool) (UpdateResult, error) {
	argv := debian.OSDRuntimeArgv(start)
	if exec == nil {
		res.Status = "succeeded"
		res.Reason = "OSD runtime argv was planned. Host commands were not run. ceph-osd is not started."
		return res, nil
	}
	if _, err := exec(ctx, argv); err != nil {
		res.Status = "failed"
		if start {
			res.Reason = "ceph-osd start failed"
		} else {
			res.Reason = "ceph-osd stop failed"
		}
		return res, nil
	}
	if start {
		res.Reason = "ceph-osd start was requested via systemd"
	} else {
		res.Reason = "ceph-osd stop was requested via systemd. Virtual machines were not stopped."
	}
	return res, nil
}

func runFeaturePackage(ctx context.Context, req UpdateRequest, res UpdateResult, exec ExecFunc, remove bool) (UpdateResult, error) {
	pkg := strings.TrimSpace(req.PackageName)
	var argv []string
	var err error
	if remove {
		argv, err = debian.FeatureRemoveArgv(pkg, req.DryRun)
	} else {
		argv, err = debian.FeatureInstallArgv(pkg, req.DryRun)
	}
	if err != nil {
		res.Supported = false
		res.Status = "failed"
		res.Reason = "feature package is not allowlisted"
		return res, nil
	}
	res.DryRun = req.DryRun
	res.Packages = []PackageStatus{{Name: pkg, Status: "not_reported"}}
	if exec == nil {
		res.Status = "succeeded"
		res.Reason = "Feature package argv was planned. Host commands were not run."
		return res, nil
	}
	if _, err := exec(ctx, argv); err != nil {
		res.Status = "failed"
		res.Reason = "feature package apply failed"
		return res, nil
	}
	res.Status = "succeeded"
	if remove {
		res.Reason = "Optional feature package removed through the signed repository."
	} else {
		res.Reason = "Optional feature package applied through the signed repository."
	}
	return res, nil
}

func trimChangelog(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4000 {
		return s[:4000]
	}
	return s
}
