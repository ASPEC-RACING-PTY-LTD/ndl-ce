package debian

import (
	"fmt"
	"strings"
)

const (
	UpdateCheck           = "check"
	UpdateStatus          = "status"
	UpdatePreflight       = "preflight"
	UpdateCheckpoint      = "checkpoint"
	UpdateApply           = "apply"
	UpdateApplyStatus     = "apply-status"
	UpdateRepoEnable      = "repository-enable"
	UpdateRollback        = "rollback"
	UpdateFeatureInstall  = "feature-install"
	UpdateFeatureRemove   = "feature-remove"
	UpdateK8sRuntimeStart = "k8s-runtime-start"
	UpdateK8sRuntimeStop  = "k8s-runtime-stop"
	UpdateOSDStart        = "ceph-osd-start"
	UpdateOSDStop         = "ceph-osd-stop"
	ChannelStable         = "stable"
	UnsupportedHost       = "Platform updates use the Debian 13 adapter and the signed nodal repository. This host is not Debian 13 amd64."
)

// PackageNames are the only packages the Phase 12 update adapter may mention.
var PackageNames = []string{"ndl-control", "ndl-agent", "ndl-ui", "nodal", "nodalctl"}

// FeaturePackageNames are optional Phase 35 modules. They are not Depends of nodal.
var FeaturePackageNames = []string{
	"nodal-feature-oci",
	"nodal-feature-docker",
	"nodal-feature-gpu",
	"nodal-feature-k8s",
	"nodal-feature-distributed-storage",
	"nodal-feature-ai",
}

// AllowedPackage reports whether name is a No-dal package, never a package-manager verb.
func AllowedPackage(name string) bool {
	n := strings.TrimSpace(name)
	for _, p := range PackageNames {
		if p == n {
			return true
		}
	}
	return AllowedFeaturePackage(n)
}

// AllowedFeaturePackage reports whether name is an optional feature metapackage.
func AllowedFeaturePackage(name string) bool {
	n := strings.TrimSpace(name)
	for _, p := range FeaturePackageNames {
		if p == n {
			return true
		}
	}
	return false
}

// FeatureInstallArgv installs one allowlisted feature package. It never names kubelet.
func FeatureInstallArgv(pkg string, dryRun bool) ([]string, error) {
	if !AllowedFeaturePackage(pkg) {
		return nil, fmt.Errorf("package is not a No-dal feature package")
	}
	argv := []string{"/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false", "-y", "--no-install-recommends"}
	if dryRun {
		argv = append(argv, "--dry-run")
	}
	return append(argv, "install", pkg), nil
}

// FeatureRemoveArgv removes one allowlisted feature package. It does not purge or stop guests.
func FeatureRemoveArgv(pkg string, dryRun bool) ([]string, error) {
	if !AllowedFeaturePackage(pkg) {
		return nil, fmt.Errorf("package is not a No-dal feature package")
	}
	argv := []string{"/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false", "-y"}
	if dryRun {
		argv = append(argv, "--dry-run")
	}
	return append(argv, "remove", pkg), nil
}

// K8sRuntimeArgv starts or stops kubelet via systemd. It is not feature-install.
func K8sRuntimeArgv(start bool) []string {
	action := "stop"
	if start {
		action = "start"
	}
	return []string{"/usr/bin/systemctl", action, "kubelet"}
}

// OSDRuntimeArgv starts or stops ceph-osd.target via systemd. It is not feature-install.
func OSDRuntimeArgv(start bool) []string {
	action := "stop"
	if start {
		action = "start"
	}
	return []string{"/usr/bin/systemctl", action, "ceph-osd.target"}
}

// CheckArgv refreshes signed package indexes. It does not install.
func CheckArgv() []string {
	return []string{"/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false", "update"}
}

// PolicyArgv inspects one allowlisted package without installing.
func PolicyArgv(pkg string) ([]string, error) {
	if !AllowedPackage(pkg) {
		return nil, fmt.Errorf("package is not a No-dal package")
	}
	return []string{"/usr/bin/apt-cache", "policy", pkg}, nil
}

// ApplyArgv upgrades every core package from the signed repo. The core
// packages are named explicitly: installing only the nodal metapackage
// leaves ndl-control and ndl-agent at their old versions when nodal itself
// is already current. Dry-run uses --dry-run.
func ApplyArgv(dryRun bool) []string {
	argv := []string{
		"/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false",
		"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold",
		"-y", "--no-install-recommends",
	}
	if dryRun {
		argv = append(argv, "--dry-run")
	}
	return append(append(argv, "install"), PackageNames...)
}

// ApplyUnit is the transient systemd unit a real apply runs in. Package
// scripts restart ndl-agent and ndl-control during the upgrade; running the
// package manager in its own unit keeps it out of their cgroups, so those
// restarts cannot kill dpkg half way through.
const ApplyUnit = "ndl-platform-update.service"

// ApplyLaunchArgv starts ApplyArgv in ApplyUnit and returns at once.
// RemainAfterExit keeps the unit's result readable after it exits.
func ApplyLaunchArgv() []string {
	argv := []string{
		"/usr/bin/systemd-run", "--unit=" + ApplyUnit,
		"--description=No-dal platform update",
		"--property=RemainAfterExit=yes",
		"--setenv=DEBIAN_FRONTEND=noninteractive",
		"--",
	}
	return append(argv, ApplyArgv(false)...)
}

// ApplyUnitShowArgv reads the state of the last apply unit.
func ApplyUnitShowArgv() []string {
	return []string{"/usr/bin/systemctl", "show", "--property=LoadState,ActiveState,SubState,Result,InvocationID", ApplyUnit}
}

