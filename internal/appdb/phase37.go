package appdb

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const (
	StoreKeyActive          = "active"
	StoreKeyRevoked         = "revoked"
	StorePolicyCommunity    = "community-allowed"
	StorePolicyVerifiedOnly = "verified-only"
	StoreVerifyPass         = "pass"
	StoreVerifyFail         = "fail"
)

// SigningKey is the public half of a Store signing key.
type SigningKey struct {
	ID        string
	ClusterID string
	Name      string
	Class     string
	PublicKey string
	Status    string
	CreatedAt time.Time
	RevokedAt *time.Time
}

// PackageSignature is a detached Ed25519 signature over stored manifest bytes.
type PackageSignature struct {
	ID            string
	ClusterID     string
	PackageID     string
	KeyID         string
	Algorithm     string
	SignatureB64  string
	PayloadSHA256 string
	CreatedAt     time.Time
}

// StoreVerification is one verify job.
type StoreVerification struct {
	ID         string
	ClusterID  string
	PackageID  string
	Status     string
	Reason     string
	TrustClass string
	KeyID      string
	CreatedAt  time.Time
}

// ScanResult is one verifier check row.
type ScanResult struct {
	ID             string
	ClusterID      string
	PackageID      string
	VerificationID string
	Kind           string
	Status         string
	Detail         string
	CreatedAt      time.Time
}

// StorePolicy is the cluster install trust policy.
type StorePolicy struct {
	ClusterID     string
	InstallPolicy string
	UpdatedAt     time.Time
}

func (m *Memory) CreateSigningKey(_ context.Context, k SigningKey, privateB64 string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.signingKeys == nil {
		m.signingKeys = map[string]SigningKey{}
		m.signingSecrets = map[string]string{}
	}
	for _, existing := range m.signingKeys {
		if existing.ClusterID == k.ClusterID && existing.Name == k.Name {
			return fmt.Errorf("signing key already exists")
		}
	}
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now().UTC()
	}
	if k.Status == "" {
		k.Status = StoreKeyActive
	}
	m.signingKeys[k.ID] = k
	m.signingSecrets[k.ID] = privateB64
	return nil
}

func (m *Memory) GetSigningKey(_ context.Context, clusterID, id string) (*SigningKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.signingKeys[id]
	if !ok || k.ClusterID != clusterID {
		return nil, nil
	}
	cp := k
	return &cp, nil
}

func (m *Memory) GetSigningKeyByName(_ context.Context, clusterID, name string) (*SigningKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.signingKeys {
		if k.ClusterID == clusterID && k.Name == name {
			cp := k
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) ListSigningKeys(_ context.Context, clusterID string) ([]SigningKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SigningKey
	for _, k := range m.signingKeys {
		if k.ClusterID == clusterID {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Memory) RevokeSigningKey(_ context.Context, clusterID, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.signingKeys[id]
	if !ok || k.ClusterID != clusterID {
		return fmt.Errorf("signing key not found")
	}
	k.Status = StoreKeyRevoked
	k.RevokedAt = &at
	m.signingKeys[id] = k
	return nil
}

func (m *Memory) SigningPrivate(_ context.Context, clusterID, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.signingKeys[id]
	if !ok || k.ClusterID != clusterID {
		return "", fmt.Errorf("signing key not found")
	}
	return m.signingSecrets[id], nil
}

func (m *Memory) CreatePackageSignature(_ context.Context, s PackageSignature) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.packageSigs == nil {
		m.packageSigs = map[string]PackageSignature{}
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	m.packageSigs[s.ID] = s
	return nil
}

func (m *Memory) LatestPackageSignature(_ context.Context, clusterID, packageID string) (*PackageSignature, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *PackageSignature
	for _, s := range m.packageSigs {
		if s.ClusterID == clusterID && s.PackageID == packageID {
			cp := s
			if best == nil || cp.CreatedAt.After(best.CreatedAt) {
				best = &cp
			}
		}
	}
	return best, nil
}

func (m *Memory) CreateStoreVerification(_ context.Context, v StoreVerification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storeVerifies == nil {
		m.storeVerifies = map[string]StoreVerification{}
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	m.storeVerifies[v.ID] = v
	return nil
}

func (m *Memory) LatestStoreVerification(_ context.Context, clusterID, packageID string) (*StoreVerification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *StoreVerification
	for _, v := range m.storeVerifies {
		if v.ClusterID == clusterID && v.PackageID == packageID {
			cp := v
			if best == nil || cp.CreatedAt.After(best.CreatedAt) {
				best = &cp
			}
		}
	}
	return best, nil
}

func (m *Memory) CreateScanResults(_ context.Context, rows []ScanResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scanResults == nil {
		m.scanResults = map[string]ScanResult{}
	}
	for _, row := range rows {
		if row.CreatedAt.IsZero() {
			row.CreatedAt = time.Now().UTC()
		}
		m.scanResults[row.ID] = row
	}
	return nil
}

func (m *Memory) ListScanResults(_ context.Context, clusterID, verificationID string) ([]ScanResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ScanResult
	for _, row := range m.scanResults {
		if row.ClusterID == clusterID && row.VerificationID == verificationID {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out, nil
}

func (m *Memory) GetStorePolicy(_ context.Context, clusterID string) (*StorePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storePolicies == nil {
		return &StorePolicy{ClusterID: clusterID, InstallPolicy: StorePolicyCommunity}, nil
	}
	p, ok := m.storePolicies[clusterID]
	if !ok {
		return &StorePolicy{ClusterID: clusterID, InstallPolicy: StorePolicyCommunity}, nil
	}
	cp := p
	return &cp, nil
}

func (m *Memory) SetStorePolicy(_ context.Context, p StorePolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storePolicies == nil {
		m.storePolicies = map[string]StorePolicy{}
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now().UTC()
	}
	m.storePolicies[p.ClusterID] = p
	return nil
}
