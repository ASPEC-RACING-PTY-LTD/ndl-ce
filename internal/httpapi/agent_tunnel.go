package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	cf "github.com/no-dal/ndl-ce/internal/cloudflare"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

// Agent tunnels let the control node reach a worker that cannot take
// inbound connections (CGNAT, a friend's network): the worker runs a
// cloudflared connector whose TCP route sends a hostname to its agent port,
// and the worker's agent address becomes cloudflare:<hostname>. Mutual TLS
// still runs end to end inside the tunnel.

const agentTunnelTicketTTL = 30 * time.Minute

type agentTunnelTicket struct {
	clusterID string
	accountID string
	tunnelID  string
	hostname  string
	expires   time.Time
}

var (
	agentAccessCache   struct{ id, secret string }
	agentAccessCacheAt time.Time
	agentAccessMu      sync.Mutex
)

// agentTunnelAccess is the Access service token saved under join settings,
// reused for tunnels to workers. It is cached for a minute.
func (s *Server) agentTunnelAccess() (string, string) {
	agentAccessMu.Lock()
	defer agentAccessMu.Unlock()
	if time.Since(agentAccessCacheAt) < time.Minute {
		return agentAccessCache.id, agentAccessCache.secret
	}
	agentAccessCacheAt = time.Now()
	agentAccessCache.id, agentAccessCache.secret = "", ""
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cl, err := s.Store.GetCluster(ctx)
	if err != nil || cl == nil {
		return "", ""
	}
	if st, secret := s.joinAccess(ctx, cl.ID); st != nil && secret != "" {
		agentAccessCache.id, agentAccessCache.secret = st.AccessClientID, secret
	}
	return agentAccessCache.id, agentAccessCache.secret
}

