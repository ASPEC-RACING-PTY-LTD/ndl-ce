package docker

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Cloudflare connectors (cloudflared) that No-dal deploys run as their own
// systemd units, named cloudflared-ndl-<tunnel prefix>.service, with the
// tunnel token in a root-only environment file. Units No-dal did not create
// are never changed or removed here.

// Connector actions.
const (
	ConnectorInstall = "install"
	ConnectorRemove  = "remove"
	ConnectorUpdate  = "update"
	ConnectorProbe   = "probe"
)

// ConnectorRequest asks the agent to manage a connector on one machine.
// Token is only set for install and is never logged or returned.
type ConnectorRequest struct {
	Action    string `json:"action"`
	MachineID string `json:"machine_id"`
	TunnelID  string `json:"tunnel_id,omitempty"`
	Token     string `json:"token,omitempty"`
	// Probe target, for ConnectorProbe.
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
	// HostApproved is set only after a SuperAdmin approved changing the host's
	// own cloudflared; without it install, update and remove refuse the host.
	HostApproved bool `json:"host_approved,omitempty"`
}

// ConnectorResult reports what was done.
type ConnectorResult struct {
	Unit      string `json:"unit,omitempty"`
	Version   string `json:"version,omitempty"`
	Message   string `json:"message,omitempty"`
	Reachable bool   `json:"reachable,omitempty"`
}

var tunnelUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ConnectorUnit is the unit name No-dal uses for a tunnel's connector.
func ConnectorUnit(tunnelID string) string {
	return "cloudflared-ndl-" + strings.ReplaceAll(tunnelID, "-", "")[:12] + ".service"
}

// ManagedConnectorUnit reports whether a unit was created by No-dal.
func ManagedConnectorUnit(unit string) bool {
	return strings.HasPrefix(unit, "cloudflared-ndl-") && ValidTunnelUnit(unit)
}

func connectorEnvPath(tunnelID string) string {
	return "/etc/cloudflared/ndl-" + tunnelID + ".env"
}

const (
	cloudflareKeyURL  = "https://pkg.cloudflare.com/cloudflare-main.gpg"
	cloudflareKeyring = "/usr/share/keyrings/cloudflare-main.gpg"
	cloudflareList    = "/etc/apt/sources.list.d/cloudflared.list"
	cloudflareRepo    = "deb [signed-by=" + cloudflareKeyring + "] https://pkg.cloudflare.com/cloudflared any main\n"
	binAptGet         = "/usr/bin/apt-get"
	binSystemctlPath  = binSystemctl
)

var cloudflaredBins = []string{"/usr/bin/cloudflared", "/usr/local/bin/cloudflared"}

func connectorUnitFile(tunnelID, bin string) string {
	return `[Unit]
Description=Cloudflare Tunnel ` + tunnelID + ` (managed by No-dal)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=` + connectorEnvPath(tunnelID) + `
ExecStart=` + bin + ` --no-autoupdate tunnel run
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
`
}

// Connector runs one connector action on a machine.
func (e *Engine) Connector(ctx context.Context, req ConnectorRequest) (ConnectorResult, error) {
	if req.Action != ConnectorProbe && IsHostMachine(req.MachineID) && !req.HostApproved {
		return ConnectorResult{}, ErrHostTunnelProtected
	}
	switch req.Action {
	case ConnectorInstall:
		return e.installConnector(ctx, req)
	case ConnectorRemove:
		return e.removeConnector(ctx, req)
	case ConnectorUpdate:
		return e.updateConnector(ctx, req)
	case ConnectorProbe:
		return e.probeOrigin(ctx, req)
	default:
		return ConnectorResult{}, fmt.Errorf("unknown connector action %q", req.Action)
	}
}

// rootOwner is the uid/gid that owns the machine's root directory. Inside an
// unprivileged container that is the mapped root user, so files written from
// the host stay readable by the container's root.
func rootOwner(root string) (int, int) {
	st, err := os.Stat(root)
	if err != nil {
		return 0, 0
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return int(s.Uid), int(s.Gid)
	}
	return 0, 0
}

func (e *Engine) writeMachineFile(root, path string, data []byte, mode os.FileMode) error {
	full := filepath.Join(root, path)
	uid, gid := rootOwner(root)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	_ = os.Chown(filepath.Dir(full), uid, gid)
	tmp := full + ".ndl-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chown(tmp, uid, gid); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, full)
}

func (e *Engine) machineRootFor(ctx context.Context, machineID string) (string, error) {
	if e.HostRoot != "" && (machineID == "" || machineID == HostMachineID) {
		return e.HostRoot, nil
	}
	root := e.machineRoot(ctx, machineID)
	if root == "" {
		return "", fmt.Errorf("the machine is not running")
	}
	return root, nil
}

func findCloudflared(root string) string {
	for _, b := range cloudflaredBins {
		if st, err := os.Stat(filepath.Join(root, b)); err == nil && !st.IsDir() {
			return b
		}
	}
	return ""
}

