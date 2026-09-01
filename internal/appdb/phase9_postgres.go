package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) GetCertificate(ctx context.Context, clusterID string) (*Certificate, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, mode, enabled, common_name, sans, fingerprint,
       not_before, not_after, cert_path, key_path, acme_directory, acme_email, acme_domain,
       acme_status, next_renewal_at, last_good_fingerprint, updated_at
FROM certificates WHERE cluster_id=$1`, clusterID)
	var c Certificate
	var sans []byte
	var nb, na, nr sql.NullTime
	if err := row.Scan(&c.ID, &c.ClusterID, &c.Mode, &c.Enabled, &c.CommonName, &sans, &c.Fingerprint,
		&nb, &na, &c.CertPath, &c.KeyPath, &c.ACMEDirectory, &c.ACMEEmail, &c.ACMEDomain,
		&c.ACMEStatus, &nr, &c.LastGoodFingerprint, &c.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal(sans, &c.SANs)
	if nb.Valid {
		t := nb.Time.UTC()
		c.NotBefore = &t
	}
	if na.Valid {
		t := na.Time.UTC()
		c.NotAfter = &t
	}
	if nr.Valid {
		t := nr.Time.UTC()
		c.NextRenewalAt = &t
	}
	return &c, nil
}

func (p *Postgres) UpsertCertificate(ctx context.Context, c Certificate) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = time.Now().UTC()
	}
	if c.SANs == nil {
		c.SANs = []string{}
	}
	raw, _ := json.Marshal(c.SANs)
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO certificates (
  id, cluster_id, mode, enabled, common_name, sans, fingerprint, not_before, not_after,
  cert_path, key_path, acme_directory, acme_email, acme_domain, acme_status, next_renewal_at,
  last_good_fingerprint, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (cluster_id) DO UPDATE SET
  mode=EXCLUDED.mode, enabled=EXCLUDED.enabled, common_name=EXCLUDED.common_name, sans=EXCLUDED.sans,
  fingerprint=EXCLUDED.fingerprint, not_before=EXCLUDED.not_before, not_after=EXCLUDED.not_after,
  cert_path=EXCLUDED.cert_path, key_path=EXCLUDED.key_path, acme_directory=EXCLUDED.acme_directory,
  acme_email=EXCLUDED.acme_email, acme_domain=EXCLUDED.acme_domain, acme_status=EXCLUDED.acme_status,
  next_renewal_at=EXCLUDED.next_renewal_at, last_good_fingerprint=EXCLUDED.last_good_fingerprint,
  updated_at=EXCLUDED.updated_at`,
		c.ID, c.ClusterID, c.Mode, c.Enabled, c.CommonName, raw, c.Fingerprint, c.NotBefore, c.NotAfter,
		c.CertPath, c.KeyPath, c.ACMEDirectory, c.ACMEEmail, c.ACMEDomain, c.ACMEStatus, c.NextRenewalAt,
		c.LastGoodFingerprint, c.UpdatedAt)
	return err
}
