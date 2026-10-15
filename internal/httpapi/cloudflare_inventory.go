package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	cf "github.com/no-dal/ndl-ce/internal/cloudflare"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

type cfCredRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type cfAccountView struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Credentials []cfCredRef `json:"credentials"`
	Zones       []cf.Zone   `json:"zones"`
	Problems    []string    `json:"problems,omitempty"`
}

type cfLocalConnector struct {
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	Unit        string `json:"unit"`
	Active      bool   `json:"active"`
	Managed     bool   `json:"managed"`
}

type cfTunnelView struct {
	cf.Tunnel
	AccountName    string             `json:"account_name"`
	RemoteManaged  bool               `json:"remote_managed"`
	Connectors     []cf.Connector     `json:"connectors"`
	RouteCount     int                `json:"route_count"`
	Local          []cfLocalConnector `json:"local"`
	ConfigVersion  int                `json:"config_version"`
	Error          string             `json:"error,omitempty"`
	config         *cf.TunnelConfig
	connectorsRead bool
}

type cfWorkloadRef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CurrentIP string `json:"current_ip,omitempty"`
	Linked    bool   `json:"linked"`
}

type cfRouteView struct {
	Key           string            `json:"key"`
	Hostname      string            `json:"hostname"`
	Path          string            `json:"path,omitempty"`
	Service       string            `json:"service"`
	Origin        *cf.ParsedService `json:"origin,omitempty"`
	Options       cf.OriginOptions  `json:"options"`
	Enabled       bool              `json:"enabled"`
	Editable      bool              `json:"editable"`
	AccountID     string            `json:"account_id"`
	AccountName   string            `json:"account_name"`
	TunnelID      string            `json:"tunnel_id"`
	TunnelName    string            `json:"tunnel_name"`
	TunnelStatus  string            `json:"tunnel_status"`
	ZoneID        string            `json:"zone_id,omitempty"`
	ZoneName      string            `json:"zone_name,omitempty"`
	ZoneAccountID string            `json:"zone_account_id,omitempty"`
	DNS           *cf.DNSPlan       `json:"dns,omitempty"`
	Workload      *cfWorkloadRef    `json:"workload,omitempty"`
	IPChanged     bool              `json:"ip_changed,omitempty"`
}

type cfDNSView struct {
	ZoneID      string       `json:"zone_id"`
	ZoneName    string       `json:"zone_name"`
	AccountID   string       `json:"account_id"`
	AccountName string       `json:"account_name"`
	Record      cf.DNSRecord `json:"record"`
	TunnelID    string       `json:"tunnel_id"`
	TunnelName  string       `json:"tunnel_name,omitempty"`
	// Status: routed, no-route (the tunnel has no route for this name) or
	// unknown-tunnel (the tunnel is deleted or in an account No-dal cannot see).
	Status  string `json:"status"`
	Managed bool   `json:"managed"`
}

type cfInventory struct {
	FetchedAt  time.Time       `json:"fetched_at"`
	Refreshing bool            `json:"refreshing"`
	Accounts   []cfAccountView `json:"accounts"`
	Tunnels    []*cfTunnelView `json:"tunnels"`
	Routes     []cfRouteView   `json:"routes"`
	DNS        []cfDNSView     `json:"dns"`
	Errors     []string        `json:"errors"`
	records    map[string][]cf.DNSRecord
}

func (inv *cfInventory) tunnel(id string) *cfTunnelView {
	for _, t := range inv.Tunnels {
		if strings.EqualFold(t.ID, id) {
			return t
		}
	}
	return nil
}

func (inv *cfInventory) zones() []cf.Zone {
	var out []cf.Zone
	for _, a := range inv.Accounts {
		out = append(out, a.Zones...)
	}
	return out
}

func (inv *cfInventory) accountName(id string) string {
	for _, a := range inv.Accounts {
		if a.ID == id {
			return a.Name
		}
	}
	return id
}

func (s *Server) cfInvalidate(clusterID string) {
	s.cf.mu.Lock()
	defer s.cf.mu.Unlock()
	if s.cf.stale == nil {
		s.cf.stale = map[string]bool{}
	}
	s.cf.stale[clusterID] = true
}

