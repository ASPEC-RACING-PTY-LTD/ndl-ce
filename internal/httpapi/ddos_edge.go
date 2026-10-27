package httpapi

import (
	"context"
	"log"
	"sort"
	"time"

	"github.com/no-dal/ndl-ce/internal/shield"
)

// DDoS protection on edge nodes. A service published through an edge
// (edge forwarding) is attacked at the edge's address, so its target's
// rules are loaded on the edge node too, on the edge's public ports. A
// flood is dropped there and never enters the tunnel. The edge's counters
// are added to the target's, so traffic, attacks and bans read as one.

// ddosEdge is DDoS protection on one edge node.
type ddosEdge struct {
	Status shield.Status
	Err    string
	pushed string
	At     time.Time
}

// ddosEdgeView is an edge node's protection in the DDoS overview.
type ddosEdgeView struct {
	NodeID  string    `json:"node_id"`
	Name    string    `json:"name"`
	Active  bool      `json:"active"`
	Mode    string    `json:"mode,omitempty"`
	Devices []string  `json:"devices,omitempty"`
	Targets []string  `json:"targets"`
	Error   string    `json:"error,omitempty"`
	At      time.Time `json:"checked_at,omitempty"`
}

// ddosEdgeSpec is the spec for an edge node: the cluster's settings with
// only the forwarded targets. The edge's own neighbours are not trusted:
// on a VPS they are other customers.
func (s *Server) ddosEdgeSpec(cfg ddosConfig, rules []shield.Rule) shield.Spec {
	spec := s.ddosSpec(cfg, nil)
	spec.Devices = nil
	spec.TrustLocal = false
	spec.Rules = append([]shield.Rule(nil), rules...)
	sort.Slice(spec.Rules, func(i, j int) bool { return spec.Rules[i].ID < spec.Rules[j].ID })
	return spec
}

// ddosPushEdges loads the forwarded targets' rules on every edge node,
// turns protection off on edges that no longer forward anything and
// returns the edges' statuses.
func (s *Server) ddosPushEdges(ctx context.Context, clusterID string, cfg ddosConfig, rules map[string][]shield.Rule) []shield.Status {
	st := s.ddosRef()
	st.mu.Lock()
	if st.edges == nil {
		st.edges = map[string]*ddosEdge{}
	}
	known := map[string]bool{}
	for id := range st.edges {
		known[id] = true
	}
	st.mu.Unlock()
	for id := range rules {
		known[id] = true
	}
	var out []shield.Status
	for nodeID := range known {
		st.mu.Lock()
		ed := st.edges[nodeID]
		if ed == nil {
			ed = &ddosEdge{}
			st.edges[nodeID] = ed
		}
		pushed := ed.pushed
		st.mu.Unlock()
		record := func(status shield.Status, err error, hash string) {
			st.mu.Lock()
			defer st.mu.Unlock()
			ed.Status, ed.Err, ed.pushed, ed.At = status, agentError(err), hash, s.now()
		}
		agent, err := s.nodeAgent(ctx, clusterID, nodeID)
		if err != nil {
			record(shield.Status{}, err, "")
			continue
		}
		rs, want := rules[nodeID]
		if !want {
			if _, err := agent.Shield(ctx, "disable", nil, "", 0); err != nil {
				record(shield.Status{}, err, "")
				continue
			}
			st.mu.Lock()
			delete(st.edges, nodeID)
			st.mu.Unlock()
			continue
		}
		spec := s.ddosEdgeSpec(cfg, rs)
		status, err := agent.Shield(ctx, "status", nil, "", 0)
		if err != nil {
			record(shield.Status{}, err, "")
			continue
		}
		hash := spec.Hash()
		if !status.Active || status.SpecHash != hash || pushed != hash {
			applied, err := agent.Shield(ctx, "apply", &spec, "", 0)
			if err != nil {
				if agentError(err) != ed.Err {
					log.Printf("ddos protection on node %s: %v", nodeID, err)
				}
				record(status, err, "")
				continue
			}
			status = applied
		}
		record(status, nil, hash)
		out = append(out, status)
	}
	return out
}

