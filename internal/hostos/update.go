package hostos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/hostos/debian"
)

const (
	ChannelStable         = debian.ChannelStable
	UpdateApplyStatus     = debian.UpdateApplyStatus
	UpdateRepoEnable      = debian.UpdateRepoEnable
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

// ReleaseRepository is where the release workflow publishes a GitHub release
// tagged v<package version> for every version it adds to the APT repository.
const ReleaseRepository = "https://github.com/ASPEC-RACING-PTY-LTD/ndl-ce"

// ReleaseURL returns the GitHub release page for a published package version,
// or "" when the version is not a plain dotted release version.
func ReleaseURL(version string) string {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) < 2 || len(parts) > 4 {
		return ""
	}
	for _, p := range parts {
		if p == "" || len(p) > 6 || strings.Trim(p, "0123456789") != "" {
			return ""
		}
	}
	return ReleaseRepository + "/releases/tag/v" + strings.TrimSpace(version)
}

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
	// RepositoryConfigured is true when the signed release repository is an APT source.
	RepositoryConfigured bool `json:"repository_configured"`
	// Log is the tail of a finished apply's package-manager output.
	Log string `json:"log,omitempty"`
	// CandidateVersion is the version an apply would install (preflight).
	CandidateVersion string `json:"candidate_version,omitempty"`
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
		return runPreflight(ctx, res, exec), nil
	case debian.UpdateCheckpoint:
		return runCheckpoint(ctx, req, res, exec)
	case debian.UpdateApply:
		return runApply(ctx, req, res, exec)
	case debian.UpdateApplyStatus:
		return runApplyStatus(ctx, res, exec)
	case debian.UpdateRepoEnable:
		return enableRepository(ctx, res, req.DryRun || exec == nil)
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
	res.RepositoryConfigured = RepositoryConfigured()
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
	res.RepositoryConfigured = RepositoryConfigured()
	if !res.RepositoryConfigured {
		res.Status = "failed"
		res.Reason = RepositoryNotConfigured
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

// MinUpdateFreeBytes is the free space an update needs on the root
// filesystem and in the package cache: downloads, unpacked files and a
// checkpoint.
const MinUpdateFreeBytes = 3 << 30

// Host probes, replaced in tests.
var (
	freeBytes = statFree
	lockHeld  = packageLockHeld
)

// runPreflight checks everything that would make an apply fail or leave the
// host half updated. A failed row fails the whole preflight, and apply does
// not start. Without exec (tests, SkipHostCmds) only local runtime facts are
// reported.
func runPreflight(ctx context.Context, res UpdateResult, exec ExecFunc) UpdateResult {
	ok := true
	add := func(name, status, detail string) {
		res.Checks = append(res.Checks, PreflightCheck{Name: name, Status: status, Detail: detail})
		if status == "failed" {
			ok = false
		}
	}
	k, z, n := debian.ObserveRuntime()
	if k {
		add("kernel", "ok", "Kernel version is readable.")
	} else {
		add("kernel", "failed", "Kernel version is unreadable.")
	}
	if exec != nil {
		ok = preflightHost(ctx, &res, exec, add) && ok
	}
	if z {
		add("zfs", "ok", "ZFS module is present.")
	} else {
		add("zfs", "warning", "ZFS module is not loaded. Directory storage remains first-class.")
	}
	if n {
		add("nvidia", "ok", "NVIDIA runtime is present.")
	} else {
		add("nvidia", "warning", "NVIDIA runtime is not present. GPU assignment is a later phase.")
	}
	add("store_compatibility", "ok", StoreCompatDetail)
	res.KernelOK = k
	res.ZFSOK = z
	res.NvidiaOK = n
	res.PreflightOK = ok
	if ok {
		res.Status = "succeeded"
	} else {
		res.Status = "failed"
		res.Reason = firstFailed(res.Checks)
	}
	return res
}

func preflightHost(ctx context.Context, res *UpdateResult, exec ExecFunc, add func(name, status, detail string)) bool {
	ok := true
	fail := func(name, detail string) {
		add(name, "failed", detail)
		ok = false
	}
	for _, path := range []string{"/", "/var/cache/apt/archives"} {
		free, err := freeBytes(path)
		switch {
		case err != nil:
			add("disk_space", "warning", "Free space on "+path+" could not be read.")
		case free < MinUpdateFreeBytes:
			fail("disk_space", fmt.Sprintf("Only %s free on %s; an update needs %s. Free up space first.", gib(free), path, gib(MinUpdateFreeBytes)))
		default:
			add("disk_space", "ok", fmt.Sprintf("%s free on %s.", gib(free), path))
		}
	}
	busy := false
	for _, l := range debian.PackageLocks {
		if lockHeld(l) {
			busy = true
			break
		}
	}
	if out, err := exec(ctx, debian.ApplyUnitShowArgv()); err == nil && debian.ParseApplyUnit(out).Running() {
		fail("package_manager", "A platform update or rollback is already running.")
	} else if busy {
		fail("package_manager", "Another package manager is running, for example automatic security updates. Try again in a few minutes.")
	} else {
		add("package_manager", "ok", "No other package operation is running.")
	}
	if out, err := exec(ctx, debian.DpkgAuditArgv()); err != nil || strings.TrimSpace(out) != "" {
		fail("package_database", "An earlier package operation did not finish. Run 'dpkg --configure -a' on the host, then try again.")
	} else {
		add("package_database", "ok", "Installed packages are fully configured.")
	}
	held := map[string]bool{}
	if out, err := exec(ctx, debian.HeldPackagesArgv()); err == nil {
		for _, name := range strings.Fields(out) {
			held[name] = true
		}
	}
	var heldCore []string
	for _, name := range PackageNames {
		if held[name] {
			heldCore = append(heldCore, name)
		}
	}
	if len(heldCore) > 0 {
		fail("held_packages", "These packages are on hold and would not update: "+strings.Join(heldCore, ", ")+".")
	} else {
		add("held_packages", "ok", "No No-dal package is on hold.")
	}
	res.RepositoryConfigured = RepositoryConfigured()
	if !res.RepositoryConfigured {
		fail("repository", RepositoryNotConfigured)
		return ok
	}
	if _, err := exec(ctx, debian.CheckArgv()); err != nil {
		fail("repository", "The release repository could not be reached. Check the host's internet connection and DNS.")
		return ok
	}
	add("repository", "ok", "The signed release repository is reachable.")
	pkgs, items, ver := readPolicies(ctx, exec)
	res.Packages, res.Items, res.Version = pkgs, items, ver
	res.CandidateVersion = upgradeVersion(items)
	if res.CandidateVersion == "" {
		add("update_available", "warning", "This host already runs the newest version.")
	} else {
		add("update_available", "ok", "Version "+res.CandidateVersion+" is available.")
	}
	return ok
}

// upgradeVersion is the version an apply installs. Every core package is
// built from one source, so the nodal metapackage's candidate is preferred.
func upgradeVersion(items []PreviewItem) string {
	version := ""
	for _, it := range items {
		if it.Action != "upgrade" || it.CandidateVersion == "" {
			continue
		}
		if it.Name == "nodal" {
			return it.CandidateVersion
		}
		if version == "" {
			version = it.CandidateVersion
		}
	}
	return version
}

func firstFailed(checks []PreflightCheck) string {
	for _, c := range checks {
		if c.Status == "failed" {
			return c.Detail
		}
	}
	return "preflight failed"
}

func gib(b uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
}

// exitCode returns a finished command's exit status, or -1.
func exitCode(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return -1
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
		res.Status = "planned"
		res.Reason = "Checkpoint argv was planned. Host commands were not run."
		return res, nil
	}
	// Old checkpoints are pruned whatever happens, and a failed one is
	// removed, so checkpoints can never pile up on the root disk.
	defer pruneCheckpoints(checkpointDir, KeepCheckpoints, id)
	failed := func(reason string, out string) (UpdateResult, error) {
		_ = os.Remove(filepath.Join(checkpointDir, id+".tar"))
		_ = os.Remove(filepath.Join(checkpointDir, id+".sql"))
		res.Status = "failed"
		res.Reason = reason
		if tail := strings.TrimSpace(out); tail != "" {
			res.Log = tailText(tail, 2000)
		}
		res.PostgresDump = false
		return res, nil
	}
	if out, err := exec(ctx, debian.MkdirCheckpointArgv()); err != nil {
		return failed("checkpoint directory could not be created", out)
	}
	tarArgv, err := debian.CheckpointTarArgv(locator)
	if err != nil {
		return failed("checkpoint locator is invalid", "")
	}
	// tar exits 1 when a file changed while it was read, which is normal
	// for a running control plane; the archive is still complete.
	if out, err := exec(ctx, tarArgv); err != nil && exitCode(err) != 1 {
		return failed("control-plane checkpoint failed", out)
	}
	if argv, err := debian.PrivateFileArgv(locator); err == nil {
		_, _ = exec(ctx, argv)
	}
	dumpPath := debian.CheckpointDumpPath(id)
	fileArgv, err := debian.DumpFileArgv(dumpPath)
	if err != nil {
		return failed("dump locator is invalid", "")
	}
	if out, err := exec(ctx, fileArgv); err != nil {
		return failed("the database dump file could not be created", out)
	}
	dumpArgv, err := debian.PgDumpArgv(dumpPath)
	if err != nil {
		return failed("dump locator is invalid", "")
	}
	if out, err := exec(ctx, dumpArgv); err != nil {
		return failed("PostgreSQL dump failed", out)
	}
	res.PostgresDump = true
	res.Status = "succeeded"
	return res, nil
}

