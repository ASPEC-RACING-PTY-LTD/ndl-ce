package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/edge"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/shield"
)

// Edge forwarding. A service on an origin node with no public address (a
// home server behind a router) is published on an edge node that has one
// (a VPS in the cluster). The control plane keeps one settings document per
// cluster, gives every edge and origin pair a WireGuard tunnel, and a loop
// pushes each node its part. Public keys are swapped through the control
// plane; private keys never leave their node.

// EdgeRPC runs edge forwarding on the control node's own agent.
type EdgeRPC interface {
	Edge(ctx context.Context, action string, spec *edge.Spec) (edge.Status, error)
}

// NodeAgent is what edge forwarding and DDoS protection run on any node.
type NodeAgent interface {
	EdgeRPC
	ShieldRPC
}

const (
	edgeTick = 15 * time.Second
	// edgeNet holds every tunnel's /30: tunnel n is 10.66.0.0 + 4n.
	edgeNet = "10.66.0.0/16"
	// edgePortBase + n is tunnel n's UDP port on its edge node.
	edgePortBase = 51870

	edgeKindGameServer = "gameserver"
	edgeKindWorkload   = "workload"
	edgeKindHost       = "host"

	edgeOldAgent = "This node's No-dal agent is too old for this. Update No-dal on it."
)

// edgeConfig is the stored settings document.
type edgeConfig struct {
	Nodes    map[string]edgeNodeCfg `json:"nodes"`
	Forwards []edgeForward          `json:"forwards"`
	Tunnels  []edgeTunnel           `json:"tunnels"`
}

// edgeNodeCfg is an edge node's settings.
type edgeNodeCfg struct {
	// PublicAddr is where origins reach the edge (and players see). Empty
	// uses the address the control plane dials the node's agent on.
	PublicAddr string `json:"public_addr,omitempty"`
}

// edgePort is a public port or range on the edge.
type edgePort struct {
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	PortEnd  int    `json:"port_end,omitempty"`
	ToPort   int    `json:"to_port,omitempty"`
}