// ApplyUnitStopArgv unloads a finished apply unit so the name can be reused.
func ApplyUnitStopArgv() []string {
	return []string{"/usr/bin/systemctl", "stop", ApplyUnit}
}

// ApplyUnitResetArgv clears a failed apply unit so the name can be reused.
func ApplyUnitResetArgv() []string {
	return []string{"/usr/bin/systemctl", "reset-failed", ApplyUnit}
}

// ApplyUnitLogArgv reads the tail of one apply run's output.
func ApplyUnitLogArgv(invocationID string) ([]string, error) {
	id := strings.TrimSpace(invocationID)
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return nil, fmt.Errorf("invocation id is invalid")
	}
	return []string{"/usr/bin/journalctl", "_SYSTEMD_INVOCATION_ID=" + id, "-n", "40", "--no-pager", "-o", "cat"}, nil
}

// ApplyUnitState is the parsed output of ApplyUnitShowArgv.
type ApplyUnitState struct {
	LoadState    string
	ActiveState  string
	SubState     string
	Result       string
	InvocationID string
}

// ParseApplyUnit reads systemctl show key=value output.
func ParseApplyUnit(out string) ApplyUnitState {
	var st ApplyUnitState
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "LoadState":
			st.LoadState = value
		case "ActiveState":
			st.ActiveState = value
		case "SubState":
			st.SubState = value
		case "Result":
			st.Result = value
		case "InvocationID":
			st.InvocationID = value
		}
	}
	return st
}

// Loaded reports whether a previous apply unit still holds the name.
func (s ApplyUnitState) Loaded() bool {
	return s.LoadState == "loaded" && (s.ActiveState != "inactive" || s.Result != "success")
}

// Running reports whether the package manager is still working.
func (s ApplyUnitState) Running() bool {
	return s.ActiveState == "activating" || s.ActiveState == "deactivating" || (s.ActiveState == "active" && s.SubState == "running")
}

// Succeeded reports a finished apply whose package manager exited 0.
func (s ApplyUnitState) Succeeded() bool {
	return s.ActiveState == "active" && s.SubState == "exited" && s.Result == "success"
}

// Failed reports a finished apply whose package manager did not exit 0.
func (s ApplyUnitState) Failed() bool {
	return s.ActiveState == "failed" || (s.Result != "" && s.Result != "success")
}

// RollbackControlArgv reinstalls a previous ndl-control version. QEMU units are not in this argv.
func RollbackControlArgv(version string, dryRun bool) ([]string, error) {
	v := strings.TrimSpace(version)
	if v == "" || strings.ContainsAny(v, " \n\x00;|&") {
		return nil, fmt.Errorf("version is invalid")
	}
	pkg := "ndl-control=" + v
	argv := []string{"/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false", "-y"}
	if dryRun {
		argv = append(argv, "--dry-run")
	}
	return append(argv, "install", pkg), nil
}

// CheckpointDir is the only directory used for update checkpoints.
const CheckpointDir = "/var/lib/ndl/update-checkpoints"

// CheckpointExcludes are data-directory trees a checkpoint never copies.
// A checkpoint holds control-plane state (config, certificates, secrets,
// container and network definitions) plus a PostgreSQL dump. Workload
// disks, pools, backups, staging, game data, image caches and earlier
// checkpoints are excluded: copying them filled the host disk.
var CheckpointExcludes = []string{
	"var/lib/ndl/storage", "var/lib/ndl/storage-extra",
	"var/lib/ndl/backup-repo", "var/lib/ndl/backups", "var/lib/ndl/backup-staging",
	"var/lib/ndl/migration", "var/lib/ndl/restore-files", "var/lib/ndl/update-checkpoints",
	"var/lib/ndl/gameservers", "var/lib/ndl/game-backups", "var/lib/ndl/cache",
	"var/lib/ndl/runtime/oci", "var/lib/ndl/reserve",
}

// checkpointExcludeGlobs skip disk images wherever they are.
var checkpointExcludeGlobs = []string{"*.qcow2", "*.img", "*.raw", "*.iso", "*.vmdk", "*.vhd", "*.vhdx"}

// CheckpointTarArgv archives control-plane state under /var/lib/ndl to
// dest. It stays on one filesystem, so mounted pools are never read.
// Dest must already be a validated absolute path.
func CheckpointTarArgv(dest string) ([]string, error) {
	if dest == "" || !strings.HasPrefix(dest, "/") || strings.Contains(dest, "..") || strings.ContainsAny(dest, " \n\x00") {
		return nil, fmt.Errorf("checkpoint locator is invalid")
	}
	argv := []string{"/usr/bin/tar", "-C", "/", "--one-file-system"}
	for _, ex := range CheckpointExcludes {
		argv = append(argv, "--exclude="+ex)
	}
	for _, g := range checkpointExcludeGlobs {
		argv = append(argv, "--exclude="+g)
	}
	return append(argv, "-cf", dest, "var/lib/ndl"), nil
}

// PgDumpArgv writes a PostgreSQL dump to dest.
func PgDumpArgv(dest string) ([]string, error) {
	if dest == "" || !strings.HasPrefix(dest, "/") || strings.Contains(dest, "..") || strings.ContainsAny(dest, " \n\x00") {
		return nil, fmt.Errorf("dump locator is invalid")
	}
	return []string{"/usr/bin/pg_dump", "-d", "nodal", "-f", dest}, nil
}

// GRUBPreviousArgv selects the previous kernel for the next reboot. Not used for QEMU guests.
func GRUBPreviousArgv() []string {
	return []string{"/usr/sbin/grub-reboot", "1"}
}
