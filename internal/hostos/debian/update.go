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
	argv, _ := InstallArgv(dryRun, "", false)
	return argv
}

// InstallArgv installs every core package. With a version, each package is
// pinned to it, so apply installs exactly what preflight checked and rollback
// moves all packages together (nodal depends on equal versions of the rest).
func InstallArgv(dryRun bool, version string, allowDowngrades bool) ([]string, error) {
	v := strings.TrimSpace(version)
	if v != "" && !ValidVersion(v) {
		return nil, fmt.Errorf("version is invalid")
	}
	argv := []string{
		"/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false",
		"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold",
		"-y", "--no-install-recommends",
	}
	if allowDowngrades {
		argv = append(argv, "--allow-downgrades")
	}
	if dryRun {
		argv = append(argv, "--dry-run")
	}
	argv = append(argv, "install")
	for _, name := range PackageNames {
		if v != "" {
			name += "=" + v
		}
		argv = append(argv, name)
	}
	return argv, nil
}

// ValidVersion accepts Debian version strings and nothing a shell or apt
// could read as an option or a second argument.
func ValidVersion(v string) bool {
	if v == "" || len(v) > 64 || strings.HasPrefix(v, "-") {
		return false
	}
	for _, r := range v {
		ok := r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || strings.ContainsRune(".+~:-", r)
		if !ok {
			return false
		}
	}
	return true
}

// ApplyUnit is the transient systemd unit a real apply runs in. Package
// scripts restart ndl-agent and ndl-control during the upgrade; running the
// package manager in its own unit keeps it out of their cgroups, so those
// restarts cannot kill dpkg half way through.
const ApplyUnit = "ndl-platform-update.service"

// ApplyLaunchArgv starts ApplyArgv in ApplyUnit and returns at once.
// RemainAfterExit keeps the unit's result readable after it exits.
func ApplyLaunchArgv() []string {
	argv, _ := ApplyLaunchVersionArgv("")
	return argv
}

// ApplyLaunchVersionArgv is ApplyLaunchArgv pinned to one version.
func ApplyLaunchVersionArgv(version string) ([]string, error) {
	install, err := InstallArgv(false, version, false)
	if err != nil {
		return nil, err
	}
	argv := []string{
		"/usr/bin/systemd-run", "--unit=" + ApplyUnit,
		"--description=No-dal platform update",
		"--property=RemainAfterExit=yes",
		"--setenv=DEBIAN_FRONTEND=noninteractive",
		"--",
	}
	return append(argv, install...), nil
}

// ControlUnits are stopped while a rollback restores the database, so
// nothing writes to it, and started again whatever the outcome.
var ControlUnits = []string{"ndl-control.socket", "ndl-control.service"}

// RollbackLaunchArgv moves every core package back to version in ApplyUnit.
// With a dump, the control plane is stopped and the database is replaced by
// the dump in one transaction before the packages are downgraded: a failed
// restore leaves the database untouched. The control plane is started again
// after success (ExecStartPost) and after failure (ExecStopPost).
func RollbackLaunchArgv(version, dump string) ([]string, error) {
	install, err := InstallArgv(false, version, true)
	if err != nil {
		return nil, err
	}
	start := "/usr/bin/systemctl start " + strings.Join(ControlUnits, " ")
	argv := []string{
		"/usr/bin/systemd-run", "--unit=" + ApplyUnit,
		"--description=No-dal platform rollback",
		"--property=RemainAfterExit=yes",
		"--setenv=DEBIAN_FRONTEND=noninteractive",
		"--property=ExecStartPost=" + start,
		"--property=ExecStopPost=" + start,
	}
	if dump != "" {
		if !safeLocator(dump) {
			return nil, fmt.Errorf("dump locator is invalid")
		}
		argv = append(argv,
			"--property=ExecStartPre=/usr/bin/systemctl stop "+strings.Join(ControlUnits, " "),
			"--property=ExecStartPre="+strings.Join(RestoreDumpArgv(dump), " "),
		)
	}
	return append(append(argv, "--"), install...), nil
}

// RestoreDumpArgv replaces the nodal database with a checkpoint dump as its
// owner. DROP OWNED and the dump run in one transaction.
func RestoreDumpArgv(dump string) []string {
	return []string{
		"/usr/sbin/runuser", "-u", DBOwner, "--", "/usr/bin/psql", "-X", "-q",
		"-v", "ON_ERROR_STOP=1", "--single-transaction", "-d", DBName,
		"-c", "\"DROP OWNED BY CURRENT_USER\"", "-f", dump,
	}
}

// DBName and DBOwner are the control-plane database and the role that owns
// it. PostgreSQL uses peer authentication, so dumps and restores run as the
// owner: root has no database role.
const (
	DBName  = "nodal"
	DBOwner = "ndl-control"
)

func safeLocator(p string) bool {
	return p != "" && strings.HasPrefix(p, "/") && !strings.Contains(p, "..") && !strings.ContainsAny(p, " \n\x00\"'\\;")
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

// RollbackDryRunArgv checks that every core package can move to version
// without changing anything. QEMU units are not in this argv.
func RollbackDryRunArgv(version string) ([]string, error) {
	if !ValidVersion(strings.TrimSpace(version)) {
		return nil, fmt.Errorf("version is invalid")
	}
	return InstallArgv(true, version, true)
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

// PgDumpArgv writes a plain SQL dump of the control-plane database to dest
// as the database owner. Dest must already exist and belong to the owner
// (DumpFileArgv), because the checkpoint directory belongs to root.
func PgDumpArgv(dest string) ([]string, error) {
	if !safeLocator(dest) {
		return nil, fmt.Errorf("dump locator is invalid")
	}
	return []string{
		"/usr/sbin/runuser", "-u", DBOwner, "--",
		"/usr/bin/pg_dump", "--no-owner", "--no-privileges", "-d", DBName, "-f", dest,
	}, nil
}

// DumpFileArgv creates an empty dump file the database owner may write.
func DumpFileArgv(dest string) ([]string, error) {
	if !safeLocator(dest) {
		return nil, fmt.Errorf("dump locator is invalid")
	}
	return []string{"/usr/bin/install", "-o", DBOwner, "-g", DBOwner, "-m", "0600", "/dev/null", dest}, nil
}

// PrivateFileArgv makes a checkpoint archive readable by root only: it holds
// host keys and secrets.
func PrivateFileArgv(path string) ([]string, error) {
	if !safeLocator(path) {
		return nil, fmt.Errorf("locator is invalid")
	}
	return []string{"/usr/bin/chmod", "0600", path}, nil
}

// GRUBPreviousArgv selects the previous kernel for the next reboot. Not used for QEMU guests.
func GRUBPreviousArgv() []string {
	return []string{"/usr/sbin/grub-reboot", "1"}
}
