package lxc

import (
	"fmt"
	"strings"
)

// AllowedPins are official images.linuxcontainers.org products.
var AllowedPins = []string{
	"alpine/3.21/amd64/default",
	"alpine/3.20/amd64/default",
	"debian/trixie/amd64/default",
	"debian/bookworm/amd64/default",
}

var allowedPinSet = func() map[string]struct{} {
	out := make(map[string]struct{}, len(AllowedPins))
	for _, p := range AllowedPins {
		out[p] = struct{}{}
	}
	return out
}()

// ValidatePin accepts only the Phase 5 allowlist.
func ValidatePin(pin string) error {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return fmt.Errorf("image pin is required")
	}
	if _, ok := allowedPinSet[pin]; !ok {
		return fmt.Errorf("image pin %q is not in the allowlist", pin)
	}
	return nil
}

// ProductKey is the simplestreams product name for a pin.
func ProductKey(pin string) string {
	return strings.ReplaceAll(strings.TrimSpace(pin), "/", ":")
}
