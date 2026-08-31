package debian

// Package implements the Debian 13 host-platform boundary.
// Apt, repository, and networkd persistence belong here in later
// phases. Do not call this package from workload or API models.

const (
	ID             = "debian"
	VersionID      = "13"
	Architecture   = "amd64"
	Family         = "debian"
	PackageTool    = "apt"
	NetworkPersist = "systemd-networkd"
)

// Is reports whether id/version match Debian 13.
func Is(id, versionID string) bool {
	return id == ID && versionID == VersionID
}

// Capabilities lists Debian-specific host-platform flags.
// Shared Linux primitives such as systemd workload units stay shared.
func Capabilities() []string {
	return []string{PackageTool, NetworkPersist}
}
