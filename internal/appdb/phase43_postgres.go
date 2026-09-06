package appdb

import (
	"context"
	"database/sql"
	"errors"
)

func (p *Postgres) GetLicenseState(ctx context.Context, clusterID string) (*LicenseState, string, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT status, reason, last_checked, updated_at, entitlement_json, edition, expires_at, grace_until, installation_id, organization
FROM license_state WHERE cluster_id=$1`, clusterID)
	st := LicenseState{ClusterID: clusterID, Status: LicenseAbsent, Reason: "Community Edition. License activation is not required.", Edition: "ce"}
	var last, exp, grace sql.NullTime
	var ent []byte
	var edition, installID, org sql.NullString
	err := row.Scan(&st.Status, &st.Reason, &last, &st.UpdatedAt, &ent, &edition, &exp, &grace, &installID, &org)
	if errors.Is(err, sql.ErrNoRows) {
		return &st, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	st.EntitlementJSON = ent
	if edition.Valid && edition.String != "" {
		st.Edition = edition.String
	}
	if installID.Valid {
		st.InstallationID = installID.String
	}
	if org.Valid {
		st.Organization = org.String
	}
	if last.Valid {
		t := last.Time.UTC()
		st.LastChecked = &t
	}
	if exp.Valid {
		t := exp.Time.UTC()
		st.ExpiresAt = &t
	}
	if grace.Valid {
		t := grace.Time.UTC()
		st.GraceUntil = &t
	}
	keyRow := p.DB.QueryRowContext(ctx, `SELECT license_key FROM secrets.license_keys WHERE cluster_id=$1`, clusterID)
	var key string
	if err := keyRow.Scan(&key); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	return &st, key, nil
}

func (p *Postgres) PutLicenseState(ctx context.Context, st LicenseState, key string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if st.Edition == "" {
		st.Edition = "ce"
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO license_state (cluster_id, status, reason, last_checked, updated_at, entitlement_json, edition, expires_at, grace_until, installation_id, organization)
VALUES ($1,$2,$3,$4,now(),$5,$6,$7,$8,$9,$10)
ON CONFLICT (cluster_id) DO UPDATE SET status=EXCLUDED.status, reason=EXCLUDED.reason, last_checked=EXCLUDED.last_checked, updated_at=now(),
  entitlement_json=EXCLUDED.entitlement_json, edition=EXCLUDED.edition, expires_at=EXCLUDED.expires_at, grace_until=EXCLUDED.grace_until,
  installation_id=EXCLUDED.installation_id, organization=EXCLUDED.organization`,
		st.ClusterID, st.Status, st.Reason, st.LastChecked, st.EntitlementJSON, st.Edition, st.ExpiresAt, st.GraceUntil, st.InstallationID, st.Organization); err != nil {
		return err
	}
	if key != "" {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets.license_keys (cluster_id, license_key, updated_at)
VALUES ($1,$2,now())
ON CONFLICT (cluster_id) DO UPDATE SET license_key=EXCLUDED.license_key, updated_at=now()`,
			st.ClusterID, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *Postgres) ClearLicense(ctx context.Context, clusterID string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM secrets.license_keys WHERE cluster_id=$1`, clusterID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM license_state WHERE cluster_id=$1`, clusterID); err != nil {
		return err
	}
	return tx.Commit()
}
