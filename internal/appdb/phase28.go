package appdb

import (
	"context"
	"time"
)

// WGPeer is a pre-join WireGuard peer. PublicKey is stored. Private keys are not.
type WGPeer struct {
	ID                  string
	ClusterID           string
	NodeID              string
	Name                string
	Role                string
	PublicKey           string
	ListenPort          int
	AddressCIDR         string
	Endpoint            string
	AllowedIPs          string
	PersistentKeepalive int
	IfaceName           string
	PrivateKeyPath      string
	PairingTokenHash    string
	LastHandshakeUnix   int64
	Status              string
	Reason              string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// RemoteNode is a worker reached over WireGuard. It is not a cluster join.
type RemoteNode struct {
	ID                string
	ClusterID         string
	WGPeerID          string
	Name              string
	ListenAddr        string
	WGPublicKey       string
	Status            string
	Reason            string
	LastSeenAt        *time.Time
	LastHandshakeUnix int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// RemoteSession is an OpenSession bind from a worker agent.
type RemoteSession struct {
	ID          string
	ClusterID   string
	NodeID      string
	ListenAddr  string
	WGPublicKey string
	LastSeenAt  time.Time
	CreatedAt   time.Time
}

func (m *Memory) CreateWGPeer(_ context.Context, p WGPeer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.wgPeers == nil {
		m.wgPeers = map[string]WGPeer{}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	m.wgPeers[p.ID] = p
	return nil
}

func (m *Memory) ListWGPeers(_ context.Context, clusterID string) ([]WGPeer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []WGPeer
	for _, p := range m.wgPeers {
		if p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *Memory) GetWGPeer(_ context.Context, clusterID, id string) (*WGPeer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.wgPeers[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (m *Memory) UpdateWGPeerObserved(_ context.Context, p WGPeer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.wgPeers[p.ID]
	if !ok || cur.ClusterID != p.ClusterID {
		return nil
	}
	cur.PublicKey = p.PublicKey
	cur.IfaceName = p.IfaceName
	cur.PrivateKeyPath = p.PrivateKeyPath
	cur.LastHandshakeUnix = p.LastHandshakeUnix
	cur.Status = p.Status
	cur.Reason = p.Reason
	cur.UpdatedAt = time.Now().UTC()
	m.wgPeers[p.ID] = cur
	return nil
}

func (m *Memory) CreateRemoteNode(_ context.Context, n RemoteNode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.remoteNodes == nil {
		m.remoteNodes = map[string]RemoteNode{}
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	if n.UpdatedAt.IsZero() {
		n.UpdatedAt = n.CreatedAt
	}
	m.remoteNodes[n.ID] = n
	return nil
}

func (m *Memory) ListRemoteNodes(_ context.Context, clusterID string) ([]RemoteNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RemoteNode
	for _, n := range m.remoteNodes {
		if n.ClusterID == clusterID {
			out = append(out, n)
		}
	}
	return out, nil
}

func (m *Memory) GetRemoteNode(_ context.Context, clusterID, id string) (*RemoteNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.remoteNodes[id]
	if !ok || n.ClusterID != clusterID {
		return nil, nil
	}
	cp := n
	return &cp, nil
}

func (m *Memory) GetRemoteNodeByPeer(_ context.Context, clusterID, peerID string) (*RemoteNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.remoteNodes {
		if n.ClusterID == clusterID && n.WGPeerID == peerID {
			cp := n
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) UpdateRemoteNodeSession(_ context.Context, n RemoteNode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.remoteNodes[n.ID]
	if !ok || cur.ClusterID != n.ClusterID {
		return nil
	}
	cur.ListenAddr = n.ListenAddr
	cur.WGPublicKey = n.WGPublicKey
	cur.Status = n.Status
	cur.Reason = n.Reason
	cur.LastSeenAt = n.LastSeenAt
	cur.LastHandshakeUnix = n.LastHandshakeUnix
	cur.UpdatedAt = time.Now().UTC()
	m.remoteNodes[n.ID] = cur
	return nil
}

func (m *Memory) CreateRemoteSession(_ context.Context, s RemoteSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.remoteSessions == nil {
		m.remoteSessions = map[string]RemoteSession{}
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.LastSeenAt.IsZero() {
		s.LastSeenAt = s.CreatedAt
	}
	m.remoteSessions[s.ID] = s
	return nil
}
