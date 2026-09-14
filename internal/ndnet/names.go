package ndnet

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// BridgeName returns the Linux locator for a network UUID.
// The UUID remains the desired identity.
func BridgeName(networkID string) (string, error) {
	id, err := uuid.Parse(strings.TrimSpace(networkID))
	if err != nil {
		return "", fmt.Errorf("network id must be a UUID")
	}
	hex := strings.ReplaceAll(id.String(), "-", "")
	return "ndl" + hex[:8], nil
}

// ValidIfName reports whether an interface name is a safe locator.
func ValidIfName(name string) bool {
	if name == "" || len(name) > 15 {
		return false
	}
	for i, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		if r == '-' || r == '_' || r == '.' {
			if i == 0 {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func ValidKind(kind string) bool {
	switch kind {
	case KindIsolated, KindIsolatedNAT, KindLANBridge:
		return true
	default:
		return false
	}
}

func Isolated(kind string) bool {
	return kind == KindIsolated || kind == KindIsolatedNAT
}
