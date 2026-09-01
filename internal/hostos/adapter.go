package hostos

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/hostos/debian"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

// Adapter is the host-platform boundary. Shared Linux primitives stay
// shared. A later distribution is one adapter plus tests, not a
// control-plane rewrite. Workload and storage APIs must not grow
// distro-only fields.
type Adapter interface {
	ID() string
	VersionID() string
	Family() string
	SupportTier() string
	Qualified() bool
	PackageTool() string
	NetworkPersist() string
	FirewallKind() string
	KernelModules() string
	GPUDrivers() string
	BootloaderRollback() string
	Gaps() []string
	NetworkFiles(plan ndnet.Plan) []ndnet.File
	WriteNetwork(host Platform, destDir string, files []ndnet.File) error
}

// Lookup selects the adapter for a detected host. Unqualified adapters
// exist so tests can prove they refuse foreign persistence. They are
// not a support claim.
func Lookup(p Platform) Adapter {
	if debian.Is(p.ID, p.VersionID) && normalizeArch(p.Architecture) == "amd64" {
		return DebianAdapter{}
	}
	if strings.EqualFold(p.ID, "ubuntu") {
		return UbuntuAdapter{Version: p.VersionID, Arch: normalizeArch(p.Architecture)}
	}
	return UnsupportedAdapter{Platform: p}
}

// DebianAdapter is the Tier 1 Debian 13 amd64 host.
type DebianAdapter struct{}

func (DebianAdapter) ID() string                 { return debian.ID }
func (DebianAdapter) VersionID() string          { return debian.VersionID }
func (DebianAdapter) Family() string             { return debian.Family }
func (DebianAdapter) SupportTier() string        { return Tier1 }
func (DebianAdapter) Qualified() bool            { return true }
func (DebianAdapter) PackageTool() string        { return debian.PackageTool }
func (DebianAdapter) NetworkPersist() string     { return debian.NetworkPersist }
func (DebianAdapter) FirewallKind() string       { return "nftables" }
func (DebianAdapter) KernelModules() string      { return "debian-kernel" }
func (DebianAdapter) GPUDrivers() string         { return "optional-nvidia" }
func (DebianAdapter) BootloaderRollback() string { return "checkpoint-tar" }
func (DebianAdapter) Gaps() []string             { return nil }
func (DebianAdapter) NetworkFiles(plan ndnet.Plan) []ndnet.File {
	return debian.NetworkdFiles(plan)
}

func (DebianAdapter) WriteNetwork(host Platform, destDir string, files []ndnet.File) error {
	if !debian.Is(host.ID, host.VersionID) {
		return fmt.Errorf("debian adapter must not write network persistence on %s %s", host.ID, host.VersionID)
	}
	return writeOwnedNetworkd(destDir, files)
}

// UnsupportedAdapter refuses enroll and host mutation.
type UnsupportedAdapter struct {
	Platform Platform
}

func (u UnsupportedAdapter) ID() string               { return u.Platform.ID }
func (u UnsupportedAdapter) VersionID() string        { return u.Platform.VersionID }
func (u UnsupportedAdapter) Family() string           { return u.Platform.Family }
func (UnsupportedAdapter) SupportTier() string        { return Unsupported }
func (UnsupportedAdapter) Qualified() bool            { return false }
func (UnsupportedAdapter) PackageTool() string        { return "" }
func (UnsupportedAdapter) NetworkPersist() string     { return "" }
func (UnsupportedAdapter) FirewallKind() string       { return "" }
func (UnsupportedAdapter) KernelModules() string      { return "" }
func (UnsupportedAdapter) GPUDrivers() string         { return "" }
func (UnsupportedAdapter) BootloaderRollback() string { return "" }
func (u UnsupportedAdapter) Gaps() []string {
	return []string{"host is not a qualified No-dal platform"}
}
func (UnsupportedAdapter) NetworkFiles(ndnet.Plan) []ndnet.File { return nil }
func (u UnsupportedAdapter) WriteNetwork(Platform, string, []ndnet.File) error {
	return Error{Platform: u.Platform}
}

func writeOwnedNetworkd(destDir string, files []ndnet.File) error {
	if destDir == "" {
		destDir = debian.NetworkDir
	}
	for _, file := range files {
		base := filepath.Base(file.RelPath)
		if !debian.Owned(base) {
			return fmt.Errorf("refusing to write unmanaged networkd file")
		}
	}
	return nil
}
