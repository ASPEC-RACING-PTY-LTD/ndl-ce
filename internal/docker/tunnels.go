package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Cloudflare tunnels (cloudflared) run as systemd services next to Docker,
// on the host or inside a system container, and route public hostnames to
// container ports. When a container is down the tunnel logs origin errors;
// these types report that and link each error to the container behind it.

const (
	binSystemctl  = "/usr/bin/systemctl"
	binJournalctl = "/usr/bin/journalctl"
	// TunnelWindow is how far back tunnel errors are read. Errors older than
	// StatusScope are history only and never set a tunnel's health.
	TunnelWindow = 15 * time.Minute
)

// IngressRule is one hostname route from a local cloudflared config file.
type IngressRule struct {
	Hostname string `json:"hostname,omitempty"`
	Path     string `json:"path,omitempty"`
	Service  string `json:"service"`
}

// TunnelError is one origin error, grouped by the origin it failed to reach.
type TunnelError struct {
	Origin   string `json:"origin,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Message  string `json:"message"`
	Count    int    `json:"count"`
	Last     string `json:"last,omitempty"`
	// Recent counts the errors inside StatusScope; only these are current.
	Recent int `json:"recent"`
}

// TunnelLink ties a tunnel origin to the container serving it.
type TunnelLink struct {
	Origin      string   `json:"origin"`
	Hostnames   []string `json:"hostnames,omitempty"`
	ContainerID string   `json:"container_id"`
	Ref         string   `json:"id"`
	Name        string   `json:"name"`
	Project     string   `json:"project,omitempty"`
	State       string   `json:"state"`
	Health      string   `json:"health"`
	Errors      int      `json:"errors"`
	Recent      int      `json:"recent_errors"`
}

// Tunnel is one cloudflared service on a machine.
type Tunnel struct {
	MachineID     string `json:"machine_id"`
	MachineName   string `json:"machine_name"`
	Unit          string `json:"unit"`
	Active        bool   `json:"active"`
	State         string `json:"state"`
	SubState      string `json:"sub_state,omitempty"`
	RemoteManaged bool   `json:"remote_managed"`
	ConfigPath    string `json:"config_path,omitempty"`
	IngressSource string `json:"ingress_source,omitempty"`
	// TunnelID is the Cloudflare tunnel this connector runs, from its log.
	TunnelID string `json:"tunnel_id,omitempty"`
	// Managed is true for connectors No-dal deployed.
	Managed    bool          `json:"managed,omitempty"`
	Ingress    []IngressRule `json:"ingress,omitempty"`
	Errors     []TunnelError `json:"errors,omitempty"`
	ErrorCount int           `json:"error_count"`
	// CurrentErrors counts errors inside StatusScope; ErrorCount is the
	// whole TunnelWindow and is history once CurrentErrors is zero.
	CurrentErrors int          `json:"current_errors"`
	LastErrorAt   string       `json:"last_error_at,omitempty"`
	Links         []TunnelLink `json:"links,omitempty"`
	Health        string       `json:"health"`
	Reason        string       `json:"reason,omitempty"`
}

var (
	unitNameRe   = regexp.MustCompile(`^cloudflared[A-Za-z0-9@._-]*\.service$`)
	originRe     = regexp.MustCompile(`originService=(\S+)`)
	destRe       = regexp.MustCompile(`dest=(\S+)`)
	dialRe       = regexp.MustCompile(`dial tcp ([^\s:]+:\d+|\[[^\]]+\]:\d+)`)
	errFieldRe   = regexp.MustCompile(`error="([^"]+)"`)
	configFlagRe = regexp.MustCompile(`--config[ =](\S+)`)
)

// ValidTunnelUnit reports whether name is a cloudflared service unit.
func ValidTunnelUnit(name string) bool {
	return unitNameRe.MatchString(name)
}