func agentTunnelName(hostname string) string {
	name := "ndl-agent-" + strings.ReplaceAll(hostname, ".", "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// createAgentTunnel is POST /cluster/agent-tunnels: it makes (or reuses) a
// tunnel for one worker, routes hostname to tcp://localhost:9444 with its
// DNS record, and returns the agent address plus a one-time command that
// installs the connector on the worker. The connector token itself never
// reaches the browser; the worker fetches it with the command.
func (s *Server) createAgentTunnel(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	if !rbac.Authorize(p.Grants, rbac.NodeUpdate) && !rbac.Authorize(p.Grants, rbac.ClusterJoin) {
		writeErr(w, http.StatusForbidden, "setting up a node's tunnel needs node.update or cluster.join")
		return
	}
	var in struct {
		AccountID string `json:"account_id"`
		Hostname  string `json:"hostname"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !cfAccountIDRe.MatchString(in.AccountID) {
		writeErr(w, http.StatusBadRequest, "choose a Cloudflare account")
		return
	}
	host, err := cf.NormalizeHostname(in.Hostname)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	name := agentTunnelName(host)
	inv, _ := s.cfInventoryFor(ctx, clusterID, true)
	tunnelID := ""
	for _, t := range inv.Tunnels {
		if t.AccountTag == in.AccountID && t.Name == name {
			tunnelID = t.ID
		}
	}
	if tunnelID == "" {
		var t cf.Tunnel
		err = s.cfWith(ctx, clusterID, in.AccountID, "", func(c *cf.Client) error {
			var err error
			t, err = c.CreateTunnel(ctx, in.AccountID, name)
			return err
		})
		if err != nil {
			cfWriteErr(w, err)
			return
		}
		tunnelID = t.ID
		s.cfInvalidate(clusterID)
		s.audit(r, clusterID, p.User.ID, "cloudflare.tunnel.create", "ok", name+" "+t.ID)
	}
	route := cfRouteIn{cfRouteRef: cfRouteRef{AccountID: in.AccountID, TunnelID: tunnelID, Hostname: host}, Protocol: "tcp", Host: "localhost", Port: 9444}
	res, err := s.cfUpsertRoute(ctx, clusterID, p.User.Username, route)
	if err != nil {
		cfFail(w, err)
		return
	}
	ticket, err := secutil.RandomHex(24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	expires := s.now().Add(agentTunnelTicketTTL)
	s.agentTunnelTickets.Store(ticket, agentTunnelTicket{clusterID: clusterID, accountID: in.AccountID, tunnelID: tunnelID, hostname: host, expires: expires})
	base := s.joinURL(r)
	header := ""
	if st, secret := s.joinAccess(ctx, clusterID); st != nil {
		if st.JoinURL != "" {
			base = strings.TrimRight(st.JoinURL, "/")
		}
		if secret != "" {
			header = " -H 'CF-Access-Client-Id: " + st.AccessClientID + "' -H 'CF-Access-Client-Secret: " + secret + "'"
		}
	}
	s.audit(r, clusterID, p.User.ID, "cluster.agent_tunnel.create", "ok", host+" "+tunnelID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"address":         ndnet.CloudflareAgentPrefix + host,
		"hostname":        host,
		"tunnel_id":       tunnelID,
		"tunnel_name":     name,
		"dns_action":      res.DNSAction,
		"warnings":        res.Warnings,
		"install_command": "curl -fsSL" + header + " '" + base + "/api/v1/agent-tunnel-install/" + ticket + "' | sudo sh",
		"expires_at":      expires.UTC().Format(time.RFC3339),
	})
}

var tunnelTokenRe = regexp.MustCompile(`^[A-Za-z0-9+/=_-]+$`)

// agentTunnelInstall is GET /agent-tunnel-install/{ticket}: the one-time
// link a worker runs to install its connector. The ticket is the credential.
func (s *Server) agentTunnelInstall(w http.ResponseWriter, r *http.Request) {
	v, ok := s.agentTunnelTickets.LoadAndDelete(r.PathValue("ticket"))
	t, _ := v.(agentTunnelTicket)
	if !ok || s.now().After(t.expires) {
		writeErr(w, http.StatusNotFound, "this install link was used or has expired; set up the tunnel again from the Cluster page")
		return
	}
	var tok string
	err := s.cfWith(r.Context(), t.clusterID, t.accountID, "", func(c *cf.Client) error {
		var err error
		tok, err = c.TunnelToken(r.Context(), t.accountID, t.tunnelID)
		return err
	})
	if err != nil || len(tok) < 20 || len(tok) > 4096 || !tunnelTokenRe.MatchString(tok) {
		writeErr(w, http.StatusBadGateway, "could not get the tunnel's connector token from Cloudflare")
		return
	}
	s.audit(r, t.clusterID, "", "cluster.agent_tunnel.install", "ok", t.hostname)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(agentTunnelScript(tok, t.hostname)))
}

func agentTunnelScript(token, hostname string) string {
	return `#!/bin/sh
# Installs the Cloudflare connector that lets No-dal reach this node's agent
# at ` + hostname + `. Generated once; the link no longer works.
set -eu
if [ "$(id -u)" != 0 ]; then echo "Run this as root (| sudo sh)." >&2; exit 1; fi
BIN=$(command -v cloudflared || true)
if [ -z "$BIN" ]; then
  echo "Installing cloudflared from Cloudflare's package repository..."
  apt-get update -qq
  apt-get install -y -qq curl ca-certificates >/dev/null
  mkdir -p -m 0755 /usr/share/keyrings
  curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg -o /usr/share/keyrings/cloudflare-main.gpg
  echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main" > /etc/apt/sources.list.d/cloudflared.list
  apt-get update -qq
  apt-get install -y -qq cloudflared
  BIN=$(command -v cloudflared)
fi
mkdir -p -m 0700 /etc/cloudflared
umask 077
cat > /etc/cloudflared/ndl-agent.env <<'NDL_EOF'
TUNNEL_TOKEN=` + token + `
NDL_EOF
umask 022
cat > /etc/systemd/system/cloudflared-ndl-agent.service <<NDL_EOF
[Unit]
Description=Cloudflare Tunnel for the No-dal agent (managed by No-dal)
After=network-online.target ndl-agent.socket
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/cloudflared/ndl-agent.env
ExecStart=$BIN --no-autoupdate tunnel run
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
NDL_EOF
systemctl daemon-reload
systemctl enable cloudflared-ndl-agent.service >/dev/null 2>&1
systemctl restart cloudflared-ndl-agent.service
echo "Connector running. The control node reaches this agent at ` + hostname + `."
`
}
