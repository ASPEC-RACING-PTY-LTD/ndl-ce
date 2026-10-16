package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Caddy and Nginx pages. The agent finds each proxy on the host and in system
// containers and manages No-dal's own site files; see internal/docker/proxy.go.
// Both are optional features. Switching one off hides it and stops scanning;
// the proxy and its sites are never changed by the switch.

// Agents that manage reverse proxies implement this.
type dockerProxyRPC interface {
	DockerProxy(ctx context.Context, req docker.ProxyRequest) (docker.ProxyResult, error)
}

type proxyCache struct {
	mu    sync.Mutex
	items map[string]proxyCacheEntry
}

type proxyCacheEntry struct {
	at  time.Time
	res docker.ProxyResult
}

// proxyCacheTTL keeps page loads from rescanning every container.
const proxyCacheTTL = 30 * time.Second

type proxySiteView struct {
	docker.ProxySite
	Workload *proxyWorkloadRef `json:"workload,omitempty"`
}

type proxyWorkloadRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type proxyInstanceView struct {
	docker.ProxyInstance
	Sites    []proxySiteView   `json:"sites"`
	Workload *proxyWorkloadRef `json:"workload,omitempty"`
}

func (s *Server) proxyEnabled(ctx context.Context, clusterID, kind string) bool {
	row, _ := s.Store.GetFeature(ctx, clusterID, kind)
	return row != nil && row.Enabled
}

func (s *Server) requireProxy(w http.ResponseWriter, r *http.Request, perm string) (*principal, string, dockerProxyRPC, bool) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return nil, "", nil, false
	}
	kind := r.PathValue("kind")
	if kind != features.IDCaddy && kind != features.IDNginx {
		writeErr(w, http.StatusNotFound, "unknown proxy")
		return nil, "", nil, false
	}
	if !s.proxyEnabled(r.Context(), p.User.ClusterID, kind) {
		writeErr(w, http.StatusNotFound, proxyTitle(kind)+" is not enabled. Enable it from Add Features.")
		return nil, "", nil, false
	}
	rpc, ok := s.dockerRPC().(dockerProxyRPC)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "this node's agent cannot manage reverse proxies; update No-dal on the node")
		return nil, "", nil, false
	}
	return p, kind, rpc, true
}

func proxyTitle(kind string) string {
	if kind == features.IDCaddy {
		return "Caddy"
	}
	return "Nginx"
}

func (s *Server) proxyScan(ctx context.Context, clusterID, kind string, rpc dockerProxyRPC, fresh bool) (docker.ProxyResult, error) {
	key := clusterID + "/" + kind
	s.proxies.mu.Lock()
	if e, ok := s.proxies.items[key]; ok && !fresh && time.Since(e.at) < proxyCacheTTL {
		s.proxies.mu.Unlock()
		return e.res, nil
	}
	s.proxies.mu.Unlock()
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	res, err := rpc.DockerProxy(sctx, docker.ProxyRequest{Action: docker.ProxyScan, Kind: kind, Hints: s.dockerHints(sctx, clusterID)})
	if err != nil {
		return res, err
	}
	s.proxies.mu.Lock()
	if s.proxies.items == nil {
		s.proxies.items = map[string]proxyCacheEntry{}
	}
	s.proxies.items[key] = proxyCacheEntry{at: time.Now(), res: res}
	s.proxies.mu.Unlock()
	return res, nil
}

func (s *Server) proxyInvalidate(clusterID, kind string) {
	s.proxies.mu.Lock()
	delete(s.proxies.items, clusterID+"/"+kind)
	s.proxies.mu.Unlock()
}

