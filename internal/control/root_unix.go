//go:build unix

package control

import (
	"fmt"
	"os"
)

// RefuseRoot keeps the control plane unprivileged.
func RefuseRoot() error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("ndl-control must not run as root")
	}
	return nil
}
