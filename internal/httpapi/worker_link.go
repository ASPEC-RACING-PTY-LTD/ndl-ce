package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/cluster"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// defaultWorkerAgentPort is the worker agent's mutual TLS port.
const defaultWorkerAgentPort = "9444"

var workerObserve sync.Mutex

// workerLinkResult is one check of a worker's agent link.
type workerLinkResult struct {
	Reachable    bool   `json:"reachable"`
	ListenAddr   string `json:"listen_addr"`
	AgentVersion string `json:"agent_version,omitempty"`
	Error        string `json:"error,omitempty"`
	CheckedAt    string `json:"checked_at"`
}

// checkWorkerLink dials a worker's agent over mutual TLS, records the
// outcome on its remote row and, when it answers, stores its inventory so
// the node shows its host, CPU and memory like the control node does.
func (s *Server) checkWorkerLink(ctx context.Context, node appdb.Node) workerLinkResult {
	now := time.Now().UTC()
	out := workerLinkResult{CheckedAt: now.Format(time.RFC3339)}
	remote, _ := s.Store.GetRemoteNode(ctx, node.ClusterID, node.ID)
	if remote == nil || strings.TrimSpace(remote.ListenAddr) == "" {
		out.Error = "no agent address is set for this node"
		return out
	}
	out.ListenAddr = remote.ListenAddr
	agent := s.workerAgent(node.ID, remote.ListenAddr)
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	version, err := agent.AgentVersion(cctx)
	if err != nil {
		out.Error = linkError(err)
		remote.Status = ndnet.NodeNotReady
		remote.Reason = out.Error
		_ = s.Store.UpdateRemoteNodeSession(ctx, *remote)
		_ = s.Store.MarkInventoryStale(ctx, node.ID)
		return out
	}
	out.Reachable = true
	out.AgentVersion = version
	remote.Status = ndnet.NodeReady
	remote.Reason = ""
	remote.LastSeenAt = &now
	remote.LastHandshakeUnix = now.Unix()
	_ = s.Store.UpdateRemoteNodeSession(ctx, *remote)
	if inv, oerr := agent.Observe(cctx); oerr == nil {
		if payload, merr := json.Marshal(inv); merr == nil {
			observed := inv.ObservedAt
			if observed.IsZero() {
				observed = now
			}
			_ = s.Store.UpsertInventory(ctx, appdb.HardwareInventory{
				NodeID: node.ID, ClusterID: node.ClusterID, Payload: payload, ObservedAt: observed,
			})
		}
	}
	return out
}

// linkError turns a dial or handshake failure into words an admin can act on.
func linkError(err error) string {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "connection refused"):
		return "connection refused: the agent is not listening. Restart ndl-agent on the node, or check the port"
	case strings.Contains(low, "i/o timeout"), strings.Contains(low, "deadline exceeded"), strings.Contains(low, "no route"):
		return "timed out: a firewall or NAT is blocking TCP " + defaultWorkerAgentPort + " to the node"
	case strings.Contains(low, "revoked"):
		return "the node's certificate is revoked"
	case strings.Contains(low, "certificate"), strings.Contains(low, "tls"):
		return "TLS handshake failed: the node does not hold this cluster's certificate (" + msg + ")"
	}
	return msg
}

// ObserveWorkers checks every joined worker's agent link. The control
// observe loop runs it, so worker status and inventory stay current.
func (s *Server) ObserveWorkers(ctx context.Context) {
	// A slow worker must not pile checks up behind it.
	if !workerObserve.TryLock() {
		return
	}
	defer workerObserve.Unlock()
	cl, err := s.Store.GetCluster(ctx)
	if err != nil || cl == nil {
		return
	}
	nodes, err := s.Store.ListClusterNodes(ctx, cl.ID)
	if err != nil {
		return
	}
	for _, n := range nodes {
		if n.RevokedAt != nil || n.Role != cluster.RoleWorker {
			continue
		}
		s.checkWorkerLink(ctx, n)
	}
}

// checkNode is POST /nodes/{id}/check: dial the worker agent now.
func (s *Server) checkNode(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	node, err := s.Store.GetNodeByID(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || node == nil || node.RevokedAt != nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	if node.Role != cluster.RoleWorker {
		writeErr(w, http.StatusUnprocessableEntity, "only worker nodes have an agent link to check")
		return
	}
	writeJSON(w, http.StatusOK, s.checkWorkerLink(r.Context(), *node))
}

// workerAgentAddr picks the address a new worker's agent is dialed on: the
// one it advertised, or the address its join request came from.
func workerAgentAddr(r *http.Request, advertised string) string {
	if a := strings.TrimSpace(advertised); a != "" {
		if _, _, err := net.SplitHostPort(a); err != nil {
			return net.JoinHostPort(a, defaultWorkerAgentPort)
		}
		return a
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	// A join relayed by a local proxy or tunnel says nothing about where the
	// worker is; the admin sets the address from the Cluster page instead.
	if ip := net.ParseIP(host); err != nil || ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	return net.JoinHostPort(host, defaultWorkerAgentPort)
}

// workerAgentJSON is a worker's agent link for the nodes list.
func workerAgentJSON(remote *appdb.RemoteNode, now time.Time) map[string]any {
	if remote == nil {
		return map[string]any{"listen_addr": "", "status": ndnet.NodeNotReady, "reason": "no agent address is set for this node"}
	}
	j := remoteNodeJSON(*remote, now)
	if j["reason"] == ndnet.WGSkipReason {
		j["reason"] = "the control plane has not reached this node's agent yet"
	}
	return map[string]any{
		"listen_addr": j["listen_addr"], "status": j["status"], "reason": j["reason"], "last_seen_at": j["last_seen_at"],
	}
}

// joinURL is the address a new worker posts its join to: the configured
// HTTPS URL, or the address the admin is using right now.
func (s *Server) joinURL(r *http.Request) string {
	if u := strings.TrimRight(strings.TrimSpace(s.HTTPSURL), "/"); u != "" {
		return u
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// controlFingerprint is the SHA-256 of the control plane's HTTPS
// certificate, or empty when it serves plain HTTP.
func (s *Server) controlFingerprint() string {
	mat, err := s.CertDir.Load()
	if err != nil {
		return ""
	}
	return mat.Fingerprint
}
