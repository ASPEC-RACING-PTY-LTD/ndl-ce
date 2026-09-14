package license

import "errors"

const (
	StatusAbsent      = "absent"
	StatusGrace       = "grace"
	StatusUnreachable = "unreachable"
	StatusActive      = "active"
	ActivateConfirm   = "activate-license"
	ClearConfirm      = "clear-license"
	DefaultEndpoint   = "https://license.no-dal.com/v1/activate"
	EditionCE         = "ce"
)

var (
	// ErrUnreachable is a transport or non-success status from the licensing API.
	ErrUnreachable = errors.New("licensing API unreachable")
	// ErrNotEntitled is a 2xx body that is not an accepted entitlement.
	ErrNotEntitled = errors.New("licensing API did not grant entitlement")
)

// Last4 returns a non-secret suffix. Short keys are fully redacted.
func Last4(key string) string {
	if len(key) < 8 {
		if key == "" {
			return ""
		}
		return "****"
	}
	return key[len(key)-4:]
}
