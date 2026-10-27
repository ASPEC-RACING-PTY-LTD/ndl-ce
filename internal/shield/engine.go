package shield

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NFTBin is the nft binary the engine runs.
const NFTBin = "/usr/sbin/nft"

const (
	specFile  = "shield.json"
	stateFile = "shield.state.json"
	rulesFile = "shield.nft"
)

// Engine applies a Spec on this node. It runs as root in the agent.
type Engine struct {
	// StateDir keeps the applied spec so protection comes back after a
	// reboot or an agent restart without the control plane.
	StateDir string
	// SysRoot prefixes /proc, /sys and /etc (tests).
	SysRoot string
	// Run runs a command; nil runs it on the host.
	Run func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	mu sync.Mutex
	// applied is the script last loaded, so Ensure re-applies only when
	// the node's addresses or uplink change.
	applied string
}

// State is what the engine remembers about the last apply.
type State struct {
	SpecHash  string    `json:"spec_hash"`
	AppliedAt time.Time `json:"applied_at"`
	Devices   []string  `json:"devices"`
	Error     string    `json:"error,omitempty"`
}

// Counters are packets and bytes.
type Counters struct {
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

// RuleStatus is one target's traffic since the last apply.
type RuleStatus struct {
	In      Counters            `json:"in"`
	Dropped Counters            `json:"dropped"`
	Reasons map[string]Counters `json:"reasons,omitempty"`
	Bans    int                 `json:"bans"`
}

// Status is the node's protection.
type Status struct {
	Active    bool                  `json:"active"`
	Mode      string                `json:"mode,omitempty"`
	SpecHash  string                `json:"spec_hash,omitempty"`
	AppliedAt time.Time             `json:"applied_at,omitempty"`
	Devices   []string              `json:"devices,omitempty"`
	Host      []string              `json:"host,omitempty"`
	Error     string                `json:"error,omitempty"`
	Rules     map[string]RuleStatus `json:"rules,omitempty"`
	Blocked   Counters              `json:"blocked"`
	Banned    Counters              `json:"banned"`
	Bans      []Ban                 `json:"bans,omitempty"`
	Conntrack struct {
		Count int64 `json:"count"`
		Max   int64 `json:"max"`
	} `json:"conntrack"`
	Hardening []SysctlState `json:"hardening,omitempty"`
	SampledAt time.Time     `json:"sampled_at"`
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
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			msg = err.Error()
		}
		return buf.Bytes(), errors.New(clip(msg, 600))
	}
	return buf.Bytes(), nil
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

