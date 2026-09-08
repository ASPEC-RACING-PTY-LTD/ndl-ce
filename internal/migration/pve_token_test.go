package migration

import (
	"strings"
	"testing"
)

func TestValidatePVEToken(t *testing.T) {
	t.Parallel()
	if err := ValidatePVEToken("root@pam!nodal=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"); err != nil {
		t.Fatalf("full token: %v", err)
	}
	if err := ValidatePVEToken("  user@pve!backup=secret  "); err != nil {
		t.Fatalf("trimmed token: %v", err)
	}

	secret := ValidatePVEToken("SECRET-TOKEN-VALUE")
	if secret == nil || !strings.Contains(secret.Error(), "not the secret alone") ||
		!strings.Contains(secret.Error(), PVETokenFormat) || !strings.Contains(secret.Error(), PVETokenExample) {
		t.Fatalf("secret alone: %v", secret)
	}
	uuidOnly := ValidatePVEToken("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if uuidOnly == nil || !strings.Contains(uuidOnly.Error(), "not the secret alone") {
		t.Fatalf("uuid alone: %v", uuidOnly)
	}
	empty := ValidatePVEToken("  ")
	if empty == nil || !strings.Contains(empty.Error(), "required") {
		t.Fatalf("empty: %v", empty)
	}
	partial := ValidatePVEToken("root@pam")
	if partial == nil || strings.Contains(partial.Error(), "not the secret alone") ||
		!strings.Contains(partial.Error(), PVETokenFormat) {
		t.Fatalf("partial user@realm: %v", partial)
	}
}
