package lxc

import (
	"crypto/sha256"
	"fmt"
	"net"
	"strings"

	"github.com/google/uuid"
)

// NormalizeMAC accepts colon, hyphen, or dotted hardware addresses and
// returns lowercase colon form. Multicast and non-EUI-48 values are rejected.
func NormalizeMAC(raw string) (string, error) {
	mac, err := net.ParseMAC(strings.TrimSpace(raw))
	if err != nil || len(mac) != 6 {
		return "", fmt.Errorf("mac must be six octets")
	}
	if mac[0]&0x01 != 0 {
		return "", fmt.Errorf("mac must be unicast")
	}
	return mac.String(), nil
}

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