func (e *Engine) saveJSON(name string, v any) error {
	if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(e.StateDir, name+".tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(e.StateDir, name))
}

func (e *Engine) loadState() State {
	var st State
	raw, err := os.ReadFile(filepath.Join(e.StateDir, stateFile))
	if err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

// Environment finds the uplink and this node's addresses.
func (e *Engine) Environment(spec Spec) (Env, error) {
	env := Env{Devices: spec.Devices, Now: e.now().Unix()}
	if len(env.Devices) == 0 {
		env.Devices = e.DetectUplinks()
	}
	if len(env.Devices) == 0 {
		return env, errors.New("no uplink interface found; choose the interfaces to filter on in DDoS protection settings")
	}
	env.Host = HostAddrs()
	env.Local = LocalNets()
	return env, nil
}

// DetectUplinks returns the physical interfaces behind the default routes.
// A bridge is replaced by its physical ports: the ingress hook of a bridge
// device does not see traffic bridged to guests, its ports do.
func (e *Engine) DetectUplinks() []string {
	seen := map[string]bool{}
	var out []string
	for _, dev := range e.defaultRouteDevs() {
		for _, d := range e.physical(dev) {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (e *Engine) defaultRouteDevs() []string {
	var out []string
	if f, err := os.Open(e.path("/proc/net/route")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) > 7 && fs[1] == "00000000" && fs[7] == "00000000" {
				out = append(out, fs[0])
			}
		}
		f.Close()
	}
	if f, err := os.Open(e.path("/proc/net/ipv6_route")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) == 10 && fs[0] == strings.Repeat("0", 32) && fs[1] == "00" && fs[9] != "lo" {
				out = append(out, fs[9])
			}
		}
		f.Close()
	}
	return out
}

func (e *Engine) exists(p string) bool {
	_, err := os.Stat(e.path(p))
	return err == nil
}

// uplinkPort reports whether a bridge port carries outside traffic: a NIC,
// a bond or a VLAN, not a guest's veth or tap.
func (e *Engine) uplinkPort(dev string) bool {
	return e.exists("/sys/class/net/"+dev+"/device") || e.exists("/sys/class/net/"+dev+"/bonding") || e.exists("/proc/net/vlan/"+dev)
}

func (e *Engine) physical(dev string) []string {
	if dev == "" || dev == "lo" || !deviceRE.MatchString(dev) {
		return nil
	}
	if !e.exists("/sys/class/net/" + dev + "/bridge") {
		return []string{dev}
	}
	ents, err := os.ReadDir(e.path("/sys/class/net/" + dev + "/brif"))
	if err != nil {
		return nil
	}
	var out []string
	for _, ent := range ents {
		if e.uplinkPort(ent.Name()) {
			out = append(out, ent.Name())
		}
	}
	return out
}

// LocalNets returns the networks of this node's global unicast addresses.
func LocalNets() []netip.Prefix {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Prefix
	for _, it := range ifs {
		addrs, err := it.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			ones, _ := ipn.Mask.Size()
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ones == 0 {
				continue
			}
			out = append(out, netip.PrefixFrom(ip, ones).Masked())
		}
	}
	return out
}

