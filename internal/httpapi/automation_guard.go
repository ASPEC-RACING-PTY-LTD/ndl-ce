package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/automation/engine"
	"github.com/no-dal/ndl-ce/internal/automation/flowdef"
)

// The guard. Flows run as the system, so what a flow may do is decided when
// it is saved: a version holding a protected step runs only once a
// SuperAdmin (or a role given approvals.decide) approves that exact
// version, unless a SuperAdmin built it. Calls to ndl-ce's own API are
// refused outright for anyone below SuperAdmin, at save and at run time.

// Protected step kinds.
const (
	protectedHarmful  = "harmful"
	protectedSecurity = "security"
	protectedOwnAPI   = "own_api"
)

// protectedStep is one step that needs approval.
type protectedStep struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// harmfulSteps are step types whose effect cannot be fully undone. Start,
// stop, restart, snapshot, backup, clone, notify and the read steps are
// free.
var harmfulSteps = map[string]string{
	"ndl.workload.migrate": "migrates a workload",
	"ndl.docker.prune":     "prunes Docker (removes containers, images, volumes or networks)",
	"ndl.script.run":       "runs a script",
	"ndl.docker.redeploy":  "redeploys a Compose project (recreates its containers)",
	"ndl.storage.pressure": "migrates workloads off full pools",
}

// dockerFreeActions are the container actions that do not remove anything.
var dockerFreeActions = map[string]bool{"restart": true, "start": true, "stop": true, "pull": true}

// stepURLs are the step types that send HTTP requests.
var stepURLs = map[string]bool{"action.http": true, "action.webhook": true}

func hasTemplate(v string) bool { return strings.Contains(v, "{{") }

// protectedSteps lists the steps of d that need approval. Own-API calls
// are reported too; callers refuse them for people below SuperAdmin.
func (s *Server) protectedSteps(ctx context.Context, clusterID string, d *flowdef.Definition) []protectedStep {
	out := []protectedStep{}
	for _, n := range d.Nodes {
		step := protectedStep{NodeID: n.ID, Name: firstNonEmpty(n.Name, n.ID), Type: n.Type}
		cfg := func(k string) string { v, _ := n.Config[k].(string); return strings.TrimSpace(v) }
		switch {
		case harmfulSteps[n.Type] != "":
			step.Kind, step.Reason = protectedHarmful, harmfulSteps[n.Type]
		case n.Type == "ndl.docker.container":
			action := firstNonEmpty(cfg("action"), "restart")
			if dockerFreeActions[action] {
				continue
			}
			step.Kind = protectedHarmful
			if hasTemplate(action) {
				step.Reason = "the Docker action is chosen when the flow runs"
			} else {
				step.Reason = action + " removes and recreates the container"
			}
		case stepURLs[n.Type]:
			raw := cfg("url")
			if self, why := s.selfTarget(ctx, clusterID, raw); self {
				step.Kind, step.Reason = protectedOwnAPI, "calls ndl-ce's own API ("+why+")"
			} else {
				continue
			}
		default:
			continue
		}
		out = append(out, step)
	}
	return out
}

func ownAPISteps(steps []protectedStep) []string {
	var out []string
	for _, st := range steps {
		if st.Kind == protectedOwnAPI {
			out = append(out, st.Name)
		}
	}
	return out
}

// selfTarget reports whether raw points at this ndl-ce instance: its own
// hostnames and URL, the Cloudflare, Caddy and Nginx routes that lead to
// it, loopback, or any address of this host. A URL that is only known when
// the flow runs is checked then (selfTargetAtRun).
func (s *Server) selfTarget(ctx context.Context, clusterID, raw string) (bool, string) {
	if raw == "" || hasTemplate(hostPart(raw)) {
		return false, ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false, ""
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return false, ""
	}
	if names := s.selfHostnames(ctx, clusterID); names[host] != "" {
		return true, names[host]
	}
	ips := []net.IP{net.ParseIP(host)}
	if ips[0] == nil {
		lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupIPAddr(lctx, host)
		if err != nil {
			return false, ""
		}
		ips = ips[:0]
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	own := hostAddresses()
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return true, "loopback address"
		}
		if own[ip.String()] {
			return true, "an address of this host"
		}
	}
	return false, ""
}

