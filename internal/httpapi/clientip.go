package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/features"
)

// Client addresses.
//
// Proxy headers (CF-Connecting-IP, X-Forwarded-For, X-Real-IP) are believed
// only when the TCP peer is a trusted proxy. The proxy in front of ndl-ce is
// trusted without setup: a cloudflared connector No-dal can see (on the host
// or in a system container), or a Caddy or Nginx reverse proxy on this host.
// Admins can add more by hand and turn a detected one off. A request from
// anywhere else is identified by its TCP peer and its headers are ignored.

// trustedProxy is one effective entry: a range and the header it sets.
type trustedProxy struct {
	ID     string `json:"id"`
	CIDR   string `json:"cidr"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Note   string `json:"note,omitempty"`
	prefix netip.Prefix
}

type trustedProxyCache struct {
	mu         sync.Mutex
	at         time.Time
	detected   []trustedProxy
	refreshing bool
	// stored mirrors the trusted_proxies rows; cleared on every change.
	stored   []appdb.TrustedProxy
	storedAt time.Time
	cluster  string
}

const trustedProxyTTL = 60 * time.Second

func loopbackProxies(id, kind, source string) []trustedProxy {
	return []trustedProxy{
		{ID: id + ":v4", CIDR: "127.0.0.0/8", Kind: kind, Source: source},
		{ID: id + ":v6", CIDR: "::1/128", Kind: kind, Source: source},
	}
}

// detectProxies works out what sits in front of ndl-ce from the features
// that are on and the cloudflared connectors No-dal can see.
func (s *Server) detectProxies(ctx context.Context, clusterID string, withTunnels bool) []trustedProxy {
	on := func(id string) bool {
		row, _ := s.Store.GetFeature(ctx, clusterID, id)
		return row != nil && row.Enabled
	}
	var out []trustedProxy
	if on(features.IDCloudflare) {
		out = append(out, loopbackProxies("auto:cloudflared-host", appdb.ProxyKindCloudflared, "Cloudflare tunnel on this host")...)
		if withTunnels {
			tunnels, _, _ := s.dockerTunnels(ctx, clusterID, false)
			seen := map[string]bool{}
			for _, t := range tunnels {
				if !t.Active || t.MachineID == "" || t.MachineID == docker.HostMachineID || seen[t.MachineID] {
					continue
				}
				seen[t.MachineID] = true
				if ip := s.workloadIPv4(ctx, clusterID, t.MachineID); ip != "" {
					out = append(out, trustedProxy{
						ID: "auto:cloudflared:" + t.MachineID, CIDR: ip + "/32", Kind: appdb.ProxyKindCloudflared,
						Source: "Cloudflare tunnel in " + firstNonEmpty(t.MachineName, t.MachineID),
					})
				}
			}
		}
	}
	if on(features.IDCaddy) {
		out = append(out, loopbackProxies("auto:caddy", appdb.ProxyKindGeneric, "Caddy on this host")...)
	}
	if on(features.IDNginx) {
		out = append(out, loopbackProxies("auto:nginx", appdb.ProxyKindGeneric, "Nginx on this host")...)
	}
	for i := range out {
		out[i].prefix, _ = netip.ParsePrefix(out[i].CIDR)
	}
	return out
}

// detectedProxies returns the cached detection, refreshing it in the
// background so a request never waits on the Docker agent.
func (s *Server) detectedProxies(ctx context.Context, clusterID string) []trustedProxy {
	s.trusted.mu.Lock()
	fresh := !s.trusted.at.IsZero() && time.Since(s.trusted.at) < trustedProxyTTL
	items := s.trusted.detected
	first := s.trusted.at.IsZero()
	start := !fresh && !s.trusted.refreshing
	if start {
		s.trusted.refreshing = true
	}
	s.trusted.mu.Unlock()
	if first {
		// The first call answers from feature switches alone.
		items = s.detectProxies(ctx, clusterID, false)
	}
	if start {
		go func() {
			bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
			defer cancel()
			next := s.detectProxies(bg, clusterID, true)
			s.trusted.mu.Lock()
			s.trusted.detected, s.trusted.at, s.trusted.refreshing = next, time.Now(), false
			s.trusted.mu.Unlock()
		}()
	}
	return items
}

// invalidateProxies makes the next request re-detect proxies.
func (s *Server) invalidateProxies() {
	s.trusted.mu.Lock()
	s.trusted.at = time.Time{}
	s.trusted.storedAt = time.Time{}
	s.trusted.mu.Unlock()
}

func (s *Server) storedProxies(ctx context.Context, clusterID string) []appdb.TrustedProxy {
	s.trusted.mu.Lock()
	if !s.trusted.storedAt.IsZero() && time.Since(s.trusted.storedAt) < trustedProxyTTL {
		out := s.trusted.stored
		s.trusted.mu.Unlock()
		return out
	}
	s.trusted.mu.Unlock()
	rows, err := s.Store.ListTrustedProxies(ctx, clusterID)
	if err != nil {
		return nil
	}
	s.trusted.mu.Lock()
	s.trusted.stored, s.trusted.storedAt = rows, time.Now()
	s.trusted.mu.Unlock()
	return rows
}

// clusterIDCached is the appliance's cluster id, read once.
func (s *Server) clusterIDCached(ctx context.Context) string {
	s.trusted.mu.Lock()
	id := s.trusted.cluster
	s.trusted.mu.Unlock()
	if id != "" {
		return id
	}
	if c, err := s.Store.GetCluster(ctx); err == nil && c != nil {
		s.trusted.mu.Lock()
		s.trusted.cluster = c.ID
		s.trusted.mu.Unlock()
		return c.ID
	}
	return ""
}

// effectiveProxies is detected minus the ones an admin turned off, plus
// manual entries.
func (s *Server) effectiveProxies(ctx context.Context, clusterID string) []trustedProxy {
	stored := s.storedProxies(ctx, clusterID)
	off := map[string]bool{}
	var out []trustedProxy
	for _, t := range stored {
		if t.Detected {
			if t.Disabled {
				off[t.ID] = true
			}
			continue
		}
		if t.Disabled {
			continue
		}
		prefix, err := netip.ParsePrefix(t.CIDR)
		if err != nil {
			continue
		}
		out = append(out, trustedProxy{ID: t.ID, CIDR: t.CIDR, Kind: t.Kind, Source: "Added by an admin", Note: t.Note, prefix: prefix})
	}
	for _, t := range s.detectedProxies(ctx, clusterID) {
		if !off[t.ID] && t.prefix.IsValid() {
			out = append(out, t)
		}
	}
	return out
}

func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func parseIP(v string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(v))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// clientAddr is the address ndl-ce sees for a request and how it got it.
type clientAddr struct {
	IP    netip.Addr
	Proxy *trustedProxy
}

func matchProxy(list []trustedProxy, addr netip.Addr) *trustedProxy {
	for i := range list {
		if list[i].prefix.Contains(addr) {
			return &list[i]
		}
	}
	return nil
}

// resolveClient returns the real client address of r.
func (s *Server) resolveClient(r *http.Request) clientAddr {
	peer := peerAddr(r)
	out := clientAddr{IP: peer}
	if !peer.IsValid() {
		return out
	}
	clusterID := s.clusterIDCached(r.Context())
	if clusterID == "" {
		return out
	}
	list := s.effectiveProxies(r.Context(), clusterID)
	proxy := matchProxy(list, peer)
	if proxy == nil {
		return out
	}
	if proxy.Kind == appdb.ProxyKindCloudflared {
		if ip, ok := parseIP(r.Header.Get("CF-Connecting-IP")); ok {
			return clientAddr{IP: ip, Proxy: proxy}
		}
	}
	// X-Forwarded-For: walk from the right, skipping trusted hops.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			ip, ok := parseIP(parts[i])
			if !ok {
				break
			}
			if matchProxy(list, ip) == nil || i == 0 {
				return clientAddr{IP: ip, Proxy: proxy}
			}
		}
	}
	if ip, ok := parseIP(r.Header.Get("X-Real-IP")); ok {
		return clientAddr{IP: ip, Proxy: proxy}
	}
	return out
}

// clientIP is the real client address of r as text, for logs and checks.
func (s *Server) clientIP(r *http.Request) string {
	c := s.resolveClient(r)
	if !c.IP.IsValid() {
		return r.RemoteAddr
	}
	return c.IP.String()
}
