package oci

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner executes a validated argv vector.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Containerd drives containerd through allowlisted /usr/bin/ctr argv only.
// SkipHostCmds writes nothing to the host and returns unavailable observations.
type Containerd struct {
	SkipHostCmds bool
	Namespace    string
	Exec         Runner
	Now          func() time.Time
	Available    *bool // when set, overrides binary presence probe
}

func (c *Containerd) ns() string {
	if c.Namespace != "" {
		return c.Namespace
	}
	return "nodal"
}

func (c *Containerd) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}

func allowedBin(name string) bool {
	switch name {
	case BinCTR, BinSystemctl:
		return true
	default:
		return false
	}
}

func (c *Containerd) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if !allowedBin(name) {
		return nil, fmt.Errorf("refusing unlisted binary %s", name)
	}
	if c.Exec != nil {
		return c.Exec(ctx, name, args...)
	}
	if c.SkipHostCmds {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) == 0 {
			return out, err
		}
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (c *Containerd) runtimePresent() bool {
	if c.Available != nil {
		return *c.Available
	}
	if c.SkipHostCmds {
		return false
	}
	_, err := exec.LookPath(BinCTR)
	return err == nil
}

// PullImageArgv builds typed ctr image pull argv. Extra user args are never accepted.
func PullImageArgv(ns, image string, user, pass string) ([]string, error) {
	if err := ValidateImageRef(image); err != nil {
		return nil, err
	}
	if ns == "" {
		ns = "nodal"
	}
	argv := []string{BinCTR, "--namespace", ns, "image", "pull"}
	if user != "" {
		argv = append(argv, "--user", user+":"+pass)
	}
	argv = append(argv, image)
	return argv, nil
}

func (c *Containerd) Pull(ctx context.Context, req PullRequest) (string, error) {
	if !c.runtimePresent() {
		if c.SkipHostCmds {
			return "sha256:skip-host", nil
		}
		return "", fmt.Errorf("containerd runtime is unavailable")
	}
	user, pass := "", ""
	if req.Creds != nil {
		user, pass = req.Creds.Username, req.Creds.Password
	}
	argv, err := PullImageArgv(c.ns(), req.Image, user, pass)
	if err != nil {
		return "", err
	}
	out, err := c.run(ctx, argv[0], argv[1:]...)
	if err != nil {
		return "", err
	}
	digest := strings.TrimSpace(string(out))
	if digest == "" {
		digest = "sha256:pulled"
	}
	return digest, nil
}

// TaskStartArgv builds typed ctr run argv from a validated Spec. No extra user args.
func TaskStartArgv(ns string, spec Spec) ([]string, error) {
	if err := ValidateSpec(spec); err != nil {
		return nil, err
	}
	if ns == "" {
		ns = "nodal"
	}
	argv := []string{BinCTR, "--namespace", ns, "run", "--rm"}
	if spec.Privileged {
		argv = append(argv, "--privileged")
	}
	for _, m := range spec.Volumes {
		host := ""
		if spec.VolumePaths != nil {
			host = spec.VolumePaths[m.VolumeID]
		}
		if host == "" {
			return nil, fmt.Errorf("volume %s has no host locator", m.VolumeID)
		}
		opts := "rbind"
		if m.ReadOnly {
			opts = "rbind,ro"
		}
		argv = append(argv, "--mount", "type=bind,src="+host+",dst="+m.ContainerPath+",options="+opts)
	}
	for _, e := range spec.Env {
		argv = append(argv, "--env", e.Name+"="+e.Value)
	}
	for _, d := range spec.GPUDevices {
		argv = append(argv, "--mount", "type=bind,src="+d+",dst="+d+",options=rbind")
	}
	argv = append(argv, spec.ImagePin, spec.WorkloadID)
	if len(spec.Command) > 0 {
		argv = append(argv, spec.Command...)
	}
	return argv, nil
}

func (c *Containerd) Run(ctx context.Context, spec Spec) error {
	if !c.runtimePresent() {
		if c.SkipHostCmds {
			return nil
		}
		return fmt.Errorf("containerd runtime is unavailable")
	}
	argv, err := TaskStartArgv(c.ns(), spec)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, argv[0], argv[1:]...)
	return err
}

func (c *Containerd) Stop(ctx context.Context, workloadID string) error {
	if !c.runtimePresent() {
		if c.SkipHostCmds {
			return nil
		}
		return fmt.Errorf("containerd runtime is unavailable")
	}
	_, err := c.run(ctx, BinCTR, "--namespace", c.ns(), "tasks", "kill", workloadID)
	return err
}

func (c *Containerd) Observe(ctx context.Context, workloadID string) (Observed, error) {
	obs := Observed{
		WorkloadID: workloadID,
		Kind:       KindOCI,
		ObservedAt: c.now(),
		Health:     Health{Status: StatusUnavailable, Message: "containerd not observed"},
	}
	if !c.runtimePresent() {
		obs.Status = StatusUnavailable
		obs.Reason = "containerd runtime is unavailable"
		obs.Health = Health{Status: StatusUnavailable, Message: "containerd binary missing"}
		return obs, nil
	}
	if c.SkipHostCmds {
		obs.Status = StatusUnavailable
		obs.Reason = "host commands skipped"
		obs.Health = Health{Status: StatusUnavailable, Message: "SkipHostCmds; no fake health"}
		return obs, nil
	}
	out, err := c.run(ctx, BinCTR, "--namespace", c.ns(), "tasks", "ls")
	if err != nil {
		obs.Status = StatusUnavailable
		obs.Reason = err.Error()
		obs.Health = Health{Status: StatusUnavailable, Message: err.Error()}
		return obs, nil
	}
	if strings.Contains(string(out), workloadID) {
		obs.Status = StatusRunning
		obs.UnitActive = true
		obs.Health = Health{Status: StatusCollecting, Message: "task listed; probe pending"}
		return obs, nil
	}
	obs.Status = StatusStopped
	obs.Health = Health{Status: StatusStopped, Message: "task not listed"}
	return obs, nil
}

func (c *Containerd) Delete(ctx context.Context, workloadID string) error {
	_ = c.Stop(ctx, workloadID)
	if !c.runtimePresent() {
		if c.SkipHostCmds {
			return nil
		}
		return fmt.Errorf("containerd runtime is unavailable")
	}
	_, err := c.run(ctx, BinCTR, "--namespace", c.ns(), "containers", "delete", workloadID)
	return err
}
