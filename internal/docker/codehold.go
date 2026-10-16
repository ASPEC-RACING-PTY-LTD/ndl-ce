package docker

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CodeHold runners. ndl-ce acts as a CodeHold host controller: each runner is
// a system container with Docker inside, running the codehold-runner agent as
// a systemd service in supervised mode. The agent registers itself with a
// one-time token written to a root-only file; it never sees a token on a
// command line. These actions run inside those containers and find the
// codehold-runner binary, preferably from a CodeHold instance on this node.

// CodeHold actions.
const (
	CodeholdDetect  = "detect"
	CodeholdInstall = "install"
	CodeholdToken   = "token"
	CodeholdStatus  = "status"
	CodeholdRestart = "restart"
)

const (
	codeholdBinary    = "/usr/local/bin/codehold-runner"
	codeholdConfigDir = "/etc/codehold-runner"
	codeholdRegFile   = codeholdConfigDir + "/registration.token"
	codeholdEnvFile   = codeholdConfigDir + "/env"
	codeholdMarker    = codeholdConfigDir + "/unauthorized"
	codeholdUnit      = "codehold-runner.service"
	codeholdUnitPath  = "/etc/systemd/system/" + codeholdUnit
	codeholdWorkDir   = "/var/lib/codehold-runner"
	// CodeholdDownloadDir is where the CodeHold image keeps runner binaries.
	CodeholdDownloadDir = "/app/downloads"
	maxRunnerBinary     = 256 << 20
)

