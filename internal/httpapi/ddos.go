package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/shield"
)

// DDoS protection. The control plane keeps one settings document per
// cluster and turns it, with the live game servers and workloads, into a
// shield.Spec that the agent loads into nftables on the uplink. A loop
// re-applies it when targets change, samples counters and records attacks.

// ShieldRPC runs DDoS protection on the node's agent.
type ShieldRPC interface {
	Shield(ctx context.Context, action string, spec *shield.Spec, addr string, until int64) (shield.Status, error)
}

const (
	ddosTick = 15 * time.Second
	// ddosSamples is one hour of 15 second samples per target.
	ddosSamples = 240
	// ddosQuietTicks below a quarter of the attack threshold end an attack.
	ddosQuietTicks       = 4
	defaultAttackPPS     = 1000
	ddosLink             = "/network/ddos"
	noteDDoSAttack       = "ddos.attack"
	noteDDoSAttackEnded  = "ddos.attack.ended"
	ddosTargetGameServer = "gameserver"
	ddosTargetWorkload   = "workload"
	ddosTargetHost       = "host"
	ddosTargetCustom     = "custom"
	ddosAutoPrefix       = "auto:gs:"
	maxDDoSBanMinutes    = 7 * 24 * 60
)

// ddosConfig is the stored settings document.
type ddosConfig struct {
	Mode            string   `json:"mode"`
	Interfaces      []string `json:"interfaces"`
	Trusted         []string `json:"trusted"`
	TrustLocal      bool     `json:"trust_local"`
	Blocked         []string `json:"blocked"`
	DropInvalidTCP  bool     `json:"drop_invalid_tcp"`
	Hardening       bool     `json:"hardening"`
	AutoGameServers bool     `json:"auto_game_servers"`
	// AttackPPS is how many dropped packets per second count as an attack.
	AttackPPS int                `json:"attack_pps"`
	Notify    bool               `json:"notify"`
	Targets   []ddosTarget       `json:"targets"`
	Bans      map[string]ddosBan `json:"bans"`
}

// ddosTarget is one protected service.
type ddosTarget struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	RefID   string `json:"ref_id,omitempty"`
	Enabled bool   `json:"enabled"`
	Profile string `json:"profile"`
	// Protocol and Ports: empty ports on a game server means its published
	// ports, on a workload every port.
	Protocol   string        `json:"protocol"`
	Ports      []string      `json:"ports"`
	Dest       []string      `json:"dest"`
	Limits     shield.Limits `json:"limits"`
	Action     string        `json:"action"`
	BanSeconds int           `json:"ban_seconds,omitempty"`
	Allow      []string      `json:"allow"`
	Block      []string      `json:"block"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

type ddosBan struct {
	Until  int64     `json:"until"`
	Reason string    `json:"reason,omitempty"`
	By     string    `json:"by,omitempty"`
	At     time.Time `json:"at"`
}

func defaultDDoSConfig() ddosConfig {
	return ddosConfig{
		Mode: shield.ModeEnforce, TrustLocal: true, DropInvalidTCP: true, Hardening: true,
		AutoGameServers: true, AttackPPS: defaultAttackPPS, Notify: true,
	}
}

// ddosState is the loop's view of the node.
type ddosState struct {
	// tick serializes ticks from the loop and from handlers.
	tick      sync.Mutex
	mu        sync.Mutex
	offErr    string
	wake      chan struct{}
	status    shield.Status
	statusErr string
	applyErr  string
	pushed    string
	disabled  bool
	prev      map[string]shield.RuleStatus
	prevAt    time.Time
	samples   map[string][]ddosSample
	events    map[string]*ddosOpen
	// edges is protection on edge nodes, by node ID.
	edges map[string]*ddosEdge
}

type ddosSample struct {
	At      time.Time `json:"at"`
	InPPS   int64     `json:"in_pps"`
	DropPPS int64     `json:"drop_pps"`
}

type ddosOpen struct {
	ev    appdb.DDoSEvent
	quiet int
}

func (s *Server) ddosRef() *ddosState {
	s.ddosOnce.Do(func() {
		s.ddos = &ddosState{wake: make(chan struct{}, 1), samples: map[string][]ddosSample{}, events: map[string]*ddosOpen{}}
	})
	return s.ddos
}

func (s *Server) ddosWake() {
	select {
	case s.ddosRef().wake <- struct{}{}:
	default:
	}
}

func (s *Server) ddosEnabled(ctx context.Context, clusterID string) bool {
	row, _ := s.Store.GetFeature(ctx, clusterID, features.IDDDoS)
	return row != nil && row.Enabled
}

func (s *Server) requireDDoS(w http.ResponseWriter, r *http.Request, perm string) (*principal, bool) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return nil, false
	}
	if !s.ddosEnabled(r.Context(), p.User.ClusterID) {
		writeErr(w, http.StatusNotFound, "DDoS Protection is not enabled. Enable it from Add Features.")
		return nil, false
	}
	return p, true
}

func (s *Server) loadDDoS(ctx context.Context, clusterID string) (ddosConfig, error) {
	raw, err := s.Store.DDoSConfig(ctx, clusterID)
	if err != nil {
		return ddosConfig{}, err
	}
	cfg := defaultDDoSConfig()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return ddosConfig{}, err
		}
	}
	if cfg.AttackPPS <= 0 {
		cfg.AttackPPS = defaultAttackPPS
	}
	return cfg, nil
}

func (s *Server) saveDDoS(ctx context.Context, clusterID, by string, cfg ddosConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.Store.PutDDoSConfig(ctx, clusterID, raw, by)
}

// ddosResolved is a target as it is protected now.
type ddosResolved struct {
	ddosTarget
	// Auto is a game server protected by default, with no stored target.
	Auto     bool     `json:"auto"`
	RefName  string   `json:"ref_name,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	// Rule is what is loaded; nil when the target is off or has no address.
	Rule *shield.Rule `json:"rule,omitempty"`
}