// cfInventoryFor returns the inventory and whether a background refresh is
// running. It builds when there is none, refresh is set, or a change made it
// stale; an inventory past its TTL is returned at once and refreshed in the
// background. Cached inventories are never modified, only replaced.
func (s *Server) cfInventoryFor(ctx context.Context, clusterID string, refresh bool) (*cfInventory, bool) {
	s.cf.mu.Lock()
	if s.cf.inv == nil {
		s.cf.inv = map[string]*cfInventory{}
	}
	cur := s.cf.inv[clusterID]
	if s.cf.stale[clusterID] {
		refresh = true
	}
	s.cf.mu.Unlock()
	if cur != nil && !refresh {
		if time.Since(cur.FetchedAt) > cfInventoryTTL && s.cf.refreshMu.TryLock() {
			go func() {
				defer s.cf.refreshMu.Unlock()
				s.cfBuild(context.Background(), clusterID)
			}()
			return cur, true
		}
		return cur, false
	}
	s.cf.refreshMu.Lock()
	defer s.cf.refreshMu.Unlock()
	return s.cfBuild(ctx, clusterID), false
}

func (s *Server) cfBuild(ctx context.Context, clusterID string) *cfInventory {
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cancel()
	inv := &cfInventory{FetchedAt: s.now(), records: map[string][]cf.DNSRecord{}, Errors: []string{}}
	creds, _ := s.Store.ListCloudflareCredentials(bctx, clusterID)
	byAccount := map[string]*cfAccountView{}
	var order []string
	for _, c := range creds {
		for _, sc := range cfCredScopes(c) {
			a := byAccount[sc.Account.ID]
			if a == nil {
				a = &cfAccountView{ID: sc.Account.ID, Name: sc.Account.Name}
				byAccount[sc.Account.ID] = a
				order = append(order, sc.Account.ID)
			}
			a.Credentials = append(a.Credentials, cfCredRef{ID: c.ID, Name: c.Name, Status: c.Status})
			if len(a.Zones) == 0 {
				a.Zones = sc.Zones
			}
		}
	}
	for _, id := range order {
		a := byAccount[id]
		if zones, err := s.cfZones(bctx, clusterID, id); err == nil {
			a.Zones = zones
		} else if err != errCFNoCredential {
			a.Problems = append(a.Problems, "zones: "+err.Error())
		}
		var tunnels []cf.Tunnel
		err := s.cfWith(bctx, clusterID, id, "", func(c *cf.Client) error {
			var err error
			tunnels, err = c.Tunnels(bctx, id)
			return err
		})
		if err != nil {
			a.Problems = append(a.Problems, "tunnels: "+err.Error())
			inv.Errors = append(inv.Errors, a.Name+": "+err.Error())
		}
		for _, t := range tunnels {
			if t.TunType != "" && t.TunType != "cfd_tunnel" {
				continue
			}
			tv := &cfTunnelView{Tunnel: t, AccountName: a.Name, RemoteManaged: t.RemotelyManaged(), Connectors: []cf.Connector{}, Local: []cfLocalConnector{}}
			_ = s.cfWith(bctx, clusterID, id, "", func(c *cf.Client) error {
				conns, err := c.Connectors(bctx, id, t.ID)
				if err == nil {
					tv.Connectors, tv.connectorsRead = conns, true
				}
				return err
			})
			if tv.RemoteManaged {
				err := s.cfWith(bctx, clusterID, id, "", func(c *cf.Client) error {
					cfg, err := c.Config(bctx, id, t.ID)
					if err == nil {
						tv.config, tv.ConfigVersion = &cfg, cfg.Version
					}
					return err
				})
				if err != nil {
					tv.Error = "routes could not be read: " + err.Error()
				}
			}
			inv.Tunnels = append(inv.Tunnels, tv)
		}
		for _, z := range a.Zones {
			zid := z.ID
			err := s.cfWith(bctx, clusterID, id, zid, func(c *cf.Client) error {
				recs, err := c.DNSRecords(bctx, zid, "")
				if err == nil {
					inv.records[zid] = recs
				}
				return err
			})
			if err != nil && err != errCFNoCredential {
				a.Problems = append(a.Problems, "DNS for "+z.Name+": "+err.Error())
			}
		}
		inv.Accounts = append(inv.Accounts, *a)
	}
	s.cfAttachLocal(bctx, clusterID, inv)
	s.cfBuildRoutes(bctx, clusterID, inv)
	s.cfBuildDNS(inv)
	sort.Slice(inv.Accounts, func(i, j int) bool {
		return strings.ToLower(inv.Accounts[i].Name) < strings.ToLower(inv.Accounts[j].Name)
	})
	sort.Slice(inv.Tunnels, func(i, j int) bool {
		return strings.ToLower(inv.Tunnels[i].Name) < strings.ToLower(inv.Tunnels[j].Name)
	})
	s.cf.mu.Lock()
	s.cf.inv[clusterID] = inv
	delete(s.cf.stale, clusterID)
	s.cf.mu.Unlock()
	return inv
}

