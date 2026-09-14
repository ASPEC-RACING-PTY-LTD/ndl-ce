package gameserver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Runner executes typed argv. Tests replace it.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Runtime starts and stops game-server containers.
type Runtime struct {
	Root        string
	Docker      string
	DockerHost  string
	NetworkMode string
	Run         Runner
	Now         func() time.Time
	mu          sync.Mutex
	logs        map[string][]string
	input       map[string][]string
}

func NewRuntime(root string) *Runtime {
	docker := strings.TrimSpace(os.Getenv("NDL_GS_DOCKER"))
	if docker == "" {
		docker = "/usr/bin/docker"
	}
	net := strings.TrimSpace(os.Getenv("NDL_GS_NETWORK"))
	if net == "" {
		net = "bridge"
	}
	return &Runtime{
		Root:        root,
		Docker:      docker,
		DockerHost:  strings.TrimSpace(os.Getenv("DOCKER_HOST")),
		NetworkMode: net,
		Now:         func() time.Time { return time.Now().UTC() },
		logs:        map[string][]string{},
		input:       map[string][]string{},
	}
}

func (r *Runtime) dockerBin() string {
	if strings.TrimSpace(r.Docker) != "" {
		return r.Docker
	}
	return "/usr/bin/docker"
}

func (r *Runtime) networkMode() string {
	mode := strings.TrimSpace(r.NetworkMode)
	if mode == "" {
		return "bridge"
	}
	return mode
}

func (r *Runtime) networkArgs() []string {
	mode := r.networkMode()
	args := []string{"--network", mode}
	if mode != "host" {
		args = append(args, "--dns", "1.1.1.1", "--dns", "8.8.8.8")
	}
	return args
}

func (r *Runtime) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.Run != nil {
		return r.Run(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if host := strings.TrimSpace(r.DockerHost); host != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+host)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

func (r *Runtime) DataDir(id string) string {
	return filepath.Join(r.Root, "gameservers", id)
}

func (r *Runtime) EnsureData(id string) (string, error) {
	dir := r.DataDir(id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

func containerName(id string) string {
	return "ndl-gs-" + id
}

type RunSpec struct {
	ID          string
	Image       string
	Startup     string
	Env         map[string]string
	Ports       []Port
	CPUs        int
	MemoryBytes int64
	WorkDir     string
}

func (r *Runtime) Start(ctx context.Context, spec RunSpec) (string, error) {
	dir, err := r.EnsureData(spec.ID)
	if err != nil {
		return "", err
	}
	_ = r.Stop(ctx, spec.ID)
	args := []string{
		"run", "-d", "--name", containerName(spec.ID),
		"--workdir", firstNonEmpty(spec.WorkDir, "/home/container"),
		"-v", dir + ":" + firstNonEmpty(spec.WorkDir, "/home/container"),
	}
	args = append(args, r.networkArgs()...)
	args = append(args,
		"--security-opt", "no-new-privileges",
		"--read-only=false",
	)
	if spec.MemoryBytes > 0 {
		args = append(args, "--memory", strconv.FormatInt(spec.MemoryBytes, 10))
	}
	if spec.CPUs > 0 {
		args = append(args, "--cpus", strconv.Itoa(spec.CPUs))
	}
	if r.networkMode() != "host" {
		for _, p := range spec.Ports {
			proto := p.Protocol
			if proto == "" {
				proto = "tcp"
			}
			host := p.HostPort
			if host == 0 {
				host = p.ContainerPort
			}
			args = append(args, "-p", fmt.Sprintf("%d:%d/%s", host, p.ContainerPort, proto))
		}
	}
	for k, v := range spec.Env {
		args = append(args, "-e", k+"="+v)
	}
	if strings.TrimSpace(spec.Startup) != "" {
		args = append(args, "--entrypoint", "/bin/sh")
		args = append(args, spec.Image, "-c", spec.Startup)
	} else {
		args = append(args, spec.Image)
	}
	out, err := r.run(ctx, r.dockerBin(), args...)
	if err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(out)+" "+err.Error()))
	}
	id := strings.TrimSpace(string(out))
	r.appendLog(spec.ID, "container started "+id)
	return id, nil
}

func (r *Runtime) Stop(ctx context.Context, id string) error {
	_, _ = r.run(ctx, r.dockerBin(), "stop", "-t", "15", containerName(id))
	_, _ = r.run(ctx, r.dockerBin(), "rm", "-f", containerName(id))
	return nil
}

func (r *Runtime) Kill(ctx context.Context, id string) error {
	_, _ = r.run(ctx, r.dockerBin(), "kill", containerName(id))
	_, _ = r.run(ctx, r.dockerBin(), "rm", "-f", containerName(id))
	return nil
}

func (r *Runtime) Logs(ctx context.Context, id string, tail int) (string, error) {
	if tail <= 0 {
		tail = 200
	}
	out, err := r.run(ctx, r.dockerBin(), "logs", "--tail", strconv.Itoa(tail), containerName(id))
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return strings.Join(r.logs[id], "\n"), nil
	}
	return string(out), nil
}

func (r *Runtime) Send(ctx context.Context, id, line string) error {
	r.mu.Lock()
	r.input[id] = append(r.input[id], line)
	r.mu.Unlock()
	_, err := r.run(ctx, r.dockerBin(), "exec", "-i", containerName(id), "/bin/sh", "-c", "printf '%s\\n' \"$1\" > /proc/1/fd/0", "--", line)
	if err != nil {
		r.appendLog(id, "> "+line)
		return nil
	}
	return nil
}

func (r *Runtime) Running(ctx context.Context, id string) bool {
	out, err := r.run(ctx, r.dockerBin(), "inspect", "-f", "{{.State.Running}}", containerName(id))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func (r *Runtime) appendLog(id, line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs[id] = append(r.logs[id], line)
	if len(r.logs[id]) > 500 {
		r.logs[id] = r.logs[id][len(r.logs[id])-500:]
	}
}

func (r *Runtime) Available(ctx context.Context) error {
	out, err := r.run(ctx, r.dockerBin(), "info")
	if err != nil {
		msg := strings.TrimSpace(string(out) + " " + err.Error())
		return fmt.Errorf("%s", msg)
	}
	return nil
}
