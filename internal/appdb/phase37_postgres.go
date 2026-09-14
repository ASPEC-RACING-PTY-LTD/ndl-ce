package appdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (p *Postgres) CreateSigningKey(ctx context.Context, k SigningKey, privateB64 string) error {
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now().UTC()
	}
	if k.Status == "" {
		k.Status = StoreKeyActive
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO store_signing_keys (id, cluster_id, name, class, public_key, status, created_at, revoked_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		k.ID, k.ClusterID, k.Name, k.Class, k.PublicKey, k.Status, k.CreatedAt, k.RevokedAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets.store_signing_private (key_id, cluster_id, private_key, updated_at)
VALUES ($1,$2,$3,now())`, k.ID, k.ClusterID, privateB64); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) GetSigningKey(ctx context.Context, clusterID, id string) (*SigningKey, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, class, public_key, status, created_at, revoked_at
FROM store_signing_keys WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	return scanSigningKey(row)
}

func (p *Postgres) GetSigningKeyByName(ctx context.Context, clusterID, name string) (*SigningKey, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, class, public_key, status, created_at, revoked_at
FROM store_signing_keys WHERE cluster_id=$1 AND name=$2`, clusterID, name)
	return scanSigningKey(row)
}

func (p *Postgres) ListSigningKeys(ctx context.Context, clusterID string) ([]SigningKey, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, class, public_key, status, created_at, revoked_at
FROM store_signing_keys WHERE cluster_id=$1 ORDER BY name`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SigningKey
	for rows.Next() {
		k, err := scanSigningKey(rows)
		if err != nil {
			return nil, err
		}
		if k != nil {
			out = append(out, *k)
		}
	}
	return out, rows.Err()
}

func (p *Postgres) RevokeSigningKey(ctx context.Context, clusterID, id string, at time.Time) error {
	res, err := p.DB.ExecContext(ctx, `
UPDATE store_signing_keys SET status=$3, revoked_at=$4 WHERE cluster_id=$1 AND id=$2`,
		clusterID, id, StoreKeyRevoked, at)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("signing key not found")
	}
	return nil
}

func (p *Postgres) SigningPrivate(ctx context.Context, clusterID, id string) (string, error) {
	var priv string
	err := p.DB.QueryRowContext(ctx, `
SELECT private_key FROM secrets.store_signing_private WHERE cluster_id=$1 AND key_id=$2`, clusterID, id).Scan(&priv)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("signing key not found")
	}
	return priv, err
}

func (p *Postgres) CreatePackageSignature(ctx context.Context, s PackageSignature) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO store_package_signatures (id, cluster_id, package_id, key_id, algorithm, signature_b64, payload_sha256, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		s.ID, s.ClusterID, s.PackageID, s.KeyID, s.Algorithm, s.SignatureB64, s.PayloadSHA256, s.CreatedAt)
	return err
}

func (p *Postgres) LatestPackageSignature(ctx context.Context, clusterID, packageID string) (*PackageSignature, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, package_id::text, key_id::text, algorithm, signature_b64, payload_sha256, created_at
FROM store_package_signatures WHERE cluster_id=$1 AND package_id=$2 ORDER BY created_at DESC LIMIT 1`, clusterID, packageID)
	var s PackageSignature
	if err := row.Scan(&s.ID, &s.ClusterID, &s.PackageID, &s.KeyID, &s.Algorithm, &s.SignatureB64, &s.PayloadSHA256, &s.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (p *Postgres) CreateStoreVerification(ctx context.Context, v StoreVerification) error {
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO store_verifications (id, cluster_id, package_id, status, reason, trust_class, key_id, created_at)
VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8)`,
		v.ID, v.ClusterID, v.PackageID, v.Status, v.Reason, v.TrustClass, v.KeyID, v.CreatedAt)
	return err
}

func (p *Postgres) LatestStoreVerification(ctx context.Context, clusterID, packageID string) (*StoreVerification, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, package_id::text, status, reason, trust_class, COALESCE(key_id::text,''), created_at
FROM store_verifications WHERE cluster_id=$1 AND package_id=$2 ORDER BY created_at DESC LIMIT 1`, clusterID, packageID)
	var v StoreVerification
	if err := row.Scan(&v.ID, &v.ClusterID, &v.PackageID, &v.Status, &v.Reason, &v.TrustClass, &v.KeyID, &v.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (p *Postgres) CreateScanResults(ctx context.Context, rows []ScanResult) error {
	for _, row := range rows {
		if row.CreatedAt.IsZero() {
			row.CreatedAt = time.Now().UTC()
		}
		if _, err := p.DB.ExecContext(ctx, `
INSERT INTO store_scan_results (id, cluster_id, package_id, verification_id, kind, status, detail, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			row.ID, row.ClusterID, row.PackageID, row.VerificationID, row.Kind, row.Status, row.Detail, row.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) ListScanResults(ctx context.Context, clusterID, verificationID string) ([]ScanResult, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, package_id::text, verification_id::text, kind, status, detail, created_at
FROM store_scan_results WHERE cluster_id=$1 AND verification_id=$2 ORDER BY kind`, clusterID, verificationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScanResult
	for rows.Next() {
		var row ScanResult
		if err := rows.Scan(&row.ID, &row.ClusterID, &row.PackageID, &row.VerificationID, &row.Kind, &row.Status, &row.Detail, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (p *Postgres) GetStorePolicy(ctx context.Context, clusterID string) (*StorePolicy, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT cluster_id::text, install_policy, updated_at FROM store_policies WHERE cluster_id=$1`, clusterID)
	var pol StorePolicy
	if err := row.Scan(&pol.ClusterID, &pol.InstallPolicy, &pol.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &StorePolicy{ClusterID: clusterID, InstallPolicy: StorePolicyCommunity}, nil
		}
		return nil, err
	}
	return &pol, nil
}

func (p *Postgres) SetStorePolicy(ctx context.Context, pol StorePolicy) error {
	if pol.UpdatedAt.IsZero() {
		pol.UpdatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO store_policies (cluster_id, install_policy, updated_at)
VALUES ($1,$2,$3)
ON CONFLICT (cluster_id) DO UPDATE SET install_policy=EXCLUDED.install_policy, updated_at=EXCLUDED.updated_at`,
		pol.ClusterID, pol.InstallPolicy, pol.UpdatedAt)
	return err
}

type signingKeyScanner interface {
	Scan(dest ...any) error
}

func scanSigningKey(row signingKeyScanner) (*SigningKey, error) {
	var k SigningKey
	var revoked sql.NullTime
	if err := row.Scan(&k.ID, &k.ClusterID, &k.Name, &k.Class, &k.PublicKey, &k.Status, &k.CreatedAt, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if revoked.Valid {
		t := revoked.Time.UTC()
		k.RevokedAt = &t
	}
	return &k, nil
}