// CheckpointDumpExists reports whether a checkpoint holds a database dump.
func CheckpointDumpExists(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\. ") {
		return false
	}
	info, err := os.Stat(filepath.Join(checkpointDir, id+".sql"))
	return err == nil && info.Size() > 0
}

// KeepCheckpoints is how many update checkpoints are kept on disk.
const KeepCheckpoints = 3

// checkpointDir is replaced in tests.
var checkpointDir = debian.CheckpointDir

// pruneCheckpoints keeps the newest keep checkpoints (each an <id>.tar and
// <id>.sql pair) and always the one just written.
func pruneCheckpoints(dir string, keep int, current string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	newest := map[string]time.Time{}
	for _, e := range entries {
		name := e.Name()
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".tar"), ".sql")
		if base == name {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newest[base]) {
			newest[base] = info.ModTime()
		}
	}
	ids := make([]string, 0, len(newest))
	for id := range newest {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return newest[ids[i]].After(newest[ids[j]]) })
	kept := 0
	for _, id := range ids {
		if id == current || kept < keep {
			kept++
			continue
		}
		_ = os.Remove(filepath.Join(dir, id+".tar"))
		_ = os.Remove(filepath.Join(dir, id+".sql"))
	}
}

func runApply(ctx context.Context, req UpdateRequest, res UpdateResult, exec ExecFunc) (UpdateResult, error) {
	res.DryRun = req.DryRun
	res.Packages = []PackageStatus{{Name: "nodal", Status: "not_reported"}}
	if exec == nil {
		res.Status = "planned"
		res.Reason = "Package apply argv was planned. Host commands were not run."
		return res, nil
	}
	res.RepositoryConfigured = RepositoryConfigured()
	if !res.RepositoryConfigured {
		res.Status = "failed"
		res.Reason = RepositoryNotConfigured
		return res, nil
	}
	if req.DryRun {
		if _, err := exec(ctx, debian.ApplyArgv(true)); err != nil {
			res.Status = "failed"
			res.Reason = "control-plane package apply failed"
			return res, nil
		}
		res.Status = "succeeded"
		res.Reason = "Dry run: control-plane packages resolve from the signed repository."
		return res, nil
	}
	out, err := exec(ctx, debian.ApplyUnitShowArgv())
	if err == nil {
		unit := debian.ParseApplyUnit(out)
		if unit.Running() {
			res.Status = "failed"
			res.Reason = "a platform update is already running"
			return res, nil
		}
		if unit.Loaded() {
			// A finished earlier run still holds the unit name.
			_, _ = exec(ctx, debian.ApplyUnitStopArgv())
			_, _ = exec(ctx, debian.ApplyUnitResetArgv())
		}
	}
	launch, err := debian.ApplyLaunchVersionArgv(req.Version)
	if err != nil {
		res.Status = "failed"
		res.Reason = "the version to install is invalid"
		return res, nil
	}
	if _, err := exec(ctx, debian.CheckArgv()); err != nil {
		res.Status = "failed"
		res.Reason = "package index refresh failed"
		return res, nil
	}
	if out, err := exec(ctx, launch); err != nil {
		res.Status = "failed"
		res.Reason = "could not start the platform update"
		res.Log = tailText(strings.TrimSpace(out), 2000)
		return res, nil
	}
	res.Version = strings.TrimSpace(req.Version)
	res.Status = "running"
	res.Reason = "The platform update is running. The control plane and agent restart during it; guests keep running."
	return res, nil
}

