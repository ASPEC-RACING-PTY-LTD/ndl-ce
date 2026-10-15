package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	cf "github.com/no-dal/ndl-ce/internal/cloudflare"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Moving routes between tunnels. The order per route keeps the site up:
// add the route to the destination, repoint DNS, check the site publicly,
// and only then remove it from the source. A failure undoes that route's
// completed steps. Source tunnels are never deleted.

type cfMoveIn struct {
	Routes      []cfRouteRef `json:"routes"`
	DestAccount string       `json:"dest_account_id"`
	DestTunnel  string       `json:"dest_tunnel_id"`
	Confirm     bool         `json:"confirm"`
}

type cfMovePlan struct {
	Route    cfRouteView `json:"route"`
	OK       bool        `json:"ok"`
	Blockers []string    `json:"blockers"`
	Warnings []string    `json:"warnings"`
}

// cfMoveCheckVerify is how long a moved route may take to answer publicly.
var cfMoveCheckVerify = []time.Duration{3 * time.Second, 5 * time.Second, 8 * time.Second, 12 * time.Second, 20 * time.Second}

func (s *Server) cfPlanMove(ctx context.Context, clusterID string, in cfMoveIn) (*cfTunnelView, []cfMovePlan, error) {
	if len(in.Routes) == 0 || len(in.Routes) > 100 {
		return nil, nil, cfRefuse(http.StatusBadRequest, "choose between 1 and 100 routes to move")
	}
	inv, dest, err := s.cfTunnelFor(ctx, clusterID, strings.ToLower(in.DestAccount), strings.ToLower(in.DestTunnel))
	if err != nil {
		return nil, nil, err
	}
	destMachines := map[string]bool{}
	for _, l := range dest.Local {
		destMachines[l.MachineID] = true
	}
	var plans []cfMovePlan
	for _, ref := range in.Routes {
		ref := ref
		if err := ref.clean(); err != nil {
			return nil, nil, cfRefuse(http.StatusBadRequest, "%s", err.Error())
		}
		var route *cfRouteView
		for i := range inv.Routes {
			if inv.Routes[i].Key == cfRouteKey(ref.TunnelID, ref.Hostname, ref.Path) {
				route = &inv.Routes[i]
			}
		}
		if route == nil {
			return nil, nil, cfRefuse(http.StatusNotFound, "route %s%s not found; refresh and try again", ref.Hostname, ref.Path)
		}
		pl := cfMovePlan{Route: *route, Blockers: []string{}, Warnings: []string{}}
		src := inv.tunnel(route.TunnelID)
		switch {
		case strings.EqualFold(route.TunnelID, dest.ID):
			pl.Blockers = append(pl.Blockers, "it is already on "+dest.Name)
		case src == nil || !src.RemoteManaged:
			pl.Blockers = append(pl.Blockers, "its tunnel is managed by a config file, so the route cannot be removed from it here")
		case !route.Enabled:
			pl.Blockers = append(pl.Blockers, "the route is switched off; switch it on first")
		}
		if !dest.RemoteManaged {
			pl.Blockers = append(pl.Blockers, dest.Name+" is managed by a config file, so routes cannot be added to it here")
		}
		if len(dest.Connectors) == 0 {
			pl.Blockers = append(pl.Blockers, dest.Name+" has no connected connector, so the site would go down")
		}
		for _, other := range inv.Routes {
			if strings.EqualFold(other.TunnelID, dest.ID) && strings.EqualFold(other.Hostname, ref.Hostname) && other.Path == ref.Path {
				pl.Blockers = append(pl.Blockers, dest.Name+" already has a route for this hostname")
			}
		}
		if route.ZoneID == "" {
			pl.Blockers = append(pl.Blockers, "its domain is not in a zone a saved token can see, so DNS cannot be repointed")
		} else if route.ZoneAccountID != "" && route.ZoneAccountID != dest.AccountTag {
			pl.Blockers = append(pl.Blockers, fmt.Sprintf("%s is in the %s account; a tunnel in %s cannot serve it (Cloudflare error 1014)", route.ZoneName, inv.accountName(route.ZoneAccountID), dest.AccountName))
		}
		if route.DNS != nil && route.DNS.Action == cf.DNSConflict {
			pl.Blockers = append(pl.Blockers, "DNS: "+route.DNS.Reason)
		}
		if o := route.Origin; o != nil {
			local := o.Host == "localhost" || o.Host == "127.0.0.1" || o.Host == "::1"
			if local && src != nil {
				same := false
				for _, l := range src.Local {
					if destMachines[l.MachineID] {
						same = true
					}
				}
				if !same {
					pl.Blockers = append(pl.Blockers, "the origin is localhost on the old connector's machine; the new tunnel's connector runs elsewhere and could not reach it. Change the origin to an address first.")
				}
			}
			if !local && net.ParseIP(o.Host) != nil {
				if rpc, ok := s.connectorRPC(); ok && len(dest.Local) > 0 {
					res, err := rpc.DockerConnector(ctx, docker.ConnectorRequest{Action: docker.ConnectorProbe, MachineID: dest.Local[0].MachineID, Host: o.Host, Port: o.Port})
					switch {
					case err != nil:
						pl.Warnings = append(pl.Warnings, "could not test the origin from "+dest.Local[0].MachineName+": "+err.Error())
					case !res.Reachable:
						pl.Blockers = append(pl.Blockers, fmt.Sprintf("%s's connector on %s cannot reach %s:%d: %s", dest.Name, dest.Local[0].MachineName, o.Host, o.Port, res.Message))
					}
				} else {
					pl.Warnings = append(pl.Warnings, "no connector for "+dest.Name+" runs on this node, so reaching the origin was not tested; the move checks the site publicly before removing the old route")
				}
			}
		}
		pl.OK = len(pl.Blockers) == 0
		plans = append(plans, pl)
	}
	return dest, plans, nil
}

