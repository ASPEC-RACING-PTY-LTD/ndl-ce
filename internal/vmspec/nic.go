package vmspec

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// MACFromID returns a stable locally-administered unicast MAC.
// Restart and spec edits that keep the NIC must not change it.
func MACFromID(stable string) string {
	var b [6]byte
	if u, err := uuid.Parse(strings.TrimSpace(stable)); err == nil {
		copy(b[:], u[:6])
	} else {
		sum := sha256.Sum256([]byte(strings.TrimSpace(stable)))
		copy(b[:], sum[:6])
	}
	b[0] = (b[0] & 0xfe) | 0x02
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

// TAPName is a derived Linux locator. It is not product identity.
// IFNAMSIZ is 16 including the trailing NUL, so the name is at most 15 bytes.
func TAPName(workloadID string, index int) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(workloadID) + ":" + fmt.Sprintf("%d", index)))
	return "nv" + fmt.Sprintf("%x", sum[:6])
}

func PersistNICs(workloadID string, spec Spec) Spec {
	for i := range spec.NICs {
		if spec.NICs[i].ID == "" {
			spec.NICs[i].ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.TrimSpace(workloadID)+fmt.Sprintf(":nic:%d", i))).String()
		}
		if spec.NICs[i].MAC == "" {
			spec.NICs[i].MAC = MACFromID(spec.NICs[i].ID)
		}
		if spec.NICs[i].Model == "" {
			spec.NICs[i].Model = NICModelVirtio
		}
	}
	return spec
}
