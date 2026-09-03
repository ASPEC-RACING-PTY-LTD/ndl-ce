package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const (
	UserKindPerson  = "person"
	UserKindService = "service"
	MFAKindTOTP     = "totp"
	EncryptionNone  = "none"
	EncryptionLUKS  = "luks"
	EncryptionZFS   = "zfs"
)

// Group is a named set of users that can receive role bindings.
type Group struct {
	ID        string
	ClusterID string
	Name      string
	CreatedAt time.Time
}

// MFAMethod is one enrolled authenticator. The TOTP secret is not on this row.
type MFAMethod struct {
	ID        string
	ClusterID string
	UserID    string
	Kind      string
	Enabled   bool
	CreatedAt time.Time
}

// MFAChallenge is a short-lived login step-up ticket.
type MFAChallenge struct {
	ID         string
	ClusterID  string
	UserID     string
	TokenHash  string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

// ServicePrincipal is a non-interactive identity bound to a user row.
type ServicePrincipal struct {
	ID        string
	ClusterID string
	UserID    string
	Name      string
	CreatedAt time.Time
}

// VolumeEncryption is desired encryption configuration. It is not proof of a live LUKS volume.
type VolumeEncryption struct {
	VolumeID       string
	ClusterID      string
	Encrypted      bool
	EncryptionKind string
}

func (m *Memory) DeleteUserMFA(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.mfaMethods, userID)
	delete(m.mfaSecrets, userID)
	delete(m.mfaRecovery, userID)
	return nil
}

func (m *Memory) ListAuditEvents(_ context.Context, clusterID string, limit int) ([]AuditEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AuditEvent
	for _, e := range m.audit {
		if e.ClusterID == clusterID || clusterID == "" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) CreateGroup(_ context.Context, g Group) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.groups == nil {
		m.groups = map[string]Group{}
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now().UTC()
	}
	m.groups[g.ID] = g
	return nil
}

func (m *Memory) ListGroups(_ context.Context, clusterID string) ([]Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Group
	for _, g := range m.groups {
		if g.ClusterID == clusterID {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Memory) GetGroup(_ context.Context, clusterID, id string) (*Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[id]
	if !ok || g.ClusterID != clusterID {
		return nil, nil
	}
	cp := g
	return &cp, nil
}

func (m *Memory) AddGroupMember(_ context.Context, clusterID, groupID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[groupID]
	if !ok || g.ClusterID != clusterID {
		return fmt.Errorf("group not found")
	}
	u, ok := m.users[userID]
	if !ok || u.ClusterID != clusterID {
		return fmt.Errorf("user not found")
	}
	if m.groupMembers == nil {
		m.groupMembers = map[string][]string{}
	}
	for _, existing := range m.groupMembers[groupID] {
		if existing == userID {
			return nil
		}
	}
	m.groupMembers[groupID] = append(m.groupMembers[groupID], userID)
	return nil
}

func (m *Memory) ListGroupMembers(_ context.Context, clusterID, groupID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[groupID]
	if !ok || g.ClusterID != clusterID {
		return nil, fmt.Errorf("group not found")
	}
	return append([]string{}, m.groupMembers[groupID]...), nil
}

func (m *Memory) BindGroupRole(_ context.Context, clusterID, groupID, roleName string) error {
	if roleName == "admin" {
		return fmt.Errorf("groups cannot grant admin")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[groupID]
	if !ok || g.ClusterID != clusterID {
		return fmt.Errorf("group not found")
	}
	if m.groupRoles == nil {
		m.groupRoles = map[string][]string{}
	}
	m.groupRoles[groupID] = append(m.groupRoles[groupID], roleName)
	return nil
}

func (m *Memory) UpsertMFAMethod(_ context.Context, method MFAMethod, totpSecret string, recoveryHashes []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mfaMethods == nil {
		m.mfaMethods = map[string]MFAMethod{}
		m.mfaSecrets = map[string]string{}
		m.mfaRecovery = map[string][]string{}
	}
	if method.CreatedAt.IsZero() {
		method.CreatedAt = time.Now().UTC()
	}
	m.mfaMethods[method.UserID] = method
	m.mfaSecrets[method.UserID] = totpSecret
	m.mfaRecovery[method.UserID] = append([]string{}, recoveryHashes...)
	return nil
}

func (m *Memory) GetMFAMethod(_ context.Context, userID string) (*MFAMethod, string, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	method, ok := m.mfaMethods[userID]
	if !ok {
		return nil, "", nil, nil
	}
	cp := method
	return &cp, m.mfaSecrets[userID], append([]string{}, m.mfaRecovery[userID]...), nil
}

func (m *Memory) EnableMFAMethod(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	method, ok := m.mfaMethods[userID]
	if !ok {
		return fmt.Errorf("mfa method not found")
	}
	method.Enabled = true
	m.mfaMethods[userID] = method
	return nil
}

func (m *Memory) ConsumeRecoveryHash(_ context.Context, userID, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	remain := make([]string, 0, len(m.mfaRecovery[userID]))
	found := false
	for _, h := range m.mfaRecovery[userID] {
		if h == hash && !found {
			found = true
			continue
		}
		remain = append(remain, h)
	}
	if !found {
		return fmt.Errorf("recovery code is invalid")
	}
	m.mfaRecovery[userID] = remain
	return nil
}

func (m *Memory) CreateMFAChallenge(_ context.Context, c MFAChallenge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mfaChallenges == nil {
		m.mfaChallenges = map[string]MFAChallenge{}
	}
	m.mfaChallenges[c.TokenHash] = c
	return nil
}

func (m *Memory) GetMFAChallengeByHash(_ context.Context, hash string) (*MFAChallenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.mfaChallenges[hash]
	if !ok {
		return nil, nil
	}
	cp := c
	return &cp, nil
}

func (m *Memory) ConsumeMFAChallenge(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for k, c := range m.mfaChallenges {
		if c.ID != id {
			continue
		}
		if c.ConsumedAt != nil {
			return fmt.Errorf("mfa challenge is invalid")
		}
		c.ConsumedAt = &now
		m.mfaChallenges[k] = c
		return nil
	}
	return fmt.Errorf("mfa challenge is invalid")
}

func (m *Memory) CreateServicePrincipal(_ context.Context, sp ServicePrincipal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.servicePrincipals == nil {
		m.servicePrincipals = map[string]ServicePrincipal{}
	}
	if sp.CreatedAt.IsZero() {
		sp.CreatedAt = time.Now().UTC()
	}
	m.servicePrincipals[sp.ID] = sp
	return nil
}

func (m *Memory) ListServicePrincipals(_ context.Context, clusterID string) ([]ServicePrincipal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ServicePrincipal
	for _, sp := range m.servicePrincipals {
		if sp.ClusterID == clusterID {
			out = append(out, sp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return lessCreatedAtID(out[i].CreatedAt, out[j].CreatedAt, out[i].ID, out[j].ID)
	})
	return out, nil
}

func (m *Memory) GetVolumeEncryption(_ context.Context, clusterID, volumeID string) (*VolumeEncryption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.volumeEnc[volumeID]
	if !ok || e.ClusterID != clusterID {
		return &VolumeEncryption{VolumeID: volumeID, ClusterID: clusterID, EncryptionKind: EncryptionNone}, nil
	}
	cp := e
	return &cp, nil
}

func (m *Memory) UpsertVolumeEncryption(_ context.Context, e VolumeEncryption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.volumeEnc == nil {
		m.volumeEnc = map[string]VolumeEncryption{}
	}
	m.volumeEnc[e.VolumeID] = e
	return nil
}