// ensureCloudflared installs cloudflared from Cloudflare's signed apt
// repository when it is missing, and returns its path.
func (e *Engine) ensureCloudflared(ctx context.Context, machineID, root string) (string, error) {
	if b := findCloudflared(root); b != "" {
		return b, nil
	}
	if _, err := os.Stat(filepath.Join(root, binAptGet)); err != nil {
		return "", fmt.Errorf("cloudflared is not installed and this machine has no apt; install cloudflared on it first")
	}
	key, err := e.fetchKeyring(ctx)
	if err != nil {
		return "", err
	}
	if err := e.writeMachineFile(root, cloudflareKeyring, key, 0o644); err != nil {
		return "", fmt.Errorf("write Cloudflare package key: %w", err)
	}
	if err := e.writeMachineFile(root, cloudflareList, []byte(cloudflareRepo), 0o644); err != nil {
		return "", fmt.Errorf("write Cloudflare package source: %w", err)
	}
	if out, err := e.machineRun(ctx, machineID, binAptGet, "update", "-o", "Dir::Etc::sourcelist="+cloudflareList,
		"-o", "Dir::Etc::sourceparts=-", "-o", "APT::Get::List-Cleanup=0"); err != nil {
		return "", fmt.Errorf("apt-get update for cloudflared failed: %s", lastLine(out, err))
	}
	if out, err := e.machineRun(ctx, machineID, binAptGet, "install", "-y", "-q", "cloudflared"); err != nil {
		return "", fmt.Errorf("installing cloudflared failed: %s", lastLine(out, err))
	}
	if b := findCloudflared(root); b != "" {
		return b, nil
	}
	return "", fmt.Errorf("cloudflared did not appear after installing it")
}

func (e *Engine) fetchKeyring(ctx context.Context) ([]byte, error) {
	if e.FetchKeyring != nil {
		return e.FetchKeyring(ctx)
	}
	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, cloudflareKeyURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download Cloudflare package key: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download Cloudflare package key: HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 64<<10))
}

func lastLine(out []byte, err error) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return err.Error()
}

func (e *Engine) installConnector(ctx context.Context, req ConnectorRequest) (ConnectorResult, error) {
	if !tunnelUUIDRe.MatchString(req.TunnelID) {
		return ConnectorResult{}, fmt.Errorf("tunnel id is invalid")
	}
	if strings.TrimSpace(req.Token) == "" || strings.ContainsAny(req.Token, "\n\r") {
		return ConnectorResult{}, fmt.Errorf("tunnel token is missing")
	}
	root, err := e.machineRootFor(ctx, req.MachineID)
	if err != nil {
		return ConnectorResult{}, err
	}
	bin, err := e.ensureCloudflared(ctx, req.MachineID, root)
	if err != nil {
		return ConnectorResult{}, err
	}
	unit := ConnectorUnit(req.TunnelID)
	if err := e.writeMachineFile(root, connectorEnvPath(req.TunnelID), []byte("TUNNEL_TOKEN="+req.Token+"\n"), 0o600); err != nil {
		return ConnectorResult{}, fmt.Errorf("write connector token: %w", err)
	}
	if err := e.writeMachineFile(root, "/etc/systemd/system/"+unit, []byte(connectorUnitFile(req.TunnelID, bin)), 0o644); err != nil {
		return ConnectorResult{}, fmt.Errorf("write connector unit: %w", err)
	}
	if _, err := e.machineRun(ctx, req.MachineID, binSystemctlPath, "daemon-reload"); err != nil {
		return ConnectorResult{Unit: unit}, fmt.Errorf("systemctl daemon-reload failed: %w", err)
	}
	if out, err := e.machineRun(ctx, req.MachineID, binSystemctlPath, "enable", "--now", unit); err != nil {
		return ConnectorResult{Unit: unit}, fmt.Errorf("starting %s failed: %s", unit, lastLine(out, err))
	}
	res := ConnectorResult{Unit: unit, Message: "connector started"}
	if out, err := e.machineRun(ctx, req.MachineID, bin, "--version"); err == nil {
		res.Version = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	}
	return res, nil
}

func (e *Engine) removeConnector(ctx context.Context, req ConnectorRequest) (ConnectorResult, error) {
	if !tunnelUUIDRe.MatchString(req.TunnelID) {
		return ConnectorResult{}, fmt.Errorf("tunnel id is invalid")
	}
	unit := ConnectorUnit(req.TunnelID)
	if !ManagedConnectorUnit(unit) {
		return ConnectorResult{}, fmt.Errorf("only connectors No-dal deployed can be removed")
	}
	root, err := e.machineRootFor(ctx, req.MachineID)
	if err != nil {
		return ConnectorResult{}, err
	}
	_, _ = e.machineRun(ctx, req.MachineID, binSystemctlPath, "disable", "--now", unit)
	for _, p := range []string{"/etc/systemd/system/" + unit, connectorEnvPath(req.TunnelID)} {
		if err := os.Remove(filepath.Join(root, p)); err != nil && !os.IsNotExist(err) {
			return ConnectorResult{Unit: unit}, fmt.Errorf("remove %s: %w", p, err)
		}
	}
	_, _ = e.machineRun(ctx, req.MachineID, binSystemctlPath, "daemon-reload")
	return ConnectorResult{Unit: unit, Message: "connector stopped and removed; cloudflared itself stays installed"}, nil
}