func (s *Server) cfZones(ctx context.Context, clusterID, accountID string) ([]cf.Zone, error) {
	var zones []cf.Zone
	err := s.cfWith(ctx, clusterID, accountID, "", func(c *cf.Client) error {
		var err error
		zones, err = c.Zones(ctx, accountID)
		return err
	})
	sort.Slice(zones, func(i, j int) bool { return zones[i].Name < zones[j].Name })
	return zones, err
}

// cfAttachLocal ties cloudflared services found on this node's machines to
// the tunnels they run.
func (s *Server) cfAttachLocal(ctx context.Context, clusterID string, inv *cfInventory) {
	local, _, err := s.dockerTunnels(ctx, clusterID, false)
	if err != nil {
		return
	}
	for _, l := range local {
		if l.TunnelID == "" {
			continue
		}
		if t := inv.tunnel(l.TunnelID); t != nil {
			t.Local = append(t.Local, cfLocalConnector{MachineID: l.MachineID, MachineName: l.MachineName, Unit: l.Unit, Active: l.Active, Managed: docker.ManagedConnectorUnit(l.Unit)})
		}
	}
}

// cfWorkloadIPs maps current IPv4 addresses to system containers.
type cfWorkloadIP struct {
	ID, Name, IP string
}

func (s *Server) cfWorkloadIPs(ctx context.Context, clusterID string) map[string]cfWorkloadIP {
	out := map[string]cfWorkloadIP{}
	wls, _ := s.Store.ListWorkloads(ctx, clusterID)
	for _, w := range wls {
		if w.Kind != lxc.KindSystemContainer {
			continue
		}
		if ip := s.workloadIPv4(ctx, clusterID, w.ID); ip != "" {
			out[w.ID] = cfWorkloadIP{ID: w.ID, Name: w.Name, IP: ip}
		}
	}
	return out
}

// workloadIPv4 is a system container's current IPv4 address, observed or static.
func (s *Server) workloadIPv4(ctx context.Context, clusterID, workloadID string) string {
	nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, workloadID)
	for _, n := range nics {
		for _, v := range []string{n.IPv4, n.IPv4Address} {
			v = strings.TrimSpace(v)
			if i := strings.IndexByte(v, '/'); i > 0 {
				v = v[:i]
			}
			if ip := net.ParseIP(v); ip != nil && ip.To4() != nil {
				return ip.String()
			}
		}
	}
	return ""
}

func cfRouteKey(tunnelID, hostname, path string) string {
	return strings.ToLower(tunnelID) + "|" + strings.ToLower(hostname) + "|" + path
}

