package hostos

import "strings"

// SupportedHost is one explicitly qualified No-dal HOST platform.
type SupportedHost struct {
	ID           string
	VersionID    string
	Architecture string
	Tier         string
}

// supported is the Phase 0 list. Ubuntu LTS is added only in Phase 29.
var supported = []SupportedHost{
	{ID: "debian", VersionID: "13", Architecture: "amd64", Tier: Tier1},
}

// Supported returns a copy of the current host support list.
func Supported() []SupportedHost {
	out := make([]SupportedHost, len(supported))
	copy(out, supported)
	return out
}

// SupportedSummary is a human list for error messages.
func SupportedSummary() string {
	parts := make([]string, 0, len(supported))
	for _, h := range supported {
		parts = append(parts, h.ID+" "+h.VersionID+" "+h.Architecture+" ("+h.Tier+")")
	}
	return strings.Join(parts, ", ")
}

// IsSupported reports whether p is on the current host support list.
func IsSupported(p Platform) bool {
	arch := normalizeArch(p.Architecture)
	for _, h := range supported {
		if p.ID == h.ID && p.VersionID == h.VersionID && arch == h.Architecture {
			return true
		}
	}
	return false
}
