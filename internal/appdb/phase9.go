package appdb

import (
	"context"
	"time"
)

// Certificate is desired TLS state. Private keys stay on disk.
type Certificate struct {
	ID                  string
	ClusterID           string
	Mode                string
	Enabled             bool
	CommonName          string
	SANs                []string
	Fingerprint         string
	NotBefore           *time.Time
	NotAfter            *time.Time
	CertPath            string
	KeyPath             string
	ACMEDirectory       string
	ACMEEmail           string
	ACMEDomain          string
	ACMEStatus          string
	NextRenewalAt       *time.Time
	LastGoodFingerprint string
	UpdatedAt           time.Time
}

func (m *Memory) GetCertificate(_ context.Context, clusterID string) (*Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.certificate == nil || m.certificate.ClusterID != clusterID {
		return nil, nil
	}
	cp := *m.certificate
	return &cp, nil
}

func (m *Memory) UpsertCertificate(_ context.Context, c Certificate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c.UpdatedAt = time.Now().UTC()
	m.certificate = &c
	return nil
}