func (s *Server) cfBuildRoutes(ctx context.Context, clusterID string, inv *cfInventory) {
	stored, _ := s.Store.ListCloudflareRoutes(ctx, clusterID)
	storedBy := map[string]appdb.CloudflareRoute{}
	for _, r := range stored {
		storedBy[cfRouteKey(r.TunnelID, r.Hostname, r.Path)] = r
	}
	ips := s.cfWorkloadIPs(ctx, clusterID)
	byIP := map[string]cfWorkloadIP{}
	for _, w := range ips {
		byIP[w.IP] = w
	}
	zones := inv.zones()
	seen := map[string]bool{}
	add := func(t *cfTunnelView, rule cf.IngressRule, enabled bool, row *appdb.CloudflareRoute) {
		key := cfRouteKey(t.ID, rule.Hostname, rule.Path)
		if seen[key] {
			return
		}
		seen[key] = true
		rv := cfRouteView{
			Key: key, Hostname: rule.Hostname, Path: rule.Path, Service: rule.Service, Options: rule.Options(), Enabled: enabled,
			Editable: t.RemoteManaged, AccountID: t.AccountTag, AccountName: t.AccountName, TunnelID: t.ID, TunnelName: t.Name, TunnelStatus: t.Status,
		}
		if ps, ok := cf.ParseService(rule.Service); ok {
			rv.Origin = &ps
		}
		if z, ok := cf.ZoneFor(rule.Hostname, zones); ok {
			rv.ZoneID, rv.ZoneName, rv.ZoneAccountID = z.ID, z.Name, z.Account.ID
			if recs, ok := inv.records[z.ID]; ok {
				plan := cf.PlanDNS(rule.Hostname, t.ID, recs)
				rv.DNS = &plan
			}
		}
		if row != nil && row.WorkloadID != "" {
			w := ips[row.WorkloadID]
			rv.Workload = &cfWorkloadRef{ID: row.WorkloadID, Name: w.Name, CurrentIP: w.IP, Linked: true}
			if w.IP != "" && rv.Origin != nil && rv.Origin.Host != w.IP && row.OriginIP != "" && rv.Origin.Host == row.OriginIP {
				rv.IPChanged = true
			}
		} else if rv.Origin != nil {
			if w, ok := byIP[rv.Origin.Host]; ok {
				rv.Workload = &cfWorkloadRef{ID: w.ID, Name: w.Name, CurrentIP: w.IP}
			}
		}
		inv.Routes = append(inv.Routes, rv)
	}
	for _, t := range inv.Tunnels {
		if t.config == nil {
			continue
		}
		for _, rule := range t.config.Config.Ingress {
			if rule.CatchAll() {
				continue
			}
			t.RouteCount++
			var row *appdb.CloudflareRoute
			if r, ok := storedBy[cfRouteKey(t.ID, rule.Hostname, rule.Path)]; ok {
				row = &r
			}
			add(t, rule, true, row)
		}
	}
	for _, r := range stored {
		if r.Enabled {
			continue
		}
		t := inv.tunnel(r.TunnelID)
		if t == nil {
			continue
		}
		var rule cf.IngressRule
		if json.Unmarshal(r.Rule, &rule) != nil || rule.Hostname == "" {
			continue
		}
		row := r
		add(t, rule, false, &row)
	}
	sort.Slice(inv.Routes, func(i, j int) bool {
		return inv.Routes[i].Hostname+inv.Routes[i].Path < inv.Routes[j].Hostname+inv.Routes[j].Path
	})
}

// cfBuildDNS lists tunnel CNAMEs and flags ones no route serves.
func (s *Server) cfBuildDNS(inv *cfInventory) {
	routed := map[string]bool{}
	for _, r := range inv.Routes {
		if r.Enabled {
			routed[strings.ToLower(r.TunnelID)+"|"+strings.ToLower(r.Hostname)] = true
		}
	}
	for _, a := range inv.Accounts {
		for _, z := range a.Zones {
			for _, rec := range inv.records[z.ID] {
				tid := cf.TunnelOfTarget(rec.Content)
				if !strings.EqualFold(rec.Type, "CNAME") || tid == "" {
					continue
				}
				v := cfDNSView{ZoneID: z.ID, ZoneName: z.Name, AccountID: a.ID, AccountName: a.Name, Record: rec, TunnelID: tid,
					Managed: strings.HasPrefix(rec.Comment, "Managed by No-dal")}
				switch t := inv.tunnel(tid); {
				case t == nil:
					v.Status = "unknown-tunnel"
				case routed[tid+"|"+strings.ToLower(rec.Name)]:
					v.Status, v.TunnelName = "routed", t.Name
				default:
					v.Status, v.TunnelName = "no-route", t.Name
				}
				inv.DNS = append(inv.DNS, v)
			}
		}
	}
	sort.Slice(inv.DNS, func(i, j int) bool { return inv.DNS[i].Record.Name < inv.DNS[j].Record.Name })
}

func (s *Server) getCloudflareInventory(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareRead)
	if err != nil {
		return
	}
	inv, refreshing := s.cfInventoryFor(r.Context(), p.User.ClusterID, r.URL.Query().Get("refresh") == "1")
	out := *inv
	out.Refreshing = refreshing
	writeJSON(w, http.StatusOK, out)
}
