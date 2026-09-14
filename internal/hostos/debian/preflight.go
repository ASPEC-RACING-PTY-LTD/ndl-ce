package debian

import (
	"os"
	"strings"
)

// Policy is one apt-cache policy parse. It is not exposed on the public API.
type Policy struct {
	Installed string
	Candidate string
}

// ParsePolicy reads Installed and Candidate lines from apt-cache policy output.
func ParsePolicy(out string) Policy {
	var p Policy
	for _, line := range strings.Split(out, "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "Installed:"):
			p.Installed = strings.TrimSpace(strings.TrimPrefix(trim, "Installed:"))
		case strings.HasPrefix(trim, "Candidate:"):
			p.Candidate = strings.TrimSpace(strings.TrimPrefix(trim, "Candidate:"))
		}
	}
	if p.Installed == "(none)" {
		p.Installed = ""
	}
	if p.Candidate == "(none)" {
		p.Candidate = ""
	}
	return p
}

// ObserveRuntime reports kernel, ZFS module, and NVIDIA presence from sysfs/proc.
// Missing optional hardware is a warning, not a fake success.
func ObserveRuntime() (kernel, zfs, nvidia bool) {
	if _, err := os.Stat("/proc/version"); err == nil {
		kernel = true
	}
	if _, err := os.Stat("/sys/module/zfs"); err == nil {
		zfs = true
	}
	if _, err := os.Stat("/proc/driver/nvidia"); err == nil {
		nvidia = true
	}
	return kernel, zfs, nvidia
}

// CheckpointTarPath is the tar locator for a checkpoint id.
func CheckpointTarPath(id string) string {
	return CheckpointDir + "/" + strings.TrimSpace(id) + ".tar"
}

// CheckpointDumpPath is the PostgreSQL dump locator for a checkpoint id.
func CheckpointDumpPath(id string) string {
	return CheckpointDir + "/" + strings.TrimSpace(id) + ".sql"
}

// ChangelogArgv reads the nodal metapackage changelog from the signed repo.
func ChangelogArgv() []string {
	return []string{"/usr/bin/apt-get", "-qq", "changelog", "nodal"}
}

// MkdirCheckpointArgv creates the checkpoint directory. Dest is fixed.
func MkdirCheckpointArgv() []string {
	return []string{"/usr/bin/mkdir", "-p", CheckpointDir}
}
