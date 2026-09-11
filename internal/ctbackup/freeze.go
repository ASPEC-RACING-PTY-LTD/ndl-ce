package ctbackup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const systemdCTSlice = `system-nodal\x2dct.slice`

var (
	cgroupRoot   = "/sys/fs/cgroup"
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

func freezeUnitID(unit string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(unit), "nodal-ct@"), ".service")
}

func underCgroupRoot(path string) bool {
	clean := filepath.Clean(path)
	root := filepath.Clean(cgroupRoot)
	if clean == root {
		return false
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(clean, root+sep)
}

func freezePathCandidates(unit string) []string {
	id := freezeUnitID(unit)
	var out []string
	seen := map[string]struct{}{}
	add := func(p string) {
		p = filepath.Clean(p)
		if !underCgroupRoot(p) {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	// Guest tasks live in the LXC payload cgroup, not the systemd unit cgroup.
	add(filepath.Join(cgroupRoot, "lxc.payload."+id, "cgroup.freeze"))
	add(filepath.Join(cgroupRoot, "system.slice", unit, "cgroup.freeze"))
	add(filepath.Join(cgroupRoot, "system.slice", systemdCTSlice, unit, "cgroup.freeze"))
	matches, _ := filepath.Glob(filepath.Join(cgroupRoot, "system.slice", "*", unit, "cgroup.freeze"))
	for _, m := range matches {
		add(m)
	}
	return out
}

func existingFreezePaths(unit string) []string {
	var out []string
	for _, path := range freezePathCandidates(unit) {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		out = append(out, path)
	}
	return out
}

func waitFrozen(dir string) {
	events := filepath.Join(dir, "cgroup.events")
	if _, err := os.Stat(events); err != nil {
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(events)
		if err == nil && strings.Contains(string(body), "frozen 1") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
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
	paths := existingFreezePaths(unit)
	if len(paths) == 0 {
		if required {
			return nop, fmt.Errorf("cgroup freezer is not available; Directory backup will not copy a live rootfs without a short freeze")
		}
		return nop, nil
	}
	var frozen []string
	unfreeze := func() {
		for i := len(frozen) - 1; i >= 0; i-- {
			_ = os.WriteFile(frozen[i], []byte("0\n"), 0o644)
		}
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil {
			unfreeze()
			return nop, fmt.Errorf("cgroup freeze: %w", err)
		}
		frozen = append(frozen, path)
		waitFrozen(filepath.Dir(path))
	}
	return unfreeze, nil
}
