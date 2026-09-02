package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/cluster"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

const (
	joinTokenTTL    = 15 * time.Minute
	joinTokenMaxTTL = 24 * time.Hour
)

func (s *Server) requireWriter(w http.ResponseWriter, r *http.Request, clusterID string) bool {
	if s.LeaseHolder == "" {
		return true
	}
	lease, err := s.Store.GetClusterLease(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if lease == nil {
		return true
	}
	if s.now().After(lease.ExpiresAt) {
		return true
	}
	if lease.HolderID != s.LeaseHolder {
		writeErr(w, http.StatusConflict, "this process is not the cluster writer")
		return false
	}
	return true
}

func (s *Server) getCluster(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ClusterRead)
	if err != nil {
		return
	}
	cl, err := s.Store.GetCluster(r.Context())
	if err != nil || cl == nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}
	nodes, err := s.Store.ListClusterNodes(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	lease, _ := s.Store.GetClusterLease(r.Context(), p.User.ClusterID)
	redact := redactViewer(p)
	items := make([]map[string]any, 0, len(nodes))
	writer := true
	if lease != nil && s.LeaseHolder != "" && lease.HolderID != s.LeaseHolder && s.now().Before(lease.ExpiresAt) {
		writer = false
	}
	for _, n := range nodes {
		inv, _ := s.Store.GetInventory(r.Context(), n.ID)
		items = append(items, s.nodeSummary(&n, inv, redact))
	}
	body := map[string]any{
		"id":     cl.ID,
		"name":   cl.Name,
		"nodes":  items,
		"writer": writer,
	}
	if lease != nil {
		body["lease_holder"] = lease.HolderID
		body["lease_expires_at"] = lease.ExpiresAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) createJoinToken(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ClusterJoin)
	if err != nil {
		return
	}
	if !s.requireWriter(w, r, p.User.ClusterID) {
		return
	}
	var req struct {
		TTLSeconds int `json:"ttl_seconds"`
	}
	_ = readJSON(r, &req)
	ttl := joinTokenTTL
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	if ttl > joinTokenMaxTTL {
		ttl = joinTokenMaxTTL
	}
	raw, err := secutil.RandomHex(32)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not generate join token")
		return
	}
	now := s.now()
	row := appdb.JoinToken{
		ID:        uuid.NewString(),
		ClusterID: p.User.ClusterID,
		TokenHash: secutil.HashSHA256(raw),
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
	}
	if err := s.Store.CreateJoinToken(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record join token")
		return
	}
	if err := s.ClusterCA.Ensure(now); err != nil {
		writeErr(w, http.StatusInternalServerError, "cluster CA is unavailable")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "cluster.join_token.create", "ok", row.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         row.ID,
		"token":      raw,
		"expires_at": row.ExpiresAt.UTC().Format(time.RFC3339),
		"warning":    "token is shown once. Pairing tokens are not join tokens.",
	})
}

func (s *Server) joinCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token        string          `json:"token"`
		Hostname     string          `json:"hostname"`
		HostPlatform json.RawMessage `json:"host_platform"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	token := strings.TrimSpace(req.Token)
	hostname := strings.TrimSpace(req.Hostname)
	if token == "" || hostname == "" {
		writeErr(w, http.StatusBadRequest, "token and hostname are required")
		return
	}
	cl, err := s.Store.GetCluster(r.Context())
	if err != nil || cl == nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}
	if !s.requireWriter(w, r, cl.ID) {
		return
	}
	hash := secutil.HashSHA256(token)
	existing, err := s.Store.GetJoinTokenByHash(r.Context(), hash)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil || existing.ConsumedAt != nil || !s.now().Before(existing.ExpiresAt) {
		if existing != nil && existing.ConsumedAt != nil {
			writeErr(w, http.StatusConflict, "join token already used")
			return
		}
		writeErr(w, http.StatusUnauthorized, "join token is invalid")
		return
	}
	nodes, _ := s.Store.ListClusterNodes(r.Context(), cl.ID)
	taken := map[string]struct{}{}
	for _, n := range nodes {
		taken[n.Name] = struct{}{}
	}
	nodeID := uuid.NewString()
	name := cluster.UniqueNodeName(hostname, nodeID, taken)
	plat := req.HostPlatform
	if len(plat) == 0 {
		plat = json.RawMessage(`{}`)
	}
	node := appdb.Node{
		ID:           nodeID,
		ClusterID:    cl.ID,
		Name:         name,
		Hostname:     hostname,
		Role:         cluster.RoleWorker,
		HostPlatform: plat,
	}
	if err := s.Store.UpsertNode(r.Context(), node); err != nil {
		writeErr(w, http.StatusConflict, "could not record worker node")
		return
	}
	consumed, err := s.Store.ConsumeJoinToken(r.Context(), hash, nodeID, s.now())
	if err != nil {
		_ = s.Store.RevokeNode(r.Context(), cl.ID, nodeID, s.now())
		if errors.Is(err, appdb.ErrJoinTokenUsed) {
			writeErr(w, http.StatusConflict, "join token already used")
			return
		}
		writeErr(w, http.StatusUnauthorized, "join token is invalid")
		return
	}
	certPEM, keyPEM, err := s.ClusterCA.IssueNode(nodeID, s.now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not issue node certificate")
		return
	}
	caPEM, _ := s.ClusterCA.CertPEM()
	s.audit(r, cl.ID, "", "cluster.join", "ok", nodeID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         node.ID,
		"cluster_id": cl.ID,
		"name":       node.Name,
		"hostname":   node.Hostname,
		"role":       node.Role,
		"token_id":   consumed.ID,
		"ca_cert":    string(caPEM),
		"node_cert":  string(certPEM),
		"node_key":   string(keyPEM),
		"warning":    "node_key is shown once. Hostname is a locator; identity is the node UUID.",
	})
}

func (s *Server) revokeClusterNode(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRevoke)
	if err != nil {
		return
	}
	if !s.requireWriter(w, r, p.User.ClusterID) {
		return
	}
	id := r.PathValue("id")
	node, err := s.Store.GetNodeByID(r.Context(), p.User.ClusterID, id)
	if err != nil || node == nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	if node.Role == "" || node.Role == cluster.RoleControl {
		writeErr(w, http.StatusConflict, "the control node cannot be revoked")
		return
	}
	if err := s.Store.RevokeNode(r.Context(), p.User.ClusterID, id, s.now()); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	_ = s.ClusterCA.RevokeNode(id)
	s.audit(r, p.User.ClusterID, p.User.ID, "cluster.node.revoke", "ok", id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "revoked": true})
}
