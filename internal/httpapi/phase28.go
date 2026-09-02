package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

func (s *Server) wireguard() func(context.Context, ndnet.WGOp) (ndnet.WGResult, error) {
	if s.Network != nil {
		return s.Network.WireGuard
	}
	return func(context.Context, ndnet.WGOp) (ndnet.WGResult, error) {
		return ndnet.WGResult{Status: ndnet.StatusUnavailable, Reason: "network agent is unavailable"}, nil
	}
}

func (s *Server) listWG(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkRead)
	if err != nil {
		return
	}
	peers, err := s.Store.ListWGPeers(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	remotes, _ := s.Store.ListRemoteNodes(r.Context(), p.User.ClusterID)
	items := make([]map[string]any, 0, len(peers))
	for _, peer := range peers {
		items = append(items, wgPeerJSON(peer))
	}
	workers := make([]map[string]any, 0, len(remotes))
	now := s.now()
	for _, n := range remotes {
		workers = append(workers, remoteNodeJSON(n, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "nodes": workers, "join": "Use POST /api/v1/cluster/join-tokens then nodalctl cluster join. Pairing tokens are not join tokens.",
	})
}

func (s *Server) createWGPeer(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkCreate)
	if err != nil {
		return
	}
	var req struct {
		Name          string `json:"name"`
		Endpoint      string `json:"endpoint"`
		LocalAddress  string `json:"local_address"`
		WorkerAddress string `json:"worker_address"`
		ListenPort    int    `json:"listen_port"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := ndnet.ValidWGEndpoint(req.Endpoint); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	localAddr := firstNonEmpty(strings.TrimSpace(req.LocalAddress), "10.64.8.1/24")
	workerAddr := firstNonEmpty(strings.TrimSpace(req.WorkerAddress), "10.64.8.2/24")
	port := req.ListenPort
	if port == 0 {
		port = ndnet.DefaultWGPort
	}
	workerPriv, workerPub, err := ndnet.GenerateWGKey()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not generate worker key")
		return
	}
	token, err := secutil.RandomHex(32)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not generate pairing token")
		return
	}
	localID := uuid.NewString()
	workerID := uuid.NewString()
	res, err := s.wireguard()(r.Context(), ndnet.WGOp{
		Action: ndnet.ActionWGApply, PeerID: localID, ListenPort: uint32(port), AddressCIDR: localAddr,
		Peers: []ndnet.WGPeerSpec{{
			PublicKey: workerPub, AllowedIPs: workerAddr, Endpoint: strings.TrimSpace(req.Endpoint),
			PersistentKeepalive: ndnet.DefaultWGKeepalive,
		}},
	})
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	local := appdb.WGPeer{
		ID: localID, ClusterID: p.User.ClusterID, Name: name + "-local", Role: "local",
		PublicKey: res.PublicKey, ListenPort: port, AddressCIDR: localAddr,
		AllowedIPs: workerAddr, PersistentKeepalive: ndnet.DefaultWGKeepalive,
		IfaceName: res.Locator, PrivateKeyPath: res.PrivateKeyPath,
		LastHandshakeUnix: res.LastHandshakeUnix, Status: res.Status, Reason: res.Reason,
	}
	worker := appdb.WGPeer{
		ID: workerID, ClusterID: p.User.ClusterID, Name: name, Role: "worker",
		PublicKey: workerPub, ListenPort: port, AddressCIDR: workerAddr,
		Endpoint: strings.TrimSpace(req.Endpoint), AllowedIPs: localAddr,
		PersistentKeepalive: ndnet.DefaultWGKeepalive, PairingTokenHash: secutil.HashSHA256(token),
		Status: ndnet.StatusUnavailable, Reason: ndnet.WGSkipReason,
	}
	if err := s.Store.CreateWGPeer(r.Context(), local); err != nil {
		writeErr(w, http.StatusConflict, "could not record local peer")
		return
	}
	if err := s.Store.CreateWGPeer(r.Context(), worker); err != nil {
		writeErr(w, http.StatusConflict, "could not record worker peer")
		return
	}
	remote := appdb.RemoteNode{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, WGPeerID: workerID, Name: name,
		WGPublicKey: workerPub, Status: ndnet.NodeNotReady, Reason: ndnet.WGSkipReason,
	}
	if err := s.Store.CreateRemoteNode(r.Context(), remote); err != nil {
		writeErr(w, http.StatusConflict, "could not record remote node")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "cluster.wg.peer.create", "ok", workerID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": workerID, "name": name, "role": "worker",
		"local": wgPeerJSON(local), "worker": wgPeerJSON(worker),
		"node":               remoteNodeJSON(remote, s.now()),
		"worker_private_key": workerPriv, "pairing_token": token,
		"desired": map[string]any{
			"peer_id": workerID, "listen_port": port, "address_cidr": workerAddr,
			"control_url": firstNonEmpty(s.HTTPSURL, ""),
			"peers": []map[string]any{{
				"public_key": res.PublicKey, "allowed_ips": localAddr,
				"persistent_keepalive": ndnet.DefaultWGKeepalive,
			}},
		},
		"warning": "worker_private_key and pairing_token are shown once. Pairing tokens are not join tokens.",
	})
}

func (s *Server) openClusterSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerID        string `json:"peer_id"`
		NodeID        string `json:"node_id"`
		ClusterID     string `json:"cluster_id"`
		ListenAddr    string `json:"listen_addr"`
		WGPublicKey   string `json:"wg_public_key"`
		HandshakeUnix int64  `json:"handshake_unix"`
		PairingToken  string `json:"pairing_token"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if strings.TrimSpace(req.PeerID) == "" {
		writeErr(w, http.StatusUnauthorized, "peer id is required")
		return
	}
	cluster, err := s.Store.GetCluster(r.Context())
	if err != nil || cluster == nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return
	}
	peer, err := s.Store.GetWGPeer(r.Context(), cluster.ID, req.PeerID)
	if err != nil || peer == nil || peer.Role != "worker" {
		writeErr(w, http.StatusUnauthorized, "unknown peer")
		return
	}
	remote, err := s.Store.GetRemoteNodeByPeer(r.Context(), cluster.ID, peer.ID)
	if err != nil || remote == nil {
		writeErr(w, http.StatusNotFound, "remote node not found")
		return
	}
	token := strings.TrimSpace(req.PairingToken)
	pairingOK := token != "" && secutil.EqualHash(peer.PairingTokenHash, secutil.HashSHA256(token))
	if pairingOK && s.ClusterCA.PairingUsed(peer.ID) {
		writeErr(w, http.StatusUnauthorized, "pairing token already used")
		return
	}
	tlsOK := s.clusterClientCertOK(r, strings.TrimSpace(req.NodeID))
	established := remote.LastSeenAt != nil && !remote.LastSeenAt.IsZero()
	if !pairingOK && !tlsOK && !established {
		writeErr(w, http.StatusUnauthorized, "pairing token is invalid")
		return
	}
	if req.WGPublicKey != "" && peer.PublicKey != "" && req.WGPublicKey != peer.PublicKey {
		writeErr(w, http.StatusUnauthorized, "public key does not match peer")
		return
	}
	listen := strings.TrimSpace(req.ListenAddr)
	if err := ndnet.ValidListenAddr(listen); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	now := s.now()
	seen := now
	remote.ListenAddr = listen
	if req.WGPublicKey != "" {
		remote.WGPublicKey = req.WGPublicKey
	}
	remote.LastSeenAt = &seen
	remote.LastHandshakeUnix = req.HandshakeUnix
	if ndnet.RemoteReady(req.HandshakeUnix, now.Unix(), now.Unix()) {
		remote.Status = ndnet.NodeReady
		remote.Reason = ""
	} else {
		remote.Status = ndnet.NodeNotReady
		remote.Reason = ndnet.WGSkipReason
	}
	if err := s.Store.UpdateRemoteNodeSession(r.Context(), *remote); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sessID := uuid.NewString()
	_ = s.Store.CreateRemoteSession(r.Context(), appdb.RemoteSession{
		ID: sessID, ClusterID: cluster.ID, NodeID: remote.ID,
		ListenAddr: remote.ListenAddr, WGPublicKey: remote.WGPublicKey, LastSeenAt: now,
	})
	_ = s.Store.UpdateWGPeerObserved(r.Context(), appdb.WGPeer{
		ID: peer.ID, ClusterID: cluster.ID, PublicKey: peer.PublicKey, IfaceName: peer.IfaceName,
		PrivateKeyPath: peer.PrivateKeyPath, LastHandshakeUnix: req.HandshakeUnix,
		Status: firstNonEmpty(remote.Status, ndnet.StatusUnavailable), Reason: remote.Reason,
	})
	if pairingOK {
		_ = s.ClusterCA.MarkPairingUsed(peer.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessID, "accepted": true, "node_id": remote.ID,
		"status": remote.Status, "reason": remote.Reason,
	})
}

