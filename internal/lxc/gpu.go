package lxc

import (
	"fmt"
	"path"
	"strings"
)

// ApplyGPUDevices rewrites last-applied LXC config with allowlisted device nodes.
// An empty list is the product default: no /dev/dri.
func (e *Engine) ApplyGPUDevices(id string, devices []string) error {
	applied, err := e.readApplied(id)
	if err != nil {
		return fmt.Errorf("system container last-applied is missing: %w", err)
	}
	clean := make([]string, 0, len(devices))
	for _, d := range devices {
		d = path.Clean(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if !strings.HasPrefix(d, "/dev/") || strings.Contains(d, "..") {
			return fmt.Errorf("device node is not allowlisted")
		}
		clean = append(clean, d)
	}
	applied.Spec.GPUDevices = clean
	if err := e.writeConfig(applied.Spec); err != nil {
		return err
	}
	return e.writeApplied(applied.Spec, applied.ImageVerified, applied.ImageSHA256)
}