// ddosLocalEdge moves the rules for the control node, when it is itself an
// edge, out of rules and into its own spec (replacing a rule of the same
// target, which would match the same ports).
func ddosLocalEdge(spec *shield.Spec, rules map[string][]shield.Rule, controlID string) {
	local := rules[controlID]
	delete(rules, controlID)
	for _, r := range local {
		replaced := false
		for i := range spec.Rules {
			if spec.Rules[i].ID == r.ID {
				spec.Rules[i], replaced = r, true
			}
		}
		if !replaced {
			spec.Rules = append(spec.Rules, r)
		}
	}
}

// ddosEdgesOff turns protection off on every edge node it was loaded on.
func (s *Server) ddosEdgesOff(ctx context.Context, clusterID string) {
	st := s.ddosRef()
	st.mu.Lock()
	var ids []string
	for id := range st.edges {
		ids = append(ids, id)
	}
	st.mu.Unlock()
	for _, id := range ids {
		agent, err := s.nodeAgent(ctx, clusterID, id)
		if err != nil {
			continue
		}
		if _, err := agent.Shield(ctx, "disable", nil, "", 0); err == nil {
			st.mu.Lock()
			delete(st.edges, id)
			st.mu.Unlock()
		}
	}
}

// ddosEdgesBan bans or unbans an address on every edge node too.
func (s *Server) ddosEdgesBan(ctx context.Context, clusterID, action, addr string, until int64) {
	st := s.ddosRef()
	st.mu.Lock()
	var ids []string
	for id, ed := range st.edges {
		if ed.Status.Active {
			ids = append(ids, id)
		}
	}
	st.mu.Unlock()
	for _, id := range ids {
		if agent, err := s.nodeAgent(ctx, clusterID, id); err == nil {
			if _, err := agent.Shield(ctx, action, nil, addr, until); err != nil {
				log.Printf("ddos protection on node %s: %s %s: %v", id, action, addr, err)
			}
		}
	}
}

// mergeShield adds an edge node's counters and bans to the control node's.
func mergeShield(into, from shield.Status) shield.Status {
	add := func(a, b shield.Counters) shield.Counters {
		return shield.Counters{Packets: a.Packets + b.Packets, Bytes: a.Bytes + b.Bytes}
	}
	rules := map[string]shield.RuleStatus{}
	for id, r := range into.Rules {
		rules[id] = r
	}
	for id, r := range from.Rules {
		cur := rules[id]
		cur.In = add(cur.In, r.In)
		cur.Dropped = add(cur.Dropped, r.Dropped)
		cur.Bans += r.Bans
		if len(r.Reasons) > 0 {
			reasons := map[string]shield.Counters{}
			for k, v := range cur.Reasons {
				reasons[k] = v
			}
			for k, v := range r.Reasons {
				reasons[k] = add(reasons[k], v)
			}
			cur.Reasons = reasons
		}
		rules[id] = cur
	}
	into.Rules = rules
	into.Blocked = add(into.Blocked, from.Blocked)
	into.Banned = add(into.Banned, from.Banned)
	into.Bans = append(append([]shield.Ban(nil), into.Bans...), from.Bans...)
	return into
}

func (s *Server) ddosEdgeViews(ctx context.Context, clusterID string, names map[string]string) []ddosEdgeView {
	nodes, _ := s.Store.ListClusterNodes(ctx, clusterID)
	nodeNames := map[string]string{}
	for _, n := range nodes {
		nodeNames[n.ID] = n.Name
	}
	st := s.ddosRef()
	st.mu.Lock()
	defer st.mu.Unlock()
	out := []ddosEdgeView{}
	for id, ed := range st.edges {
		v := ddosEdgeView{NodeID: id, Name: nodeNames[id], Active: ed.Status.Active, Mode: ed.Status.Mode,
			Devices: ed.Status.Devices, Error: firstNonEmpty(ed.Err, ed.Status.Error), At: ed.At, Targets: []string{}}
		for rid := range ed.Status.Rules {
			v.Targets = append(v.Targets, firstNonEmpty(names[rid], rid))
		}
		sort.Strings(v.Targets)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