func (s *Server) previewCloudflareMove(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareRead)
	if err != nil {
		return
	}
	var in cfMoveIn
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	dest, plans, err := s.cfPlanMove(r.Context(), p.User.ClusterID, in)
	if err != nil {
		cfFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"destination": map[string]any{"id": dest.ID, "name": dest.Name, "account_name": dest.AccountName}, "items": plans})
}

// moveCloudflareRoutes moves the routes that passed the preview, one at a
// time, in a background job with a live log.
func (s *Server) moveCloudflareRoutes(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	var in cfMoveIn
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !in.Confirm {
		writeErr(w, http.StatusBadRequest, "confirm the move")
		return
	}
	clusterID := p.User.ClusterID
	dest, plans, err := s.cfPlanMove(r.Context(), clusterID, in)
	if err != nil {
		cfFail(w, err)
		return
	}
	var ready []cfMovePlan
	for _, pl := range plans {
		if pl.OK {
			ready = append(ready, pl)
		}
	}
	if len(ready) == 0 {
		writeErr(w, http.StatusConflict, "none of the routes can be moved; see the preview for why")
		return
	}
	j := s.newOpsJob("cloudflare-move", fmt.Sprintf("Move %d route(s) to %s", len(ready), dest.Name), p.User.Username)
	s.opsJobs.add(j)
	by, userID := p.User.Username, p.User.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		moved := 0
		for _, pl := range plans {
			if !pl.OK {
				j.log("warn", "%s skipped: %s", pl.Route.Hostname+pl.Route.Path, strings.Join(pl.Blockers, "; "))
				continue
			}
			if s.cfMoveOne(ctx, clusterID, by, j, pl.Route, dest) {
				moved++
			}
		}
		s.cfInvalidate(clusterID)
		j.mu.Lock()
		j.FinishedAt = time.Now().UTC()
		j.Status = "succeeded"
		if j.Failed > 0 {
			j.Status = "failed"
		}
		j.mu.Unlock()
		j.log("info", "%d of %d route(s) moved. The old tunnels were kept; delete them yourself once you no longer need them.", moved, len(plans))
		s.auditBackground(clusterID, userID, "cloudflare.route.move", j.Status, fmt.Sprintf("%d route(s) to %s", moved, dest.Name))
	}()
	s.audit(r, clusterID, p.User.ID, "cloudflare.route.move.start", "ok", fmt.Sprintf("%d route(s) to %s", len(ready), dest.Name))
	writeJSON(w, http.StatusAccepted, j.json())
}

