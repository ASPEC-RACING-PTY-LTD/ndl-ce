package ctbackup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	GuestHookPre  = "/etc/ndl/hooks/backup-pre"
	GuestHookPost = "/etc/ndl/hooks/backup-post"
	BinLXCAttach  = "/usr/bin/lxc-attach"
)

var (
	hookLXCPath = "/var/lib/ndl/runtime/lxc"
	hookTimeout = 60 * time.Second
	hookRunner  = runGuestHookDefault
)

func runGuestHook(ctx context.Context, unit, rootfs, guestPath string) error {
	return hookRunner(ctx, unit, rootfs, guestPath)
}

// RunGuestHook runs an optional in-guest pre/post backup hook. It never
// freezes, pauses, or stops the guest.
func RunGuestHook(ctx context.Context, unit, rootfs, guestPath string) error {
	return runGuestHook(ctx, unit, rootfs, guestPath)
}

func runGuestHookDefault(ctx context.Context, unit, rootfs, guestPath string) error {
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return nil
	}
	if guestPath != GuestHookPre && guestPath != GuestHookPost {
		return fmt.Errorf("unsupported backup hook")
	}
	if err := ValidateFreezeUnit(unit); err != nil {
		return fmt.Errorf("backup hook unit: %w", err)
	}
	hostPath := filepath.Join(rootfs, filepath.FromSlash(strings.TrimPrefix(guestPath, "/")))
	if !underRootfs(rootfs, hostPath) {
		return fmt.Errorf("backup hook path is invalid")
	}
	st, err := os.Stat(hostPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.IsDir() || st.Mode()&0o111 == 0 {
		return nil
	}
	id := freezeUnitID(unit)
	if id == "" {
		return fmt.Errorf("backup hook unit is invalid")
	}
	runCtx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, BinLXCAttach, "-P", hookLXCPath, "-n", id, "--clear-env", "--", guestPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("guest hook %s: %w", guestPath, err)
		}
		return fmt.Errorf("guest hook %s: %w: %s", guestPath, err, msg)
	}
	return nil
}

func underRootfs(rootfs, path string) bool {
	root := filepath.Clean(rootfs)
	clean := filepath.Clean(path)
	if clean == root {
		return false
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(clean, root+sep)
}