// gamePorts returns a game server's published host ports and whether every
// one is TCP.
func gamePorts(row appdb.GameServer) ([]string, string) {
	var ports []gameserver.Port
	_ = json.Unmarshal(row.PortsJSON, &ports)
	seen := map[string]bool{}
	var out []string
	udp, tcp := false, false
	for _, p := range ports {
		host := gameserver.HostPortOf(p)
		if host <= 0 {
			continue
		}
		if strings.EqualFold(p.Protocol, "tcp") {
			tcp = true
		} else {
			udp = true
		}
		v := fmt.Sprint(host)
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	proto := shield.ProtoBoth
	switch {
	case tcp && !udp:
		proto = shield.ProtoTCP
	case udp && !tcp:
		proto = shield.ProtoUDP
	}
	return out, proto
}

func workloadAddrs(nics []appdb.WorkloadNIC) []string {
	var out []string
	for _, n := range nics {
		for _, v := range []string{n.IPv4, n.IPv4Address, n.IPv6Address} {
			for _, part := range strings.Split(v, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if p, err := shield.ParseCIDR(part); err == nil {
					a := p.Addr()
					out = append(out, netip.PrefixFrom(a, a.BitLen()).String())
				}
			}
		}
	}
	return out
}

func ruleFromTarget(t ddosTarget) shield.Rule {
	return shield.Rule{
		ID: t.ID, Name: t.Name, Protocol: t.Protocol, Ports: t.Ports, Dest: t.Dest, Limits: t.Limits,
		Action: t.Action, BanSeconds: t.BanSeconds, Allow: t.Allow, Block: t.Block,
	}
}

func autoGameTarget(row appdb.GameServer) ddosTarget {
	ports, proto := gamePorts(row)
	prof, _ := shield.LookupProfile(shield.ProfileForGame(row.Family, proto == shield.ProtoTCP))
	return ddosTarget{
		ID: ddosAutoPrefix + row.ID, Name: row.Name, Kind: ddosTargetGameServer, RefID: row.ID, Enabled: true,
		Profile: prof.ID, Protocol: proto, Ports: ports, Limits: prof.Limits, Action: prof.Action,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

// resolveDDoS turns the settings into what is protected now.
func (s *Server) resolveDDoS(ctx context.Context, clusterID string, cfg ddosConfig) ([]ddosResolved, error) {
	games := map[string]appdb.GameServer{}
	if s.gameServersEnabled(ctx, clusterID) {
		rows, err := s.Store.ListGameServers(ctx, clusterID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			games[row.ID] = row
		}
	}
	wls, err := s.Store.ListWorkloads(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	workloads := map[string]appdb.Workload{}
	for _, wl := range wls {
		workloads[wl.ID] = wl
	}
	var out []ddosResolved
	explicit := map[string]bool{}
	for _, t := range cfg.Targets {
		res := ddosResolved{ddosTarget: t}
		rule := ruleFromTarget(t)
		switch t.Kind {
		case ddosTargetGameServer:
			explicit[t.RefID] = true
			row, ok := games[t.RefID]
			if !ok {
				res.Warnings = append(res.Warnings, "This game server no longer exists, or Game Servers is turned off.")
				break
			}
			res.RefName = row.Name
			rule.Host = true
			if len(rule.Ports) == 0 {
				rule.Ports, _ = gamePorts(row)
			}
			if len(rule.Ports) == 0 {
				res.Warnings = append(res.Warnings, "This game server publishes no ports.")
			}
		case ddosTargetWorkload:
			wl, ok := workloads[t.RefID]
			if !ok {
				res.Warnings = append(res.Warnings, "This workload no longer exists.")
				break
			}
			res.RefName = wl.Name
			nics, err := s.Store.ListWorkloadNICs(ctx, clusterID, wl.ID)
			if err != nil {
				return nil, err
			}
			rule.Dest = workloadAddrs(nics)
			if len(rule.Dest) == 0 {
				res.Warnings = append(res.Warnings, "No address is known for this workload yet; it is protected once it has one.")
			}
		case ddosTargetHost:
			rule.Host = true
		}
		if t.Enabled && (rule.Host || len(rule.Dest) > 0) && len(res.Warnings) == 0 {
			r := rule
			res.Rule = &r
		}
		out = append(out, res)
	}
	if cfg.AutoGameServers {
		ids := make([]string, 0, len(games))
		for id := range games {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if explicit[id] {
				continue
			}
			row := games[id]
			t := autoGameTarget(row)
			res := ddosResolved{ddosTarget: t, Auto: true, RefName: row.Name}
			if len(t.Ports) == 0 {
				res.Warnings = append(res.Warnings, "This game server publishes no ports.")
			} else {
				rule := ruleFromTarget(t)
				rule.Host = true
				res.Rule = &rule
			}
			out = append(out, res)
		}
	}
	return out, nil
}

func (s *Server) ddosSpec(cfg ddosConfig, targets []ddosResolved) shield.Spec {
	spec := shield.Spec{
		Mode: cfg.Mode, Devices: cfg.Interfaces, Trusted: cfg.Trusted, Blocked: cfg.Blocked, TrustLocal: cfg.TrustLocal,
		DropInvalidTCP: cfg.DropInvalidTCP, Hardening: cfg.Hardening, Rules: []shield.Rule{},
	}
	for _, t := range targets {
		if t.Rule != nil {
			spec.Rules = append(spec.Rules, *t.Rule)
		}
	}
	now := s.now().Unix()
	for addr, b := range cfg.Bans {
		if b.Until > now {
			if spec.Bans == nil {
				spec.Bans = map[string]int64{}
			}
			spec.Bans[addr] = b.Until
		}
	}
	return spec
}

// RunDDoS keeps the node's protection in step with the settings.
func (s *Server) RunDDoS(ctx context.Context) {
	if s.Shield == nil {
		return
	}
	st := s.ddosRef()
	t := time.NewTicker(ddosTick)
	defer t.Stop()
	for {
		s.tickDDoS(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-st.wake:
		}
	}
}

func (s *Server) tickDDoS(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil {
		return
	}
	st := s.ddosRef()
	st.tick.Lock()
	defer st.tick.Unlock()
	if !s.ddosEnabled(ctx, cluster.ID) {
		s.ddosEdgesOff(ctx, cluster.ID)
		st.mu.Lock()
		done := st.disabled
		st.mu.Unlock()
		if done {
			return
		}
		if _, err := s.Shield.Shield(ctx, "disable", nil, "", 0); err != nil {
			st.mu.Lock()
			if st.offErr != err.Error() {
				log.Printf("ddos protection: turning off: %v", err)
			}
			st.offErr = err.Error()
			st.mu.Unlock()
			return
		}
		st.mu.Lock()
		st.disabled, st.pushed, st.status = true, "", shield.Status{}
		st.mu.Unlock()
		return
	}
	cfg, err := s.loadDDoS(ctx, cluster.ID)
	if err != nil {
		return
	}
	targets, err := s.resolveDDoS(ctx, cluster.ID, cfg)
	if err != nil {
		return
	}
	spec := s.ddosSpec(cfg, targets)
	edgeRules := s.edgeDDoSRules(ctx, cluster.ID, targets)
	if control, _ := s.Store.GetNode(ctx, cluster.ID); control != nil {
		ddosLocalEdge(&spec, edgeRules, control.ID)
	}
	status, err := s.Shield.Shield(ctx, "status", nil, "", 0)
	st.mu.Lock()
	st.disabled = false
	if err != nil {
		st.statusErr = err.Error()
		st.mu.Unlock()
		return
	}
	st.statusErr = ""
	pushed := st.pushed
	st.mu.Unlock()
	hash := spec.Hash()
	if !status.Active || status.SpecHash != hash || pushed != hash {
		applied, err := s.Shield.Shield(ctx, "apply", &spec, "", 0)
		st.mu.Lock()
		if err != nil {
			if st.applyErr != err.Error() {
				log.Printf("ddos protection: %v", err)
			}
			st.applyErr = err.Error()
			st.status = status
			st.mu.Unlock()
			return
		}
		st.applyErr, st.pushed = "", hash
		st.mu.Unlock()
		status = applied
	}
	for _, es := range s.ddosPushEdges(ctx, cluster.ID, cfg, edgeRules) {
		status = mergeShield(status, es)
	}
	s.sampleDDoS(ctx, cluster.ID, cfg, targets, status)
}

// sampleDDoS turns counters into rates and opens and closes attacks.
func (s *Server) sampleDDoS(ctx context.Context, clusterID string, cfg ddosConfig, targets []ddosResolved, status shield.Status) {
	st := s.ddosRef()
	st.mu.Lock()
	defer st.mu.Unlock()
	now := status.SampledAt
	if now.IsZero() {
		now = s.now()
	}
	dt := now.Sub(st.prevAt).Seconds()
	names := map[string]string{}
	for _, t := range targets {
		names[t.ID] = t.Name
	}
	live := map[string]bool{}
	for id, cur := range status.Rules {
		live[id] = true
		prev, ok := st.prev[id]
		if !ok || dt <= 0 || cur.In.Packets < prev.In.Packets || cur.Dropped.Packets < prev.Dropped.Packets {
			continue
		}
		in := int64(float64(cur.In.Packets-prev.In.Packets) / dt)
		drop := int64(float64(cur.Dropped.Packets-prev.Dropped.Packets) / dt)
		ss := append(st.samples[id], ddosSample{At: now, InPPS: in, DropPPS: drop})
		if len(ss) > ddosSamples {
			ss = ss[len(ss)-ddosSamples:]
		}
		st.samples[id] = ss
		s.trackAttack(ctx, clusterID, cfg, id, names[id], cur, prev, drop, now)
	}
	for id := range st.samples {
		if !live[id] {
			delete(st.samples, id)
		}
	}
	for id, open := range st.events {
		if !live[id] {
			s.closeAttack(ctx, clusterID, cfg, open, now)
			delete(st.events, id)
		}
	}
	st.prev, st.prevAt, st.status = status.Rules, now, status
}

func topReason(cur, prev shield.RuleStatus) string {
	best, bestN := "", uint64(0)
	for reason, c := range cur.Reasons {
		n := c.Packets - prev.Reasons[reason].Packets
		if c.Packets < prev.Reasons[reason].Packets {
			n = c.Packets
		}
		if n > bestN {
			best, bestN = reason, n
		}
	}
	return best
}

// ddosReasonText says what a drop reason means.
var ddosReasonText = map[string]string{
	"block": "blocked networks",
	"ban":   "banned sources",
	"bad":   "malformed packets",
	"amp":   "reflection traffic",
	"query": "server query floods",
	"syn":   "SYN floods",
	"flood": "per-source floods",
	"cap":   "the total packet cap",
	"conns": "too many connections",
}

// trackAttack is called with st.mu held.
func (s *Server) trackAttack(ctx context.Context, clusterID string, cfg ddosConfig, id, name string, cur, prev shield.RuleStatus, drop int64, now time.Time) {
	st := s.ddos
	open := st.events[id]
	dropped := int64(cur.Dropped.Packets - prev.Dropped.Packets)
	bytes := int64(cur.Dropped.Bytes - prev.Dropped.Bytes)
	if open == nil {
		if drop < int64(cfg.AttackPPS) {
			return
		}
		open = &ddosOpen{ev: appdb.DDoSEvent{
			ID: uuid.NewString(), ClusterID: clusterID, RuleID: id, Target: name, StartedAt: now,
		}}
		st.events[id] = open
		if cfg.Notify {
			reason := ddosReasonText[topReason(cur, prev)]
			body := fmt.Sprintf("Dropping %d packets per second to %s", drop, name)
			if reason != "" {
				body += ", mostly " + reason
			}
			body += ". The filter is holding it back; check the target if players report lag."
			go s.notifyWithPermission(context.Background(), clusterID, rbac.DDoSRead, "", noteDDoSAttack, "DDoS attack on "+name, body, ddosLink)
		}
	}
	ev := &open.ev
	ev.DroppedPackets += dropped
	ev.DroppedBytes += bytes
	if drop > ev.PeakPPS {
		ev.PeakPPS = drop
	}
	if cur.Bans > ev.Sources {
		ev.Sources = cur.Bans
	}
	if r := topReason(cur, prev); r != "" {
		ev.TopReason = r
	}
	if drop*4 < int64(cfg.AttackPPS) {
		open.quiet++
	} else {
		open.quiet = 0
	}
	if open.quiet >= ddosQuietTicks {
		s.closeAttack(ctx, clusterID, cfg, open, now)
		delete(st.events, id)
		return
	}
	_ = s.Store.PutDDoSEvent(ctx, *ev)
}

func (s *Server) closeAttack(ctx context.Context, clusterID string, cfg ddosConfig, open *ddosOpen, now time.Time) {
	ev := open.ev
	ev.EndedAt = &now
	_ = s.Store.PutDDoSEvent(ctx, ev)
	if cfg.Notify {
		body := fmt.Sprintf("It lasted %s, peaked at %d packets per second and %d packets were dropped.",
			now.Sub(ev.StartedAt).Round(time.Second), ev.PeakPPS, ev.DroppedPackets)
		go s.notifyWithPermission(context.Background(), clusterID, rbac.DDoSRead, "", noteDDoSAttackEnded, "DDoS attack on "+ev.Target+" ended", body, ddosLink)
	}
}

// ddosDisableNow removes the rules when the feature is turned off.
func (s *Server) ddosDisableNow(ctx context.Context) error {
	if s.Shield == nil {
		return nil
	}
	st := s.ddosRef()
	st.tick.Lock()
	defer st.tick.Unlock()
	if _, err := s.Shield.Shield(ctx, "disable", nil, "", 0); err != nil {
		return err
	}
	st.mu.Lock()
	st.disabled, st.pushed, st.status = true, "", shield.Status{}
	st.mu.Unlock()
	if cluster, err := s.Store.GetCluster(ctx); err == nil && cluster != nil {
		s.ddosEdgesOff(ctx, cluster.ID)
	}
	return nil
}

// ddosApplyNow applies the settings at once so a change shows up in the
// response, not 15 seconds later.
func (s *Server) ddosApplyNow(ctx context.Context) {
	if s.Shield == nil {
		return
	}
	s.tickDDoS(ctx)
}

type ddosTargetView struct {
	ddosResolved
	Traffic struct {
		InPackets      uint64                     `json:"in_packets"`
		DroppedPackets uint64                     `json:"dropped_packets"`
		DroppedBytes   uint64                     `json:"dropped_bytes"`
		Reasons        map[string]shield.Counters `json:"reasons,omitempty"`
		Bans           int                        `json:"bans"`
		InPPS          int64                      `json:"in_pps"`
		DropPPS        int64                      `json:"drop_pps"`
		UnderAttack    bool                       `json:"under_attack"`
	} `json:"traffic"`
	History []ddosSample `json:"history,omitempty"`
}

type ddosBanView struct {
	Addr     string `json:"addr"`
	TargetID string `json:"target_id,omitempty"`
	Target   string `json:"target,omitempty"`
	Seconds  int    `json:"seconds"`
	Manual   bool   `json:"manual"`
	Reason   string `json:"reason,omitempty"`
	By       string `json:"by,omitempty"`
}

func (s *Server) ddosView(ctx context.Context, clusterID string, history bool) (map[string]any, error) {
	cfg, err := s.loadDDoS(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	targets, err := s.resolveDDoS(ctx, clusterID, cfg)
	if err != nil {
		return nil, err
	}
	st := s.ddosRef()
	st.mu.Lock()
	status := st.status
	statusErr, applyErr := st.statusErr, st.applyErr
	samples := map[string][]ddosSample{}
	for id, ss := range st.samples {
		samples[id] = append([]ddosSample(nil), ss...)
	}
	attacks := map[string]bool{}
	for id := range st.events {
		attacks[id] = true
	}
	st.mu.Unlock()
	names := map[string]string{}
	views := make([]ddosTargetView, 0, len(targets))
	for _, t := range targets {
		names[t.ID] = t.Name
		v := ddosTargetView{ddosResolved: t}
		if rs, ok := status.Rules[t.ID]; ok {
			v.Traffic.InPackets, v.Traffic.DroppedPackets, v.Traffic.DroppedBytes = rs.In.Packets, rs.Dropped.Packets, rs.Dropped.Bytes
			v.Traffic.Reasons, v.Traffic.Bans = rs.Reasons, rs.Bans
		}
		if ss := samples[t.ID]; len(ss) > 0 {
			last := ss[len(ss)-1]
			v.Traffic.InPPS, v.Traffic.DropPPS = last.InPPS, last.DropPPS
			if history {
				v.History = ss
			}
		}
		v.Traffic.UnderAttack = attacks[t.ID]
		views = append(views, v)
	}
	bans := []ddosBanView{}
	for _, b := range status.Bans {
		v := ddosBanView{Addr: b.Addr, TargetID: b.RuleID, Target: names[b.RuleID], Seconds: b.Seconds, Manual: b.RuleID == ""}
		if m, ok := cfg.Bans[b.Addr]; ok && v.Manual {
			v.Reason, v.By = m.Reason, m.By
		}
		bans = append(bans, v)
	}
	sort.Slice(bans, func(i, j int) bool { return bans[i].Seconds > bans[j].Seconds })
	cfgOut := cfg
	cfgOut.Targets, cfgOut.Bans = nil, nil
	return map[string]any{
		"settings": cfgOut,
		"targets":  views,
		"bans":     bans,
		"status": map[string]any{
			"active": status.Active, "mode": status.Mode, "applied_at": status.AppliedAt, "devices": status.Devices,
			"host": status.Host, "blocked": status.Blocked, "banned": status.Banned, "conntrack": status.Conntrack,
			"hardening": status.Hardening, "error": firstNonEmpty(applyErr, status.Error, statusErr), "sampled_at": status.SampledAt,
		},
		"edges":         s.ddosEdgeViews(ctx, clusterID, names),
		"profiles":      shield.Profiles(),
		"amp_ports":     shield.AmplificationPorts,
		"under_attack":  len(attacks) > 0,
		"attack_counts": len(attacks),
	}, nil
}

func (s *Server) getDDoS(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDDoS(w, r, rbac.DDoSRead)
	if !ok {
		return
	}
	out, err := s.ddosView(r.Context(), p.User.ClusterID, r.URL.Query().Get("history") != "0")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ddosMutate loads, changes, validates, saves and applies the settings
// under one lock.
func (s *Server) ddosMutate(w http.ResponseWriter, r *http.Request, p *principal, action, subject string, change func(*ddosConfig) error) bool {
	ctx := r.Context()
	clusterID := p.User.ClusterID
	s.ddosMu.Lock()
	cfg, err := s.loadDDoS(ctx, clusterID)
	if err != nil {
		s.ddosMu.Unlock()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if err := change(&cfg); err != nil {
		s.ddosMu.Unlock()
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	targets, err := s.resolveDDoS(ctx, clusterID, cfg)
	if err != nil {
		s.ddosMu.Unlock()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return false
	}
	spec := s.ddosSpec(cfg, targets)
	if err := spec.Normalize(); err != nil {
		s.ddosMu.Unlock()
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	if err := s.saveDDoS(ctx, clusterID, p.User.ID, cfg); err != nil {
		s.ddosMu.Unlock()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return false
	}
	s.ddosMu.Unlock()
	s.audit(r, clusterID, p.User.ID, action, "ok", subject)
	s.ddosApplyNow(ctx)
	return true
}

func (s *Server) writeDDoS(w http.ResponseWriter, r *http.Request, clusterID string) {
	out, err := s.ddosView(r.Context(), clusterID, false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type ddosSettingsReq struct {
	Mode            *string   `json:"mode"`
	Interfaces      *[]string `json:"interfaces"`
	Trusted         *[]string `json:"trusted"`
	TrustLocal      *bool     `json:"trust_local"`
	Blocked         *[]string `json:"blocked"`
	DropInvalidTCP  *bool     `json:"drop_invalid_tcp"`
	Hardening       *bool     `json:"hardening"`
	AutoGameServers *bool     `json:"auto_game_servers"`
	AttackPPS       *int      `json:"attack_pps"`
	Notify          *bool     `json:"notify"`
}

func (s *Server) putDDoSSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDDoS(w, r, rbac.DDoSManage)
	if !ok {
		return
	}
	var req ddosSettingsReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	if !s.ddosMutate(w, r, p, "ddos.settings.update", "settings", func(cfg *ddosConfig) error {
		if req.Mode != nil {
			cfg.Mode = strings.TrimSpace(*req.Mode)
		}
		if req.Interfaces != nil {
			cfg.Interfaces = *req.Interfaces
		}
		if req.Trusted != nil {
			cfg.Trusted = *req.Trusted
		}
		if req.TrustLocal != nil {
			cfg.TrustLocal = *req.TrustLocal
		}
		if req.Blocked != nil {
			cfg.Blocked = *req.Blocked
		}
		if req.DropInvalidTCP != nil {
			cfg.DropInvalidTCP = *req.DropInvalidTCP
		}
		if req.Hardening != nil {
			cfg.Hardening = *req.Hardening
		}
		if req.AutoGameServers != nil {
			cfg.AutoGameServers = *req.AutoGameServers
		}
		if req.AttackPPS != nil {
			if *req.AttackPPS < 10 || *req.AttackPPS > 100_000_000 {
				return errors.New("attack threshold must be between 10 and 100000000 dropped packets per second")
			}
			cfg.AttackPPS = *req.AttackPPS
		}
		if req.Notify != nil {
			cfg.Notify = *req.Notify
		}
		// Normalize here too so the stored lists are the cleaned ones.
		probe := shield.Spec{Mode: cfg.Mode, Devices: cfg.Interfaces, Trusted: cfg.Trusted, Blocked: cfg.Blocked}
		if err := probe.Normalize(); err != nil {
			return err
		}
		cfg.Mode, cfg.Interfaces, cfg.Trusted, cfg.Blocked = probe.Mode, probe.Devices, probe.Trusted, probe.Blocked
		return nil
	}) {
		return
	}
	s.writeDDoS(w, r, p.User.ClusterID)
}

type ddosTargetReq struct {
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	RefID      string         `json:"ref_id"`
	Enabled    *bool          `json:"enabled"`
	Profile    string         `json:"profile"`
	Protocol   string         `json:"protocol"`
	Ports      []string       `json:"ports"`
	Dest       []string       `json:"dest"`
	Limits     *shield.Limits `json:"limits"`
	Action     string         `json:"action"`
	BanSeconds int            `json:"ban_seconds"`
	Allow      []string       `json:"allow"`
	Block      []string       `json:"block"`
}

// applyTargetReq fills a target from a request. Limits, protocol and action
// default to the profile's.
func applyTargetReq(t *ddosTarget, req ddosTargetReq) error {
	t.Name = strings.TrimSpace(req.Name)
	if t.Enabled = true; req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	t.Profile = strings.TrimSpace(req.Profile)
	if t.Profile == "" {
		t.Profile = shield.ProfileCustom
	}
	prof, ok := shield.LookupProfile(t.Profile)
	if !ok {
		return fmt.Errorf("profile %q is unknown", t.Profile)
	}
	t.Protocol = firstNonEmpty(strings.ToLower(strings.TrimSpace(req.Protocol)), prof.Protocol)
	t.Action = firstNonEmpty(strings.ToLower(strings.TrimSpace(req.Action)), prof.Action)
	t.Limits = prof.Limits
	if req.Limits != nil {
		t.Limits = *req.Limits
	}
	t.Ports, t.Dest, t.Allow, t.Block, t.BanSeconds = req.Ports, req.Dest, req.Allow, req.Block, req.BanSeconds
	switch t.Kind {
	case ddosTargetGameServer, ddosTargetWorkload:
		if strings.TrimSpace(t.RefID) == "" {
			return errors.New("choose the " + strings.Replace(t.Kind, "gameserver", "game server", 1) + " to protect")
		}
		t.Dest = nil
	case ddosTargetHost:
		t.RefID, t.Dest = "", nil
		if len(t.Ports) == 0 {
			return errors.New("choose the ports of the host service to protect")
		}
	case ddosTargetCustom:
		t.RefID = ""
		if len(t.Dest) == 0 {
			return errors.New("add at least one destination address or network")
		}
	default:
		return errors.New("kind must be gameserver, workload, host or custom")
	}
	if t.Name == "" {
		t.Name = t.Kind
	}
	// Validate the rule now so a bad port or address is refused with the
	// target, not on the next apply.
	rule := ruleFromTarget(*t)
	rule.Host = true
	if err := rule.Normalize(); err != nil {
		return err
	}
	t.Protocol, t.Ports, t.Action, t.Allow, t.Block = rule.Protocol, rule.Ports, rule.Action, rule.Allow, rule.Block
	if t.Kind == ddosTargetCustom {
		t.Dest = rule.Dest
	}
	return nil
}

func (s *Server) createDDoSTarget(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDDoS(w, r, rbac.DDoSManage)
	if !ok {
		return
	}
	var req ddosTargetReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	now := s.now()
	t := ddosTarget{ID: uuid.NewString(), Kind: strings.TrimSpace(req.Kind), RefID: strings.TrimSpace(req.RefID), CreatedAt: now, UpdatedAt: now}
	if !s.ddosMutate(w, r, p, "ddos.target.create", t.ID, func(cfg *ddosConfig) error {
		if err := applyTargetReq(&t, req); err != nil {
			return err
		}
		for _, o := range cfg.Targets {
			if t.RefID != "" && o.Kind == t.Kind && o.RefID == t.RefID {
				return errors.New(o.Name + " already has DDoS protection settings; change those instead")
			}
		}
		cfg.Targets = append(cfg.Targets, t)
		return nil
	}) {
		return
	}
	s.writeDDoS(w, r, p.User.ClusterID)
}

func (s *Server) updateDDoSTarget(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDDoS(w, r, rbac.DDoSManage)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req ddosTargetReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	if !s.ddosMutate(w, r, p, "ddos.target.update", id, func(cfg *ddosConfig) error {
		// Saving an automatic game server target stores it, so it keeps
		// the changes.
		if ref, auto := strings.CutPrefix(id, ddosAutoPrefix); auto {
			for _, o := range cfg.Targets {
				if o.Kind == ddosTargetGameServer && o.RefID == ref {
					return errors.New("this game server already has stored settings")
				}
			}
			now := s.now()
			t := ddosTarget{ID: uuid.NewString(), Kind: ddosTargetGameServer, RefID: ref, CreatedAt: now, UpdatedAt: now}
			if err := applyTargetReq(&t, req); err != nil {
				return err
			}
			cfg.Targets = append(cfg.Targets, t)
			return nil
		}
		for i := range cfg.Targets {
			if cfg.Targets[i].ID != id {
				continue
			}
			t := cfg.Targets[i]
			if err := applyTargetReq(&t, req); err != nil {
				return err
			}
			t.UpdatedAt = s.now()
			cfg.Targets[i] = t
			return nil
		}
		return errNotFoundDDoS
	}) {
		return
	}
	s.writeDDoS(w, r, p.User.ClusterID)
}

var errNotFoundDDoS = errors.New("no such protected target")

func (s *Server) deleteDDoSTarget(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDDoS(w, r, rbac.DDoSManage)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !s.ddosMutate(w, r, p, "ddos.target.delete", id, func(cfg *ddosConfig) error {
		for i := range cfg.Targets {
			if cfg.Targets[i].ID == id {
				cfg.Targets = append(cfg.Targets[:i], cfg.Targets[i+1:]...)
				return nil
			}
		}
		if strings.HasPrefix(id, ddosAutoPrefix) {
			return errors.New("a game server protected by default has nothing stored to delete; turn it off instead")
		}
		return errNotFoundDDoS
	}) {
		return
	}
	s.writeDDoS(w, r, p.User.ClusterID)
}

func (s *Server) banDDoS(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDDoS(w, r, rbac.DDoSManage)
	if !ok {
		return
	}
	var req struct {
		Addr    string `json:"addr"`
		Minutes int    `json:"minutes"`
		Reason  string `json:"reason"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	addr, err := shield.ParseAddr(req.Addr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Minutes <= 0 {
		req.Minutes = 60
	}
	if req.Minutes > maxDDoSBanMinutes {
		writeErr(w, http.StatusBadRequest, "a ban can last at most 7 days; add the address to Blocked networks to block it for good")
		return
	}
	until := s.now().Add(time.Duration(req.Minutes) * time.Minute).Unix()
	if !s.ddosMutate(w, r, p, "ddos.ban", addr.String(), func(cfg *ddosConfig) error {
		if cfg.Bans == nil {
			cfg.Bans = map[string]ddosBan{}
		}
		now := s.now().Unix()
		for a, b := range cfg.Bans {
			if b.Until <= now {
				delete(cfg.Bans, a)
			}
		}
		cfg.Bans[addr.String()] = ddosBan{Until: until, Reason: clipText(strings.TrimSpace(req.Reason), 200), By: p.User.Username, At: s.now()}
		return nil
	}) {
		return
	}
	if s.Shield != nil {
		if _, err := s.Shield.Shield(r.Context(), "ban", nil, addr.String(), until); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.ddosEdgesBan(r.Context(), p.User.ClusterID, "ban", addr.String(), until)
		s.ddosApplyNow(r.Context())
	}
	s.writeDDoS(w, r, p.User.ClusterID)
}

func clipText(v string, n int) string {
	if len(v) <= n {
		return v
	}
	return v[:n]
}

func (s *Server) unbanDDoS(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDDoS(w, r, rbac.DDoSManage)
	if !ok {
		return
	}
	addr, err := shield.ParseAddr(r.PathValue("addr"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.ddosMutate(w, r, p, "ddos.unban", addr.String(), func(cfg *ddosConfig) error {
		delete(cfg.Bans, addr.String())
		return nil
	}) {
		return
	}
	if s.Shield != nil {
		if _, err := s.Shield.Shield(r.Context(), "unban", nil, addr.String(), 0); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.ddosEdgesBan(r.Context(), p.User.ClusterID, "unban", addr.String(), 0)
		s.ddosApplyNow(r.Context())
	}
	s.writeDDoS(w, r, p.User.ClusterID)
}

func (s *Server) listDDoSEvents(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDDoS(w, r, rbac.DDoSRead)
	if !ok {
		return
	}
	items, err := s.Store.ListDDoSEvents(r.Context(), p.User.ClusterID, 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []appdb.DDoSEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) applyDDoS(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDDoS(w, r, rbac.DDoSManage)
	if !ok {
		return
	}
	st := s.ddosRef()
	st.mu.Lock()
	st.pushed = ""
	st.mu.Unlock()
	s.ddosApplyNow(r.Context())
	s.audit(r, p.User.ClusterID, p.User.ID, "ddos.apply", "ok", "")
	s.writeDDoS(w, r, p.User.ClusterID)
}