func (s *Server) clusterClientCertOK(r *http.Request, wantNodeID string) bool {
	if r == nil || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return false
	}
	leaf := r.TLS.PeerCertificates[0]
	raw := make([][]byte, 0, len(r.TLS.PeerCertificates))
	for _, cert := range r.TLS.PeerCertificates {
		if cert != nil {
			raw = append(raw, cert.Raw)
		}
	}
	if err := s.ClusterCA.VerifyClientCerts(raw); err != nil {
		return false
	}
	want := strings.TrimSpace(wantNodeID)
	if want == "" {
		return true
	}
	cn := leaf.Subject.CommonName
	if cn == want {
		return true
	}
	if len(want) > 64 && cn == want[:64] {
		return true
	}
	for _, uri := range leaf.URIs {
		if uri != nil && strings.HasSuffix(uri.String(), "/"+want) {
			return true
		}
	}
	return false
}

func wgPeerJSON(p appdb.WGPeer) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "role": p.Role, "public_key": p.PublicKey,
		"listen_port": p.ListenPort, "address_cidr": p.AddressCIDR, "endpoint": p.Endpoint,
		"allowed_ips": p.AllowedIPs, "persistent_keepalive": p.PersistentKeepalive,
		"locator": p.IfaceName, "status": p.Status, "reason": p.Reason,
		"last_handshake_unix": p.LastHandshakeUnix,
	}
}

func remoteNodeJSON(n appdb.RemoteNode, now time.Time) map[string]any {
	last := int64(0)
	lastSeen := ""
	if n.LastSeenAt != nil && !n.LastSeenAt.IsZero() {
		last = n.LastSeenAt.Unix()
		lastSeen = n.LastSeenAt.UTC().Format(time.RFC3339)
	}
	status, reason := n.Status, n.Reason
	if ndnet.RemoteReady(n.LastHandshakeUnix, last, now.Unix()) {
		status, reason = ndnet.NodeReady, ""
	} else {
		status = ndnet.NodeNotReady
		if reason == "" {
			reason = ndnet.WGSkipReason
		}
	}
	return map[string]any{
		"id": n.ID, "name": n.Name, "role": "worker", "status": status, "reason": reason,
		"listen_addr": n.ListenAddr, "wg_peer_id": n.WGPeerID, "wg_public_key": n.WGPublicKey,
		"last_seen_at": lastSeen, "last_handshake_unix": n.LastHandshakeUnix,
	}
}
