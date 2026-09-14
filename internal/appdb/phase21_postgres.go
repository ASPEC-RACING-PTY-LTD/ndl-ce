package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) CreateRegistry(ctx context.Context, r Registry, username, password string) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	r.UpdatedAt = r.CreatedAt
	r.HasCredentials = username != "" || password != ""
	if r.Status == "" {
		r.Status = RegistryConfigured
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
INSERT INTO registries (id, cluster_id, name, url, insecure, has_credentials, status, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		r.ID, r.ClusterID, r.Name, r.URL, r.Insecure, r.HasCredentials, r.Status, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO secrets.registry_secrets (registry_id, cluster_id, username, password, updated_at)
VALUES ($1,$2,$3,$4,$5)`,
		r.ID, r.ClusterID, username, password, r.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) ListRegistries(ctx context.Context, clusterID string) ([]Registry, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, url, insecure, has_credentials, status, created_at, updated_at
FROM registries WHERE cluster_id=$1 ORDER BY created_at ASC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Registry
	for rows.Next() {
		r, err := scanRegistry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) GetRegistry(ctx context.Context, clusterID, id string) (*Registry, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, url, insecure, has_credentials, status, created_at, updated_at
FROM registries WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	r, err := scanRegistry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (p *Postgres) RegistrySecrets(ctx context.Context, clusterID, id string) (string, string, error) {
	var user, pass string
	err := p.DB.QueryRowContext(ctx, `
SELECT username, password FROM secrets.registry_secrets WHERE cluster_id=$1 AND registry_id=$2`, clusterID, id).
		Scan(&user, &pass)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return user, pass, err
}

type registryScanner interface {
	Scan(dest ...any) error
}

func scanRegistry(s registryScanner) (Registry, error) {
	var r Registry
	err := s.Scan(&r.ID, &r.ClusterID, &r.Name, &r.URL, &r.Insecure, &r.HasCredentials, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}