func (e *Engine) updateConnector(ctx context.Context, req ConnectorRequest) (ConnectorResult, error) {
	root, err := e.machineRootFor(ctx, req.MachineID)
	if err != nil {
		return ConnectorResult{}, err
	}
	bin := findCloudflared(root)
	if bin == "" {
		return ConnectorResult{}, fmt.Errorf("cloudflared is not installed on this machine")
	}
	if bin != "/usr/bin/cloudflared" {
		return ConnectorResult{}, fmt.Errorf("cloudflared at %s was not installed from a package; update it by hand", bin)
	}
	if _, err := os.Stat(filepath.Join(root, binAptGet)); err != nil {
		return ConnectorResult{}, fmt.Errorf("this machine has no apt; update cloudflared by hand")
	}
	if out, err := e.machineRun(ctx, req.MachineID, binAptGet, "update", "-q"); err != nil {
		return ConnectorResult{}, fmt.Errorf("apt-get update failed: %s", lastLine(out, err))
	}
	if out, err := e.machineRun(ctx, req.MachineID, binAptGet, "install", "-y", "-q", "--only-upgrade", "cloudflared"); err != nil {
		return ConnectorResult{}, fmt.Errorf("upgrading cloudflared failed: %s", lastLine(out, err))
	}
	res := ConnectorResult{Message: "cloudflared updated"}
	if out, err := e.machineRun(ctx, req.MachineID, bin, "--version"); err == nil {
		res.Version = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	}
	// Restart every cloudflared service on the machine so it runs the new binary.
	if raw, err := e.machineRun(ctx, req.MachineID, binSystemctl, "list-units", "--type=service", "--state=active", "--plain", "--no-legend", "--no-pager", "cloudflared*"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if f := strings.Fields(line); len(f) > 0 && ValidTunnelUnit(f[0]) {
				_, _ = e.machineRun(ctx, req.MachineID, binSystemctl, "restart", f[0])
			}
		}
	}
	return res, nil
}

// probeOrigin checks that the machine can open a TCP connection to an origin.
// It connects from inside the machine's network namespace, sends nothing and
// reads nothing. Only IP addresses are accepted, so no name is resolved in the
// wrong namespace.
func (e *Engine) probeOrigin(ctx context.Context, req ConnectorRequest) (ConnectorResult, error) {
	host := req.Host
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return ConnectorResult{}, fmt.Errorf("probe target must be an IP address")
	}
	if req.Port < 1 || req.Port > 65535 {
		return ConnectorResult{}, fmt.Errorf("probe port is invalid")
	}
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(req.Port))
	dial := func() error {
		d := net.Dialer{Timeout: 4 * time.Second}
		c, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return c.Close()
	}
	var err error
	if e.ProbeDial != nil {
		err = e.ProbeDial(ctx, req.MachineID, addr)
	} else if req.MachineID == "" || req.MachineID == HostMachineID {
		err = dial()
	} else {
		pid, _, lerr := e.lookupLXC(ctx, req.MachineID)
		if lerr != nil || pid <= 0 {
			return ConnectorResult{}, fmt.Errorf("the machine is not running")
		}
		err = inNetNS(pid, dial)
	}
	if err != nil {
		return ConnectorResult{Message: compactDialErr(err)}, nil
	}
	return ConnectorResult{Reachable: true, Message: "connected to " + addr}, nil
}

// inNetNS runs fn on a thread switched into pid's network namespace. If the
// thread cannot be switched back it is not unlocked, so Go retires it.
func inNetNS(pid int, fn func() error) error {
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		self, err := os.Open("/proc/thread-self/ns/net")
		if err != nil {
			errc <- err
			return
		}
		defer self.Close()
		target, err := os.Open("/proc/" + strconv.Itoa(pid) + "/ns/net")
		if err != nil {
			errc <- err
			return
		}
		defer target.Close()
		if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
			errc <- fmt.Errorf("enter network namespace: %w", err)
			return
		}
		res := fn()
		if err := unix.Setns(int(self.Fd()), unix.CLONE_NEWNET); err != nil {
			errc <- res
			return
		}
		runtime.UnlockOSThread()
		errc <- res
	}()
	return <-errc
}

func compactDialErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "nothing is listening on that address and port"
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "deadline exceeded"):
		return "the connection timed out; a firewall or wrong address may be the cause"
	case strings.Contains(msg, "no route to host"), strings.Contains(msg, "network is unreachable"):
		return "there is no network route to that address from the connector"
	}
	return msg
}