// hostPart is the host of a URL that may hold {{ }} references.
func hostPart(raw string) string {
	rest := raw
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func hostAddresses() map[string]bool {
	out := map[string]bool{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			out[ipn.IP.String()] = true
		}
	}
	return out
}

func (s *Server) panelPorts() map[string]bool {
	out := map[string]bool{}
	for _, l := range []string{s.tlsListen(), s.httpListen()} {
		if _, port, err := net.SplitHostPort(l); err == nil && port != "" {
			out[port] = true
		}
	}
	return out
}

// pointsAtPanel reports whether an upstream address is this host's panel.
func (s *Server) pointsAtPanel(upstream string, own map[string]bool, ports map[string]bool) bool {
	if upstream == "" {
		return false
	}
	if !strings.Contains(upstream, "://") {
		upstream = "http://" + upstream
	}
	u, err := url.Parse(upstream)
	if err != nil {
		return false
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[u.Scheme]
	}
	ip := net.ParseIP(host)
	local := host == "localhost" || (ip != nil && (ip.IsLoopback() || own[ip.String()]))
	return local && ports[port]
}

// selfHostnames maps each hostname known to reach this instance to why.
func (s *Server) selfHostnames(ctx context.Context, clusterID string) map[string]string {
	out := map[string]string{"localhost": "loopback address"}
	if h, _ := os.Hostname(); h != "" {
		out[strings.ToLower(h)] = "this host's name"
	}
	if u, err := url.Parse(s.httpsURL()); err == nil && u.Hostname() != "" {
		out[strings.ToLower(u.Hostname())] = "ndl-ce's own URL"
	}
	own, ports := hostAddresses(), s.panelPorts()
	if routes, err := s.Store.ListCloudflareRoutes(ctx, clusterID); err == nil {
		for _, rt := range routes {
			var rule struct {
				Service string `json:"service"`
			}
			_ = json.Unmarshal(rt.Rule, &rule)
			if rt.Hostname != "" && s.pointsAtPanel(rule.Service, own, ports) {
				out[strings.ToLower(rt.Hostname)] = "a Cloudflare route to ndl-ce"
			}
		}
	}
	s.proxies.mu.Lock()
	for _, e := range s.proxies.items {
		for _, inst := range e.res.Instances {
			for _, site := range inst.Sites {
				if !s.pointsAtPanel(site.Upstream, own, ports) {
					continue
				}
				for _, d := range site.Domains {
					out[strings.ToLower(strings.TrimSpace(d))] = "a " + proxyTitle(inst.Kind) + " site for ndl-ce"
				}
			}
		}
	}
	s.proxies.mu.Unlock()
	return out
}

// selfTargetAtRun refuses an HTTP step aimed at ndl-ce itself when it runs
// (its URL may be rendered from {{ }} or its DNS may have changed), unless
// the running version was built by a SuperAdmin.
func (s *Server) selfTargetAtRun(ctx context.Context, in *engine.ExecInput, raw string) error {
	self, why := s.selfTarget(ctx, in.ClusterID, raw)
	if !self {
		return nil
	}
	if f, _ := s.Store.GetAutomationFlow(ctx, in.ClusterID, in.FlowID); f != nil && f.PublishedVersion > 0 {
		if v, _ := s.Store.GetAutomationFlowVersion(ctx, in.ClusterID, f.ID, f.PublishedVersion); v != nil && v.Exempt {
			return nil
		}
	}
	return fmt.Errorf("the URL is refused: it reaches ndl-ce's own API (%s); only flows built by a SuperAdmin may call it", why)
}

// taintDraft records that someone below SuperAdmin changed the draft: its
// next version loses the SuperAdmin exemption.
func taintDraft(p *principal, f *appdb.AutomationFlow) {
	if !isOwner(p) {
		f.DraftTainted = true
	}
}
