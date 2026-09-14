package appdb

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (p *Postgres) CreatePolicy(ctx context.Context, pol Policy) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO policies (id, cluster_id, name, kind, action, threshold_percent, require_approval, enabled, spec_yaml, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now())`,
		pol.ID, pol.ClusterID, pol.Name, pol.Kind, pol.Action, pol.ThresholdPercent, pol.RequireApproval, pol.Enabled, pol.SpecYAML)
	return err
}

func (p *Postgres) ListPolicies(ctx context.Context, clusterID string) ([]Policy, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, kind, action, threshold_percent, require_approval, enabled, spec_yaml, created_at
FROM policies WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Policy
	for rows.Next() {
		var pol Policy
		if err := rows.Scan(&pol.ID, &pol.ClusterID, &pol.Name, &pol.Kind, &pol.Action, &pol.ThresholdPercent, &pol.RequireApproval, &pol.Enabled, &pol.SpecYAML, &pol.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pol)
	}
	return out, rows.Err()
}

func (p *Postgres) GetPolicy(ctx context.Context, clusterID, id string) (*Policy, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, kind, action, threshold_percent, require_approval, enabled, spec_yaml, created_at
FROM policies WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var pol Policy
	if err := row.Scan(&pol.ID, &pol.ClusterID, &pol.Name, &pol.Kind, &pol.Action, &pol.ThresholdPercent, &pol.RequireApproval, &pol.Enabled, &pol.SpecYAML, &pol.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &pol, nil
}

func (p *Postgres) CreatePolicyRun(ctx context.Context, r PolicyRun) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO policy_runs (id, cluster_id, policy_id, actor_id, status, reason, operation_ids, created_at)
VALUES ($1,$2,$3,$4,$5,$6,COALESCE(string_to_array(NULLIF($7, ''), ','), '{}'),now())`,
		r.ID, r.ClusterID, r.PolicyID, r.ActorID, r.Status, r.Reason, strings.Join(r.OperationIDs, ","))
	return err
}

func (p *Postgres) ListPolicyRuns(ctx context.Context, clusterID string, limit int) ([]PolicyRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, policy_id::text, actor_id::text, status, reason, array_to_string(operation_ids, ','), created_at
FROM policy_runs WHERE cluster_id=$1 ORDER BY created_at DESC LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyRun
	for rows.Next() {
		var r PolicyRun
		var ops string
		if err := rows.Scan(&r.ID, &r.ClusterID, &r.PolicyID, &r.ActorID, &r.Status, &r.Reason, &ops, &r.CreatedAt); err != nil {
			return nil, err
		}
		if strings.TrimSpace(ops) != "" {
			r.OperationIDs = strings.Split(ops, ",")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