func (s *Server) getProxies(w http.ResponseWriter, r *http.Request) {
	p, kind, rpc, ok := s.requireProxy(w, r, rbac.NetworkRead)
	if !ok {
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	res, err := s.proxyScan(ctx, clusterID, kind, rpc, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	ips := s.cfWorkloadIPs(ctx, clusterID)
	byIP := map[string]*proxyWorkloadRef{}
	byID := map[string]*proxyWorkloadRef{}
	for _, wl := range ips {
		ref := &proxyWorkloadRef{ID: wl.ID, Name: wl.Name}
		byIP[wl.IP], byID[wl.ID] = ref, ref
	}
	instances := make([]proxyInstanceView, 0, len(res.Instances))
	for _, in := range res.Instances {
		v := proxyInstanceView{ProxyInstance: in, Sites: make([]proxySiteView, 0, len(in.Sites)), Workload: byID[in.MachineID]}
		for _, site := range in.Sites {
			sv := proxySiteView{ProxySite: site}
			if host := upstreamHost(site.Upstream); host != "" {
				if ref := byIP[host]; ref != nil {
					sv.Workload = ref
				} else if isLoopback(host) && v.Workload != nil {
					sv.Workload = v.Workload
				}
			}
			v.Sites = append(v.Sites, sv)
		}
		instances = append(instances, v)
	}
	machines := res.Machines
	if machines == nil {
		machines = []docker.ProxyMachine{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": instances, "machines": machines})
}

func upstreamHost(up string) string {
	if up == "" {
		return ""
	}
	if !strings.Contains(up, "://") {
		up = "http://" + up
	}
	u, err := url.Parse(up)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// validProxyMachine checks that a machine is the host or a system container.
func (s *Server) validProxyMachine(ctx context.Context, clusterID, id string) bool {
	if id == "" || id == docker.HostMachineID {
		return true
	}
	for _, h := range s.dockerHints(ctx, clusterID) {
		if h.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) proxyRun(w http.ResponseWriter, r *http.Request, p *principal, kind string, rpc dockerProxyRPC, req docker.ProxyRequest, audit, detail string) {
	ctx, clusterID := r.Context(), p.User.ClusterID
	if !s.validProxyMachine(ctx, clusterID, req.MachineID) {
		writeErr(w, http.StatusBadRequest, "machine not found")
		return
	}
	req.Kind = kind
	if req.MachineID == "" {
		req.MachineID = docker.HostMachineID
	}
	limit := 2 * time.Minute
	if req.Action == docker.ProxyInstall {
		limit = 11 * time.Minute
	}
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), limit)
	defer cancel()
	res, err := rpc.DockerProxy(actx, req)
	s.proxyInvalidate(clusterID, kind)
	if err != nil {
		s.audit(r, clusterID, p.User.ID, audit, "failed", detail)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "output": tailLines(res.Output, 30)})
		return
	}
	s.audit(r, clusterID, p.User.ID, audit, "ok", detail)
	res.Output = tailLines(res.Output, 30)
	writeJSON(w, http.StatusOK, res)
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (s *Server) upsertProxySite(w http.ResponseWriter, r *http.Request) {
	p, kind, rpc, ok := s.requireProxy(w, r, rbac.NetworkApply)
	if !ok {
		return
	}
	var in struct {
		MachineID string           `json:"machine_id"`
		Previous  string           `json:"previous"`
		Site      docker.ProxySite `json:"site"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	site, err := docker.CleanProxySite(kind, in.Site)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.proxyRun(w, r, p, kind, rpc, docker.ProxyRequest{Action: docker.ProxyApply, MachineID: in.MachineID, Site: &site, Previous: in.Previous},
		kind+".site.upsert", strings.Join(site.Domains, ",")+" -> "+site.Upstream)
}

func (s *Server) deleteProxySite(w http.ResponseWriter, r *http.Request) {
	p, kind, rpc, ok := s.requireProxy(w, r, rbac.NetworkApply)
	if !ok {
		return
	}
	var in struct {
		MachineID string `json:"machine_id"`
		Name      string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	s.proxyRun(w, r, p, kind, rpc, docker.ProxyRequest{Action: docker.ProxyRemove, MachineID: in.MachineID, Name: in.Name}, kind+".site.delete", in.Name)
}

func (s *Server) proxyService(w http.ResponseWriter, r *http.Request) {
	p, kind, rpc, ok := s.requireProxy(w, r, rbac.NetworkApply)
	if !ok {
		return
	}
	var in struct {
		MachineID string `json:"machine_id"`
		Action    string `json:"action"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	switch in.Action {
	case docker.ProxyReload, docker.ProxyRestart, docker.ProxyStart, docker.ProxyStop, docker.ProxyValidate, docker.ProxyInstall:
	default:
		writeErr(w, http.StatusBadRequest, errors.New("action must be reload, restart, start, stop, validate or install").Error())
		return
	}
	s.proxyRun(w, r, p, kind, rpc, docker.ProxyRequest{Action: in.Action, MachineID: in.MachineID}, kind+"."+in.Action, firstNonEmpty(in.MachineID, docker.HostMachineID))
}
