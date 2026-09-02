package hostos

import (
	"fmt"

	"github.com/no-dal/ndl-ce/internal/hostos/ubuntu"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

// UbuntuAdapter exists so a later qualification can enable Tier 1 without
// rewriting the control plane. It is not a supported host today.
type UbuntuAdapter struct {
	Version string
	Arch    string
}

func (UbuntuAdapter) ID() string                 { return ubuntu.ID }
func (u UbuntuAdapter) VersionID() string        { return firstNonEmpty(u.Version, ubuntu.LTS) }
func (UbuntuAdapter) Family() string             { return ubuntu.Family }
func (UbuntuAdapter) SupportTier() string        { return Unsupported }
func (UbuntuAdapter) Qualified() bool            { return false }
func (UbuntuAdapter) PackageTool() string        { return ubuntu.PackageTool }
func (UbuntuAdapter) NetworkPersist() string     { return ubuntu.NetworkPersist }
func (UbuntuAdapter) FirewallKind() string       { return "nftables" }
func (UbuntuAdapter) KernelModules() string      { return "unqualified" }
func (UbuntuAdapter) GPUDrivers() string         { return "unqualified" }
func (UbuntuAdapter) BootloaderRollback() string { return "unqualified" }
func (UbuntuAdapter) Gaps() []string             { return append([]string(nil), ubuntu.QualificationGaps...) }
func (UbuntuAdapter) NetworkFiles(ndnet.Plan) []ndnet.File {
	return nil
}

func (u UbuntuAdapter) WriteNetwork(host Platform, destDir string, _ []ndnet.File) error {
	if err := ubuntu.RefuseDebianNetworkd(host.ID, destDir); err != nil {
		return err
	}
	return fmt.Errorf("ubuntu LTS is not a qualified No-dal host")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
