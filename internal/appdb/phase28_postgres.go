package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (p *Postgres) CreateWGPeer(ctx context.Context, peer WGPeer) error {
	if peer.CreatedAt.IsZero() {
		peer.CreatedAt = time.Now().UTC()
	}
	if peer.UpdatedAt.IsZero() {
		peer.UpdatedAt = peer.CreatedAt
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO wg_peers (id, cluster_id, node_id, name, role, public_key, listen_port, address_cidr, endpoint, allowed_ips, persistent_keepalive, iface_name, private_key_path, pairing_token_hash, last_handshake_unix, status, reason, created_at, updated_at)
VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		peer.ID, peer.ClusterID, peer.NodeID, peer.Name, peer.Role, peer.PublicKey, peer.ListenPort, peer.AddressCIDR,
		peer.Endpoint, peer.AllowedIPs, peer.PersistentKeepalive, peer.IfaceName, peer.PrivateKeyPath, peer.PairingTokenHash,
		peer.LastHandshakeUnix, peer.Status, peer.Reason, peer.CreatedAt, peer.UpdatedAt)
	return err
}

func (p *Postgres) ListWGPeers(ctx context.Context, clusterID string) ([]WGPeer, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(node_id::text,''), name, role, public_key, listen_port, address_cidr, endpoint, allowed_ips, persistent_keepalive, iface_name, private_key_path, pairing_token_hash, last_handshake_unix, status, reason, created_at, updated_at
FROM wg_peers WHERE cluster_id=$1 ORDER BY created_at, id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WGPeer
	for rows.Next() {
		var peer WGPeer
		if err := rows.Scan(&peer.ID, &peer.ClusterID, &peer.NodeID, &peer.Name, &peer.Role, &peer.PublicKey, &peer.ListenPort, &peer.AddressCIDR, &peer.Endpoint, &peer.AllowedIPs, &peer.PersistentKeepalive, &peer.IfaceName, &peer.PrivateKeyPath, &peer.PairingTokenHash, &peer.LastHandshakeUnix, &peer.Status, &peer.Reason, &peer.CreatedAt, &peer.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, peer)
	}
	return out, rows.Err()
}

func (p *Postgres) GetWGPeer(ctx context.Context, clusterID, id string) (*WGPeer, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(node_id::text,''), name, role, public_key, listen_port, address_cidr, endpoint, allowed_ips, persistent_keepalive, iface_name, private_key_path, pairing_token_hash, last_handshake_unix, status, reason, created_at, updated_at
FROM wg_peers WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var peer WGPeer
	if err := row.Scan(&peer.ID, &peer.ClusterID, &peer.NodeID, &peer.Name, &peer.Role, &peer.PublicKey, &peer.ListenPort, &peer.AddressCIDR, &peer.Endpoint, &peer.AllowedIPs, &peer.PersistentKeepalive, &peer.IfaceName, &peer.PrivateKeyPath, &peer.PairingTokenHash, &peer.LastHandshakeUnix, &peer.Status, &peer.Reason, &peer.CreatedAt, &peer.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &peer, nil
}

func (p *Postgres) UpdateWGPeerObserved(ctx context.Context, peer WGPeer) error {
	_, err := p.DB.ExecContext(ctx, `
UPDATE wg_peers SET public_key=$3, iface_name=$4, private_key_path=$5, last_handshake_unix=$6, status=$7, reason=$8, updated_at=now()
WHERE cluster_id=$1 AND id=$2`, peer.ClusterID, peer.ID, peer.PublicKey, peer.IfaceName, peer.PrivateKeyPath, peer.LastHandshakeUnix, peer.Status, peer.Reason)
	return err
}

func (p *Postgres) CreateRemoteNode(ctx context.Context, n RemoteNode) error {
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	if n.UpdatedAt.IsZero() {
		n.UpdatedAt = n.CreatedAt
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO remote_nodes (id, cluster_id, wg_peer_id, name, listen_addr, wg_public_key, status, reason, last_seen_at, last_handshake_unix, created_at, updated_at)
VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		n.ID, n.ClusterID, n.WGPeerID, n.Name, n.ListenAddr, n.WGPublicKey, n.Status, n.Reason, n.LastSeenAt, n.LastHandshakeUnix, n.CreatedAt, n.UpdatedAt)
	return err
}

func (p *Postgres) ListRemoteNodes(ctx context.Context, clusterID string) ([]RemoteNode, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(wg_peer_id::text,''), name, listen_addr, wg_public_key, status, reason, last_seen_at, last_handshake_unix, created_at, updated_at
FROM remote_nodes WHERE cluster_id=$1 ORDER BY created_at, id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RemoteNode
	for rows.Next() {
		var n RemoteNode
		if err := rows.Scan(&n.ID, &n.ClusterID, &n.WGPeerID, &n.Name, &n.ListenAddr, &n.WGPublicKey, &n.Status, &n.Reason, &n.LastSeenAt, &n.LastHandshakeUnix, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (p *Postgres) GetRemoteNode(ctx context.Context, clusterID, id string) (*RemoteNode, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(wg_peer_id::text,''), name, listen_addr, wg_public_key, status, reason, last_seen_at, last_handshake_unix, created_at, updated_at
FROM remote_nodes WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var n RemoteNode
	if err := row.Scan(&n.ID, &n.ClusterID, &n.WGPeerID, &n.Name, &n.ListenAddr, &n.WGPublicKey, &n.Status, &n.Reason, &n.LastSeenAt, &n.LastHandshakeUnix, &n.CreatedAt, &n.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &n, nil
}

func (p *Postgres) GetRemoteNodeByPeer(ctx context.Context, clusterID, peerID string) (*RemoteNode, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(wg_peer_id::text,''), name, listen_addr, wg_public_key, status, reason, last_seen_at, last_handshake_unix, created_at, updated_at
FROM remote_nodes WHERE cluster_id=$1 AND wg_peer_id=$2 LIMIT 1`, clusterID, peerID)
	var n RemoteNode
	if err := row.Scan(&n.ID, &n.ClusterID, &n.WGPeerID, &n.Name, &n.ListenAddr, &n.WGPublicKey, &n.Status, &n.Reason, &n.LastSeenAt, &n.LastHandshakeUnix, &n.CreatedAt, &n.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &n, nil
}

func (p *Postgres) UpdateRemoteNodeSession(ctx context.Context, n RemoteNode) error {
	_, err := p.DB.ExecContext(ctx, `
UPDATE remote_nodes SET listen_addr=$3, wg_public_key=$4, status=$5, reason=$6, last_seen_at=$7, last_handshake_unix=$8, updated_at=now()
WHERE cluster_id=$1 AND id=$2`, n.ClusterID, n.ID, n.ListenAddr, n.WGPublicKey, n.Status, n.Reason, n.LastSeenAt, n.LastHandshakeUnix)
	return err
}

func (p *Postgres) CreateRemoteSession(ctx context.Context, s RemoteSession) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.LastSeenAt.IsZero() {
		s.LastSeenAt = s.CreatedAt
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO remote_sessions (id, cluster_id, node_id, listen_addr, wg_public_key, last_seen_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)`, s.ID, s.ClusterID, s.NodeID, s.ListenAddr, s.WGPublicKey, s.LastSeenAt, s.CreatedAt)
	return err
}
