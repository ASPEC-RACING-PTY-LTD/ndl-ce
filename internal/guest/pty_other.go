//go:build !linux

package guest

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

func openGuestPTY(context.Context, string) (ptySession, error) {
	return nil, fmt.Errorf("guest PTY is not implemented on %s", runtime.GOOS)
}

func guestShutdown(mode string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("guest shutdown is not implemented on %s", runtime.GOOS)
	}
	args := []string{"/s", "/t", "0"}
	if mode == "reboot" {
		args = []string{"/r", "/t", "0"}
	}
	cmd := exec.Command("shutdown", args...)
	return cmd.Start()
}