func (s *Server) cfMoveOne(ctx context.Context, clusterID, by string, j *opsJob, route cfRouteView, dest *cfTunnelView) bool {
	name := route.Hostname + route.Path
	srcAccount, srcTunnel := route.AccountID, route.TunnelID
	var rule cf.IngressRule
	// 1. Add to the destination, copying the source rule exactly.
	err := s.cfWith(ctx, clusterID, srcAccount, "", func(c *cf.Client) error {
		cfg, err := c.Config(ctx, srcAccount, srcTunnel)
		if err != nil {
			return err
		}
		r, i := cf.FindRule(cfg.Config, route.Hostname, route.Path)
		if i < 0 {
			return fmt.Errorf("the route is no longer on the source tunnel")
		}
		rule = r
		return nil
	})
	if err != nil {
		j.log("error", "%s: could not read the source route: %v", name, err)
		return false
	}
	addDest := func(c *cf.Client) error {
		_, _, err := c.ApplyConfig(ctx, dest.AccountTag, dest.ID, func(b *cf.ConfigBody) error {
			_, err := cf.UpsertRule(b, rule)
			return err
		})
		return err
	}
	removeDest := func() {
		_ = s.cfWith(context.WithoutCancel(ctx), clusterID, dest.AccountTag, "", func(c *cf.Client) error {
			_, _, err := c.ApplyConfig(ctx, dest.AccountTag, dest.ID, func(b *cf.ConfigBody) error {
				cf.RemoveRule(b, route.Hostname, route.Path)
				return nil
			})
			return err
		})
	}
	if err := s.cfWith(ctx, clusterID, dest.AccountTag, "", addDest); err != nil {
		j.log("error", "%s: could not add the route to %s: %v", name, dest.Name, err)
		return false
	}
	j.log("info", "%s: added to %s", name, dest.Name)
	// 2. Repoint DNS.
	inv, _ := s.cfInventoryFor(ctx, clusterID, false)
	z, plan, err := s.cfPlanHostname(ctx, clusterID, inv, route.Hostname, dest)
	if err == nil && plan.Action == cf.DNSConflict {
		err = fmt.Errorf("%s", plan.Reason)
	}
	if err == nil {
		err = s.cfApplyDNS(ctx, clusterID, z, plan, dest)
	}
	if err != nil {
		removeDest()
		j.log("error", "%s: DNS could not be repointed (%v); the route was taken off %s again and still runs on the old tunnel", name, err, dest.Name)
		return false
	}
	j.log("info", "%s: DNS now points at %s", name, dest.Name)
	// 3. Check the site publicly.
	var res uptimeResult
	for _, wait := range cfMoveCheckVerify {
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		res = s.cfPublic(cctx, "https://"+route.Hostname+route.Path)
		cancel()
		if res.OK {
			break
		}
	}
	if !res.OK {
		j.log("warn", "%s: the site did not answer through %s (%s); undoing", name, dest.Name, firstNonEmpty(res.Error, fmt.Sprintf("HTTP %d", res.Status)))
		back := &cfTunnelView{Tunnel: cf.Tunnel{ID: srcTunnel, Name: route.TunnelName, AccountTag: srcAccount}}
		if _, bplan, perr := s.cfPlanHostname(ctx, clusterID, inv, route.Hostname, back); perr == nil {
			if err := s.cfApplyDNS(ctx, clusterID, z, bplan, back); err != nil {
				j.log("error", "%s: DNS could not be pointed back at %s: %v. Fix the CNAME by hand.", name, route.TunnelName, err)
			}
		}
		removeDest()
		j.log("error", "%s: rolled back; it still runs on %s", name, route.TunnelName)
		return false
	}
	// 4. Remove from the source.
	err = s.cfWith(ctx, clusterID, srcAccount, "", func(c *cf.Client) error {
		_, _, err := c.ApplyConfig(ctx, srcAccount, srcTunnel, func(b *cf.ConfigBody) error {
			cf.RemoveRule(b, route.Hostname, route.Path)
			return nil
		})
		return err
	})
	if err != nil {
		j.log("warn", "%s: works on %s, but the old route on %s could not be removed: %v", name, dest.Name, route.TunnelName, err)
	} else {
		j.log("info", "%s: moved (HTTP %d in %d ms); removed from %s", name, res.Status, res.LatencyMS, route.TunnelName)
	}
	if stored := s.cfStoredRoute(ctx, clusterID, srcTunnel, route.Hostname, route.Path); stored != nil {
		_ = s.Store.DeleteCloudflareRoute(ctx, clusterID, stored.ID)
		stored.TunnelID, stored.AccountID, stored.UpdatedBy = dest.ID, dest.AccountTag, by
		_ = s.Store.PutCloudflareRoute(ctx, *stored)
	}
	return true
}