// edgeForward is one service published through an edge.
type edgeForward struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	RefID        string `json:"ref_id,omitempty"`
	EdgeNodeID   string `json:"edge_node_id"`
	OriginNodeID string `json:"origin_node_id,omitempty"`
	// Ports empty uses a game server's published ports.
	Ports []edgePort `json:"ports"`
	// KeepAddress (workloads) passes the player's address on. The workload
	// must send its replies back through the origin for that to work.
	KeepAddress bool `json:"keep_address"`
	// Egress sends the service's own outbound traffic out through the edge
	// too, so it is seen (and listed by Steam) at the edge's address.
	Egress    bool      `json:"egress"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// edgeTunnel is the WireGuard link between one edge and one origin.
type edgeTunnel struct {
	ID           string `json:"id"`
	EdgeNodeID   string `json:"edge_node_id"`
	OriginNodeID string `json:"origin_node_id"`
	Index        int    `json:"index"`
	EdgeKey      string `json:"edge_key,omitempty"`
	OriginKey    string `json:"origin_key,omitempty"`
}

func (t edgeTunnel) addrs() (edgeAddr, originAddr netip.Addr) {
	base := netip.MustParsePrefix(edgeNet).Addr().As4()
	n := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	n += uint32(4 * t.Index)
	mk := func(v uint32) netip.Addr {
		return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
	}
	return mk(n + 1), mk(n + 2)
}

func (t edgeTunnel) port() int { return edgePortBase + t.Index }

// edgeResolved is a forward as it is published now.
type edgeResolved struct {
	edgeForward
	RefName      string     `json:"ref_name,omitempty"`
	EdgeName     string     `json:"edge_name,omitempty"`
	OriginName   string     `json:"origin_name,omitempty"`
	PublicAddr   string     `json:"public_addr,omitempty"`
	Resolved     []edgePort `json:"resolved_ports"`
	Target       string     `json:"target,omitempty"`
	TunnelID     string     `json:"tunnel_id,omitempty"`
	Warnings     []string   `json:"warnings,omitempty"`
	egressNames  []string
	resolvedNode string
	active       bool
}

type edgeNodeState struct {
	Status edge.Status
	Err    string
	At     time.Time
	pushed string
}

type edgeState struct {
	tick  sync.Mutex
	mu    sync.Mutex
	wake  chan struct{}
	nodes map[string]*edgeNodeState
}

func (s *Server) edgeRef() *edgeState {
	s.edgeOnce.Do(func() {
		s.edge = &edgeState{wake: make(chan struct{}, 1), nodes: map[string]*edgeNodeState{}}
	})
	return s.edge
}

func (s *Server) edgeWake() {
	select {
	case s.edgeRef().wake <- struct{}{}:
	default:
	}
}

// localAgent joins the control node's edge and shield clients.
type localAgent struct {
	EdgeRPC
	ShieldRPC
}

// nodeAgent returns the agent of a node: the local one for the control
// node, otherwise the worker's agent over mutual TLS.
func (s *Server) nodeAgent(ctx context.Context, clusterID, nodeID string) (NodeAgent, error) {
	if s.NodeAgents != nil {
		if a, ok := s.NodeAgents[nodeID]; ok {
			return a, nil
		}
		return nil, errors.New("this node's agent is not connected")
	}
	if s.applyLocal(ctx, clusterID, nodeID) {
		if s.EdgeAgent == nil || s.Shield == nil {
			return nil, errors.New("the control node's agent is not connected")
		}
		return localAgent{EdgeRPC: s.EdgeAgent, ShieldRPC: s.Shield}, nil
	}
	remote, err := s.Store.GetRemoteNode(ctx, clusterID, nodeID)
	if err != nil || remote == nil || strings.TrimSpace(remote.ListenAddr) == "" {
		return nil, errors.New("this node has no agent address")
	}
	return s.workerAgent(nodeID, remote.ListenAddr), nil
}

// agentError turns an old agent's refusal into words an admin can act on.
func agentError(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "unknown execute method") {
		return edgeOldAgent
	}
	return err.Error()
}

func (s *Server) loadEdge(ctx context.Context, clusterID string) (edgeConfig, error) {
	raw, err := s.Store.EdgeConfig(ctx, clusterID)
	if err != nil {
		return edgeConfig{}, err
	}
	cfg := edgeConfig{Nodes: map[string]edgeNodeCfg{}}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return edgeConfig{}, err
		}
	}
	if cfg.Nodes == nil {
		cfg.Nodes = map[string]edgeNodeCfg{}
	}
	return cfg, nil
}

func (s *Server) saveEdge(ctx context.Context, clusterID, by string, cfg edgeConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.Store.PutEdgeConfig(ctx, clusterID, raw, by)
}

// edgePublicAddr is where origins reach an edge node.
func (s *Server) edgePublicAddr(ctx context.Context, clusterID string, cfg edgeConfig, nodeID string) string {
	if a := strings.TrimSpace(cfg.Nodes[nodeID].PublicAddr); a != "" {
		return a
	}
	remote, _ := s.Store.GetRemoteNode(ctx, clusterID, nodeID)
	if remote == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(remote.ListenAddr))
	if err != nil {
		return ""
	}
	if a, err := netip.ParseAddr(host); err == nil && a.Is4() && !a.IsPrivate() && !a.IsLoopback() {
		return a.String()
	}
	return ""
}

// gameEdgePorts are the ports a game server is published on by default:
// every published port, except a Source server's TCP port, which is only
// RCON and is better kept off the internet.
func gameEdgePorts(row appdb.GameServer) []edgePort {
	var ports []gameserver.Port
	_ = json.Unmarshal(row.PortsJSON, &ports)
	seen := map[string]bool{}
	var out []edgePort
	for _, p := range ports {
		host := gameserver.HostPortOf(p)
		if host <= 0 {
			continue
		}
		proto := "udp"
		if strings.EqualFold(p.Protocol, "tcp") {
			proto = "tcp"
		}
		if proto == "tcp" && strings.EqualFold(row.Family, "source") {
			continue
		}
		k := fmt.Sprintf("%s/%d", proto, host)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, edgePort{Protocol: proto, Port: host})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Protocol < out[j].Protocol
	})
	return out
}

func firstIPv4(nics []appdb.WorkloadNIC) string {
	for _, c := range workloadAddrs(nics) {
		if p, err := netip.ParsePrefix(c); err == nil && p.Addr().Is4() {
			return p.Addr().String()
		}
	}
	return ""
}

// resolveEdge turns the settings into what is published now.
func (s *Server) resolveEdge(ctx context.Context, clusterID string, cfg edgeConfig) ([]edgeResolved, error) {
	nodes, err := s.Store.ListClusterNodes(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, n := range nodes {
		if n.RevokedAt == nil {
			names[n.ID] = n.Name
		}
	}
	control, _ := s.Store.GetNode(ctx, clusterID)
	controlID := ""
	if control != nil {
		controlID = control.ID
	}
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
	var out []edgeResolved
	for _, f := range cfg.Forwards {
		res := edgeResolved{edgeForward: f, EdgeName: names[f.EdgeNodeID]}
		ports := f.Ports
		switch f.Kind {
		case edgeKindGameServer:
			row, ok := games[f.RefID]
			if !ok {
				res.Warnings = append(res.Warnings, "This game server no longer exists, or Game Servers is turned off.")
				break
			}
			res.RefName = row.Name
			res.resolvedNode = firstNonEmpty(row.NodeID, controlID)
			if len(ports) == 0 {
				ports = gameEdgePorts(row)
			}
			if f.Egress {
				res.egressNames = []string{"ndl-gs-" + row.ID}
			}
		case edgeKindWorkload:
			wl, err := s.Store.GetWorkload(ctx, clusterID, f.RefID)
			if err != nil || wl == nil {
				res.Warnings = append(res.Warnings, "This workload no longer exists.")
				break
			}
			res.RefName = wl.Name
			res.resolvedNode = firstNonEmpty(wl.NodeID, wl.OwnerNodeID, controlID)
			nics, err := s.Store.ListWorkloadNICs(ctx, clusterID, wl.ID)
			if err != nil {
				return nil, err
			}
			res.Target = firstIPv4(nics)
			if res.Target == "" {
				res.Warnings = append(res.Warnings, "No IPv4 address is known for this workload yet; it is published once it has one.")
			} else if f.Egress {
				res.egressNames = []string{res.Target}
			}
		case edgeKindHost:
			res.resolvedNode = f.OriginNodeID
		}
		res.Resolved = ports
		res.OriginNodeID = res.resolvedNode
		res.OriginName = names[res.resolvedNode]
		if len(res.Warnings) == 0 {
			switch {
			case names[f.EdgeNodeID] == "":
				res.Warnings = append(res.Warnings, "The edge node is no longer in the cluster.")
			case names[res.resolvedNode] == "":
				res.Warnings = append(res.Warnings, "The node this runs on is no longer in the cluster.")
			case f.EdgeNodeID == res.resolvedNode:
				res.Warnings = append(res.Warnings, "This already runs on the edge node; it needs no forwarding.")
			case len(ports) == 0:
				res.Warnings = append(res.Warnings, "Nothing to forward: this publishes no ports.")
			}
		}
		res.PublicAddr = s.edgePublicAddr(ctx, clusterID, cfg, f.EdgeNodeID)
		if len(res.Warnings) == 0 && res.PublicAddr == "" {
			res.Warnings = append(res.Warnings, "The edge node's public address is not known. Set it under Edge nodes.")
		}
		res.active = f.Enabled && len(res.Warnings) == 0
		out = append(out, res)
	}
	return out, nil
}

// edgeAllocate gives every edge and origin pair in use a tunnel and drops
// tunnels nothing uses. It reports whether cfg changed.
func edgeAllocate(cfg *edgeConfig, forwards []edgeResolved) bool {
	type pair struct{ edge, origin string }
	used := map[pair]bool{}
	for _, f := range forwards {
		if f.active {
			used[pair{f.EdgeNodeID, f.resolvedNode}] = true
		}
	}
	changed := false
	kept := cfg.Tunnels[:0]
	taken := map[int]bool{}
	have := map[pair]bool{}
	for _, t := range cfg.Tunnels {
		p := pair{t.EdgeNodeID, t.OriginNodeID}
		if !used[p] || have[p] {
			changed = true
			continue
		}
		have[p] = true
		taken[t.Index] = true
		kept = append(kept, t)
	}
	cfg.Tunnels = kept
	var missing []pair
	for p := range used {
		if !have[p] {
			missing = append(missing, p)
		}
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].edge != missing[j].edge {
			return missing[i].edge < missing[j].edge
		}
		return missing[i].origin < missing[j].origin
	})
	for _, p := range missing {
		idx := 1
		for taken[idx] {
			idx++
		}
		if idx > edge.MaxIndex {
			break
		}
		taken[idx] = true
		cfg.Tunnels = append(cfg.Tunnels, edgeTunnel{ID: uuid.NewString(), EdgeNodeID: p.edge, OriginNodeID: p.origin, Index: idx})
		changed = true
	}
	return changed
}

// edgeSpecs builds every node's spec from the tunnels and forwards.
func edgeSpecs(cfg edgeConfig, forwards []edgeResolved) (map[string]edge.Spec, error) {
	specs := map[string]edge.Spec{}
	for _, t := range cfg.Tunnels {
		ea, oa := t.addrs()
		bits := 30
		et := edge.Tunnel{ID: t.ID, Role: edge.RoleEdge, Index: t.Index, Address: netip.PrefixFrom(ea, bits).String(),
			Peer: oa.String(), PeerKey: t.OriginKey, ListenPort: t.port()}
		ot := edge.Tunnel{ID: t.ID, Role: edge.RoleOrigin, Index: t.Index, Address: netip.PrefixFrom(oa, bits).String(),
			Peer: ea.String(), PeerKey: t.EdgeKey}
		for _, f := range forwards {
			if !f.active || f.EdgeNodeID != t.EdgeNodeID || f.resolvedNode != t.OriginNodeID {
				continue
			}
			if ot.Endpoint == "" && f.PublicAddr != "" {
				ot.Endpoint = net.JoinHostPort(f.PublicAddr, fmt.Sprint(t.port()))
			}
			for i, p := range f.Resolved {
				id := fmt.Sprintf("%s:%s:%d", f.ID, p.Protocol, p.Port)
				if i > 0 && p == f.Resolved[i-1] {
					continue
				}
				et.Forwards = append(et.Forwards, edge.Forward{ID: id, Protocol: p.Protocol, Port: p.Port, PortEnd: p.PortEnd, ToPort: p.ToPort})
				of := edge.Forward{ID: id, Protocol: p.Protocol, Port: p.Port, PortEnd: p.PortEnd, ToPort: p.ToPort}
				if f.Kind == edgeKindWorkload {
					of.Target = f.Target
					of.Masquerade = !f.KeepAddress
				}
				ot.Forwards = append(ot.Forwards, of)
			}
			ot.Egress = append(ot.Egress, f.egressNames...)
		}
		es := specs[t.EdgeNodeID]
		es.Tunnels = append(es.Tunnels, et)
		specs[t.EdgeNodeID] = es
		os := specs[t.OriginNodeID]
		os.Tunnels = append(os.Tunnels, ot)
		specs[t.OriginNodeID] = os
	}
	for id, spec := range specs {
		norm, err := edge.Normalize(spec)
		if err != nil {
			return nil, err
		}
		specs[id] = norm
	}
	return specs, nil
}

// RunEdge keeps every node's tunnels and forwards in step with the settings.
func (s *Server) RunEdge(ctx context.Context) {
	if s.EdgeAgent == nil && s.NodeAgents == nil {
		return
	}
	st := s.edgeRef()
	t := time.NewTicker(edgeTick)
	defer t.Stop()
	for {
		s.tickEdge(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-st.wake:
		}
	}
}

func (s *Server) tickEdge(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil {
		return
	}
	st := s.edgeRef()
	st.tick.Lock()
	defer st.tick.Unlock()
	nodes, err := s.Store.ListClusterNodes(ctx, cluster.ID)
	if err != nil {
		return
	}
	// Keys only become known when a node first applies its side, so a new
	// tunnel takes a couple of passes to come up.
	for pass := 0; pass < 3; pass++ {
		s.edgeMu.Lock()
		cfg, err := s.loadEdge(ctx, cluster.ID)
		if err != nil {
			s.edgeMu.Unlock()
			return
		}
		forwards, err := s.resolveEdge(ctx, cluster.ID, cfg)
		if err != nil {
			s.edgeMu.Unlock()
			return
		}
		changed := edgeAllocate(&cfg, forwards)
		specs, err := edgeSpecs(cfg, forwards)
		if err != nil {
			s.edgeMu.Unlock()
			log.Printf("edge forwarding: %v", err)
			return
		}
		if changed {
			if err := s.saveEdge(ctx, cluster.ID, "system", cfg); err != nil {
				s.edgeMu.Unlock()
				return
			}
		}
		s.edgeMu.Unlock()
		keys := map[string]string{}
		for _, n := range nodes {
			if n.RevokedAt != nil {
				continue
			}
			spec, want := specs[n.ID]
			status, ok := s.pushEdge(ctx, cluster.ID, n.ID, spec, want)
			if !ok {
				continue
			}
			for id, ts := range status.Tunnels {
				keys[id+"/"+ts.Role] = ts.PublicKey
			}
		}
		if !s.edgeLearnKeys(ctx, cluster.ID, keys) {
			return
		}
	}
}

// pushEdge applies a node's spec when it changed (or turns forwarding off
// on a node that has nothing left) and records its status.
func (s *Server) pushEdge(ctx context.Context, clusterID, nodeID string, spec edge.Spec, want bool) (edge.Status, bool) {
	st := s.edgeRef()
	st.mu.Lock()
	ns := st.nodes[nodeID]
	if ns == nil {
		ns = &edgeNodeState{}
		st.nodes[nodeID] = ns
	}
	pushed := ns.pushed
	st.mu.Unlock()
	record := func(status edge.Status, err error) {
		st.mu.Lock()
		defer st.mu.Unlock()
		ns.Status, ns.Err, ns.At = status, agentError(err), s.now()
	}
	agent, err := s.nodeAgent(ctx, clusterID, nodeID)
	if err != nil {
		if want {
			record(edge.Status{}, err)
		}
		return edge.Status{}, false
	}
	status, err := agent.Edge(ctx, "status", nil)
	if err != nil {
		if want {
			record(edge.Status{}, err)
		} else {
			record(edge.Status{}, nil)
		}
		return edge.Status{}, false
	}
	if !want {
		if status.Active {
			status, err = agent.Edge(ctx, "disable", nil)
		}
		record(status, err)
		st.mu.Lock()
		ns.pushed = ""
		st.mu.Unlock()
		return status, err == nil
	}
	hash := spec.Hash()
	if !status.Active || status.SpecHash != hash || pushed != hash {
		applied, err := agent.Edge(ctx, "apply", &spec)
		if err != nil {
			if agentError(err) != ns.Err {
				log.Printf("edge forwarding on node %s: %v", nodeID, err)
			}
			record(status, err)
			return status, false
		}
		status = applied
		st.mu.Lock()
		ns.pushed = hash
		st.mu.Unlock()
	}
	record(status, nil)
	return status, true
}

// edgeLearnKeys stores public keys the nodes reported. It reports whether
// any changed, so the peers get them.
func (s *Server) edgeLearnKeys(ctx context.Context, clusterID string, keys map[string]string) bool {
	s.edgeMu.Lock()
	defer s.edgeMu.Unlock()
	cfg, err := s.loadEdge(ctx, clusterID)
	if err != nil {
		return false
	}
	changed := false
	for i := range cfg.Tunnels {
		t := &cfg.Tunnels[i]
		if k := keys[t.ID+"/"+edge.RoleEdge]; k != "" && k != t.EdgeKey {
			t.EdgeKey, changed = k, true
		}
		if k := keys[t.ID+"/"+edge.RoleOrigin]; k != "" && k != t.OriginKey {
			t.OriginKey, changed = k, true
		}
	}
	if !changed {
		return false
	}
	return s.saveEdge(ctx, clusterID, "system", cfg) == nil
}

// edgeDDoSRules returns, per edge node, the DDoS rules for what it
// forwards: the target's limits on the edge's public ports, so a flood is
// dropped on the edge before it ever enters the tunnel.
func (s *Server) edgeDDoSRules(ctx context.Context, clusterID string, targets []ddosResolved) map[string][]shield.Rule {
	cfg, err := s.loadEdge(ctx, clusterID)
	if err != nil || len(cfg.Forwards) == 0 {
		return nil
	}
	forwards, err := s.resolveEdge(ctx, clusterID, cfg)
	if err != nil {
		return nil
	}
	byRef := map[string]ddosResolved{}
	for _, t := range targets {
		if t.Enabled && t.RefID != "" {
			byRef[t.Kind+"/"+t.RefID] = t
		}
	}
	out := map[string][]shield.Rule{}
	for _, f := range forwards {
		if !f.active {
			continue
		}
		kind := map[string]string{edgeKindGameServer: ddosTargetGameServer, edgeKindWorkload: ddosTargetWorkload}[f.Kind]
		t, ok := byRef[kind+"/"+f.RefID]
		if !ok {
			continue
		}
		udp, tcp := false, false
		var ports []string
		for _, p := range f.Resolved {
			if p.Protocol == "tcp" {
				tcp = true
			} else {
				udp = true
			}
			if p.PortEnd > p.Port {
				ports = append(ports, fmt.Sprintf("%d-%d", p.Port, p.PortEnd))
			} else {
				ports = append(ports, fmt.Sprint(p.Port))
			}
		}
		rule := ruleFromTarget(t.ddosTarget)
		rule.Host, rule.Dest, rule.Ports = true, nil, ports
		switch {
		case udp && tcp:
			rule.Protocol = shield.ProtoBoth
		case tcp:
			rule.Protocol = shield.ProtoTCP
		default:
			rule.Protocol = shield.ProtoUDP
		}
		out[f.EdgeNodeID] = append(out[f.EdgeNodeID], rule)
	}
	return out
}

// ---- API ----

type edgeNodeView struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Role       string    `json:"role"`
	Local      bool      `json:"local"`
	PublicAddr string    `json:"public_addr,omitempty"`
	SetAddr    string    `json:"set_addr,omitempty"`
	Error      string    `json:"error,omitempty"`
	Warnings   []string  `json:"warnings,omitempty"`
	CheckedAt  time.Time `json:"checked_at,omitempty"`
}

type edgeTunnelView struct {
	ID           string    `json:"id"`
	EdgeNodeID   string    `json:"edge_node_id"`
	OriginNodeID string    `json:"origin_node_id"`
	Interface    string    `json:"interface"`
	EdgeAddr     string    `json:"edge_addr"`
	OriginAddr   string    `json:"origin_addr"`
	ListenPort   int       `json:"listen_port"`
	Connected    bool      `json:"connected"`
	Handshake    time.Time `json:"handshake,omitempty"`
	Endpoint     string    `json:"endpoint,omitempty"`
	RxBytes      int64     `json:"rx_bytes"`
	TxBytes      int64     `json:"tx_bytes"`
	Egress       []string  `json:"egress,omitempty"`
	Error        string    `json:"error,omitempty"`
}

type edgeForwardView struct {
	edgeResolved
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
	Active  bool   `json:"active"`
}

func (s *Server) edgeView(ctx context.Context, clusterID string) (map[string]any, error) {
	cfg, err := s.loadEdge(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	forwards, err := s.resolveEdge(ctx, clusterID, cfg)
	if err != nil {
		return nil, err
	}
	nodes, err := s.Store.ListClusterNodes(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	st := s.edgeRef()
	st.mu.Lock()
	states := map[string]edgeNodeState{}
	for id, ns := range st.nodes {
		states[id] = *ns
	}
	st.mu.Unlock()

	nodeViews := []edgeNodeView{}
	for _, n := range nodes {
		if n.RevokedAt != nil {
			continue
		}
		ns := states[n.ID]
		nodeViews = append(nodeViews, edgeNodeView{
			ID: n.ID, Name: n.Name, Role: n.Role, Local: s.applyLocal(ctx, clusterID, n.ID),
			PublicAddr: s.edgePublicAddr(ctx, clusterID, cfg, n.ID), SetAddr: cfg.Nodes[n.ID].PublicAddr,
			Error: ns.Err, Warnings: ns.Status.Warnings, CheckedAt: ns.At,
		})
	}
	now := s.now()
	tunnels := []edgeTunnelView{}
	for _, t := range cfg.Tunnels {
		ea, oa := t.addrs()
		v := edgeTunnelView{ID: t.ID, EdgeNodeID: t.EdgeNodeID, OriginNodeID: t.OriginNodeID, Interface: edge.IfName(t.ID),
			EdgeAddr: ea.String(), OriginAddr: oa.String(), ListenPort: t.port()}
		es, os := states[t.EdgeNodeID], states[t.OriginNodeID]
		if ts, ok := os.Status.Tunnels[t.ID]; ok {
			v.Handshake, v.Endpoint, v.RxBytes, v.TxBytes, v.Egress = ts.Handshake, ts.Endpoint, ts.RxBytes, ts.TxBytes, ts.Egress
			v.Error = ts.Error
		}
		if ts, ok := es.Status.Tunnels[t.ID]; ok && v.Handshake.IsZero() {
			v.Handshake = ts.Handshake
		}
		v.Connected = !v.Handshake.IsZero() && now.Sub(v.Handshake) < 3*time.Minute
		switch {
		case es.Err != "":
			v.Error = "Edge node: " + es.Err
		case os.Err != "":
			v.Error = "Origin node: " + os.Err
		case v.Error == "" && !v.Connected && t.EdgeKey != "" && t.OriginKey != "":
			v.Error = fmt.Sprintf("No handshake yet. Allow UDP %d in to the edge node (in the provider's firewall too).", t.port())
		}
		tunnels = append(tunnels, v)
	}
	tunnelOf := map[string]string{}
	for _, t := range cfg.Tunnels {
		tunnelOf[t.EdgeNodeID+"/"+t.OriginNodeID] = t.ID
	}
	views := []edgeForwardView{}
	for _, f := range forwards {
		v := edgeForwardView{edgeResolved: f, Active: f.active}
		if f.active {
			v.TunnelID = tunnelOf[f.EdgeNodeID+"/"+f.resolvedNode]
			for _, p := range f.Resolved {
				c := states[f.EdgeNodeID].Status.Forwards[fmt.Sprintf("%s/%s:%s:%d", v.TunnelID, f.ID, p.Protocol, p.Port)]
				v.Packets += c.Packets
				v.Bytes += c.Bytes
			}
		}
		if v.Resolved == nil {
			v.Resolved = []edgePort{}
		}
		if v.Ports == nil {
			v.Ports = []edgePort{}
		}
		views = append(views, v)
	}
	return map[string]any{"nodes": nodeViews, "tunnels": tunnels, "forwards": views}, nil
}

func (s *Server) getEdge(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkRead)
	if err != nil {
		return
	}
	out, err := s.edgeView(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// edgeMutate loads, changes, validates, saves and applies the settings
// under one lock.
func (s *Server) edgeMutate(w http.ResponseWriter, r *http.Request, p *principal, action, subject string, change func(*edgeConfig) error) bool {
	ctx := r.Context()
	clusterID := p.User.ClusterID
	s.edgeMu.Lock()
	cfg, err := s.loadEdge(ctx, clusterID)
	if err != nil {
		s.edgeMu.Unlock()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if err := change(&cfg); err != nil {
		s.edgeMu.Unlock()
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	forwards, err := s.resolveEdge(ctx, clusterID, cfg)
	if err != nil {
		s.edgeMu.Unlock()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return false
	}
	edgeAllocate(&cfg, forwards)
	if _, err := edgeSpecs(cfg, forwards); err != nil {
		s.edgeMu.Unlock()
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	if err := s.saveEdge(ctx, clusterID, p.User.ID, cfg); err != nil {
		s.edgeMu.Unlock()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return false
	}
	s.edgeMu.Unlock()
	s.audit(r, clusterID, p.User.ID, action, "ok", subject)
	s.edgeWake()
	s.ddosWake()
	return true
}

func (s *Server) writeEdge(w http.ResponseWriter, r *http.Request, clusterID string) {
	out, err := s.edgeView(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type edgeForwardReq struct {
	Name         *string     `json:"name"`
	Kind         string      `json:"kind"`
	RefID        string      `json:"ref_id"`
	EdgeNodeID   *string     `json:"edge_node_id"`
	OriginNodeID *string     `json:"origin_node_id"`
	Ports        *[]edgePort `json:"ports"`
	KeepAddress  *bool       `json:"keep_address"`
	Egress       *bool       `json:"egress"`
	Enabled      *bool       `json:"enabled"`
}

func applyForwardReq(f *edgeForward, req edgeForwardReq) error {
	if req.Name != nil {
		f.Name = strings.TrimSpace(*req.Name)
	}
	if req.EdgeNodeID != nil {
		f.EdgeNodeID = strings.TrimSpace(*req.EdgeNodeID)
	}
	if req.OriginNodeID != nil {
		f.OriginNodeID = strings.TrimSpace(*req.OriginNodeID)
	}
	if req.Ports != nil {
		f.Ports = nil
		for _, p := range *req.Ports {
			p.Protocol = strings.ToLower(strings.TrimSpace(p.Protocol))
			if p.Protocol != "udp" && p.Protocol != "tcp" {
				return errors.New("each port needs protocol udp or tcp")
			}
			if p.Port < 1 || p.Port > 65535 || (p.PortEnd != 0 && (p.PortEnd < p.Port || p.PortEnd > 65535)) {
				return fmt.Errorf("port %d is not valid", p.Port)
			}
			if p.ToPort == p.Port {
				p.ToPort = 0
			}
			f.Ports = append(f.Ports, p)
		}
	}
	if req.KeepAddress != nil {
		f.KeepAddress = *req.KeepAddress
	}
	if req.Egress != nil {
		f.Egress = *req.Egress
	}
	if req.Enabled != nil {
		f.Enabled = *req.Enabled
	}
	if f.EdgeNodeID == "" {
		return errors.New("choose the edge node to publish on")
	}
	switch f.Kind {
	case edgeKindGameServer, edgeKindWorkload:
		if f.RefID == "" {
			return errors.New("ref_id is required")
		}
		f.OriginNodeID = ""
	case edgeKindHost:
		if f.OriginNodeID == "" {
			return errors.New("choose the node the service runs on")
		}
		if len(f.Ports) == 0 {
			return errors.New("add at least one port")
		}
		f.Egress = false
	default:
		return errors.New("kind must be gameserver, workload or host")
	}
	return nil
}

func (s *Server) createEdgeForward(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkApply)
	if err != nil {
		return
	}
	var req edgeForwardReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	now := s.now()
	f := edgeForward{ID: uuid.NewString(), Kind: strings.TrimSpace(req.Kind), RefID: strings.TrimSpace(req.RefID),
		Enabled: true, Egress: strings.TrimSpace(req.Kind) == edgeKindGameServer, CreatedAt: now, UpdatedAt: now}
	if !s.edgeMutate(w, r, p, "edge.forward.create", f.ID, func(cfg *edgeConfig) error {
		if err := applyForwardReq(&f, req); err != nil {
			return err
		}
		for _, o := range cfg.Forwards {
			if f.RefID != "" && o.Kind == f.Kind && o.RefID == f.RefID && o.EdgeNodeID == f.EdgeNodeID {
				return errors.New("this is already published on that edge node")
			}
		}
		if f.Name == "" {
			f.Name = s.edgeRefName(r.Context(), p.User.ClusterID, f)
		}
		cfg.Forwards = append(cfg.Forwards, f)
		return nil
	}) {
		return
	}
	s.writeEdge(w, r, p.User.ClusterID)
}

func (s *Server) edgeRefName(ctx context.Context, clusterID string, f edgeForward) string {
	switch f.Kind {
	case edgeKindGameServer:
		if row, err := s.Store.GetGameServer(ctx, clusterID, f.RefID); err == nil && row != nil {
			return row.Name
		}
	case edgeKindWorkload:
		if wl, err := s.Store.GetWorkload(ctx, clusterID, f.RefID); err == nil && wl != nil {
			return wl.Name
		}
	}
	return "Forward"
}

func (s *Server) updateEdgeForward(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkApply)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	var req edgeForwardReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	found := false
	if !s.edgeMutate(w, r, p, "edge.forward.update", id, func(cfg *edgeConfig) error {
		for i := range cfg.Forwards {
			if cfg.Forwards[i].ID != id {
				continue
			}
			found = true
			f := cfg.Forwards[i]
			if err := applyForwardReq(&f, req); err != nil {
				return err
			}
			f.UpdatedAt = s.now()
			cfg.Forwards[i] = f
			return nil
		}
		return nil
	}) {
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "forward not found")
		return
	}
	s.writeEdge(w, r, p.User.ClusterID)
}

func (s *Server) deleteEdgeForward(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkApply)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	if !s.edgeMutate(w, r, p, "edge.forward.delete", id, func(cfg *edgeConfig) error {
		kept := cfg.Forwards[:0]
		for _, f := range cfg.Forwards {
			if f.ID != id {
				kept = append(kept, f)
			}
		}
		cfg.Forwards = kept
		return nil
	}) {
		return
	}
	s.writeEdge(w, r, p.User.ClusterID)
}

func (s *Server) putEdgeNode(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkApply)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	var req struct {
		PublicAddr string `json:"public_addr"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	addr := strings.TrimSpace(req.PublicAddr)
	if addr != "" {
		a, err := netip.ParseAddr(addr)
		if err != nil || !a.Is4() {
			writeErr(w, http.StatusBadRequest, "public address must be an IPv4 address")
			return
		}
		addr = a.String()
	}
	if !s.edgeMutate(w, r, p, "edge.node.update", id, func(cfg *edgeConfig) error {
		if addr == "" {
			delete(cfg.Nodes, id)
		} else {
			cfg.Nodes[id] = edgeNodeCfg{PublicAddr: addr}
		}
		return nil
	}) {
		return
	}
	s.writeEdge(w, r, p.User.ClusterID)
}

func (s *Server) applyEdge(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkApply)
	if err != nil {
		return
	}
	s.applyEdgeNow(r.Context())
	s.writeEdge(w, r, p.User.ClusterID)
}

// applyEdgeNow pushes every node's spec again, even when unchanged.
func (s *Server) applyEdgeNow(ctx context.Context) {
	st := s.edgeRef()
	st.mu.Lock()
	for _, ns := range st.nodes {
		ns.pushed = ""
	}
	st.mu.Unlock()
	s.tickEdge(ctx)
}
