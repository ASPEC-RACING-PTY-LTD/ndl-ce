package inventory

import (
	"os"

	"github.com/no-dal/ndl-ce/internal/hostos"
)

func collectHost(opt Options) Host {
	fs := opt.fs()
	f, err := os.Open(fs.join("etc/os-release"))
	if err != nil {
		return Host{Status: StatusUnavailable}
	}
	defer f.Close()

	p, err := hostos.DetectFrom(f, opt.arch())
	if p.ID == "" {
		if err != nil {
			return Host{Status: StatusUnavailable, Note: "os-release not parsed"}
		}
		return Host{Status: StatusUnavailable}
	}
	return Host{
		Status:       StatusAvailable,
		ID:           p.ID,
		VersionID:    p.VersionID,
		Family:       p.Family,
		Architecture: p.Architecture,
		PrettyName:   p.PrettyName,
		SupportTier:  p.SupportTier,
		Capabilities: p.Capabilities,
	}
}
