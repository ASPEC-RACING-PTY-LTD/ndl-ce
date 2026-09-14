package cluster

import (
	"strings"
	"unicode"
)

const (
	RoleControl = "control"
	RoleWorker  = "worker"
)

// UniqueNodeName returns a cluster-unique display name. Hostname is a locator, not identity.
func UniqueNodeName(hostname, nodeID string, taken map[string]struct{}) string {
	base := SanitizeName(hostname)
	if base == "" {
		base = RoleWorker
	}
	if taken == nil || !nameTaken(taken, base) {
		return base
	}
	suffix := strings.ReplaceAll(nodeID, "-", "")
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	if suffix == "" {
		suffix = "node"
	}
	return base + "-" + suffix
}

// SanitizeName keeps a short hostname-like locator for display names.
func SanitizeName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if i := strings.Index(raw, "."); i > 0 {
		raw = raw[:i]
	}
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

func nameTaken(taken map[string]struct{}, name string) bool {
	_, ok := taken[name]
	return ok
}
