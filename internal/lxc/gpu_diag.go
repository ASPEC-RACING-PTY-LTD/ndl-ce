package lxc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Guest exposure states for one GPU node in a running container.
const (
	GuestNodePresent    = "present"
	GuestNodeMissing    = "missing"
	GuestNodeMismatch   = "mismatch"
	GuestNodeNotRunning = "not_running"
	GuestNodeUnknown    = "unknown"
)

// procRoot is where guest mount namespaces are reached (/proc/<pid>/root).
var procRoot = "/proc"

// optionalGPUNodes are created by the NVIDIA driver on demand (nvidia-uvm on
// first CUDA use, nvidia-modeset with the modeset module). A missing one is
// reported, not treated as a broken assignment.
var optionalGPUNodes = map[string]bool{
	"/dev/nvidia-uvm":       true,
	"/dev/nvidia-uvm-tools": true,
	"/dev/nvidia-modeset":   true,
}

// GPUNodeDiagnosis compares one expected node across host, config, and guest.
type GPUNodeDiagnosis struct {
	Path string `json:"path"`
	// Canonical marks a node derived from a by-path locator on this host.
	Canonical bool `json:"canonical,omitempty"`
	Optional  bool `json:"optional,omitempty"`
	// HostRule is the host node's "c MAJ:MIN rwm"; empty means unavailable.
	HostRule string `json:"host_rule,omitempty"`
	Mounted  bool   `json:"mounted"`
	Allowed  bool   `json:"allowed"`
	Guest    string `json:"guest"`
}

// GPUDiagnosis is a read-only comparison of the saved GPU devices, the
// on-disk LXC config, the host nodes, and what the running guest can see.
type GPUDiagnosis struct {
	Saved         []string           `json:"saved"`
	Nodes         []GPUNodeDiagnosis `json:"nodes"`
	Running       bool               `json:"running"`
	PID           int                `json:"pid,omitempty"`
	ConfigPresent bool               `json:"config_present"`
	// ConfigCurrent is true when the on-disk config matches a fresh render.
	ConfigCurrent bool `json:"config_current"`
	// RestartRequired means the running guest lacks a node the config grants.
	RestartRequired bool `json:"restart_required"`
	// DevicesMissing means a required node is absent on the host, so the GPU
	// is removed, renumbered, or its driver is not loaded.
	DevicesMissing bool     `json:"devices_missing"`
	Issues         []string `json:"issues"`
}

// DiagnoseGPU never writes config, starts, or stops the container.
func (e *Engine) DiagnoseGPU(ctx context.Context, id string) (GPUDiagnosis, error) {
	out := GPUDiagnosis{Saved: []string{}, Nodes: []GPUNodeDiagnosis{}, Issues: []string{}}
	applied, err := e.readApplied(id)
	if err != nil {
		return out, fmt.Errorf("system container last-applied is missing: %w", err)
	}
	spec, err := normalizeSpec(applied.Spec)
	if err != nil {
		return out, err
	}
	out.Saved = append(out.Saved, spec.GPUDevices...)

	mounts, rules := map[string]bool{}, map[string]bool{}
	raw, readErr := os.ReadFile(e.configPath(id))
	if readErr == nil {
		out.ConfigPresent = true
		mounts, rules = parseDeviceConfig(string(raw))
		out.ConfigCurrent = string(raw) == hostLXCIncludes(spec)+RenderConfig(spec)+hostLXCOverrides(spec)
		if !out.ConfigCurrent {
			out.Issues = append(out.Issues, "LXC config differs from the saved assignment; reapply to regenerate it")
		}
	} else {
		out.Issues = append(out.Issues, "LXC config is missing")
	}

	if !e.SkipHostCmds {
		if pid, _, infoErr := e.lxcInfo(ctx, id); infoErr == nil && pid > 0 {
			out.Running, out.PID = true, pid
		}
	}
	guestDev := ""
	if out.Running {
		guestDev = filepath.Join(procRoot, strconv.Itoa(out.PID), "root")
		if _, err := os.Stat(filepath.Join(guestDev, "dev")); err != nil {
			guestDev = ""
			out.Issues = append(out.Issues, "guest /dev could not be inspected")
		}
	}

	saved := map[string]bool{}
	for _, d := range spec.GPUDevices {
		saved[d] = true
	}
	for _, dev := range gpuDeviceNodes(spec) {
		n := GPUNodeDiagnosis{
			Path:      dev,
			Canonical: !saved[dev],
			Optional:  optionalGPUNodes[dev],
			HostRule:  cgroupRuleFromStat(dev),
			Mounted:   mounts[dev],
		}
		n.Allowed = n.HostRule != "" && rules[n.HostRule]
		switch {
		case !out.Running:
			n.Guest = GuestNodeNotRunning
		case guestDev == "":
			n.Guest = GuestNodeUnknown
		default:
			got := charNodeRule(filepath.Join(guestDev, filepath.FromSlash(strings.TrimPrefix(dev, "/"))))
			switch {
			case got == "":
				n.Guest = GuestNodeMissing
			case n.HostRule != "" && got != n.HostRule:
				n.Guest = GuestNodeMismatch
			default:
				n.Guest = GuestNodePresent
			}
		}
		out.Issues = append(out.Issues, nodeIssues(n, out.ConfigPresent)...)
		if out.Running && n.HostRule != "" && n.Guest != GuestNodePresent && n.Guest != GuestNodeUnknown {
			out.RestartRequired = true
		}
		if n.HostRule == "" && !n.Optional {
			out.DevicesMissing = true
		}
		out.Nodes = append(out.Nodes, n)
	}
	if out.DevicesMissing {
		out.Issues = append(out.Issues, "a GPU device is missing on the host; if the GPU was removed, unassign it from this workload")
	}
	if out.RestartRequired {
		out.Issues = append(out.Issues, "the running container has not received every GPU device; restart it to apply the config")
	}
	return out, nil
}

func nodeIssues(n GPUNodeDiagnosis, configPresent bool) []string {
	var out []string
	if n.HostRule == "" {
		if n.Optional {
			out = append(out, n.Path+" is not present on the host (created on demand by the driver)")
		} else {
			out = append(out, n.Path+" is missing on the host (GPU removed or driver not loaded)")
		}
		return out
	}
	if configPresent && !n.Mounted {
		out = append(out, n.Path+" has no mount entry in the LXC config")
	}
	if configPresent && !n.Allowed {
		out = append(out, n.Path+" has no device permission in the LXC config")
	}
	switch n.Guest {
	case GuestNodeMissing:
		out = append(out, n.Path+" is missing inside the running container")
	case GuestNodeMismatch:
		out = append(out, n.Path+" inside the container does not match the host device")
	}
	return out
}

// parseDeviceConfig returns mount-entry sources and cgroup2 allow rules.
func parseDeviceConfig(cfg string) (map[string]bool, map[string]bool) {
	mounts, rules := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(cfg, "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "lxc.mount.entry":
			if f := strings.Fields(val); len(f) > 0 {
				mounts[f[0]] = true
			}
		case "lxc.cgroup2.devices.allow":
			rules[val] = true
		}
	}
	return mounts, rules
}