func (e *Engine) hostRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	if e.HostRun != nil {
		return e.HostRun(ctx, name, args...)
	}
	if e.SkipHostCmds {
		return nil, fmt.Errorf("host commands are skipped")
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// machineRun runs argv on the host or inside a system container.
func (e *Engine) machineRun(ctx context.Context, machineID string, argv ...string) ([]byte, error) {
	if machineID == "" || machineID == HostMachineID {
		return e.hostRun(ctx, argv[0], argv[1:]...)
	}
	if e.GuestRun == nil {
		return nil, fmt.Errorf("system container commands are unavailable")
	}
	return e.GuestRun(ctx, machineID, argv...)
}

// machineRoot is the machine's filesystem as the agent sees it.
func (e *Engine) machineRoot(ctx context.Context, machineID string) string {
	if machineID == "" || machineID == HostMachineID {
		return "/"
	}
	pid, _, err := e.lookupLXC(ctx, machineID)
	if err != nil || pid <= 0 {
		return ""
	}
	return "/proc/" + strconv.Itoa(pid) + "/root"
}

// Tunnels finds cloudflared services on the host and the hinted system
// containers, with their state, local ingress and recent origin errors.
func (e *Engine) Tunnels(ctx context.Context, hints []MachineHint) []Tunnel {
	machines := []MachineHint{{ID: HostMachineID, Name: "Host", Kind: KindHost}}
	machines = append(machines, hints...)
	var out []Tunnel
	for _, m := range machines {
		mctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		out = append(out, e.machineTunnels(mctx, m)...)
		cancel()
	}
	return out
}

func (e *Engine) machineTunnels(ctx context.Context, m MachineHint) []Tunnel {
	raw, err := e.machineRun(ctx, m.ID, binSystemctl, "list-units", "--type=service", "--all", "--plain", "--no-legend", "--no-pager", "cloudflared*")
	if err != nil {
		return nil
	}
	var out []Tunnel
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !ValidTunnelUnit(f[0]) {
			continue
		}
		t := Tunnel{MachineID: m.ID, MachineName: firstNonEmptyStr(m.Name, m.ID), Unit: f[0], State: f[2], SubState: f[3]}
		t.Active = f[2] == "active"
		e.fillTunnelConfig(ctx, &t)
		t.Managed = ManagedConnectorUnit(t.Unit)
		if raw, err := e.machineRun(ctx, m.ID, binJournalctl, "-u", t.Unit, "--no-pager", "-o", "cat", "-g", "tunnelID=", "-n", "1", "-r"); err == nil {
			t.TunnelID = ParseTunnelID(string(raw))
		}
		if jr, err := e.machineRun(ctx, m.ID, binJournalctl, "-u", t.Unit, "--since", "-"+strconv.Itoa(int(TunnelWindow.Minutes()))+"min", "--no-pager", "-o", "short-iso", "-n", "500"); err == nil {
			t.Errors = ParseTunnelErrorsAt(string(jr), time.Now())
			for _, te := range t.Errors {
				t.ErrorCount += te.Count
				t.CurrentErrors += te.Recent
				if te.Last > t.LastErrorAt {
					t.LastErrorAt = te.Last
				}
			}
		}
		out = append(out, t)
	}
	return out
}

