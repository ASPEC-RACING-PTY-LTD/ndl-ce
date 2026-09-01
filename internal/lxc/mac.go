package lxc

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// MACFromUUID returns a stable locally-administered unicast MAC derived from id.
func MACFromUUID(id string) string {
	var b [6]byte
	if u, err := uuid.Parse(strings.TrimSpace(id)); err == nil {
		copy(b[:], u[:6])
	} else {
		sum := sha256.Sum256([]byte(strings.TrimSpace(id)))
		copy(b[:], sum[:6])
	}
	b[0] = (b[0] & 0xfe) | 0x02
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}
