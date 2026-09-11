package backuppack

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

const (
	FormatNDLB    = "ndlb"
	Version       = 1
	LayoutRoot    = "backups"
	ManifestName  = "manifest.json"
	ConfigName    = "config.json"
	ChecksumsName = "checksums.json"
	ChunkDir      = "chunks"
	PayloadTar    = "tar"
	PayloadZFS    = "zfs-send"
	PayloadQCOW2  = "qcow2"
	MaxNameLen    = 64
	shortIDLen    = 8
)

// SanitizeName turns a workload hostname/name into a single object-key path
// element. Letters, digits, dot, underscore, and hyphen are kept. Everything
// else becomes a hyphen. Empty results become "workload".
func SanitizeName(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "workload"
	}
	if len(out) > MaxNameLen {
		out = strings.Trim(out[:MaxNameLen], "-.")
	}
	if out == "" {
		return "workload"
	}
	return out
}

// UniqueName is a human-readable prefix for one workload. Duplicate sanitized
// names get a stable 8-hex suffix from the workload UUID so the layout stays
// readable instead of becoming UUID-only paths.
func UniqueName(name, workloadID string, others []string) string {
	base := SanitizeName(name)
	id := shortID(workloadID)
	for _, other := range others {
		if strings.TrimSpace(other) == "" {
			continue
		}
		if SanitizeName(other) == base {
			if id == "" {
				return base
			}
			return base + "-" + id
		}
	}
	return base
}

func shortID(workloadID string) string {
	hex := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(workloadID)), "-", "")
	if len(hex) < shortIDLen {
		return hex
	}
	return hex[:shortIDLen]
}

// BackupStamp is a UTC backup id used in object keys. It is not the catalog
// artifact UUID. Same-second copies append a short artifact suffix.
func BackupStamp(t time.Time, artifactID string) string {
	if t.IsZero() {
		t = time.Now()
	}
	stamp := t.UTC().Format("2006-01-02T15-04-05Z")
	_ = artifactID
	return stamp
}

// ObjectPrefix is the pack directory under an optional repository prefix.
// Example: backups/SoundDock/2026-09-12T05-40-00Z
func ObjectPrefix(repoPrefix, workloadName, stamp string) string {
	repoPrefix = strings.Trim(strings.TrimSpace(repoPrefix), "/")
	name := SanitizeName(workloadName)
	stamp = strings.Trim(strings.TrimSpace(stamp), "/")
	if stamp == "" {
		stamp = BackupStamp(time.Now(), "")
	}
	parts := []string{LayoutRoot, name, stamp}
	if repoPrefix == "" {
		return strings.Join(parts, "/")
	}
	return repoPrefix + "/" + strings.Join(parts, "/")
}

func Join(prefix, name string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	name = strings.Trim(strings.TrimSpace(name), "/")
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

func ManifestKey(prefix string) string  { return Join(prefix, ManifestName) }
func ConfigKey(prefix string) string    { return Join(prefix, ConfigName) }
func ChecksumsKey(prefix string) string { return Join(prefix, ChecksumsName) }
func ChunkKey(prefix string, i int) string {
	return Join(prefix, fmt.Sprintf("%s/%06d", ChunkDir, i))
}
