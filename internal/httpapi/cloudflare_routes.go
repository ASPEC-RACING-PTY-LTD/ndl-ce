package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	cf "github.com/no-dal/ndl-ce/internal/cloudflare"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// cfRouteRef names one route: a hostname and path on a tunnel.
type cfRouteRef struct {
	AccountID string `json:"account_id"`
	TunnelID  string `json:"tunnel_id"`
	Hostname  string `json:"hostname"`
	Path      string `json:"path"`
}

// cfRouteIn creates or edits a public hostname route.
type cfRouteIn struct {
	cfRouteRef
	Protocol string           `json:"protocol"`
	Host     string           `json:"host"`
	Port     int              `json:"port"`
	Status   int              `json:"status_code"`
	Options  cf.OriginOptions `json:"options"`
	// DNS creates or fixes the hostname's CNAME (default true).
	DNS *bool `json:"dns"`
	// ReplaceDNS lets a CNAME that points at another tunnel be repointed.
	ReplaceDNS bool   `json:"replace_dns"`
	WorkloadID string `json:"workload_id"`
	// Original is the route being edited when its hostname or path changes.
	Original *cfRouteRef `json:"original"`
}

type cfRouteResult struct {
	Key       string   `json:"key"`
	Hostname  string   `json:"hostname"`
	Service   string   `json:"service"`
	DNSAction string   `json:"dns_action"`
	Replaced  bool     `json:"replaced"`
	Warnings  []string `json:"warnings,omitempty"`
}

// cfError is a refusal with a clear reason and HTTP status.
type cfError struct {
	status int
	msg    string
}

func (e *cfError) Error() string { return e.msg }

func cfRefuse(status int, format string, args ...any) error {
	return &cfError{status: status, msg: fmt.Sprintf(format, args...)}
}

func cfFail(w http.ResponseWriter, err error) {
	var ce *cfError
	if errors.As(err, &ce) {
		writeErr(w, ce.status, ce.msg)
		return
	}
	cfWriteErr(w, err)
}

func cleanPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if len(p) > 512 || strings.ContainsAny(p, "\r\n\t ") {
		return "", fmt.Errorf("path is invalid")
	}
	return p, nil
}

func (in *cfRouteRef) clean() error {
	h, err := cf.NormalizeHostname(in.Hostname)
	if err != nil {
		return err
	}
	in.Hostname = h
	if in.Path, err = cleanPath(in.Path); err != nil {
		return err
	}
	in.AccountID = strings.ToLower(strings.TrimSpace(in.AccountID))
	in.TunnelID = strings.ToLower(strings.TrimSpace(in.TunnelID))
	if !cfAccountIDRe.MatchString(in.AccountID) {
		return fmt.Errorf("account is required")
	}
	if _, err := uuid.Parse(in.TunnelID); err != nil {
		return fmt.Errorf("tunnel is required")
	}
	return nil
}

func (in cfRouteIn) service() (string, error) {
	if in.Status > 0 {
		if in.Status < 100 || in.Status > 599 {
			return "", fmt.Errorf("status code is invalid")
		}
		return fmt.Sprintf("http_status:%d", in.Status), nil
	}
	return cf.BuildService(in.Protocol, in.Host, in.Port)
}

// cfTunnelFor finds a tunnel in the inventory and checks its account.
func (s *Server) cfTunnelFor(ctx context.Context, clusterID, accountID, tunnelID string) (*cfInventory, *cfTunnelView, error) {
	inv, _ := s.cfInventoryFor(ctx, clusterID, false)
	t := inv.tunnel(tunnelID)
	if t == nil {
		inv, _ = s.cfInventoryFor(ctx, clusterID, true)
		t = inv.tunnel(tunnelID)
	}
	if t == nil || t.AccountTag != accountID {
		return inv, nil, cfRefuse(http.StatusNotFound, "tunnel not found in that account")
	}
	return inv, t, nil
}

