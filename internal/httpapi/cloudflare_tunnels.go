package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	cf "github.com/no-dal/ndl-ce/internal/cloudflare"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Agents that deploy cloudflared connectors implement this.
type dockerConnectorRPC interface {
	DockerConnector(ctx context.Context, req docker.ConnectorRequest) (docker.ConnectorResult, error)
}

func (s *Server) connectorRPC() (dockerConnectorRPC, bool) {
	rpc, ok := s.dockerRPC().(dockerConnectorRPC)
	return rpc, ok
}

func (s *Server) createCloudflareTunnel(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	var in struct {
		AccountID string `json:"account_id"`
		Name      string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !cfAccountIDRe.MatchString(in.AccountID) {
		writeErr(w, http.StatusBadRequest, "choose an account")
		return
	}
	if err := cf.ValidTunnelName(in.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var t cf.Tunnel
	err = s.cfWith(r.Context(), p.User.ClusterID, in.AccountID, "", func(c *cf.Client) error {
		var err error
		t, err = c.CreateTunnel(r.Context(), in.AccountID, in.Name)
		return err
	})
	if err != nil {
		s.audit(r, p.User.ClusterID, p.User.ID, "cloudflare.tunnel.create", "failed", in.Name)
		cfWriteErr(w, err)
		return
	}
	s.cfInvalidate(p.User.ClusterID)
	s.audit(r, p.User.ClusterID, p.User.ID, "cloudflare.tunnel.create", "ok", in.Name+" "+t.ID)
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) renameCloudflareTunnel(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	var in struct {
		AccountID string `json:"account_id"`
		Name      string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := cf.ValidTunnelName(in.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_, t, err := s.cfTunnelFor(r.Context(), p.User.ClusterID, in.AccountID, strings.ToLower(r.PathValue("id")))
	if err != nil {
		cfFail(w, err)
		return
	}
	err = s.cfWith(r.Context(), p.User.ClusterID, t.AccountTag, "", func(c *cf.Client) error {
		_, err := c.RenameTunnel(r.Context(), t.AccountTag, t.ID, in.Name)
		return err
	})
	if err != nil {
		cfWriteErr(w, err)
		return
	}
	s.cfInvalidate(p.User.ClusterID)
	s.audit(r, p.User.ClusterID, p.User.ID, "cloudflare.tunnel.rename", "ok", t.Name+" -> "+in.Name)
	writeJSON(w, http.StatusOK, map[string]any{"id": t.ID, "name": in.Name})
}

// deleteCloudflareTunnel deletes a tunnel only when nothing uses it: no
// routes, no DNS records pointing at it and no connected connectors.
func (s *Server) deleteCloudflareTunnel(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	var in struct {
		AccountID string `json:"account_id"`
		Confirm   string `json:"confirm"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	inv, _ := s.cfInventoryFor(ctx, clusterID, true)
	t := inv.tunnel(strings.ToLower(r.PathValue("id")))
	if t == nil || t.AccountTag != in.AccountID {
		writeErr(w, http.StatusNotFound, "tunnel not found in that account")
		return
	}
	if strings.TrimSpace(in.Confirm) != t.Name {
		writeErr(w, http.StatusBadRequest, "type the tunnel name to confirm")
		return
	}
	var blockers []string
	if t.RouteCount > 0 {
		blockers = append(blockers, fmt.Sprintf("it still has %d route(s); move or remove them first", t.RouteCount))
	}
	for _, d := range inv.DNS {
		if strings.EqualFold(d.TunnelID, t.ID) {
			blockers = append(blockers, "DNS record "+d.Record.Name+" points at it")
		}
	}
	if len(t.Connectors) > 0 {
		blockers = append(blockers, fmt.Sprintf("%d connector(s) are connected; stop or remove them first", len(t.Connectors)))
	}
	if len(blockers) > 0 {
		writeErr(w, http.StatusConflict, "the tunnel was not deleted: "+strings.Join(blockers, "; "))
		return
	}
	if err := s.cfWith(ctx, clusterID, t.AccountTag, "", func(c *cf.Client) error { return c.DeleteTunnel(ctx, t.AccountTag, t.ID) }); err != nil {
		cfWriteErr(w, err)
		return
	}
	s.cfInvalidate(clusterID)
	s.audit(r, clusterID, p.User.ID, "cloudflare.tunnel.delete", "ok", t.Name+" "+t.ID)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// cfMachines lists the host and system containers a connector can run on.
func (s *Server) listCloudflareMachines(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareRead)
	if err != nil {
		return
	}
	items := []map[string]any{{"id": docker.HostMachineID, "name": "Host", "kind": "host"}}
	for _, h := range s.dockerHints(r.Context(), p.User.ClusterID) {
		items = append(items, map[string]any{"id": h.ID, "name": h.Name, "kind": h.Kind})
	}
	_, supported := s.connectorRPC()
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "supported": supported})
}

// cloudflareConnector installs, updates, restarts or removes a connector on a
// machine. The tunnel token is fetched from Cloudflare and handed straight to
// the agent; it is never stored by the control plane or sent to the browser.
func (s *Server) cloudflareConnector(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	var in struct {
		AccountID string `json:"account_id"`
		MachineID string `json:"machine_id"`
		Action    string `json:"action"`
		Unit      string `json:"unit"`
		Confirm   bool   `json:"confirm"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	_, t, err := s.cfTunnelFor(ctx, clusterID, in.AccountID, strings.ToLower(r.PathValue("id")))
	if err != nil {
		cfFail(w, err)
		return
	}
	if in.MachineID == "" {
		in.MachineID = docker.HostMachineID
	}
	if in.MachineID != docker.HostMachineID {
		if wl, _ := s.Store.GetWorkload(ctx, clusterID, in.MachineID); wl == nil {
			writeErr(w, http.StatusBadRequest, "machine not found")
			return
		}
	}
	if in.Action == "restart" {
		rpc, ok := s.dockerRPC().(dockerTunnelRPC)
		if !ok || !docker.ValidTunnelUnit(in.Unit) {
			writeErr(w, http.StatusBadRequest, "choose a cloudflared service to restart")
			return
		}
		if err := rpc.DockerTunnelUnit(ctx, in.MachineID, in.Unit, "restart"); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.audit(r, clusterID, p.User.ID, "cloudflare.connector.restart", "ok", in.MachineID+" "+in.Unit)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	rpc, ok := s.connectorRPC()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "this node's agent cannot deploy connectors; update No-dal on the node")
		return
	}
	req := docker.ConnectorRequest{Action: in.Action, MachineID: in.MachineID, TunnelID: t.ID}
	switch in.Action {
	case docker.ConnectorInstall:
		err := s.cfWith(ctx, clusterID, t.AccountTag, "", func(c *cf.Client) error {
			tok, err := c.TunnelToken(ctx, t.AccountTag, t.ID)
			req.Token = tok
			return err
		})
		if err != nil {
			cfWriteErr(w, err)
			return
		}
	case docker.ConnectorRemove:
		if !in.Confirm {
			writeErr(w, http.StatusBadRequest, "confirm removing the connector")
			return
		}
	case docker.ConnectorUpdate:
	default:
		writeErr(w, http.StatusBadRequest, "action must be install, update, restart or remove")
		return
	}
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()
	res, err := rpc.DockerConnector(actx, req)
	req.Token = ""
	if err != nil {
		s.audit(r, clusterID, p.User.ID, "cloudflare.connector."+in.Action, "failed", in.MachineID+" "+t.Name)
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	switch in.Action {
	case docker.ConnectorInstall:
		_ = s.Store.PutCloudflareConnector(ctx, appdb.CloudflareConnector{ID: uuid.NewString(), ClusterID: clusterID, AccountID: t.AccountTag,
			TunnelID: t.ID, MachineID: in.MachineID, Unit: res.Unit, CreatedBy: p.User.Username})
	case docker.ConnectorRemove:
		list, _ := s.Store.ListCloudflareConnectors(ctx, clusterID)
		for _, c := range list {
			if c.MachineID == in.MachineID && c.Unit == res.Unit {
				_ = s.Store.DeleteCloudflareConnector(ctx, clusterID, c.ID)
			}
		}
	}
	s.tunnels.mu.Lock()
	s.tunnels.at = time.Time{}
	s.tunnels.mu.Unlock()
	s.cfInvalidate(clusterID)
	s.audit(r, clusterID, p.User.ID, "cloudflare.connector."+in.Action, "ok", in.MachineID+" "+t.Name+" "+res.Unit)
	writeJSON(w, http.StatusOK, res)
}

// --- diagnostics ---

type cfCheck struct {
	Area   string `json:"area"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// diagnoseCloudflareRoute checks a route layer by layer so the failing part
// is clear: the tunnel's connectors, DNS, the origin as seen from each local
// connector, and the public URL. Only the route's own origin is probed.
func (s *Server) diagnoseCloudflareRoute(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareRead)
	if err != nil {
		return
	}
	var in cfRouteRef
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := in.clean(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	inv, t, err := s.cfTunnelFor(ctx, clusterID, in.AccountID, in.TunnelID)
	if err != nil {
		cfFail(w, err)
		return
	}
	var route *cfRouteView
	for i := range inv.Routes {
		if inv.Routes[i].Key == cfRouteKey(t.ID, in.Hostname, in.Path) {
			route = &inv.Routes[i]
		}
	}
	if route == nil {
		writeErr(w, http.StatusNotFound, "route not found; refresh and try again")
		return
	}
	var checks []cfCheck
	// Tunnel and connectors.
	var conns []cf.Connector
	cerr := s.cfWith(ctx, clusterID, t.AccountTag, "", func(c *cf.Client) error {
		var err error
		conns, err = c.Connectors(ctx, t.AccountTag, t.ID)
		return err
	})
	switch {
	case cerr != nil:
		checks = append(checks, cfCheck{"tunnel", "warn", "could not read the tunnel's connectors: " + cerr.Error()})
	case len(conns) == 0:
		checks = append(checks, cfCheck{"tunnel", "fail", "no connector is connected to " + t.Name + ", so Cloudflare has nowhere to send traffic (visitors see error 1033). Start or deploy a connector."})
	default:
		colos := []string{}
		for _, c := range conns {
			for _, e := range c.Connections {
				colos = append(colos, e.ColoName)
			}
		}
		checks = append(checks, cfCheck{"tunnel", "ok", fmt.Sprintf("%d connector(s) connected via %s", len(conns), strings.Join(colos, ", "))})
	}
	// DNS.
	if _, plan, perr := s.cfPlanHostname(ctx, clusterID, inv, in.Hostname, t); perr != nil {
		checks = append(checks, cfCheck{"dns", "fail", perr.Error()})
	} else {
		switch plan.Action {
		case cf.DNSOK:
			checks = append(checks, cfCheck{"dns", "ok", in.Hostname + " is a proxied CNAME to this tunnel"})
		case cf.DNSCreate:
			checks = append(checks, cfCheck{"dns", "fail", "there is no DNS record for " + in.Hostname + "; edit the route with DNS on to create it"})
		default:
			checks = append(checks, cfCheck{"dns", "fail", plan.Reason})
		}
	}
	// Origin, from where the connectors run.
	checks = append(checks, s.cfProbeOrigin(ctx, route, t))
	// Public.
	if !route.Enabled {
		checks = append(checks, cfCheck{"public", "skip", "the route is switched off"})
	} else {
		pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		res := s.cfPublic(pctx, "https://"+in.Hostname+in.Path)
		cancel()
		checks = append(checks, cfPublicCheck(res))
	}
	writeJSON(w, http.StatusOK, map[string]any{"checks": checks, "summary": cfSummary(checks)})
}

func (s *Server) cfProbeOrigin(ctx context.Context, route *cfRouteView, t *cfTunnelView) cfCheck {
	if route.Origin == nil {
		return cfCheck{"origin", "skip", "the route answers with " + route.Service + " and has no origin"}
	}
	host := route.Origin.Host
	if host != "localhost" && net.ParseIP(host) == nil {
		return cfCheck{"origin", "skip", "the origin is a name (" + host + "); only addresses can be probed from here"}
	}
	rpc, ok := s.connectorRPC()
	if !ok {
		return cfCheck{"origin", "skip", "this node's agent cannot probe origins; update No-dal"}
	}
	machines := map[string]string{}
	for _, l := range t.Local {
		machines[l.MachineID] = l.MachineName
	}
	where := "where its connectors run"
	if len(machines) == 0 {
		if host == "localhost" || host == "127.0.0.1" {
			return cfCheck{"origin", "skip", "the origin is localhost on the connector, and no connector for this tunnel runs on this node"}
		}
		machines[docker.HostMachineID] = "Host"
		where = "this host (no connector for this tunnel runs on this node)"
	}
	var bad, good []string
	for id, name := range machines {
		res, err := rpc.DockerConnector(ctx, docker.ConnectorRequest{Action: docker.ConnectorProbe, MachineID: id, Host: host, Port: route.Origin.Port})
		switch {
		case err != nil:
			bad = append(bad, name+": "+err.Error())
		case res.Reachable:
			good = append(good, name)
		default:
			bad = append(bad, name+": "+res.Message)
		}
	}
	addr := net.JoinHostPort(host, fmt.Sprint(route.Origin.Port))
	if len(bad) > 0 {
		return cfCheck{"origin", "fail", addr + " is not reachable from " + strings.Join(bad, "; ")}
	}
	return cfCheck{"origin", "ok", addr + " accepts connections from " + where + " (" + strings.Join(good, ", ") + ")"}
}

func cfPublicCheck(res uptimeResult) cfCheck {
	switch {
	case res.OK:
		return cfCheck{"public", "ok", fmt.Sprintf("the site answered HTTP %d in %d ms", res.Status, res.LatencyMS)}
	case res.Status == 530 || res.Status == 1033:
		return cfCheck{"public", "fail", "Cloudflare could not reach a connector for this tunnel (HTTP 530)"}
	case res.Status == 502 || res.Status == 504:
		return cfCheck{"public", "fail", fmt.Sprintf("the connector could not reach the origin (HTTP %d)", res.Status)}
	case res.Status == 404:
		return cfCheck{"public", "warn", "HTTP 404: the request reached the tunnel or app but nothing matched; check the path and the app"}
	case res.Status > 0:
		return cfCheck{"public", "warn", fmt.Sprintf("the site answered HTTP %d", res.Status)}
	}
	return cfCheck{"public", "fail", firstNonEmpty(res.Error, "the site did not answer")}
}

func cfSummary(checks []cfCheck) string {
	for _, c := range checks {
		if c.Status == "fail" {
			switch c.Area {
			case "tunnel":
				return "Cloudflare connectivity: the tunnel has no working connector."
			case "dns":
				return "DNS: the hostname does not point at this tunnel."
			case "origin":
				return "Internal service: the connector cannot reach the origin."
			}
			return "The public check failed: " + c.Detail
		}
	}
	return "No problems found."
}
