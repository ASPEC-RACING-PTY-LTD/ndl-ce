package license

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