// cfPlanHostname looks up the zone and current records for hostname and
// plans its CNAME to tunnelID.
func (s *Server) cfPlanHostname(ctx context.Context, clusterID string, inv *cfInventory, hostname string, t *cfTunnelView) (cf.Zone, cf.DNSPlan, error) {
	z, ok := cf.ZoneFor(hostname, inv.zones())
	if !ok {
		return z, cf.DNSPlan{}, cfRefuse(http.StatusUnprocessableEntity, "%s is not in any zone a saved token can see; add the domain to Cloudflare or add a token that can read it", hostname)
	}
	if z.Account.ID != "" && z.Account.ID != t.AccountTag {
		return z, cf.DNSPlan{}, cfRefuse(http.StatusUnprocessableEntity,
			"%s is in the %s account but the tunnel is in %s; Cloudflare does not route a domain to a tunnel in another account (error 1014)",
			z.Name, inv.accountName(z.Account.ID), t.AccountName)
	}
	var recs []cf.DNSRecord
	err := s.cfWith(ctx, clusterID, z.Account.ID, z.ID, func(c *cf.Client) error {
		var err error
		recs, err = c.DNSRecords(ctx, z.ID, hostname)
		return err
	})
	if errors.Is(err, cf.ErrPermission) || errors.Is(err, errCFNoCredential) {
		// The token can route the tunnel but not read DNS (for example a
		// Zero Trust only token). That is not a reason to refuse the route.
		return z, cf.DNSPlan{Action: cf.DNSUnavailable, Hostname: hostname,
			Reason: "no saved token may read DNS in " + z.Name + "; add Zone: DNS Edit to the token so No-dal can create and check the CNAME, or manage DNS for " + hostname + " yourself"}, nil
	}
	if err != nil {
		return z, cf.DNSPlan{}, err
	}
	return z, cf.PlanDNS(hostname, t.ID, recs), nil
}

// cfApplyDNS makes hostname's CNAME point at the tunnel as planned.
func (s *Server) cfApplyDNS(ctx context.Context, clusterID string, z cf.Zone, plan cf.DNSPlan, t *cfTunnelView) error {
	want := cf.TunnelCNAME(plan.Hostname, t.ID, t.Name)
	return s.cfWith(ctx, clusterID, z.Account.ID, z.ID, func(c *cf.Client) error {
		switch plan.Action {
		case cf.DNSCreate:
			_, err := c.CreateDNS(ctx, z.ID, want)
			return err
		case cf.DNSProxy, cf.DNSRetarget:
			_, err := c.UpdateDNS(ctx, z.ID, plan.Record.ID, want)
			return err
		}
		return nil
	})
}

