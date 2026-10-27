package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Binaries the engine runs.
const (
	NFTBin      = "/usr/sbin/nft"
	IPBin       = "/usr/sbin/ip"
	IPTablesBin = "/usr/sbin/iptables"
	DockerBin   = "/usr/bin/docker"
)

const (
	specFile   = "edge.json"
	stateFile  = "edge.state.json"
	rulesFile  = "edge.nft"
	keyDir     = "keys"
	originFile = "edge.forward.origin"
	// SysctlFile turns on IPv4 forwarding while a tunnel exists.
	SysctlFile = "/etc/sysctl.d/50-ndl-edge.conf"
	// dockerComment marks the DOCKER-USER rules this engine owns.
	dockerComment = "ndl-edge"
)

// WG configures WireGuard devices (wgctrl over netlink in production).
type WG interface {
	Device(name string) (*wgtypes.Device, error)
	ConfigureDevice(name string, cfg wgtypes.Config) error
}

// Engine applies a Spec on this node. It runs as root in the agent.
type Engine struct {
	// StateDir keeps the spec and the tunnel private keys (0600), so the
	// tunnels come back after a reboot without the control plane.
	StateDir string
	// SysRoot prefixes /proc and /etc (tests).
	SysRoot string
	// Run runs a command; nil runs it on the host.
	Run func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)
	// WG configures WireGuard; nil opens wgctrl.
	WG WG
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	mu      sync.Mutex
	applied string
}

