package appdb

import (
	"context"
	"errors"
	"time"
)

var (
	ErrLeaseHeld        = errors.New("another control plane holds the writer lease")
	ErrJoinTokenUsed    = errors.New("join token already used")
	ErrJoinTokenInvalid = errors.New("join token is invalid")
	ErrNodeNotFound     = errors.New("node not found")
)

func nodeRole(n Node) string {
	if n.Role == "" {
		return "control"
	}
	return n.Role
}

func controlNodeLocked(nodes map[string]Node, clusterID string) *Node {
	var control *Node
	var first *Node
	for _, n := range nodes {
		if n.ClusterID != clusterID || n.RevokedAt != nil {
			continue
		}
		cp := n
		if first == nil {
			first = &cp
		}
		if nodeRole(n) == "control" {
			c := n
			if control == nil || n.Name == "local" {
				control = &c
			}
		}
	}
	if control != nil {
		return control
	}
	return first
}

func (m *Memory) GetNodeByID(_ context.Context, clusterID, id string) (*Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[id]
	if !ok || n.ClusterID != clusterID {
		return nil, nil
	}
	cp := n
	return &cp, nil
}

func (m *Memory) ListClusterNodes(_ context.Context, clusterID string) ([]Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Node
	for _, n := range m.nodes {
		if n.ClusterID == clusterID {
			out = append(out, n)
		}
	}
	return out, nil
}

func (m *Memory) RevokeNode(_ context.Context, clusterID, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[id]
	if !ok || n.ClusterID != clusterID {
		return ErrNodeNotFound
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	n.RevokedAt = &at
	m.nodes[id] = n
	return nil
}

func (m *Memory) CreateJoinToken(_ context.Context, t JoinToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.joinTokens == nil {
		m.joinTokens = map[string]JoinToken{}
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	m.joinTokens[t.ID] = t
	return nil
}

func (m *Memory) GetJoinTokenByHash(_ context.Context, tokenHash string) (*JoinToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.joinTokens {
		if t.TokenHash == tokenHash {
			cp := t
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) ConsumeJoinToken(_ context.Context, tokenHash, nodeID string, at time.Time) (*JoinToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	for id, t := range m.joinTokens {
		if t.TokenHash != tokenHash {
			continue
		}
		if t.ConsumedAt != nil {
			return nil, ErrJoinTokenUsed
		}
		if !t.ExpiresAt.IsZero() && !at.Before(t.ExpiresAt) {
			return nil, ErrJoinTokenInvalid
		}
		t.ConsumedAt = &at
		t.ConsumedNodeID = nodeID
		m.joinTokens[id] = t
		cp := t
		return &cp, nil
	}
	return nil, ErrJoinTokenInvalid
}

func (m *Memory) AcquireLease(_ context.Context, clusterID, holderID string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	if m.clusterLease != nil && m.clusterLease.ClusterID == clusterID {
		if m.clusterLease.HolderID != holderID && now.Before(m.clusterLease.ExpiresAt) {
			return ErrLeaseHeld
		}
	}
	m.clusterLease = &ClusterLease{ClusterID: clusterID, HolderID: holderID, ExpiresAt: expiresAt}
	return nil
}

func (m *Memory) GetClusterLease(_ context.Context, clusterID string) (*ClusterLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clusterLease == nil || m.clusterLease.ClusterID != clusterID {
		return nil, nil
	}
	cp := *m.clusterLease
	return &cp, nil
}
