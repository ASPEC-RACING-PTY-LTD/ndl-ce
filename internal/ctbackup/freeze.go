package ctbackup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	cgroupSlice  = "/sys/fs/cgroup/system.slice"
	freezeUnitRe = regexp.MustCompile(`^nodal-ct@[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\.service$`)
)

// ParseFreezeUnit returns the systemd unit encoded in archive:<unit>, or empty.
func ParseFreezeUnit(action string) (string, error) {
	act := strings.TrimSpace(action)
	if act == "" || act == "archive" {
		return "", nil
	}
	if !strings.HasPrefix(act, "archive:") {
		return "", fmt.Errorf("unsupported backup action")
	}
	unit := strings.TrimSpace(strings.TrimPrefix(act, "archive:"))
	if unit == "" {
		return "", nil
	}
	if err := ValidateFreezeUnit(unit); err != nil {
		return "", err
	}
	return unit, nil
}

// ParseSyncTree returns the systemd unit encoded in sync-tree:<unit>, or empty.
func ParseSyncTree(action string) (string, error) {
	act := strings.TrimSpace(action)
	if act == "" || act == "sync-tree" {
		return "", nil
	}
	if !strings.HasPrefix(act, "sync-tree:") {
		return "", fmt.Errorf("unsupported backup action")
	}
	unit := strings.TrimSpace(strings.TrimPrefix(act, "sync-tree:"))
	if unit == "" {
		return "", nil
	}
	if err := ValidateFreezeUnit(unit); err != nil {
		return "", err
	}
	return unit, nil
}

// ValidateFreezeUnit allows only nodal-ct@<uuid>.service names.
func ValidateFreezeUnit(unit string) error {
	if !freezeUnitRe.MatchString(strings.TrimSpace(unit)) {
		return fmt.Errorf("freeze unit is not a nodal-ct service")
	}
	return nil
}

func freezeUnitIfPresent(unit string, required bool) (func(), error) {
	nop := func() {}
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return nop, nil
	}
	if err := ValidateFreezeUnit(unit); err != nil {
		return nop, err
	}
	path := filepath.Join(cgroupSlice, unit, "cgroup.freeze")
	if !strings.HasPrefix(path, cgroupSlice+"/") {
		return nop, fmt.Errorf("freeze unit is not a nodal-ct service")
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if required {
				return nop, fmt.Errorf("cgroup freezer is not available; Directory backup will not copy a live rootfs without a short freeze")
			}
			return nop, nil
		}
		return nop, fmt.Errorf("cgroup freeze: %w", err)
	}
	if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil {
		return nop, fmt.Errorf("cgroup freeze: %w", err)
	}
	return func() {
		_ = os.WriteFile(path, []byte("0\n"), 0o644)
	}, nil
}
