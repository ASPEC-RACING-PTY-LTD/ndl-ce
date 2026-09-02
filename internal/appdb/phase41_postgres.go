package appdb

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (p *Postgres) CreateAIProvider(ctx context.Context, prov AIProvider, apiKey string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO ai_providers (id, cluster_id, name, kind, endpoint, model, enabled, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,now())`,
		prov.ID, prov.ClusterID, prov.Name, prov.Kind, prov.Endpoint, prov.Model, prov.Enabled); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets.ai_credentials (provider_id, cluster_id, api_key, updated_at)
VALUES ($1,$2,$3,now())`, prov.ID, prov.ClusterID, apiKey); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) ListAIProviders(ctx context.Context, clusterID string) ([]AIProvider, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, kind, endpoint, model, enabled, created_at
FROM ai_providers WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIProvider
	for rows.Next() {
		var prov AIProvider
		if err := rows.Scan(&prov.ID, &prov.ClusterID, &prov.Name, &prov.Kind, &prov.Endpoint, &prov.Model, &prov.Enabled, &prov.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, prov)
	}
	return out, rows.Err()
}

func (p *Postgres) GetAIProvider(ctx context.Context, clusterID, id string) (*AIProvider, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, kind, endpoint, model, enabled, created_at
FROM ai_providers WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var prov AIProvider
	if err := row.Scan(&prov.ID, &prov.ClusterID, &prov.Name, &prov.Kind, &prov.Endpoint, &prov.Model, &prov.Enabled, &prov.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &prov, nil
}

func (p *Postgres) AIProviderKey(ctx context.Context, clusterID, id string) (string, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT api_key FROM secrets.ai_credentials WHERE cluster_id=$1 AND provider_id=$2`, clusterID, id)
	var key string
	if err := row.Scan(&key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return key, nil
}

func (p *Postgres) CreateAIProfile(ctx context.Context, prof AIProfile) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO ai_profiles (id, cluster_id, name, provider_id, mode, grants, created_at)
VALUES ($1,$2,$3,NULLIF($4,'')::uuid,$5,COALESCE(string_to_array(NULLIF($6, ''), ','), '{}'),now())`,
		prof.ID, prof.ClusterID, prof.Name, prof.ProviderID, prof.Mode, strings.Join(prof.Grants, ","))
	return err
}

func (p *Postgres) ListAIProfiles(ctx context.Context, clusterID string) ([]AIProfile, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, COALESCE(provider_id::text, ''), mode, array_to_string(grants, ','), created_at
FROM ai_profiles WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIProfile
	for rows.Next() {
		var prof AIProfile
		var grants string
		if err := rows.Scan(&prof.ID, &prof.ClusterID, &prof.Name, &prof.ProviderID, &prof.Mode, &grants, &prof.CreatedAt); err != nil {
			return nil, err
		}
		if strings.TrimSpace(grants) != "" {
			prof.Grants = strings.Split(grants, ",")
		}
		out = append(out, prof)
	}
	return out, rows.Err()
}

func (p *Postgres) GetAIProfile(ctx context.Context, clusterID, id string) (*AIProfile, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, COALESCE(provider_id::text, ''), mode, array_to_string(grants, ','), created_at
FROM ai_profiles WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var prof AIProfile
	var grants string
	if err := row.Scan(&prof.ID, &prof.ClusterID, &prof.Name, &prof.ProviderID, &prof.Mode, &grants, &prof.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(grants) != "" {
		prof.Grants = strings.Split(grants, ",")
	}
	return &prof, nil
}