// HostAddrs returns this node's global unicast addresses.
func HostAddrs() []netip.Addr {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, it := range ifs {
		addrs, err := it.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
				continue
			}
			out = append(out, ip)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

// Apply loads a spec and keeps it for Ensure. Automatic bans of targets
// that are still protected are carried over.
func (e *Engine) Apply(ctx context.Context, spec Spec) (Status, error) {
	if err := spec.Normalize(); err != nil {
		return Status{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.load(ctx, spec); err != nil {
		st := e.loadState()
		st.Error = err.Error()
		_ = e.saveJSON(stateFile, st)
		return Status{}, err
	}
	if err := e.saveJSON(specFile, spec); err != nil {
		return Status{}, err
	}
	if err := e.harden(spec.Hardening); err != nil {
		return Status{}, err
	}
	return e.statusLocked(ctx)
}

func (e *Engine) load(ctx context.Context, spec Spec) error {
	env, err := e.Environment(spec)
	if err != nil {
		return err
	}
	prev, _ := e.LoadSpec()
	env.Carry = e.carryBans(ctx, prev)
	script, err := Render(spec, env)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(e.StateDir, rulesFile)
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		return err
	}
	if _, err := e.run(ctx, nil, NFTBin, "-c", "-f", path); err != nil {
		return fmt.Errorf("the rules did not pass nft's check, nothing was changed: %w", err)
	}
	if _, err := e.run(ctx, nil, NFTBin, "-f", path); err != nil {
		return fmt.Errorf("loading the rules failed: %w", err)
	}
	e.applied = withoutBans(script)
	return e.saveJSON(stateFile, State{SpecHash: spec.Hash(), AppliedAt: e.now().UTC(), Devices: env.Devices})
}

// withoutBans drops the ban lines, which change with the clock, so Ensure
// compares only the rules.
func withoutBans(script string) string {
	var b strings.Builder
	for _, l := range strings.Split(script, "\n") {
		if !strings.HasPrefix(l, "add element ") {
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// carryBans reads automatic bans by rule ID before the tables are replaced.
func (e *Engine) carryBans(ctx context.Context, prev Spec) map[string][]Ban {
	doc, err := e.listTable(ctx, "netdev")
	if err != nil {
		return nil
	}
	out := map[string][]Ban{}
	for _, b := range bansFrom(doc, prev) {
		if b.RuleID != "" {
			out[b.RuleID] = append(out[b.RuleID], b)
		}
	}
	return out
}

// Ensure re-applies the kept spec when the tables are missing or the node's
// addresses or uplink changed. The agent calls it at start and on a timer.
func (e *Engine) Ensure(ctx context.Context) error {
	spec, ok := e.LoadSpec()
	if !ok {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	env, err := e.Environment(spec)
	if err != nil {
		return err
	}
	script, err := Render(spec, env)
	if err != nil {
		return err
	}
	_, missing := e.listTable(ctx, "netdev")
	if missing == nil && withoutBans(script) == e.applied {
		return nil
	}
	if err := e.load(ctx, spec); err != nil {
		return err
	}
	return e.harden(spec.Hardening)
}

// Disable removes both tables and the kept spec and undoes hardening.
func (e *Engine) Disable(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	script := fmt.Sprintf("destroy table netdev %s\ndestroy table inet %s\n", TableName, TableName)
	if _, err := e.run(ctx, []byte(script), NFTBin, "-f", "-"); err != nil {
		return err
	}
	e.applied = ""
	for _, f := range []string{specFile, stateFile, rulesFile} {
		_ = os.Remove(filepath.Join(e.StateDir, f))
	}
	return e.harden(false)
}

// Ban blocks an address from every target until the time given.
func (e *Engine) Ban(ctx context.Context, addr string, until int64) error {
	a, err := ParseAddr(addr)
	if err != nil {
		return err
	}
	left := until - e.now().Unix()
	if left < 1 {
		return errors.New("the ban has already ended")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	spec, ok := e.LoadSpec()
	if !ok {
		return errors.New("DDoS protection is not applied on this node")
	}
	cmd := fmt.Sprintf("add element netdev %s ban%s { %s timeout %ds }\n", TableName, family(a), a, left)
	if _, err := e.run(ctx, []byte(cmd), NFTBin, "-f", "-"); err != nil {
		return err
	}
	if spec.Bans == nil {
		spec.Bans = map[string]int64{}
	}
	spec.Bans[a.String()] = until
	return e.saveJSON(specFile, spec)
}

// Unban lifts every ban of an address: the hand-added one and automatic
// ones on each target.
func (e *Engine) Unban(ctx context.Context, addr string) error {
	a, err := ParseAddr(addr)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	spec, ok := e.LoadSpec()
	if !ok {
		return errors.New("DDoS protection is not applied on this node")
	}
	key := a.String()
	if a.Is6() {
		key = netip.PrefixFrom(a, 64).Masked().Addr().String()
	}
	sets := []string{"ban" + family(a)}
	for i := range spec.Rules {
		sets = append(sets, chainName(i)+"_ban"+family(a))
	}
	for _, set := range sets {
		elem := key
		if set == "ban6" {
			elem = a.String()
		}
		// A set or element that is not there is fine: nothing to lift.
		_, _ = e.run(ctx, nil, NFTBin, "delete", "element", "netdev", TableName, set, "{ "+elem+" }")
	}
	delete(spec.Bans, a.String())
	return e.saveJSON(specFile, spec)
}

type nftDoc struct {
	Nftables []map[string]json.RawMessage `json:"nftables"`
}

func (e *Engine) listTable(ctx context.Context, fam string) (nftDoc, error) {
	out, err := e.run(ctx, nil, NFTBin, "-j", "list", "table", fam, TableName)
	if err != nil {
		return nftDoc{}, err
	}
	var doc nftDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		return nftDoc{}, err
	}
	return doc, nil
}

type nftCounter struct {
	Name    string `json:"name"`
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

type nftSet struct {
	Name string            `json:"name"`
	Elem []json.RawMessage `json:"elem"`
}

func counters(doc nftDoc) map[string]Counters {
	out := map[string]Counters{}
	for _, it := range doc.Nftables {
		raw, ok := it["counter"]
		if !ok {
			continue
		}
		var c nftCounter
		if json.Unmarshal(raw, &c) == nil {
			out[c.Name] = Counters{Packets: c.Packets, Bytes: c.Bytes}
		}
	}
	return out
}

func setElems(doc nftDoc) map[string][]Ban {
	out := map[string][]Ban{}
	for _, it := range doc.Nftables {
		raw, ok := it["set"]
		if !ok {
			continue
		}
		var s nftSet
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		for _, el := range s.Elem {
			var plain string
			if json.Unmarshal(el, &plain) == nil {
				out[s.Name] = append(out[s.Name], Ban{Addr: plain})
				continue
			}
			var wrapped struct {
				Elem struct {
					Val     json.RawMessage `json:"val"`
					Expires int             `json:"expires"`
				} `json:"elem"`
			}
			if json.Unmarshal(el, &wrapped) != nil {
				continue
			}
			var val string
			if json.Unmarshal(wrapped.Elem.Val, &val) != nil {
				continue
			}
			out[s.Name] = append(out[s.Name], Ban{Addr: val, Seconds: wrapped.Elem.Expires})
		}
	}
	return out
}

// bansFrom lists the hand-added and automatic bans in a netdev listing.
func bansFrom(doc nftDoc, spec Spec) []Ban {
	elems := setElems(doc)
	var out []Ban
	for _, set := range []string{"ban4", "ban6"} {
		out = append(out, elems[set]...)
	}
	for i, rule := range spec.Rules {
		for _, set := range []string{chainName(i) + "_ban4", chainName(i) + "_ban6"} {
			for _, b := range elems[set] {
				b.RuleID = rule.ID
				out = append(out, b)
			}
		}
	}
	return out
}

// Status reads counters and bans.
func (e *Engine) Status(ctx context.Context) (Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusLocked(ctx)
}

func (e *Engine) statusLocked(ctx context.Context) (Status, error) {
	st := Status{SampledAt: e.now().UTC()}
	for _, a := range HostAddrs() {
		st.Host = append(st.Host, a.String())
	}
	st.Conntrack.Count = e.readInt("/proc/sys/net/netfilter/nf_conntrack_count")
	st.Conntrack.Max = e.readInt("/proc/sys/net/netfilter/nf_conntrack_max")
	st.Hardening = e.sysctlStates()
	spec, ok := e.LoadSpec()
	state := e.loadState()
	st.Error = state.Error
	if !ok {
		return st, nil
	}
	st.Mode, st.SpecHash, st.AppliedAt, st.Devices = spec.Mode, state.SpecHash, state.AppliedAt, state.Devices
	doc, err := e.listTable(ctx, "netdev")
	if err != nil {
		if st.Error == "" {
			st.Error = "the protection rules are not loaded: " + err.Error()
		}
		return st, nil
	}
	st.Active = true
	cs := counters(doc)
	if inet, err := e.listTable(ctx, "inet"); err == nil {
		for k, v := range counters(inet) {
			cs[k] = v
		}
	}
	st.Blocked, st.Banned = cs["blocked"], cs["banned"]
	st.Bans = bansFrom(doc, spec)
	st.Rules = map[string]RuleStatus{}
	for i, rule := range spec.Rules {
		n := chainName(i)
		rs := RuleStatus{In: cs[n+"_in"], Reasons: map[string]Counters{}}
		for _, reason := range CounterReasons {
			c, ok := cs[n+"_"+reason]
			if !ok || c.Packets == 0 {
				continue
			}
			rs.Reasons[reason] = c
			rs.Dropped.Packets += c.Packets
			rs.Dropped.Bytes += c.Bytes
		}
		for _, b := range st.Bans {
			if b.RuleID == rule.ID {
				rs.Bans++
			}
		}
		st.Rules[rule.ID] = rs
	}
	return st, nil
}

func (e *Engine) readInt(p string) int64 {
	raw, err := os.ReadFile(e.path(p))
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return n
}