// cfUpsertRoute adds or changes a route. DNS conflicts are checked before
// anything is written, and a failed DNS change undoes the route change.
func (s *Server) cfUpsertRoute(ctx context.Context, clusterID, by string, in cfRouteIn) (cfRouteResult, error) {
	var res cfRouteResult
	if err := in.clean(); err != nil {
		return res, cfRefuse(http.StatusBadRequest, "%s", err.Error())
	}
	service, err := in.service()
	if err != nil {
		return res, cfRefuse(http.StatusBadRequest, "%s", err.Error())
	}
	if in.Original != nil {
		if err := in.Original.clean(); err != nil {
			return res, cfRefuse(http.StatusBadRequest, "original route: %s", err.Error())
		}
		if in.Original.TunnelID != in.TunnelID {
			return res, cfRefuse(http.StatusBadRequest, "use Move to change a route's tunnel")
		}
	}
	inv, t, err := s.cfTunnelFor(ctx, clusterID, in.AccountID, in.TunnelID)
	if err != nil {
		return res, err
	}
	if !t.RemoteManaged {
		return res, cfRefuse(http.StatusUnprocessableEntity, "%s is managed by a config file on its connector; its routes cannot be changed from No-dal", t.Name)
	}
	for _, r := range inv.Routes {
		if r.Enabled && strings.EqualFold(r.Hostname, in.Hostname) && r.Path == in.Path && !strings.EqualFold(r.TunnelID, t.ID) {
			return res, cfRefuse(http.StatusConflict, "%s%s is already routed by tunnel %s; use Move to change its tunnel", in.Hostname, in.Path, r.TunnelName)
		}
	}
	wantDNS := in.DNS == nil || *in.DNS
	var zone cf.Zone
	var plan cf.DNSPlan
	if wantDNS {
		zone, plan, err = s.cfPlanHostname(ctx, clusterID, inv, in.Hostname, t)
		if err != nil {
			return res, err
		}
		switch plan.Action {
		case cf.DNSConflict:
			return res, cfRefuse(http.StatusConflict, "DNS for %s cannot be set: %s. Nothing was changed.", in.Hostname, plan.Reason)
		case cf.DNSRetarget:
			if !in.ReplaceDNS {
				name := plan.CurrentTunnel
				if ot := inv.tunnel(plan.CurrentTunnel); ot != nil {
					name = ot.Name
				}
				return res, cfRefuse(http.StatusConflict, "%s already points at tunnel %s. Move the route, or confirm replacing the DNS record.", in.Hostname, name)
			}
		}
	}
	var undo func()
	err = s.cfWith(ctx, clusterID, t.AccountTag, "", func(c *cf.Client) error {
		_, _, err := c.ApplyConfig(ctx, t.AccountTag, t.ID, func(b *cf.ConfigBody) error {
			base := cf.IngressRule{Hostname: in.Hostname, Path: in.Path}
			if in.Original != nil {
				if old, ok := cf.RemoveRule(b, in.Original.Hostname, in.Original.Path); ok {
					base.OriginRequest = old.OriginRequest
				}
			}
			if cur, i := cf.FindRule(*b, in.Hostname, in.Path); i >= 0 {
				base.OriginRequest = cur.OriginRequest
			}
			rule := base.WithOptions(in.Options)
			rule.Hostname, rule.Path, rule.Service = in.Hostname, in.Path, service
			replaced, err := cf.UpsertRule(b, rule)
			res.Replaced = replaced
			return err
		})
		if err != nil {
			return err
		}
		undo = func() {
			_, _, _ = c.ApplyConfig(context.WithoutCancel(ctx), t.AccountTag, t.ID, func(b *cf.ConfigBody) error {
				cf.RemoveRule(b, in.Hostname, in.Path)
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	res.DNSAction = "skipped"
	if wantDNS {
		res.DNSAction = plan.Action
		if plan.Action == cf.DNSUnavailable {
			res.Warnings = append(res.Warnings, "DNS was not changed: "+plan.Reason+".")
		} else if err := s.cfApplyDNS(ctx, clusterID, zone, plan, t); err != nil {
			if !res.Replaced && in.Original == nil && undo != nil {
				undo()
				return res, fmt.Errorf("the DNS record could not be set, so the new route was removed again: %w", err)
			}
			res.Warnings = append(res.Warnings, "the route was saved but its DNS record could not be set: "+err.Error())
		}
	}
	rule := cf.IngressRule{Hostname: in.Hostname, Path: in.Path, Service: service}.WithOptions(in.Options)
	raw, _ := json.Marshal(rule)
	row := appdb.CloudflareRoute{
		ID: uuid.NewString(), ClusterID: clusterID, AccountID: t.AccountTag, TunnelID: t.ID, Hostname: in.Hostname, Path: in.Path,
		Rule: raw, Enabled: true, WorkloadID: in.WorkloadID, UpdatedBy: by,
	}
	if ip := net.ParseIP(in.Host); ip != nil {
		row.OriginIP = ip.String()
	}
	if in.WorkloadID == "" {
		if prev := s.cfStoredRoute(ctx, clusterID, t.ID, in.Hostname, in.Path); prev != nil {
			row.WorkloadID = prev.WorkloadID
		}
	}
	_ = s.Store.PutCloudflareRoute(ctx, row)
	if in.Original != nil && (in.Original.Hostname != in.Hostname || in.Original.Path != in.Path) {
		if prev := s.cfStoredRoute(ctx, clusterID, t.ID, in.Original.Hostname, in.Original.Path); prev != nil {
			_ = s.Store.DeleteCloudflareRoute(ctx, clusterID, prev.ID)
		}
		res.Warnings = append(res.Warnings, "the DNS record for "+in.Original.Hostname+" was left in place; remove it under DNS if it is no longer needed")
	}
	s.cfInvalidate(clusterID)
	res.Key, res.Hostname, res.Service = cfRouteKey(t.ID, in.Hostname, in.Path), in.Hostname, service
	return res, nil
}

func (s *Server) cfStoredRoute(ctx context.Context, clusterID, tunnelID, hostname, path string) *appdb.CloudflareRoute {
	rows, _ := s.Store.ListCloudflareRoutes(ctx, clusterID)
	for _, r := range rows {
		if strings.EqualFold(r.TunnelID, tunnelID) && strings.EqualFold(r.Hostname, hostname) && r.Path == path {
			return &r
		}
	}
	return nil
}

func (s *Server) upsertCloudflareRoute(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	var in cfRouteIn
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if in.WorkloadID != "" {
		if wl, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, in.WorkloadID); wl == nil {
			writeErr(w, http.StatusBadRequest, "workload not found")
			return
		}
	}
	res, err := s.cfUpsertRoute(r.Context(), p.User.ClusterID, p.User.Username, in)
	if err != nil {
		s.audit(r, p.User.ClusterID, p.User.ID, "cloudflare.route.upsert", "failed", in.Hostname)
		cfFail(w, err)
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "cloudflare.route.upsert", "ok", res.Hostname+" -> "+res.Service+" dns="+res.DNSAction)
	writeJSON(w, http.StatusOK, res)
}

// planCloudflareRoute previews DNS for a hostname on a tunnel without changing anything.
func (s *Server) planCloudflareRoute(w http.ResponseWriter, r *http.Request) {
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
	inv, t, err := s.cfTunnelFor(r.Context(), p.User.ClusterID, in.AccountID, in.TunnelID)
	if err != nil {
		cfFail(w, err)
		return
	}
	z, plan, err := s.cfPlanHostname(r.Context(), p.User.ClusterID, inv, in.Hostname, t)
	if err != nil {
		cfFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"zone": z, "plan": plan, "remote_managed": t.RemoteManaged})
}

// setCloudflareRouteEnabled switches a route off (removing it from the
// tunnel while No-dal keeps a copy) or back on. DNS is left alone.
func (s *Server) setCloudflareRouteEnabled(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	var in struct {
		cfRouteRef
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := in.cfRouteRef.clean(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	_, t, err := s.cfTunnelFor(ctx, clusterID, in.AccountID, in.TunnelID)
	if err != nil {
		cfFail(w, err)
		return
	}
	if !t.RemoteManaged {
		writeErr(w, http.StatusUnprocessableEntity, "routes on a config-file tunnel cannot be switched from No-dal")
		return
	}
	stored := s.cfStoredRoute(ctx, clusterID, t.ID, in.Hostname, in.Path)
	var rule cf.IngressRule
	if in.Enabled {
		if stored == nil || json.Unmarshal(stored.Rule, &rule) != nil || rule.Hostname == "" {
			writeErr(w, http.StatusNotFound, "No-dal has no saved copy of that route")
			return
		}
	}
	err = s.cfWith(ctx, clusterID, t.AccountTag, "", func(c *cf.Client) error {
		_, _, err := c.ApplyConfig(ctx, t.AccountTag, t.ID, func(b *cf.ConfigBody) error {
			if in.Enabled {
				_, err := cf.UpsertRule(b, rule)
				return err
			}
			removed, ok := cf.RemoveRule(b, in.Hostname, in.Path)
			if !ok {
				return cfRefuse(http.StatusNotFound, "the route is not on the tunnel")
			}
			rule = removed
			return nil
		})
		return err
	})
	if err != nil {
		cfFail(w, err)
		return
	}
	raw, _ := json.Marshal(rule)
	row := appdb.CloudflareRoute{ID: uuid.NewString(), ClusterID: clusterID, AccountID: t.AccountTag, TunnelID: t.ID, Hostname: in.Hostname, Path: in.Path,
		Rule: raw, Enabled: in.Enabled, UpdatedBy: p.User.Username}
	if stored != nil {
		row.ID, row.WorkloadID, row.OriginIP = stored.ID, stored.WorkloadID, stored.OriginIP
	}
	_ = s.Store.PutCloudflareRoute(ctx, row)
	s.cfInvalidate(clusterID)
	action := "cloudflare.route.disable"
	if in.Enabled {
		action = "cloudflare.route.enable"
	}
	s.audit(r, clusterID, p.User.ID, action, "ok", in.Hostname+in.Path)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": in.Enabled})
}

// deleteCloudflareRoute removes a route. Its CNAME is only removed when asked
// and only if it points at this tunnel. confirm must repeat the hostname.
func (s *Server) deleteCloudflareRoute(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	var in struct {
		cfRouteRef
		DeleteDNS bool   `json:"delete_dns"`
		Confirm   string `json:"confirm"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := in.cfRouteRef.clean(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !strings.EqualFold(strings.TrimSpace(in.Confirm), in.Hostname) {
		writeErr(w, http.StatusBadRequest, "type the hostname to confirm")
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	inv, t, err := s.cfTunnelFor(ctx, clusterID, in.AccountID, in.TunnelID)
	if err != nil {
		cfFail(w, err)
		return
	}
	if t.RemoteManaged {
		err = s.cfWith(ctx, clusterID, t.AccountTag, "", func(c *cf.Client) error {
			_, _, err := c.ApplyConfig(ctx, t.AccountTag, t.ID, func(b *cf.ConfigBody) error {
				cf.RemoveRule(b, in.Hostname, in.Path)
				return nil
			})
			return err
		})
		if err != nil {
			cfFail(w, err)
			return
		}
	} else if !in.DeleteDNS {
		writeErr(w, http.StatusUnprocessableEntity, "routes on a config-file tunnel are removed in that file")
		return
	}
	note := "DNS record kept"
	if in.DeleteDNS {
		note = "no DNS record pointed at this tunnel"
		if z, plan, perr := s.cfPlanHostname(ctx, clusterID, inv, in.Hostname, t); perr == nil && plan.Record != nil &&
			(plan.Action == cf.DNSOK || plan.Action == cf.DNSProxy) {
			if err := s.cfWith(ctx, clusterID, z.Account.ID, z.ID, func(c *cf.Client) error { return c.DeleteDNS(ctx, z.ID, plan.Record.ID) }); err != nil {
				note = "the route was removed but its DNS record could not be: " + err.Error()
			} else {
				note = "DNS record removed"
			}
		}
	}
	if stored := s.cfStoredRoute(ctx, clusterID, t.ID, in.Hostname, in.Path); stored != nil {
		_ = s.Store.DeleteCloudflareRoute(ctx, clusterID, stored.ID)
	}
	s.cfInvalidate(clusterID)
	s.audit(r, clusterID, p.User.ID, "cloudflare.route.delete", "ok", in.Hostname+in.Path+" ("+note+")")
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "note": note})
}

// syncCloudflareRouteIP points a workload's route at the workload's current address.
func (s *Server) syncCloudflareRouteIP(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
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
	stored := s.cfStoredRoute(ctx, clusterID, in.TunnelID, in.Hostname, in.Path)
	if stored == nil || stored.WorkloadID == "" {
		writeErr(w, http.StatusNotFound, "the route is not linked to a workload")
		return
	}
	ip := s.workloadIPv4(ctx, clusterID, stored.WorkloadID)
	if ip == "" {
		writeErr(w, http.StatusConflict, "the workload has no address yet")
		return
	}
	var rule cf.IngressRule
	_ = json.Unmarshal(stored.Rule, &rule)
	ps, ok := cf.ParseService(rule.Service)
	if !ok {
		inv, _ := s.cfInventoryFor(ctx, clusterID, false)
		for _, rv := range inv.Routes {
			if rv.Key == cfRouteKey(in.TunnelID, in.Hostname, in.Path) && rv.Origin != nil {
				ps, ok, rule.OriginRequest = *rv.Origin, true, nil
			}
		}
	}
	if !ok {
		writeErr(w, http.StatusConflict, "the route's origin is not an address and port")
		return
	}
	dns := false
	res, err := s.cfUpsertRoute(ctx, clusterID, p.User.Username, cfRouteIn{cfRouteRef: in, Protocol: ps.Protocol, Host: ip, Port: ps.Port,
		Options: rule.Options(), DNS: &dns, WorkloadID: stored.WorkloadID})
	if err != nil {
		cfFail(w, err)
		return
	}
	s.audit(r, clusterID, p.User.ID, "cloudflare.route.sync-ip", "ok", in.Hostname+" -> "+res.Service)
	writeJSON(w, http.StatusOK, res)
}
