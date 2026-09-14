package migration

import (
	"fmt"
	"strings"
)

const (
	// PVETokenExample is the operator-visible form Proxmox shows once.
	PVETokenExample = "root@pam!nodal=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
	// PVETokenFormat is user@realm!tokenid=secret.
	PVETokenFormat = "user@realm!tokenid=secret"
)

// ValidatePVEToken requires the full PVEAPIToken value. The UUID secret
// alone is not accepted because Authorization: PVEAPIToken= needs the
// user@realm!tokenid prefix.
func ValidatePVEToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("proxmox API token is required as %s. Example: %s", PVETokenFormat, PVETokenExample)
	}
	at := strings.Index(token, "@")
	bang := strings.Index(token, "!")
	eq := strings.LastIndex(token, "=")
	if at < 1 || bang < at+2 || eq < bang+2 || eq == len(token)-1 {
		if at < 0 && bang < 0 {
			return fmt.Errorf("proxmox API token must be %s, not the secret alone. Example: %s", PVETokenFormat, PVETokenExample)
		}
		return fmt.Errorf("proxmox API token must be %s. Example: %s", PVETokenFormat, PVETokenExample)
	}
	user := token[:at]
	realm := token[at+1 : bang]
	id := token[bang+1 : eq]
	secret := token[eq+1:]
	if strings.ContainsAny(user, " \t") || strings.ContainsAny(realm, " \t@!") ||
		strings.ContainsAny(id, " \t") || strings.ContainsAny(secret, " \t") {
		return fmt.Errorf("proxmox API token must be %s. Example: %s", PVETokenFormat, PVETokenExample)
	}
	if realm == "" || id == "" || secret == "" {
		return fmt.Errorf("proxmox API token must be %s. Example: %s", PVETokenFormat, PVETokenExample)
	}
	return nil
}
