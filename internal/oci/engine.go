package oci

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Engine drives OCI workloads through a Runtime and systemd unit enablement.
// systemd starts the container via ndl-oci-launch. The agent does not hold the process.
type Engine struct {
	DataDir      string
	WorkloadsDir string
	RuntimeDir   string
	Runtime      Runtime
	Run          Runner
	Now          func() time.Time
	SkipHostCmds bool
	Creds        func(registryID string) (*RegistryCreds, error)
}

func (e *Engine) dataDir() string {
	if e.DataDir != "" {
		return e.DataDir
	}
	return defaultDataDir
}

func (e *Engine) workloadsDir() string {
	if e.WorkloadsDir != "" {
		return e.WorkloadsDir
	}
	if e.DataDir != "" {
		return filepath.Join(e.DataDir, "workloads")
	}
	return defaultWorkloads
}

func (e *Engine) runtime() Runtime {
	if e.Runtime != nil {
		return e.Runtime
	}
	return &Containerd{SkipHostCmds: e.SkipHostCmds, Exec: e.Run, Now: e.Now, SecretDir: filepath.Join(e.dataDir(), "secrets", "oci")}
}

func normalizeSpec(spec Spec) (Spec, error) {
	if _, err := uuid.Parse(strings.TrimSpace(spec.WorkloadID)); err != nil {
		return Spec{}, fmt.Errorf("workload_id must be a UUID")
	}
	if err := ValidateSpec(spec); err != nil {
		return Spec{}, err
	}
	if spec.Resources.CPUs < 1 {
		spec.Resources.CPUs = DefaultCPUs
	}
	if spec.Resources.MemoryBytes < 1 {
		spec.Resources.MemoryBytes = DefaultMemoryBytes
	}
	if spec.Name == "" {
		spec.Name = spec.WorkloadID
	}
	return spec, nil
}

// Create validates, pulls (with optional registry creds), writes last-applied, and enables the unit.
func (e *Engine) Create(ctx context.Context, spec Spec) (Result, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return Result{}, err
	}
	if prev, err := e.readApplied(spec.WorkloadID); err == nil && prev.Spec.WorkloadID == spec.WorkloadID {
		if err := e.enableUnit(ctx, spec.WorkloadID); err != nil {
			return Result{}, err
		}
		if err := e.Start(ctx, spec.WorkloadID); err != nil {
			return Result{}, err
		}
		return Result{
			WorkloadID: prev.Spec.WorkloadID, ImageDigest: prev.ImageDigest,
			Status: StatusRunning, Health: Health{Status: StatusCollecting, Message: "replay create"},
		}, nil
	}
	var creds *RegistryCreds
	if spec.PullUsername != "" || spec.PullPassword != "" {
		creds = &RegistryCreds{Username: spec.PullUsername, Password: spec.PullPassword}
	} else if spec.RegistryID != "" && e.Creds != nil {
		creds, err = e.Creds(spec.RegistryID)
		if err != nil {
			return Result{}, err
		}
	}
	digest, err := e.runtime().Pull(ctx, PullRequest{Image: spec.ImagePin, Creds: creds})
	if err != nil {
		return Result{}, err
	}
	if err := e.writeApplied(spec, digest, true); err != nil {
		return Result{}, err
	}
	if err := e.enableUnit(ctx, spec.WorkloadID); err != nil {
		return Result{}, err
	}
	if err := e.Start(ctx, spec.WorkloadID); err != nil {
		return Result{}, err
	}
	status := StatusCollecting
	if e.SkipHostCmds {
		status = StatusUnavailable
	}
	health := Health{Status: StatusNotConfigured, Message: "healthcheck not configured"}
	if spec.Health != nil && (spec.Health.HTTPPath != "" || spec.Health.Port > 0) {
		health = Health{Status: StatusCollecting, Message: "healthcheck configured; awaiting observation"}
	}
	if e.SkipHostCmds {
		health = Health{Status: StatusUnavailable, Message: "host commands skipped; containerd not observed"}
		if spec.Health != nil && (spec.Health.HTTPPath != "" || spec.Health.Port > 0) {
			health = Health{Status: StatusCollecting, Message: "healthcheck configured; awaiting observation"}
		}
	}
	return Result{WorkloadID: spec.WorkloadID, ImageDigest: digest, Status: status, Health: health}, nil
}

// Start starts nodal-oci@<uuid> via systemd.
func (e *Engine) Start(ctx context.Context, id string) error {
	_, err := e.runHost(ctx, BinSystemctl, "start", unitName(id))
	return err
}

// Stop stops the OCI unit.
func (e *Engine) Stop(ctx context.Context, id string) error {
	_ = e.runtime().Stop(ctx, id)
	_, err := e.runHost(ctx, BinSystemctl, "stop", unitName(id))
	return err
}

// Restart restarts the OCI unit.
func (e *Engine) Restart(ctx context.Context, id string) error {
	_, err := e.runHost(ctx, BinSystemctl, "restart", unitName(id))
	return err
}

// Delete stops the unit and removes runtime files. Desired rows stay in the store.
func (e *Engine) Delete(ctx context.Context, id string) error {
	_ = e.Stop(ctx, id)
	_ = e.runtime().Delete(ctx, id)
	_, _ = e.runHost(ctx, BinSystemctl, "disable", unitName(id))
	_ = os.RemoveAll(filepath.Join(e.workloadsDir(), id))
	return nil
}