// --- workloads ---

type cfOriginSuggestion struct {
	Label string `json:"label"`
	Host  string `json:"host"`
	Port  int    `json:"port,omitempty"`
}

// cloudflareForWorkload shows which routes expose a workload and suggests
// origin addresses for publishing it.
func (s *Server) cloudflareForWorkload(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareRead)
	if err != nil {
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	wl, _ := s.Store.GetWorkload(ctx, clusterID, r.PathValue("id"))
	if wl == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	inv, refreshing := s.cfInventoryFor(ctx, clusterID, false)
	routes := []cfRouteView{}
	for _, rv := range inv.Routes {
		if rv.Workload != nil && rv.Workload.ID == wl.ID {
			routes = append(routes, rv)
		}
	}
	out := map[string]any{"routes": routes, "suggestions": s.cfSuggestions(ctx, clusterID, *wl), "connected": len(inv.Accounts) > 0, "refreshing": refreshing}
	if pref, _ := s.Store.GetCloudflareWorkloadPref(ctx, clusterID, wl.ID); pref != nil {
		out["preferred"] = map[string]string{"account_id": pref.AccountID, "tunnel_id": pref.TunnelID}
	}
	if wl.Kind == lxc.KindSystemContainer {
		out["current_ip"] = s.workloadIPv4(ctx, clusterID, wl.ID)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) cfSuggestions(ctx context.Context, clusterID string, wl appdb.Workload) []cfOriginSuggestion {
	out := []cfOriginSuggestion{}
	switch wl.Kind {
	case lxc.KindSystemContainer:
		if ip := s.workloadIPv4(ctx, clusterID, wl.ID); ip != "" {
			out = append(out, cfOriginSuggestion{Label: wl.Name + " address", Host: ip})
		}
	case oci.KindOCI:
		var spec oci.Spec
		if len(wl.SpecJSON) > 0 {
			_ = json.Unmarshal(wl.SpecJSON, &spec)
		}
		for _, port := range spec.Ports {
			if port.HostPort > 0 {
				out = append(out, cfOriginSuggestion{Label: fmt.Sprintf("host port %d (connector on the host)", port.HostPort), Host: "127.0.0.1", Port: port.HostPort})
			}
		}
	}
	return out
}

// publishWorkload adds a public hostname for a workload. The origin address is
// the workload's own unless one is given. Nothing is published without this
// explicit call.
func (s *Server) publishWorkload(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	wl, _ := s.Store.GetWorkload(ctx, clusterID, r.PathValue("id"))
	if wl == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	var in cfRouteIn
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if strings.TrimSpace(in.Host) == "" {
		if sug := s.cfSuggestions(ctx, clusterID, *wl); len(sug) > 0 {
			in.Host = sug[0].Host
			if in.Port == 0 {
				in.Port = sug[0].Port
			}
		}
	}
	if strings.TrimSpace(in.Host) == "" {
		writeErr(w, http.StatusConflict, "the workload has no address yet; start it or enter the origin address")
		return
	}
	in.WorkloadID = wl.ID
	res, err := s.cfUpsertRoute(ctx, clusterID, p.User.Username, in)
	if err != nil {
		s.audit(r, clusterID, p.User.ID, "cloudflare.workload.publish", "failed", wl.Name+" "+in.Hostname)
		cfFail(w, err)
		return
	}
	_ = s.Store.PutCloudflareWorkloadPref(ctx, appdb.CloudflareWorkloadPref{WorkloadID: wl.ID, ClusterID: clusterID, AccountID: in.AccountID, TunnelID: in.TunnelID})
	s.audit(r, clusterID, p.User.ID, "cloudflare.workload.publish", "ok", wl.Name+" "+res.Hostname+" -> "+res.Service)
	writeJSON(w, http.StatusOK, res)
}
