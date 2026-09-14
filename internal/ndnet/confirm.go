package ndnet

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

const confirmSlot = 300

// ConfirmToken is the X-Nodal-Confirm value for a dangerous apply.
func ConfirmToken(secret, userID, kind, ifname string, now time.Time) string {
	slot := now.UTC().Unix() / confirmSlot
	return hex.EncodeToString(confirmMAC(secret, userID, kind, ifname, slot))
}

// ValidConfirm accepts the current or previous 5-minute slot.
func ValidConfirm(secret, userID, kind, ifname, token string, now time.Time) bool {
	if token == "" || ifname == "" {
		return false
	}
	slot := now.UTC().Unix() / confirmSlot
	for _, s := range []int64{slot, slot - 1} {
		want := hex.EncodeToString(confirmMAC(secret, userID, kind, ifname, s))
		if hmac.Equal([]byte(want), []byte(token)) {
			return true
		}
	}
	return false
}

func confirmMAC(secret, userID, kind, ifname string, slot int64) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "net.confirm|%s|%s|%s|%d", userID, kind, ifname, slot)
	return mac.Sum(nil)
}