// fillTunnelConfig reads the unit's config file for ingress routes. A
// token-run tunnel is configured in the Cloudflare dashboard instead. The
// token itself is never read into the report.
func (e *Engine) fillTunnelConfig(ctx context.Context, t *Tunnel) {
	cfg := ""
	if raw, err := e.machineRun(ctx, t.MachineID, binSystemctl, "show", "-p", "ExecStart", "--value", t.Unit); err == nil {
		line := string(raw)
		if m := configFlagRe.FindStringSubmatch(line); m != nil {
			cfg = strings.Trim(m[1], `"';`)
		}
		if strings.Contains(line, "--token") || strings.Contains(line, "TUNNEL_TOKEN") {
			t.RemoteManaged = true
		}
	}
	if root := e.machineRoot(ctx, t.MachineID); root != "" {
		candidates := []string{"/etc/cloudflared/config.yml", "/etc/cloudflared/config.yaml", "/root/.cloudflared/config.yml", "/root/.cloudflared/config.yaml", "/usr/local/etc/cloudflared/config.yml"}
		if cfg != "" && strings.HasPrefix(cfg, "/") && !strings.Contains(cfg, "..") {
			candidates = append([]string{cfg}, candidates...)
		}
		for _, p := range candidates {
			raw, err := os.ReadFile(filepath.Join(root, p))
			if err != nil {
				continue
			}
			t.ConfigPath = p
			t.Ingress = ParseIngress(raw)
			return
		}
	}
	// A dashboard-managed tunnel logs the routes Cloudflare pushed to it.
	if raw, err := e.machineRun(ctx, t.MachineID, binJournalctl, "-u", t.Unit, "--no-pager", "-o", "cat", "-g", remoteConfigMarker, "-n", "1", "-r"); err == nil {
		if rules := ParseRemoteIngress(string(raw)); len(rules) > 0 {
			t.Ingress = rules
			t.IngressSource = IngressSourceRemote
		}
	}
}

var tunnelIDRe = regexp.MustCompile(`tunnelID=([0-9a-fA-F-]{36})`)

