package gameserver

import (
	"errors"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	// OnProgress, when set, receives install phase changes so the API can
	// persist them for the UI.
	OnProgress func(id, phase, message string)
	mu         sync.Mutex
	logs       map[string][]string
	input      map[string][]string
	imageMu    sync.Mutex
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

// ErrNoDocker means the Docker Engine game servers run in is not installed.
var ErrNoDocker = errors.New("docker engine is not installed on this host")

// Ready reports whether game servers can run here: the Docker CLI exists
// and its daemon answers. Checked before a server is created or started, so
// a missing Docker is a clear message instead of a failed install.
func (r *Runtime) Ready(ctx context.Context) error {
	if r.Run == nil {
		if _, err := os.Stat(r.dockerBin()); err != nil {
			return ErrNoDocker
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := r.run(ctx, r.dockerBin(), "version", "--format", "{{.Server.Version}}")
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("docker engine is not running: %s", clip(msg, 300))
	}
	return nil
}

func (r *Runtime) networkMode() string {
	mode := strings.TrimSpace(r.NetworkMode)
	if mode == "" {
		return "bridge"
	}
	return mode
}

func (r *Runtime) networkArgs() []string {
	return networkArgsFor(r.networkMode())
}

func (r *Runtime) specNetworkMode(spec RunSpec) string {
	switch mode := strings.TrimSpace(spec.NetworkMode); mode {
	case "bridge", "host":
		return mode
	}
	return r.networkMode()
}

func networkArgsFor(mode string) []string {
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
	// Dependencies are Debian packages layered onto Image before start.
	Dependencies []string
	// NetworkMode is "bridge" or "host". Empty uses the runtime default.
	NetworkMode string `json:",omitempty"`
}

func (r *Runtime) Start(ctx context.Context, spec RunSpec) (string, error) {
	dir, err := r.EnsureData(spec.ID)
	if err != nil {
		return "", err
	}
	_ = r.Stop(ctx, spec.ID)
	image := spec.Image
	if len(spec.Dependencies) > 0 {
		built, err := r.EnsureRuntimeImage(ctx, spec.Image, spec.Dependencies)
		if err != nil {
			return "", fmt.Errorf("runtime dependencies failed: %w", err)
		}
		image = built
	}
	workdir := firstNonEmpty(spec.WorkDir, "/home/container")
	// -i keeps stdin open so console commands and graceful stop commands
	// written to /proc/1/fd/0 reach the server process.
	args := []string{
		"run", "-d", "-i", "--name", containerName(spec.ID),
		"--workdir", workdir,
		"-v", dir + ":" + workdir,
	}
	netMode := r.specNetworkMode(spec)
	args = append(args, networkArgsFor(netMode)...)
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
	if netMode != "host" {
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
	if _, ok := spec.Env["HOME"]; !ok {
		// Game servers keep per-user state (Steam client libraries, Klei
		// clusters, Unity prefs) under $HOME, so it must be the persistent
		// server folder rather than the image's ephemeral /root.
		args = append(args, "-e", "HOME="+workdir)
	}
	for _, k := range sortedKeys(spec.Env) {
		args = append(args, "-e", k+"="+spec.Env[k])
	}
	if strings.TrimSpace(spec.Startup) != "" {
		args = append(args, "--entrypoint", "/bin/sh")
		args = append(args, image, "-c", spec.Startup)
	} else {
		args = append(args, image)
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

// StopGraceful asks the server to shut down the way the game expects, then
// falls back to docker stop. stopCmd "^C" sends SIGINT, "^\\" SIGQUIT,
// "SIGTERM" or "" goes straight to docker stop, anything else is typed into
// the console. The wait is bounded by timeout.
func (r *Runtime) StopGraceful(ctx context.Context, id, stopCmd string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if !r.Running(ctx, id) {
		return r.Stop(ctx, id)
	}
	stopCmd = strings.TrimSpace(stopCmd)
	switch stopCmd {
	case "", "SIGTERM":
	case "^C", "SIGINT":
		_, _ = r.run(ctx, r.dockerBin(), "kill", "-s", "SIGINT", containerName(id))
	case "^\\", "SIGQUIT":
		_, _ = r.run(ctx, r.dockerBin(), "kill", "-s", "SIGQUIT", containerName(id))
	default:
		_ = r.Send(ctx, id, stopCmd)
	}
	if stopCmd != "" && stopCmd != "SIGTERM" {
		// docker wait returns as soon as the server exits; the context
		// bounds how long a server may take to save before docker stop.
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		_, _ = r.run(waitCtx, r.dockerBin(), "wait", containerName(id))
		cancel()
	}
	r.appendLog(id, "stopped with "+firstNonEmpty(stopCmd, "SIGTERM"))
	return r.Stop(ctx, id)
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

func (r *Runtime) progress(id, phase, msg string) {
	r.appendLog(id, "["+phase+"] "+msg)
	if r.OnProgress != nil {
		r.OnProgress(id, phase, msg)
	}
}

// LogTail returns the buffered install/runtime log lines for a server.
func (r *Runtime) LogTail(id string, max int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := r.logs[id]
	if max > 0 && len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	return strings.Join(lines, "\n")
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