// clearFinishedUnit frees the shared apply unit name, or reports that an
// apply or rollback still runs in it.
func clearFinishedUnit(ctx context.Context, exec ExecFunc) (running bool) {
	out, err := exec(ctx, debian.ApplyUnitShowArgv())
	if err != nil {
		return false
	}
	unit := debian.ParseApplyUnit(out)
	if unit.Running() {
		return true
	}
	if unit.Loaded() {
		_, _ = exec(ctx, debian.ApplyUnitStopArgv())
		_, _ = exec(ctx, debian.ApplyUnitResetArgv())
	}
	return false
}

// runApplyStatus reports the state of the last detached apply.
func runApplyStatus(ctx context.Context, res UpdateResult, exec ExecFunc) (UpdateResult, error) {
	res.DryRun = false
	if exec == nil {
		res.Status = "not_reported"
		return res, nil
	}
	out, err := exec(ctx, debian.ApplyUnitShowArgv())
	if err != nil {
		res.Status = "not_reported"
		res.Reason = "the platform update unit could not be read"
		return res, nil
	}
	unit := debian.ParseApplyUnit(out)
	switch {
	case unit.LoadState != "loaded":
		res.Status = "not_reported"
		res.Reason = "no platform update unit is loaded"
		return res, nil
	case unit.Running():
		res.Status = "running"
		return res, nil
	case unit.Succeeded():
		res.Status = "succeeded"
		res.Reason = "Control-plane packages applied through the signed repository."
	case unit.Failed():
		res.Status = "failed"
		res.Reason = "control-plane package apply failed"
	default:
		res.Status = "not_reported"
		return res, nil
	}
	if argv, err := debian.ApplyUnitLogArgv(unit.InvocationID); err == nil {
		if logOut, err := exec(ctx, argv); err == nil {
			res.Log = tailText(strings.TrimSpace(logOut), 4000)
		}
	}
	pkgs, _, ver := readPolicies(ctx, exec)
	res.Packages = pkgs
	res.Version = ver
	return res, nil
}