func (e *Engine) enableUnit(ctx context.Context, id string) error {
	_, err := e.runHost(ctx, BinSystemctl, "enable", unitName(id))
	return err
}

func (e *Engine) runHost(ctx context.Context, name string, args ...string) ([]byte, error) {
	if !allowedBin(name) {
		return nil, fmt.Errorf("refusing unlisted binary %s", name)
	}
	if e.Run != nil {
		return e.Run(ctx, name, args...)
	}
	if e.SkipHostCmds {
		return nil, nil
	}
	rt, ok := e.runtime().(*Containerd)
	if ok {
		return rt.run(ctx, name, args...)
	}
	c := &Containerd{SkipHostCmds: e.SkipHostCmds}
	return c.run(ctx, name, args...)
}

// ApplyGPUDevices rewrites last-applied OCI spec with allowlisted device nodes.
func (e *Engine) ApplyGPUDevices(id string, devices []string) error {
	applied, err := e.readApplied(id)
	if err != nil {
		return fmt.Errorf("oci last-applied is missing: %w", err)
	}
	clean := make([]string, 0, len(devices))
	for _, d := range devices {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if !strings.HasPrefix(d, "/dev/") || strings.Contains(d, "..") || strings.ContainsAny(d, ",=\n\r\x00") {
			return fmt.Errorf("device node is not allowlisted")
		}
		clean = append(clean, d)
	}
	applied.Spec.GPUDevices = clean
	return e.writeApplied(applied.Spec, applied.ImageDigest, applied.Pulled)
}

// Lifecycle dispatches a typed action.
func (e *Engine) Lifecycle(ctx context.Context, req LifecycleRequest) (Result, error) {
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "start":
		if err := e.Start(ctx, req.WorkloadID); err != nil {
			return Result{}, err
		}
		return Result{WorkloadID: req.WorkloadID, Status: StatusRunning, Health: Health{Status: StatusCollecting}}, nil
	case "stop":
		if err := e.Stop(ctx, req.WorkloadID); err != nil {
			return Result{}, err
		}
		return Result{WorkloadID: req.WorkloadID, Status: StatusStopped, Health: Health{Status: StatusStopped}}, nil
	case "restart":
		if err := e.Restart(ctx, req.WorkloadID); err != nil {
			return Result{}, err
		}
		return Result{WorkloadID: req.WorkloadID, Status: StatusRunning, Health: Health{Status: StatusCollecting}}, nil
	case "delete":
		if err := e.Delete(ctx, req.WorkloadID); err != nil {
			return Result{}, err
		}
		return Result{WorkloadID: req.WorkloadID, Status: StatusUnavailable, Health: Health{Status: StatusUnavailable}}, nil
	default:
		return Result{}, fmt.Errorf("unknown lifecycle action %q", req.Action)
	}
}

// Observe reports unit and runtime state. Missing is unavailable, never fake healthy.
func (e *Engine) Observe(ctx context.Context, hints []Hint) (Observation, error) {
	obs := Observation{ObservedAt: e.now(), Workloads: make([]Observed, 0, len(hints))}
	for _, h := range hints {
		if h.Kind != "" && h.Kind != KindOCI {
			continue
		}
		item := Observed{
			WorkloadID: h.WorkloadID,
			Kind:       KindOCI,
			ObservedAt: e.now(),
			Health:     Health{Status: StatusUnavailable, Message: "not observed"},
		}
		applied, err := e.readApplied(h.WorkloadID)
		if err != nil {
			item.Status = StatusUnavailable
			item.Reason = "last-applied missing"
			item.Health = Health{Status: StatusUnavailable, Message: "last-applied missing"}
			obs.Workloads = append(obs.Workloads, item)
			continue
		}
		rt, err := e.runtime().Observe(ctx, h.WorkloadID)
		if err != nil {
			item.Status = StatusUnavailable
			item.Reason = err.Error()
			item.Health = Health{Status: StatusUnavailable, Message: err.Error()}
			obs.Workloads = append(obs.Workloads, item)
			continue
		}
		item.Status = rt.Status
		item.Reason = rt.Reason
		item.UnitActive = rt.UnitActive
		item.Health = rt.Health
		if applied.Spec.Health == nil || (applied.Spec.Health.HTTPPath == "" && applied.Spec.Health.Port == 0) {
			if item.Health.Status == StatusRunning || item.Health.Status == StatusCollecting {
				item.Health = Health{Status: StatusNotConfigured, Message: "healthcheck not configured"}
			}
		}
		obs.Workloads = append(obs.Workloads, item)
	}
	return obs, nil
}

// LaunchFromApplied is used by ndl-oci-launch to run the frozen last-applied Spec.
func (e *Engine) LaunchFromApplied(ctx context.Context, id string) error {
	applied, err := e.readApplied(id)
	if err != nil {
		return err
	}
	if applied.Spec.WorkloadID != id {
		return fmt.Errorf("last-applied workload id does not match")
	}
	return e.runtime().Run(ctx, applied.Spec)
}
