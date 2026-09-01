package debian

import (
	"fmt"
	"strings"
)

const (
	UpdateCheck      = "check"
	UpdatePreflight  = "preflight"
	UpdateCheckpoint = "checkpoint"
	UpdateApply      = "apply"
	UpdateRollback   = "rollback"
	ChannelStable    = "stable"
	UnsupportedHost  = "Platform updates use the Debian 13 adapter and the signed nodal repository. This host is not Debian 13 amd64."
)

// PackageNames are the only packages the update adapter may mention.
var PackageNames = []string{"ndl-control", "ndl-agent", "ndl-ui", "nodal", "nodalctl"}

// AllowedPackage reports whether name is a No-dal package, never a package-manager verb.
func AllowedPackage(name string) bool {
	n := strings.TrimSpace(name)
	for _, p := range PackageNames {
		if p == n {
			return true
		}
	}
	return false
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

// ApplyArgv installs the nodal metapackage from the signed repo. Dry-run uses --dry-run.
func ApplyArgv(dryRun bool) []string {
	argv := []string{"/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false", "-y", "--no-install-recommends"}
	if dryRun {
		argv = append(argv, "--dry-run")
	}
	return append(argv, "install", "nodal")
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

// CheckpointTarArgv archives /var/lib/ndl to dest. Dest must already be a validated absolute path.
func CheckpointTarArgv(dest string) ([]string, error) {
	if dest == "" || !strings.HasPrefix(dest, "/") || strings.Contains(dest, "..") || strings.ContainsAny(dest, " \n\x00") {
		return nil, fmt.Errorf("checkpoint locator is invalid")
	}
	return []string{"/usr/bin/tar", "-C", "/", "-cf", dest, "var/lib/ndl"}, nil
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
