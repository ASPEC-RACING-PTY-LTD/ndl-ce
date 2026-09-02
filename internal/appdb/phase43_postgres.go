package appdb

import (
	"context"
	"database/sql"
	"errors"
)

func (p *Postgres) GetLicenseState(ctx context.Context, clusterID string) (*LicenseState, string, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT status, reason, last_checked, updated_at FROM license_state WHERE cluster_id=$1`, clusterID)
	st := LicenseState{ClusterID: clusterID, Status: LicenseAbsent, Reason: "Community Edition. License activation is not required."}
	var last sql.NullTime
	err := row.Scan(&st.Status, &st.Reason, &last, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return &st, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if last.Valid {
		t := last.Time.UTC()
		st.LastChecked = &t
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
	if _, err := tx.ExecContext(ctx, `
INSERT INTO license_state (cluster_id, status, reason, last_checked, updated_at)
VALUES ($1,$2,$3,$4,now())
ON CONFLICT (cluster_id) DO UPDATE SET status=EXCLUDED.status, reason=EXCLUDED.reason, last_checked=EXCLUDED.last_checked, updated_at=now()`,
		st.ClusterID, st.Status, st.Reason, st.LastChecked); err != nil {
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