// Counters are packets and bytes.
type Counters struct {
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

// TunnelStatus is one tunnel on this node.
type TunnelStatus struct {
	Interface  string    `json:"interface"`
	Role       string    `json:"role"`
	PublicKey  string    `json:"public_key,omitempty"`
	ListenPort int       `json:"listen_port,omitempty"`
	Up         bool      `json:"up"`
	Handshake  time.Time `json:"handshake,omitempty"`
	Endpoint   string    `json:"endpoint,omitempty"`
	RxBytes    int64     `json:"rx_bytes"`
	TxBytes    int64     `json:"tx_bytes"`
	Egress     []string  `json:"egress,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Status is edge forwarding on this node. Forwards are keyed
// "<tunnel id>/<forward id>".
type Status struct {
	Active    bool                    `json:"active"`
	SpecHash  string                  `json:"spec_hash,omitempty"`
	AppliedAt time.Time               `json:"applied_at,omitempty"`
	Tunnels   map[string]TunnelStatus `json:"tunnels,omitempty"`
	Forwards  map[string]Counters     `json:"forwards,omitempty"`
	Warnings  []string                `json:"warnings,omitempty"`
	Error     string                  `json:"error,omitempty"`
	SampledAt time.Time               `json:"sampled_at"`
}

type state struct {
	SpecHash  string    `json:"spec_hash"`
	AppliedAt time.Time `json:"applied_at"`
	Error     string    `json:"error,omitempty"`
	Warnings  []string  `json:"warnings,omitempty"`
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) path(p string) string {
	if e.SysRoot == "" || e.SysRoot == "/" {
		return p
	}
	return filepath.Join(e.SysRoot, p)
}

func (e *Engine) run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	if e.Run != nil {
		return e.Run(ctx, stdin, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			msg = err.Error()
		}
		return buf.Bytes(), errors.New(clip(msg, 600))
	}
	return buf.Bytes(), nil
}

func (e *Engine) wg() (WG, error) {
	if e.WG != nil {
		return e.WG, nil
	}
	c, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("WireGuard is not available: %w", err)
	}
	e.WG = c
	return c, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// LoadSpec returns the applied spec, if any.
func (e *Engine) LoadSpec() (Spec, bool) {
	raw, err := os.ReadFile(filepath.Join(e.StateDir, specFile))
	if err != nil {
		return Spec{}, false
	}
	var s Spec
	if json.Unmarshal(raw, &s) != nil {
		return Spec{}, false
	}
	return s, true
}

func (e *Engine) loadState() state {
	var st state
	if raw, err := os.ReadFile(filepath.Join(e.StateDir, stateFile)); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

func (e *Engine) saveState(st state) {
	raw, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(filepath.Join(e.StateDir, stateFile), raw, 0o600)
}

// Apply makes the node match spec and returns its status, which carries
// this node's public key for each tunnel.
func (e *Engine) Apply(ctx context.Context, spec Spec) (Status, error) {
	spec, err := Normalize(spec)
	if err != nil {
		return Status{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
		return Status{}, err
	}
	raw, _ := json.MarshalIndent(spec, "", "  ")
	if err := os.WriteFile(filepath.Join(e.StateDir, specFile), raw, 0o600); err != nil {
		return Status{}, err
	}
	e.applied = ""
	st := state{SpecHash: spec.Hash(), AppliedAt: e.now().UTC()}
	warnings, err := e.reconcile(ctx, spec)
	st.Warnings = warnings
	if err != nil {
		st.Error = err.Error()
	}
	e.saveState(st)
	if err != nil {
		return Status{}, err
	}
	return e.status(ctx, spec, st)
}

// Ensure puts the applied spec back when something removed part of it, a
// container's address changed or the node restarted. It is cheap when
// nothing changed.
func (e *Engine) Ensure(ctx context.Context) error {
	spec, ok := e.LoadSpec()
	if !ok || len(spec.Tunnels) == 0 {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	warnings, err := e.reconcile(ctx, spec)
	st := e.loadState()
	st.Warnings = warnings
	st.Error = ""
	if err != nil {
		st.Error = err.Error()
	}
	e.saveState(st)
	return err
}

// Disable removes every tunnel, rule and route this engine made and puts
// IP forwarding back the way it was.
func (e *Engine) Disable(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	err := e.disableLocked(ctx)
	_ = os.Remove(filepath.Join(e.StateDir, specFile))
	_ = os.Remove(filepath.Join(e.StateDir, stateFile))
	_ = os.Remove(filepath.Join(e.StateDir, rulesFile))
	_ = os.RemoveAll(filepath.Join(e.StateDir, keyDir))
	return err
}

func (e *Engine) reconcile(ctx context.Context, spec Spec) ([]string, error) {
	if len(spec.Tunnels) == 0 {
		return nil, e.disableLocked(ctx)
	}
	var warnings []string
	if err := e.enableForwarding(); err != nil {
		warnings = append(warnings, "could not turn on IP forwarding: "+err.Error())
	}
	wg, err := e.wg()
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, t := range spec.Tunnels {
		want[IfName(t.ID)] = true
		if err := e.ensureTunnel(ctx, wg, t); err != nil {
			return warnings, fmt.Errorf("tunnel %s: %w", t.ID, err)
		}
	}
	if err := e.pruneLinks(ctx, want); err != nil {
		warnings = append(warnings, err.Error())
	}
	e.pruneRules(ctx, spec.Tunnels)
	env := Env{Egress: map[string][]string{}}
	for _, t := range spec.Tunnels {
		if t.Role == RoleOrigin && len(t.Egress) > 0 {
			env.Egress[t.ID] = e.resolveEgress(ctx, t.Egress)
		}
	}
	script := Render(spec, env)
	if script != e.applied || !e.tableLoaded(ctx) {
		if _, err := e.run(ctx, []byte(script), NFTBin, "-c", "-f", "-"); err != nil {
			return warnings, fmt.Errorf("rules failed the check: %w", err)
		}
		if _, err := e.run(ctx, []byte(script), NFTBin, "-f", "-"); err != nil {
			return warnings, fmt.Errorf("rules did not load: %w", err)
		}
		e.applied = script
		_ = os.WriteFile(filepath.Join(e.StateDir, rulesFile), []byte(script), 0o600)
	}
	if w := e.syncDockerUser(ctx, spec.Tunnels); w != "" {
		warnings = append(warnings, w)
	}
	return warnings, nil
}

func (e *Engine) disableLocked(ctx context.Context) error {
	var errs []string
	if _, err := e.run(ctx, []byte(Render(Spec{}, Env{})), NFTBin, "-f", "-"); err != nil {
		errs = append(errs, "nft: "+err.Error())
	}
	e.applied = ""
	if err := e.pruneLinks(ctx, nil); err != nil {
		errs = append(errs, err.Error())
	}
	e.pruneRules(ctx, nil)
	e.syncDockerUser(ctx, nil)
	e.restoreForwarding()
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (e *Engine) tableLoaded(ctx context.Context) bool {
	_, err := e.run(ctx, nil, NFTBin, "list", "table", "inet", TableName)
	return err == nil
}

// ensureTunnel creates the WireGuard interface, sets its key, peer and
// address and, on an origin, the return route.
func (e *Engine) ensureTunnel(ctx context.Context, wg WG, t Tunnel) error {
	ifn := IfName(t.ID)
	if _, err := e.run(ctx, nil, IPBin, "link", "show", "dev", ifn); err != nil {
		if _, err := e.run(ctx, nil, IPBin, "link", "add", "dev", ifn, "type", "wireguard"); err != nil {
			return fmt.Errorf("could not create %s: %w", ifn, err)
		}
	}
	key, err := e.privateKey(ifn)
	if err != nil {
		return err
	}
	cfg := wgtypes.Config{PrivateKey: &key, ReplacePeers: true}
	if t.Role == RoleEdge {
		port := t.ListenPort
		cfg.ListenPort = &port
	}
	if t.PeerKey != "" {
		pub, err := wgtypes.ParseKey(t.PeerKey)
		if err != nil {
			return fmt.Errorf("peer key: %w", err)
		}
		peer := wgtypes.PeerConfig{PublicKey: pub, ReplaceAllowedIPs: true}
		if t.Role == RoleEdge {
			peer.AllowedIPs = []net.IPNet{{IP: net.ParseIP(t.Peer).To4(), Mask: net.CIDRMask(32, 32)}}
		} else {
			// Players' packets arrive with their own source address.
			peer.AllowedIPs = []net.IPNet{{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)}}
			ka := Keepalive * time.Second
			peer.PersistentKeepaliveInterval = &ka
			if t.Endpoint != "" {
				ep, err := net.ResolveUDPAddr("udp4", t.Endpoint)
				if err != nil {
					return fmt.Errorf("endpoint: %w", err)
				}
				peer.Endpoint = ep
			}
		}
		cfg.Peers = []wgtypes.PeerConfig{peer}
	}
	if same, _ := e.wgMatches(wg, ifn, t, cfg); !same {
		if err := wg.ConfigureDevice(ifn, cfg); err != nil {
			return fmt.Errorf("could not configure %s: %w", ifn, err)
		}
	}
	if _, err := e.run(ctx, nil, IPBin, "link", "set", "dev", ifn, "mtu", strconv.Itoa(MTU), "up"); err != nil {
		return err
	}
	if !e.hasOnlyAddr(ctx, ifn, t.Address) {
		_, _ = e.run(ctx, nil, IPBin, "-4", "addr", "flush", "dev", ifn)
		if _, err := e.run(ctx, nil, IPBin, "-4", "addr", "add", t.Address, "dev", ifn); err != nil {
			return fmt.Errorf("address: %w", err)
		}
	}
	// Loose reverse-path checking: players' addresses route via the uplink.
	_ = os.WriteFile(e.path("/proc/sys/net/ipv4/conf/"+ifn+"/rp_filter"), []byte("2\n"), 0o644)
	if t.Role == RoleOrigin {
		table := strconv.Itoa(Table(t.Index))
		if _, err := e.run(ctx, nil, IPBin, "-4", "route", "replace", "default", "dev", ifn, "table", table); err != nil {
			return fmt.Errorf("route: %w", err)
		}
		// Kill switch: if the tunnel interface disappears its route goes
		// with it, and marked traffic must not fall back to the home line.
		if _, err := e.run(ctx, nil, IPBin, "-4", "route", "replace", "unreachable", "default", "metric", "4096", "table", table); err != nil {
			return fmt.Errorf("route: %w", err)
		}
		if err := e.ensureRule(ctx, t.Index); err != nil {
			return err
		}
	}
	return nil
}

// wgMatches reports whether the device already has cfg, so an Ensure does
// not reset the peer (and its handshake) every few seconds.
func (e *Engine) wgMatches(wg WG, ifn string, t Tunnel, cfg wgtypes.Config) (bool, error) {
	dev, err := wg.Device(ifn)
	if err != nil {
		return false, err
	}
	if dev.PrivateKey != *cfg.PrivateKey {
		return false, nil
	}
	if cfg.ListenPort != nil && dev.ListenPort != *cfg.ListenPort {
		return false, nil
	}
	if len(dev.Peers) != len(cfg.Peers) {
		return false, nil
	}
	for i, want := range cfg.Peers {
		have := dev.Peers[i]
		if have.PublicKey != want.PublicKey || len(have.AllowedIPs) != len(want.AllowedIPs) {
			return false, nil
		}
		for j := range want.AllowedIPs {
			if have.AllowedIPs[j].String() != want.AllowedIPs[j].String() {
				return false, nil
			}
		}
		if want.PersistentKeepaliveInterval != nil && have.PersistentKeepaliveInterval != *want.PersistentKeepaliveInterval {
			return false, nil
		}
		// The edge learns the origin's endpoint from its packets; only the
		// origin pins it.
		if t.Role == RoleOrigin && want.Endpoint != nil && (have.Endpoint == nil || have.Endpoint.String() != want.Endpoint.String()) {
			return false, nil
		}
	}
	return true, nil
}

func (e *Engine) privateKey(ifn string) (wgtypes.Key, error) {
	dir := filepath.Join(e.StateDir, keyDir)
	path := filepath.Join(dir, ifn+".key")
	if raw, err := os.ReadFile(path); err == nil {
		if k, err := wgtypes.ParseKey(strings.TrimSpace(string(raw))); err == nil {
			return k, nil
		}
	}
	k, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return wgtypes.Key{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return wgtypes.Key{}, err
	}
	if err := os.WriteFile(path, []byte(k.String()+"\n"), 0o600); err != nil {
		return wgtypes.Key{}, err
	}
	return k, nil
}

func (e *Engine) hasOnlyAddr(ctx context.Context, ifn, cidr string) bool {
	out, err := e.run(ctx, nil, IPBin, "-j", "-4", "addr", "show", "dev", ifn)
	if err != nil {
		return false
	}
	var links []struct {
		AddrInfo []struct {
			Local     string `json:"local"`
			PrefixLen int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if json.Unmarshal(out, &links) != nil || len(links) != 1 || len(links[0].AddrInfo) != 1 {
		return false
	}
	a := links[0].AddrInfo[0]
	return fmt.Sprintf("%s/%d", a.Local, a.PrefixLen) == cidr
}

type ipRule struct {
	Priority int    `json:"priority"`
	FWMark   string `json:"fwmark"`
	Table    string `json:"table"`
}

func (e *Engine) rules(ctx context.Context) []ipRule {
	out, err := e.run(ctx, nil, IPBin, "-j", "-4", "rule", "show")
	if err != nil {
		return nil
	}
	var rs []ipRule
	_ = json.Unmarshal(out, &rs)
	return rs
}

func ours(prio int) bool { return prio > RulePriority && prio <= RulePriority+MaxIndex }

func (e *Engine) ensureRule(ctx context.Context, index int) error {
	prio := RulePriority + index
	mark := fmt.Sprintf("0x%x", Mark(index))
	table := strconv.Itoa(Table(index))
	found := false
	for _, r := range e.rules(ctx) {
		if r.Priority != prio {
			continue
		}
		if r.FWMark == mark && r.Table == table && !found {
			found = true
			continue
		}
		_, _ = e.run(ctx, nil, IPBin, "-4", "rule", "del", "priority", strconv.Itoa(prio))
	}
	if found {
		return nil
	}
	if _, err := e.run(ctx, nil, IPBin, "-4", "rule", "add", "priority", strconv.Itoa(prio), "fwmark", mark, "lookup", table); err != nil {
		return fmt.Errorf("rule: %w", err)
	}
	return nil
}

// pruneRules removes rules and routes of tunnels that are gone.
func (e *Engine) pruneRules(ctx context.Context, keep []Tunnel) {
	want := map[int]bool{}
	for _, t := range keep {
		if t.Role == RoleOrigin {
			want[RulePriority+t.Index] = true
		}
	}
	for _, r := range e.rules(ctx) {
		if !ours(r.Priority) || want[r.Priority] {
			continue
		}
		_, _ = e.run(ctx, nil, IPBin, "-4", "rule", "del", "priority", strconv.Itoa(r.Priority))
		_, _ = e.run(ctx, nil, IPBin, "-4", "route", "flush", "table", strconv.Itoa(Table(r.Priority-RulePriority)))
	}
}

// pruneLinks deletes No-dal edge interfaces not in want.
func (e *Engine) pruneLinks(ctx context.Context, want map[string]bool) error {
	out, err := e.run(ctx, nil, IPBin, "-j", "link", "show", "type", "wireguard")
	if err != nil {
		return nil
	}
	var links []struct {
		Name string `json:"ifname"`
	}
	_ = json.Unmarshal(out, &links)
	var errs []string
	for _, l := range links {
		if !strings.HasPrefix(l.Name, IfPrefix) || want[l.Name] {
			continue
		}
		if _, err := e.run(ctx, nil, IPBin, "link", "del", "dev", l.Name); err != nil {
			errs = append(errs, err.Error())
		}
		_ = os.Remove(filepath.Join(e.StateDir, keyDir, l.Name+".key"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("could not remove old tunnels: %s", strings.Join(errs, "; "))
	}
	return nil
}

// resolveEgress turns container names into their current addresses.
func (e *Engine) resolveEgress(ctx context.Context, names []string) []string {
	var out []string
	for _, n := range names {
		if net.ParseIP(n) != nil {
			out = append(out, n)
			continue
		}
		raw, err := e.run(ctx, nil, DockerBin, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}", n)
		if err != nil {
			continue
		}
		out = append(out, strings.Fields(string(raw))...)
	}
	return sortedUnique(out)
}

// syncDockerUser keeps tunnel traffic out of Docker's FORWARD drop policy.
// Docker evaluates DOCKER-USER first and never flushes it; an accept in
// No-dal's own table cannot override a drop in Docker's chain.
func (e *Engine) syncDockerUser(ctx context.Context, tunnels []Tunnel) string {
	if _, err := os.Stat(e.path(IPTablesBin)); err != nil {
		return ""
	}
	out, err := e.run(ctx, nil, IPTablesBin, "-w", "5", "-S", "DOCKER-USER")
	if err != nil {
		return ""
	}
	var want [][]string
	for _, t := range tunnels {
		ifn := IfName(t.ID)
		if t.Role == RoleEdge {
			want = append(want,
				[]string{"-i", ifn, "-s", t.Peer + "/32"},
				[]string{"-o", ifn, "-d", t.Peer + "/32"})
			continue
		}
		want = append(want, []string{"-o", ifn})
		seen := map[string]bool{}
		for _, f := range t.Forwards {
			if f.Target != "" && !seen[f.Target] {
				seen[f.Target] = true
				want = append(want, []string{"-i", ifn, "-d", f.Target + "/32"})
			}
		}
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "--comment "+dockerComment) || !strings.HasPrefix(line, "-A DOCKER-USER ") {
			continue
		}
		have[line] = true
	}
	wantLine := map[string]bool{}
	var warn string
	for _, w := range want {
		line := "-A DOCKER-USER " + strings.Join(w, " ") + " -m comment --comment " + dockerComment + " -j ACCEPT"
		wantLine[line] = true
		if have[line] {
			continue
		}
		args := append([]string{"-w", "5", "-I", "DOCKER-USER"}, w...)
		args = append(args, "-m", "comment", "--comment", dockerComment, "-j", "ACCEPT")
		if _, err := e.run(ctx, nil, IPTablesBin, args...); err != nil {
			warn = "could not let tunnel traffic past Docker's forward policy: " + err.Error()
		}
	}
	for line := range have {
		if wantLine[line] {
			continue
		}
		args := append([]string{"-w", "5", "-D"}, strings.Fields(strings.TrimPrefix(line, "-A "))...)
		_, _ = e.run(ctx, nil, IPTablesBin, args...)
	}
	return warn
}

func (e *Engine) enableForwarding() error {
	proc := e.path("/proc/sys/net/ipv4/ip_forward")
	origin := filepath.Join(e.StateDir, originFile)
	if _, err := os.Stat(origin); err != nil {
		cur := "1"
		if raw, err := os.ReadFile(proc); err == nil {
			cur = strings.TrimSpace(string(raw))
		}
		if err := os.WriteFile(origin, []byte(cur+"\n"), 0o600); err != nil {
			return err
		}
	}
	conf := e.path(SysctlFile)
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		return err
	}
	body := "# No-dal edge forwarding. Removed with the last tunnel.\nnet.ipv4.ip_forward=1\n"
	if raw, err := os.ReadFile(conf); err != nil || string(raw) != body {
		if err := os.WriteFile(conf, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(proc, []byte("1\n"), 0o644)
}

func (e *Engine) restoreForwarding() {
	origin := filepath.Join(e.StateDir, originFile)
	raw, err := os.ReadFile(origin)
	_ = os.Remove(e.path(SysctlFile))
	if err != nil {
		return
	}
	_ = os.Remove(origin)
	// Something else (Docker, a NAT network) may need forwarding now, so it
	// is only turned off when it was off and nothing else asks for it.
	if strings.TrimSpace(string(raw)) == "0" && !e.forwardingWanted() {
		_ = os.WriteFile(e.path("/proc/sys/net/ipv4/ip_forward"), []byte("0\n"), 0o644)
	}
}

func (e *Engine) forwardingWanted() bool {
	entries, _ := os.ReadDir(e.path("/etc/sysctl.d"))
	for _, ent := range entries {
		raw, err := os.ReadFile(filepath.Join(e.path("/etc/sysctl.d"), ent.Name()))
		if err == nil && strings.Contains(strings.ReplaceAll(string(raw), " ", ""), "net.ipv4.ip_forward=1") {
			return true
		}
	}
	_, err := os.Stat(e.path("/run/docker.sock"))
	return err == nil
}

// Status reports the tunnels and forward counters.
func (e *Engine) Status(ctx context.Context) (Status, error) {
	spec, ok := e.LoadSpec()
	if !ok {
		return Status{SampledAt: e.now().UTC()}, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status(ctx, spec, e.loadState())
}

func (e *Engine) status(ctx context.Context, spec Spec, st state) (Status, error) {
	out := Status{
		Active: len(spec.Tunnels) > 0, SpecHash: st.SpecHash, AppliedAt: st.AppliedAt,
		Warnings: st.Warnings, Error: st.Error, SampledAt: e.now().UTC(),
		Tunnels: map[string]TunnelStatus{}, Forwards: map[string]Counters{},
	}
	wg, werr := e.wg()
	for _, t := range spec.Tunnels {
		ifn := IfName(t.ID)
		ts := TunnelStatus{Interface: ifn, Role: t.Role}
		if key, err := e.privateKey(ifn); err == nil {
			ts.PublicKey = key.PublicKey().String()
		}
		if werr != nil {
			ts.Error = werr.Error()
		} else if dev, err := wg.Device(ifn); err != nil {
			ts.Error = "tunnel interface is missing"
		} else {
			ts.Up = true
			ts.ListenPort = dev.ListenPort
			for _, p := range dev.Peers {
				if !p.LastHandshakeTime.IsZero() {
					ts.Handshake = p.LastHandshakeTime.UTC()
				}
				if p.Endpoint != nil {
					ts.Endpoint = p.Endpoint.String()
				}
				ts.RxBytes += p.ReceiveBytes
				ts.TxBytes += p.TransmitBytes
			}
		}
		if t.Role == RoleOrigin && len(t.Egress) > 0 {
			ts.Egress = e.resolveEgress(ctx, t.Egress)
		}
		out.Tunnels[t.ID] = ts
	}
	counters := e.counters(ctx)
	for _, t := range spec.Tunnels {
		for _, f := range t.Forwards {
			out.Forwards[t.ID+"/"+f.ID] = counters[CounterName(t.ID, f.ID)]
		}
	}
	return out, nil
}

func (e *Engine) counters(ctx context.Context) map[string]Counters {
	out := map[string]Counters{}
	raw, err := e.run(ctx, nil, NFTBin, "-j", "list", "counters", "table", "inet", TableName)
	if err != nil {
		return out
	}
	var doc struct {
		Nftables []struct {
			Counter *struct {
				Name    string `json:"name"`
				Packets uint64 `json:"packets"`
				Bytes   uint64 `json:"bytes"`
			} `json:"counter"`
		} `json:"nftables"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return out
	}
	for _, item := range doc.Nftables {
		if item.Counter != nil {
			out[item.Counter.Name] = Counters{Packets: item.Counter.Packets, Bytes: item.Counter.Bytes}
		}
	}
	return out
}
