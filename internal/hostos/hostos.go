package hostos

import (
	"fmt"
	"io"
	"strings"

	"github.com/no-dal/ndl-ce/internal/hostos/debian"
	"github.com/no-dal/ndl-ce/internal/linux"
)

// Support tiers. Experimental is reserved and is not the default.
const (
	Tier1       = "tier1"
	Unsupported = "unsupported"
)

// Platform is the detected host operating system. This is the No-dal
// HOST OS, not a VM guest, system-container image, or OCI image.
type Platform struct {
	ID           string
	VersionID    string
	Family       string
	Architecture string
	PrettyName   string
	SupportTier  string
	Capabilities []string
}

func (p Platform) String() string {
	if p.PrettyName != "" {
		return p.PrettyName
	}
	return strings.TrimSpace(p.ID + " " + p.VersionID)
}

// Error is returned when the host is not a supported No-dal host.
type Error struct {
	Platform Platform
}

func (e Error) Error() string {
	return fmt.Sprintf(
		"No-dal does not currently support this host platform (%s, %s). Currently supported host platforms: %s",
		e.Platform.String(),
		e.Platform.Architecture,
		SupportedSummary(),
	)
}

// DetectFrom parses os-release content and a kernel architecture.
func DetectFrom(r io.Reader, arch string) (Platform, error) {
	rel, err := linux.ParseOSRelease(r)
	if err != nil {
		return Platform{}, err
	}
	p := Platform{
		ID:           rel.ID,
		VersionID:    rel.VersionID,
		Family:       familyOf(rel),
		Architecture: normalizeArch(arch),
		PrettyName:   rel.PrettyName,
	}
	if IsSupported(p) {
		p.SupportTier = Tier1
		if debian.Is(p.ID, p.VersionID) {
			p.Capabilities = debian.Capabilities()
		}
		return p, nil
	}
	p.SupportTier = Unsupported
	return p, Error{Platform: p}
}

func familyOf(rel linux.OSRelease) string {
	if rel.ID == "debian" {
		return "debian"
	}
	for _, like := range rel.IDLike {
		if like == "debian" {
			return "debian"
		}
	}
	if rel.ID != "" {
		return rel.ID
	}
	return "unknown"
}

func normalizeArch(arch string) string {
	switch strings.ToLower(arch) {
	case "x86_64", "amd64":
		return "amd64"
	default:
		return strings.ToLower(arch)
	}
}