// CodeholdSource says where the codehold-runner binary comes from: a CodeHold
// container on this node, or an HTTPS URL with an optional SHA-256.
type CodeholdSource struct {
	MachineID   string `json:"machine_id,omitempty"`
	ContainerID string `json:"container_id,omitempty"`
	URL         string `json:"url,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
}

// CodeholdRequest asks the agent to act for the CodeHold integration.
// Token and the Cloudflare Access secret are never logged or returned.
type CodeholdRequest struct {
	Action    string         `json:"action"`
	MachineID string         `json:"machine_id,omitempty"`
	Hints     []MachineHint  `json:"hints,omitempty"`
	Source    CodeholdSource `json:"source,omitempty"`
	Arch      string         `json:"arch,omitempty"`
	RunnerURL string         `json:"runner_url,omitempty"`
	Token     string         `json:"token,omitempty"`
	CFID      string         `json:"cf_client_id,omitempty"`
	CFSecret  string         `json:"cf_client_secret,omitempty"`
}

// CodeholdInstance is a CodeHold server container found on this node.
type CodeholdInstance struct {
	MachineID     string `json:"machine_id"`
	MachineName   string `json:"machine_name"`
	ContainerID   string `json:"container_id"`
	ContainerName string `json:"container_name"`
	Image         string `json:"image"`
	// HTTPPort is the host port published for the container's port 80.
	HTTPPort int `json:"http_port,omitempty"`
	// HasRunner is true when the container holds the runner binaries.
	HasRunner bool `json:"has_runner"`
}

// CodeholdResult answers a CodeHold request.
type CodeholdResult struct {
	Instances []CodeholdInstance `json:"instances,omitempty"`
	Active    bool               `json:"active"`
	State     string             `json:"state,omitempty"`
	Installed bool               `json:"installed"`
	// AuthFailed is true while the agent has no working credentials.
	AuthFailed bool     `json:"auth_failed"`
	TokenWait  bool     `json:"token_waiting"`
	Logs       []string `json:"logs,omitempty"`
	Version    string   `json:"version,omitempty"`
	SHA256     string   `json:"sha256,omitempty"`
	Message    string   `json:"message,omitempty"`
}

// Codehold runs one CodeHold action.
func (e *Engine) Codehold(ctx context.Context, req CodeholdRequest) (CodeholdResult, error) {
	switch req.Action {
	case CodeholdDetect:
		return CodeholdResult{Instances: e.codeholdDetect(ctx, req.Hints)}, nil
	case CodeholdInstall:
		return e.codeholdInstall(ctx, req)
	case CodeholdToken:
		return e.codeholdToken(ctx, req)
	case CodeholdStatus:
		return e.codeholdStatus(ctx, req)
	case CodeholdRestart:
		if _, err := e.machineRootFor(ctx, req.MachineID); err != nil {
			return CodeholdResult{}, err
		}
		if out, err := e.machineRun(ctx, req.MachineID, binSystemctl, "restart", codeholdUnit); err != nil {
			return CodeholdResult{}, fmt.Errorf("restarting the runner failed: %s", lastLine(out, err))
		}
		return CodeholdResult{Message: "runner restarted"}, nil
	}
	return CodeholdResult{}, fmt.Errorf("unknown CodeHold action %q", req.Action)
}

// isCodeholdServer reports whether a container is a CodeHold server (not a
// runner or its engine).
func isCodeholdServer(it listItem) bool {
	if it.State != "running" {
		return false
	}
	if svc := it.Labels["com.docker.compose.service"]; svc == "codehold" {
		return true
	}
	img := strings.ToLower(it.Image)
	return strings.Contains(img, "codehold") && !strings.Contains(img, "runner")
}

func (e *Engine) codeholdDetect(ctx context.Context, hints []MachineHint) []CodeholdInstance {
	machines := append([]MachineHint{{ID: HostMachineID, Name: "Host", Kind: KindHost}}, hints...)
	var out []CodeholdInstance
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, m := range machines {
		wg.Add(1)
		go func(m MachineHint) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			mctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			sock, err := e.resolveSocket(mctx, m.ID, nil)
			if err != nil {
				return
			}
			found := e.codeholdDetectOn(mctx, m, sock)
			mu.Lock()
			out = append(out, found...)
			mu.Unlock()
		}(m)
	}
	wg.Wait()
	return out
}

// codeholdDetectOn lists CodeHold server containers behind one Docker socket.
func (e *Engine) codeholdDetectOn(ctx context.Context, m MachineHint, sock string) []CodeholdInstance {
	items, err := e.client(sock).listContainers(ctx)
	if err != nil {
		return nil
	}
	var out []CodeholdInstance
	for _, it := range items {
		if !isCodeholdServer(it) {
			continue
		}
		in := CodeholdInstance{MachineID: m.ID, MachineName: firstNonEmptyStr(m.Name, m.ID), ContainerID: it.ID, Image: it.Image}
		if len(it.Names) > 0 {
			in.ContainerName = strings.TrimPrefix(it.Names[0], "/")
		}
		for _, p := range it.Ports {
			if p.PrivatePort == 80 && p.PublicPort > 0 && p.Type == "tcp" {
				in.HTTPPort = p.PublicPort
			}
		}
		_, err := e.archiveFile(ctx, sock, it.ID, CodeholdDownloadDir+"/codehold-runner-linux-amd64", 1)
		in.HasRunner = err == nil || errors.Is(err, errTooLarge)
		out = append(out, in)
	}
	return out
}

var errTooLarge = errors.New("file is larger than allowed")

// archiveFile reads one file out of a container through the Docker API's
// archive endpoint, without starting anything in the container.
func (e *Engine) archiveFile(ctx context.Context, socket, containerID, p string, limit int64) ([]byte, error) {
	if e.ArchiveFile != nil {
		return e.ArchiveFile(ctx, socket, containerID, p, limit)
	}
	res, err := sharedUnixClient(socket).do(ctx, http.MethodGet, "/containers/"+url.PathEscape(containerID)+"/archive?path="+url.QueryEscape(p), nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	tr := tar.NewReader(res.Body)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("%s was not in the archive: %w", p, err)
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != path.Base(p) {
			continue
		}
		if h.Size > limit {
			return nil, errTooLarge
		}
		return io.ReadAll(io.LimitReader(tr, limit))
	}
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// runnerBinaries caches fetched binaries by source, so creating several
// runners reads the file once.
var runnerBinaries sync.Map

type cachedBinary struct {
	at   time.Time
	data []byte
}

func (e *Engine) codeholdBinary(ctx context.Context, src CodeholdSource, arch string) ([]byte, error) {
	if arch == "" {
		arch = "amd64"
	}
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("unsupported architecture %q", arch)
	}
	want := strings.ToLower(strings.TrimSpace(src.SHA256))
	if want != "" && !sha256Re.MatchString(want) {
		return nil, fmt.Errorf("the checksum must be 64 hexadecimal characters")
	}
	key := fmt.Sprintf("%s|%s|%s|%s", src.MachineID, src.ContainerID, src.URL, arch)
	if c, ok := runnerBinaries.Load(key); ok && time.Since(c.(cachedBinary).at) < 10*time.Minute {
		data := c.(cachedBinary).data
		if err := checkSum(data, want); err != nil {
			return nil, err
		}
		return data, nil
	}
	var data []byte
	var err error
	switch {
	case src.ContainerID != "":
		sock, serr := e.resolveSocket(ctx, src.MachineID, nil)
		if serr != nil {
			return nil, fmt.Errorf("the CodeHold container's Docker is not reachable: %w", serr)
		}
		data, err = e.archiveFile(ctx, sock, src.ContainerID, CodeholdDownloadDir+"/codehold-runner-linux-"+arch, maxRunnerBinary)
		if err != nil {
			return nil, fmt.Errorf("reading the runner from the CodeHold container: %w", err)
		}
	case src.URL != "":
		data, err = e.downloadRunner(ctx, src.URL, arch)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("no source for the runner binary is set")
	}
	if len(data) < 1<<20 || !strings.HasPrefix(string(data[:4]), "\x7fELF") {
		return nil, fmt.Errorf("the runner binary is not a Linux program")
	}
	if err := checkSum(data, want); err != nil {
		return nil, err
	}
	runnerBinaries.Store(key, cachedBinary{at: time.Now(), data: data})
	return data, nil
}

func checkSum(data []byte, want string) error {
	if want == "" {
		return nil
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("the runner binary's SHA-256 is %s, not the expected %s", got, want)
	}
	return nil
}

// downloadRunner fetches the binary over HTTPS. A URL ending in "/" or one
// naming a CodeHold server is completed with the download path.
func (e *Engine) downloadRunner(ctx context.Context, raw, arch string) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("the runner download must be an https:// address")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/downloads/codehold-runner-linux-" + arch
	}
	u.Path = strings.ReplaceAll(u.Path, "{arch}", arch)
	if e.Download != nil {
		return e.Download(ctx, u.String())
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(dctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading the runner failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading the runner failed: HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, maxRunnerBinary))
}

var runnerURLRe = regexp.MustCompile(`^https?://[A-Za-z0-9.\-:\[\]]+(/[A-Za-z0-9._~/\-]*)?$`)

func codeholdUnitFile(runnerURL string) string {
	return `[Unit]
Description=CodeHold Actions runner (managed by No-dal)
After=network-online.target docker.service
Wants=network-online.target docker.service

[Service]
Type=simple
EnvironmentFile=-` + codeholdEnvFile + `
ExecStart=` + codeholdBinary + ` run --config ` + codeholdConfigDir + `/config.json --registration-file ` + codeholdRegFile + ` --url ` + runnerURL + ` --work-dir ` + codeholdWorkDir + ` --engine docker
Restart=always
RestartSec=5s

[Install]
WantedBy=multi-user.target
`
}

func (e *Engine) codeholdInstall(ctx context.Context, req CodeholdRequest) (CodeholdResult, error) {
	if !runnerURLRe.MatchString(req.RunnerURL) {
		return CodeholdResult{}, fmt.Errorf("the address runners use to reach CodeHold is invalid")
	}
	if strings.ContainsAny(req.CFID+req.CFSecret, "\n\r") {
		return CodeholdResult{}, fmt.Errorf("the Cloudflare Access token is invalid")
	}
	root, err := e.machineRootFor(ctx, req.MachineID)
	if err != nil {
		return CodeholdResult{}, err
	}
	bin, err := e.codeholdBinary(ctx, req.Source, req.Arch)
	if err != nil {
		return CodeholdResult{}, err
	}
	sum := sha256.Sum256(bin)
	if err := writeInRoot(root, codeholdBinary, bin, 0o755); err != nil {
		return CodeholdResult{}, fmt.Errorf("installing the runner binary: %w", err)
	}
	env := ""
	if req.CFID != "" && req.CFSecret != "" {
		env = "CF_ACCESS_CLIENT_ID=" + req.CFID + "\nCF_ACCESS_CLIENT_SECRET=" + req.CFSecret + "\n"
	}
	if err := writeInRoot(root, codeholdEnvFile, []byte(env), 0o600); err != nil {
		return CodeholdResult{}, fmt.Errorf("writing the runner environment: %w", err)
	}
	if err := writeInRoot(root, codeholdUnitPath, []byte(codeholdUnitFile(req.RunnerURL)), 0o644); err != nil {
		return CodeholdResult{}, fmt.Errorf("writing the runner service: %w", err)
	}
	if out, err := e.machineRun(ctx, req.MachineID, binSystemctl, "daemon-reload"); err != nil {
		return CodeholdResult{}, fmt.Errorf("systemctl daemon-reload failed: %s", lastLine(out, err))
	}
	if out, err := e.machineRun(ctx, req.MachineID, binSystemctl, "enable", "--now", codeholdUnit); err != nil {
		return CodeholdResult{}, fmt.Errorf("starting the runner failed: %s", lastLine(out, err))
	}
	// A running agent rereads its binary only when restarted.
	_, _ = e.machineRun(ctx, req.MachineID, binSystemctl, "restart", codeholdUnit)
	res := CodeholdResult{Installed: true, Active: true, SHA256: hex.EncodeToString(sum[:]), Message: "runner installed"}
	if out, err := e.machineRun(ctx, req.MachineID, codeholdBinary, "--version"); err == nil {
		res.Version = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	}
	return res, nil
}

var regTokenRe = regexp.MustCompile(`^[A-Za-z0-9_\-.]{8,512}$`)

// codeholdToken hands a one-time registration token to the agent, which
// registers with it and deletes the file.
func (e *Engine) codeholdToken(ctx context.Context, req CodeholdRequest) (CodeholdResult, error) {
	if !regTokenRe.MatchString(req.Token) {
		return CodeholdResult{}, fmt.Errorf("the registration token is invalid")
	}
	root, err := e.machineRootFor(ctx, req.MachineID)
	if err != nil {
		return CodeholdResult{}, err
	}
	if err := writeInRoot(root, codeholdRegFile, []byte(req.Token+"\n"), 0o600); err != nil {
		return CodeholdResult{}, fmt.Errorf("handing over the registration token: %w", err)
	}
	// An agent that is not running is started; a running one picks the
	// file up by itself.
	if out, err := e.machineRun(ctx, req.MachineID, binSystemctl, "show", "-p", "ActiveState", "--value", codeholdUnit); err == nil && strings.TrimSpace(string(out)) != "active" {
		_, _ = e.machineRun(ctx, req.MachineID, binSystemctl, "restart", codeholdUnit)
	}
	return CodeholdResult{Message: "registration token handed over"}, nil
}

func (e *Engine) codeholdStatus(ctx context.Context, req CodeholdRequest) (CodeholdResult, error) {
	root, err := e.machineRootFor(ctx, req.MachineID)
	if err != nil {
		return CodeholdResult{State: "stopped"}, nil
	}
	res := CodeholdResult{Installed: existsInRoot(root, codeholdBinary) && existsInRoot(root, codeholdUnitPath)}
	if out, err := e.machineRun(ctx, req.MachineID, binSystemctl, "show", "-p", "ActiveState,ExecMainStatus", "--value", codeholdUnit); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		res.State = strings.TrimSpace(lines[0])
		res.Active = res.State == "active"
		if len(lines) > 1 {
			if code, _ := strconv.Atoi(strings.TrimSpace(lines[1])); code == 78 {
				res.AuthFailed = true
			}
		}
	}
	res.AuthFailed = res.AuthFailed || existsInRoot(root, codeholdMarker)
	res.TokenWait = existsInRoot(root, codeholdRegFile)
	if !res.Active || res.AuthFailed {
		if out, err := e.machineRun(ctx, req.MachineID, binJournalctl, "-u", codeholdUnit, "--no-pager", "-o", "cat", "-n", "15"); err == nil {
			for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "-- ") && !strings.Contains(l, "chreg_") {
					res.Logs = append(res.Logs, shortMsg(l))
				}
			}
		}
	}
	return res, nil
}
