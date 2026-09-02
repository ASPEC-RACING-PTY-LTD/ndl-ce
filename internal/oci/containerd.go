package oci

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
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
	SecretDir    string
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
		return nil, fmt.Errorf("containerd runtime is unavailable")
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

func (c *Containerd) secretDir() string {
	if c.SecretDir != "" {
		return c.SecretDir
	}
	return filepath.Join(defaultDataDir, "secrets", "oci")
}

// PullImageArgv builds typed ctr image pull argv. Extra user args are never accepted.
// Registry passwords are not placed on argv. Pass hostsDir when a 0600 hosts.toml was written.
func PullImageArgv(ns, image string, hostsDir string) ([]string, error) {
	if err := ValidateImageRef(image); err != nil {
		return nil, err
	}
	if ns == "" {
		ns = "nodal"
	}
	if hostsDir != "" {
		if strings.ContainsAny(hostsDir, "\n\r\x00") || strings.Contains(hostsDir, "..") {
			return nil, fmt.Errorf("hosts dir is invalid")
		}
	}
	argv := []string{BinCTR, "--namespace", ns, "image", "pull"}
	if hostsDir != "" {
		argv = append(argv, "--hosts-dir", hostsDir)
	}
	argv = append(argv, image)
	return argv, nil
}

func registryHost(image string) string {
	image = strings.TrimSpace(image)
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	slash := strings.IndexByte(image, '/')
	if slash < 0 {
		return "docker.io"
	}
	first := image[:slash]
	if !strings.ContainsAny(first, ".:") && first != "localhost" {
		return "docker.io"
	}
	return first
}

// writeRegistryHosts writes a containerd hosts.toml (0600) under dir. Password never goes on argv.
func writeRegistryHosts(dir, image, user, pass string) (string, error) {
	if strings.TrimSpace(user) == "" && strings.TrimSpace(pass) == "" {
		return "", nil
	}
	if strings.ContainsAny(user, "\n\r\x00") || strings.ContainsAny(pass, "\n\r\x00") {
		return "", fmt.Errorf("registry credentials contain banned characters")
	}
	host := registryHost(image)
	if host == "" || strings.Contains(host, "..") || strings.ContainsAny(host, "/\\\n\r\x00") {
		return "", fmt.Errorf("registry host is invalid")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	hostDir := filepath.Join(dir, host)
	if err := os.MkdirAll(hostDir, 0o700); err != nil {
		return "", err
	}
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	body := "server = \"https://" + host + "\"\n\n" +
		"[host.\"https://" + host + "\"]\n" +
		"  capabilities = [\"pull\", \"resolve\"]\n\n" +
		"[host.\"https://" + host + "\".header]\n" +
		"  authorization = [\"Basic " + auth + "\"]\n"
	p := filepath.Join(hostDir, "hosts.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(p, 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

func (c *Containerd) Pull(ctx context.Context, req PullRequest) (string, error) {
	if c.SkipHostCmds || !c.runtimePresent() {
		return "", fmt.Errorf("containerd runtime is unavailable")
	}
	user, pass := "", ""
	if req.Creds != nil {
		user, pass = req.Creds.Username, req.Creds.Password
	}
	hostsDir := ""
	if user != "" || pass != "" {
		written, err := writeRegistryHosts(c.secretDir(), req.Image, user, pass)
		if err != nil {
			return "", err
		}
		hostsDir = written
	}
	argv, err := PullImageArgv(c.ns(), req.Image, hostsDir)
	if err != nil {
		return "", err
	}
	for _, a := range argv {
		if pass != "" && strings.Contains(a, pass) {
			return "", fmt.Errorf("registry password must not appear on pull argv")
		}
	}
	out, err := c.run(ctx, argv[0], argv[1:]...)
	if err != nil {
		return "", err
	}
	digest := strings.TrimSpace(string(out))
	if digest == "" {
		return "", fmt.Errorf("containerd pull did not report a digest")
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
	if spec.Resources.CPUs > 0 {
		argv = append(argv, "--cpus", strconv.Itoa(spec.Resources.CPUs))
	}
	if spec.Resources.MemoryBytes > 0 {
		argv = append(argv, "--memory-limit", strconv.FormatInt(spec.Resources.MemoryBytes, 10))
	}
	for _, p := range spec.Ports {
		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if proto == "" {
			proto = "tcp"
		}
		host := p.HostPort
		if host == 0 {
			host = p.ContainerPort
		}
		argv = append(argv, "--label", fmt.Sprintf("ndl.port=%d:%d/%s", host, p.ContainerPort, proto))
	}
	if spec.BridgeName != "" {
		if strings.ContainsAny(spec.BridgeName, " \n\r\x00,=") {
			return nil, fmt.Errorf("bridge name contains banned characters")
		}
		argv = append(argv, "--label", "ndl.bridge="+spec.BridgeName)
	}
	for _, m := range spec.Volumes {
		host := ""
		if spec.VolumePaths != nil {
			host = spec.VolumePaths[m.VolumeID]
		}
		if host == "" {
			return nil, fmt.Errorf("volume %s has no host locator", m.VolumeID)
		}
		if path.Clean(host) == "/" {
			return nil, fmt.Errorf("host bind to / is not allowed")
		}
		if strings.ContainsAny(host, ",=\n\r\x00") || strings.ContainsAny(m.ContainerPath, ",=\n\r\x00") {
			return nil, fmt.Errorf("bind path contains banned characters")
		}
		opts := "rbind"
		if m.ReadOnly {
			opts = "rbind,ro"
		}
		argv = append(argv, "--mount", "type=bind,src="+host+",dst="+m.ContainerPath+",options="+opts)
	}
	for _, e := range spec.Env {
		if e.Name == "NVIDIA_VISIBLE_DEVICES" && strings.EqualFold(strings.TrimSpace(e.Value), "all") {
			return nil, fmt.Errorf("NVIDIA_VISIBLE_DEVICES=all is refused")
		}
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
	if c.SkipHostCmds || !c.runtimePresent() {
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
	if c.SkipHostCmds || !c.runtimePresent() {
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
	if c.SkipHostCmds || !c.runtimePresent() {
		return fmt.Errorf("containerd runtime is unavailable")
	}
	_, err := c.run(ctx, BinCTR, "--namespace", c.ns(), "containers", "delete", workloadID)
	return err
}
