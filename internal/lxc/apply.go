package lxc

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func cpuMaxValue(cpus int) string {
	if cpus < 1 {
		cpus = DefaultCPUs
	}
	return strconv.Itoa(cpus*100000) + " 100000"
}

// ApplySpec writes last-applied and on-disk LXC config without starting or
// stopping the unit. CPU and memory are applied live via lxc-cgroup when the
// unit is already active. Hostname and IP stay pending until the next start.
func (e *Engine) ApplySpec(ctx context.Context, req LifecycleRequest) (Result, error) {
	id := strings.TrimSpace(req.WorkloadID)
	applied, err := e.readApplied(id)
	if err != nil {
		return Result{}, fmt.Errorf("last-applied is missing")
	}
	spec := applied.Spec
	if req.CPUs > 0 {
		spec.CPUs = req.CPUs
	}
	if req.MemoryBytes > 0 {
		spec.MemoryBytes = req.MemoryBytes
	}
	if strings.TrimSpace(req.Name) != "" {
		spec.Name = strings.TrimSpace(req.Name)
	}
	if req.IPSet {
		ip, ierr := NormalizeIPConfig(req.IP)
		if ierr != nil {
			return Result{}, ierr
		}
		spec.IP = ip
	}
	if strings.TrimSpace(req.MAC) != "" {
		mac, merr := NormalizeMAC(req.MAC)
		if merr != nil {
			return Result{}, merr
		}
		spec.MAC = mac
	}
	spec, err = normalizeSpec(spec)
	if err != nil {
		return Result{}, err
	}
	if err := e.writeConfig(spec); err != nil {
		return Result{}, err
	}
	if err := e.writeApplied(spec, applied.ImageVerified, applied.ImageSHA256); err != nil {
		return Result{}, err
	}
	if req.Autostart != nil {
		if err := e.EnableAutostart(ctx, id, *req.Autostart); err != nil {
			return Result{}, err
		}
	}
	running := e.AlreadyRunning(ctx, id)
	if running {
		if err := e.applyLiveCgroups(ctx, id, spec); err != nil {
			return Result{}, err
		}
	}
	status := StatusStopped
	if running {
		status = StatusRunning
	}
	return Result{
		WorkloadID: id, VolumeID: spec.VolumeID, RootfsPath: spec.RootfsPath,
		MAC: spec.MAC, ImageVerified: applied.ImageVerified, ImageSHA256: applied.ImageSHA256,
		Status: status,
	}, nil
}

func (e *Engine) EnableAutostart(ctx context.Context, id string, on bool) error {
	action := "disable"
	if on {
		action = "enable"
	}
	_, err := e.run(ctx, BinSystemctl, action, unitName(id))
	return err
}

func (e *Engine) applyLiveCgroups(ctx context.Context, id string, spec Spec) error {
	if e.SkipHostCmds && e.Run == nil {
		return nil
	}
	mem := spec.MemoryBytes
	if mem < 1 {
		mem = DefaultMemoryBytes
	}
	if _, err := e.run(ctx, BinLXCCgroup, "-P", e.lxcPath(), "-n", id, "memory.max", strconv.FormatInt(mem, 10)); err != nil {
		return fmt.Errorf("live memory.max: %w", err)
	}
	if _, err := e.run(ctx, BinLXCCgroup, "-P", e.lxcPath(), "-n", id, "cpu.max", cpuMaxValue(spec.CPUs)); err != nil {
		return fmt.Errorf("live cpu.max: %w", err)
	}
	return nil
}
