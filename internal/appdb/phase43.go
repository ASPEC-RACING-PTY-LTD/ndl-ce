package appdb

import (
	"context"
	"time"
)

const (
	LicenseAbsent      = "absent"
	LicenseGrace       = "grace"
	LicenseUnreachable = "unreachable"
	LicenseActive      = "active"
)

// LicenseState is the CE license-activation surface. Empty means CE with no key.
type LicenseState struct {
	ClusterID   string
	Status      string
	Reason      string
	LastChecked *time.Time
	UpdatedAt   time.Time
}

func (m *Memory) GetLicenseState(_ context.Context, clusterID string) (*LicenseState, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.licenseState[clusterID]
	if !ok {
		return &LicenseState{ClusterID: clusterID, Status: LicenseAbsent, Reason: "Community Edition. License activation is not required."}, "", nil
	}
	cp := st
	return &cp, m.licenseKeys[clusterID], nil
}

func (m *Memory) PutLicenseState(_ context.Context, st LicenseState, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.licenseState == nil {
		m.licenseState = map[string]LicenseState{}
		m.licenseKeys = map[string]string{}
	}
	if st.UpdatedAt.IsZero() {
		st.UpdatedAt = time.Now().UTC()
	}
	m.licenseState[st.ClusterID] = st
	if key != "" {
		m.licenseKeys[st.ClusterID] = key
	}
	return nil
}

func (m *Memory) ClearLicense(_ context.Context, clusterID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.licenseState, clusterID)
	delete(m.licenseKeys, clusterID)
	return nil
}