// runRollback moves every core package back to req.Version in the detached
// update unit. With req.CheckpointID the database is first restored from the
// dump taken right before the update, because the newer control plane may
// have changed its schema. Nothing changes unless the dry run resolves.
func runRollback(ctx context.Context, req UpdateRequest, res UpdateResult, exec ExecFunc) (UpdateResult, error) {
	res.DryRun = req.DryRun
	version := strings.TrimSpace(req.Version)
	if !debian.ValidVersion(version) {
		res.Status = "failed"
		res.Reason = "the version to roll back to is not recorded"
		return res, nil
	}
	res.PreviousVersion = version
	res.Packages = make([]PackageStatus, 0, len(PackageNames))
	for _, name := range PackageNames {
		res.Packages = append(res.Packages, PackageStatus{Name: name, Version: version, Status: "not_reported"})
	}
	dump := ""
	if id := strings.TrimSpace(req.CheckpointID); id != "" {
		if !CheckpointDumpExists(id) {
			res.Status = "failed"
			res.Reason = "the database checkpoint taken before the update is missing, so a rollback cannot restore the database"
			return res, nil
		}
		dump = debian.CheckpointDumpPath(id)
		res.CheckpointID = id
	}
	if exec == nil {
		res.Status = "planned"
		res.Reason = "Package rollback argv was planned. Host commands were not run."
		return res, nil
	}
	if clearFinishedUnit(ctx, exec) {
		res.Status = "failed"
		res.Reason = "a platform update is already running"
		return res, nil
	}
	_, _ = exec(ctx, debian.CheckArgv())
	dry, err := debian.RollbackDryRunArgv(version)
	if err != nil {
		res.Status = "failed"
		res.Reason = "the version to roll back to is invalid"
		return res, nil
	}
	if out, err := exec(ctx, dry); err != nil {
		res.Status = "failed"
		res.Reason = "version " + version + " cannot be installed from the release repository"
		res.Log = tailText(strings.TrimSpace(out), 2000)
		return res, nil
	}
	if req.DryRun {
		res.Status = "succeeded"
		res.Reason = "Dry run: every package can move back to " + version + "."
		return res, nil
	}
	launch, err := debian.RollbackLaunchArgv(version, dump)
	if err != nil {
		res.Status = "failed"
		res.Reason = "the rollback could not be planned"
		return res, nil
	}
	if out, err := exec(ctx, launch); err != nil {
		res.Status = "failed"
		res.Reason = "could not start the rollback"
		res.Log = tailText(strings.TrimSpace(out), 2000)
		return res, nil
	}
	res.Version = version
	res.Status = "running"
	res.Reason = "The rollback is running. The control plane restarts during it; guests keep running."
	return res, nil
}

func runK8sRuntime(ctx context.Context, res UpdateResult, exec ExecFunc, start bool) (UpdateResult, error) {
	argv := debian.K8sRuntimeArgv(start)
	if exec == nil {
		res.Status = "planned"
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
		res.Status = "planned"
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
		res.Status = "planned"
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

func tailText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[len(s)-limit:]
}