// ParseTunnelID reads the tunnel ID cloudflared logs when it starts.
func ParseTunnelID(log string) string {
	if m := tunnelIDRe.FindStringSubmatch(log); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

const (
	remoteConfigMarker = "Updated to new configuration"
	// IngressSourceRemote marks ingress read from the routes Cloudflare pushed.
	IngressSourceRemote = "cloudflare"
)

var remoteConfigRe = regexp.MustCompile(`config="((?:[^"\\]|\\.)*)"`)

// ParseRemoteIngress reads the ingress rules from cloudflared's
// "Updated to new configuration" log line. Only hostname, path and service
// are kept; origin request settings are dropped.
func ParseRemoteIngress(line string) []IngressRule {
	m := remoteConfigRe.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	raw, err := strconv.Unquote(`"` + m[1] + `"`)
	if err != nil {
		return nil
	}
	var doc struct {
		Ingress []IngressRule `json:"ingress"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil
	}
	out := make([]IngressRule, 0, len(doc.Ingress))
	for _, r := range doc.Ingress {
		out = append(out, IngressRule{Hostname: r.Hostname, Path: r.Path, Service: r.Service})
	}
	return out
}

// ParseIngress reads the ingress rules from a cloudflared config file.
func ParseIngress(raw []byte) []IngressRule {
	var doc struct {
		Ingress []IngressRule `yaml:"ingress"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	return doc.Ingress
}

// ParseTunnelErrors groups cloudflared error lines by origin and hostname.
func ParseTunnelErrors(journal string) []TunnelError {
	return ParseTunnelErrorsAt(journal, time.Now())
}

// journalTime parses a short-iso journal timestamp.
func journalTime(v string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02T15:04:05-0700", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05.000000-0700"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ParseTunnelErrorsAt groups origin errors and counts, per group, the ones
// inside StatusScope of now.
func ParseTunnelErrorsAt(journal string, now time.Time) []TunnelError {
	byKey := map[string]*TunnelError{}
	var order []string
	for _, line := range strings.Split(journal, "\n") {
		if !strings.Contains(line, " ERR ") && !strings.Contains(line, "level=error") && !strings.Contains(line, `error="`) {
			continue
		}
		origin := ""
		if m := originRe.FindStringSubmatch(line); m != nil {
			origin = strings.Trim(m[1], `"`)
		} else if m := dialRe.FindStringSubmatch(line); m != nil {
			origin = m[1]
		}
		host := ""
		if m := destRe.FindStringSubmatch(line); m != nil {
			if u, err := url.Parse(strings.Trim(m[1], `"`)); err == nil {
				host = u.Hostname()
			}
		}
		msg := ""
		if m := errFieldRe.FindStringSubmatch(line); m != nil {
			msg = m[1]
		} else if i := strings.Index(line, " ERR "); i >= 0 {
			msg = strings.TrimSpace(line[i+5:])
		} else {
			msg = strings.TrimSpace(line)
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		key := origin + "|" + host
		if origin == "" && host == "" {
			key = "|" + shortMsg(msg)
		}
		te, ok := byKey[key]
		if !ok {
			te = &TunnelError{Origin: origin, Hostname: host, Message: msg}
			byKey[key] = te
			order = append(order, key)
		}
		te.Count++
		te.Message = msg
		if f := strings.Fields(line); len(f) > 0 {
			te.Last = f[0]
			if at, ok := journalTime(f[0]); ok && now.Sub(at) <= StatusScope {
				te.Recent++
			}
		}
	}
	out := make([]TunnelError, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func shortMsg(s string) string {
	if len(s) > 60 {
		return s[:60]
	}
	return s
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// originAddr turns "http://localhost:8080" or "127.0.0.1:8080" into host and port.
func originAddr(origin string) (string, int) {
	origin = strings.TrimSpace(origin)
	if origin == "" || strings.HasPrefix(origin, "http_status:") || strings.HasPrefix(origin, "hello_world") {
		return "", 0
	}
	if strings.Contains(origin, "://") {
		u, err := url.Parse(origin)
		if err != nil {
			return "", 0
		}
		port, _ := strconv.Atoi(u.Port())
		if port == 0 {
			switch u.Scheme {
			case "https":
				port = 443
			case "http", "ws":
				port = 80
			case "ssh":
				port = 22
			case "rdp":
				port = 3389
			}
		}
		return u.Hostname(), port
	}
	h, p, err := net.SplitHostPort(origin)
	if err != nil {
		return "", 0
	}
	port, _ := strconv.Atoi(p)
	return strings.Trim(h, "[]"), port
}

func isLocalHost(host, machineIP string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0", "":
		return true
	}
	return machineIP != "" && host == machineIP
}

// containerForOrigin finds the container on machineID that serves origin.
func containerForOrigin(inv Inventory, machineID, origin string) *Container {
	host, port := originAddr(origin)
	if port == 0 && host == "" {
		return nil
	}
	machineIP := ""
	for _, m := range inv.Machines {
		if m.ID == machineID {
			machineIP = m.IPv4
		}
	}
	for i := range inv.Containers {
		c := &inv.Containers[i]
		if c.MachineID != machineID {
			continue
		}
		if isLocalHost(host, machineIP) {
			for _, p := range c.Ports {
				if p.PublicPort == port {
					return c
				}
			}
			continue
		}
		for _, ip := range c.IPs {
			if ip == host {
				return c
			}
		}
		if host == c.Name || (c.Service != "" && host == c.Service) {
			return c
		}
	}
	return nil
}

// LinkTunnels ties each tunnel's origins (from its config and its errors)
// to containers in the inventory and rates the tunnel's health.
func LinkTunnels(tunnels []Tunnel, inv Inventory) []Tunnel {
	out := make([]Tunnel, len(tunnels))
	for i, t := range tunnels {
		hostsFor := map[string][]string{}
		errorsFor := map[string]int{}
		recentFor := map[string]int{}
		var origins []string
		add := func(o string) {
			if _, ok := hostsFor[o]; !ok {
				hostsFor[o] = nil
				origins = append(origins, o)
			}
		}
		for _, r := range t.Ingress {
			if r.Service == "" {
				continue
			}
			add(r.Service)
			if r.Hostname != "" {
				hostsFor[r.Service] = appendUnique(hostsFor[r.Service], r.Hostname)
			}
		}
		for _, te := range t.Errors {
			if te.Origin == "" {
				continue
			}
			add(te.Origin)
			errorsFor[te.Origin] += te.Count
			recentFor[te.Origin] += te.Recent
			if te.Hostname != "" {
				hostsFor[te.Origin] = appendUnique(hostsFor[te.Origin], te.Hostname)
			}
		}
		t.Links = nil
		var broken []string
		for _, o := range origins {
			c := containerForOrigin(inv, t.MachineID, o)
			if c == nil {
				continue
			}
			t.Links = append(t.Links, TunnelLink{
				Origin: o, Hostnames: hostsFor[o], ContainerID: c.EngineID, Ref: c.Ref, Name: c.Name,
				Project: c.Project, State: c.State, Health: c.Health, Errors: errorsFor[o], Recent: recentFor[o],
			})
			if c.State != "running" || c.Health == HealthCritical || c.Health == HealthDegraded {
				broken = append(broken, c.Name)
			}
		}
		switch {
		case !t.Active:
			t.Health, t.Reason = HealthCritical, "cloudflared is "+firstNonEmptyStr(t.SubState, t.State, "not running")
		case len(broken) > 0:
			t.Health = HealthDegraded
			t.Reason = "routes to containers that are down or unhealthy: " + strings.Join(broken, ", ")
		case t.CurrentErrors > 0:
			t.Health = HealthDegraded
			t.Reason = strconv.Itoa(t.CurrentErrors) + " origin errors in the last minute"
		case t.ErrorCount > 0:
			// Older errors are history: the tunnel has recovered.
			t.Health = HealthHealthy
			t.Reason = "Recovered: " + strconv.Itoa(t.ErrorCount) + " errors in the last 15 minutes, none in the last minute"
		default:
			t.Health, t.Reason = HealthHealthy, ""
		}
		out[i] = t
	}
	return out
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// ErrHostTunnelProtected refuses a change to the host's own cloudflared.
// That tunnel usually carries the panel itself, so No-dal never starts,
// restarts, installs, updates or removes it unless a SuperAdmin approved that
// one change.
var ErrHostTunnelProtected = errors.New("No-dal does not manage the host's own cloudflared tunnel; a SuperAdmin must approve this change")

// IsHostMachine reports whether machineID names the host itself.
func IsHostMachine(machineID string) bool {
	return machineID == "" || machineID == HostMachineID
}

// HostTunnelAdvice is what to do by hand about the host's tunnel.
func HostTunnelAdvice(unit string) string {
	if unit == "" {
		unit = "cloudflared"
	}
	return "Check it on the host as root: systemctl status " + unit + " and journalctl -u " + unit + " -n 100. " +
		"If it needs a restart, run systemctl restart " + unit + ", or approve the restart here as a SuperAdmin. " +
		"Restarting it briefly drops every connection through the tunnel, including this panel."
}

// TunnelUnitAction starts or restarts a cloudflared unit. The host's own
// units need hostApproved.
func (e *Engine) TunnelUnitAction(ctx context.Context, machineID, unit, action string, hostApproved bool) error {
	if !ValidTunnelUnit(unit) {
		return fmt.Errorf("only cloudflared services can be managed here")
	}
	if IsHostMachine(machineID) && !hostApproved {
		return ErrHostTunnelProtected
	}
	if action != "restart" && action != "start" {
		return fmt.Errorf("tunnel action must be start or restart")
	}
	if _, err := e.machineRun(ctx, machineID, binSystemctl, action, unit); err != nil {
		return fmt.Errorf("could not %s %s: %w", action, unit, err)
	}
	return nil
}

// ExecRun runs a command in a container once, without a terminal.
func (e *Engine) ExecRun(ctx context.Context, machineID, containerID string, argv []string) (string, int, error) {
	if len(argv) == 0 {
		return "", -1, fmt.Errorf("command is required")
	}
	sock, err := e.resolveSocket(ctx, machineID, nil)
	if err != nil {
		return "", -1, err
	}
	runner, ok := e.client(sock).(interface {
		execRun(ctx context.Context, id string, argv []string) (string, int, error)
	})
	if !ok {
		return "", -1, fmt.Errorf("exec is unavailable")
	}
	return runner.execRun(ctx, containerID, argv)
}
