package appdb

import (
	"context"
	"database/sql"
	"errors"
)

func (p *Postgres) CreateAIPlan(ctx context.Context, plan AIPlan, steps []AIPlanStep) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO ai_plans (id, cluster_id, profile_id, prompt, status, actor_type, reason, created_by, created_at)
VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,now())`,
		plan.ID, plan.ClusterID, plan.ProfileID, plan.Prompt, plan.Status, plan.ActorType, plan.Reason, plan.CreatedBy); err != nil {
		return err
	}
	for _, st := range steps {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO ai_plan_steps (id, cluster_id, plan_id, ordinal, action, permission, method, path, title, body_json, status, reason, operation_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			st.ID, st.ClusterID, st.PlanID, st.Ordinal, st.Action, st.Permission, st.Method, st.Path, st.Title, st.BodyJSON, st.Status, st.Reason, st.OperationID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *Postgres) GetAIPlan(ctx context.Context, clusterID, id string) (*AIPlan, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(profile_id::text, ''), prompt, status, actor_type, reason, created_by::text, created_at
FROM ai_plans WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var plan AIPlan
	if err := row.Scan(&plan.ID, &plan.ClusterID, &plan.ProfileID, &plan.Prompt, &plan.Status, &plan.ActorType, &plan.Reason, &plan.CreatedBy, &plan.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &plan, nil
}

func (p *Postgres) ListAIPlans(ctx context.Context, clusterID string, limit int) ([]AIPlan, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(profile_id::text, ''), prompt, status, actor_type, reason, created_by::text, created_at
FROM ai_plans WHERE cluster_id=$1 ORDER BY created_at DESC LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIPlan
	for rows.Next() {
		var plan AIPlan
		if err := rows.Scan(&plan.ID, &plan.ClusterID, &plan.ProfileID, &plan.Prompt, &plan.Status, &plan.ActorType, &plan.Reason, &plan.CreatedBy, &plan.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, plan)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateAIPlan(ctx context.Context, plan AIPlan) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE ai_plans SET status=$3, reason=$4 WHERE cluster_id=$1 AND id=$2`,
		plan.ClusterID, plan.ID, plan.Status, plan.Reason)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (p *Postgres) ListAIPlanSteps(ctx context.Context, clusterID, planID string) ([]AIPlanStep, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, plan_id::text, ordinal, action, permission, method, path, title, body_json, status, reason, operation_id
FROM ai_plan_steps WHERE cluster_id=$1 AND plan_id=$2 ORDER BY ordinal`, clusterID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIPlanStep
	for rows.Next() {
		var st AIPlanStep
		if err := rows.Scan(&st.ID, &st.ClusterID, &st.PlanID, &st.Ordinal, &st.Action, &st.Permission, &st.Method, &st.Path, &st.Title, &st.BodyJSON, &st.Status, &st.Reason, &st.OperationID); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateAIPlanStep(ctx context.Context, st AIPlanStep) error {
	res, err := p.DB.ExecContext(ctx, `
UPDATE ai_plan_steps SET status=$3, reason=$4, operation_id=$5 WHERE cluster_id=$1 AND id=$2`,
		st.ClusterID, st.ID, st.Status, st.Reason, st.OperationID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
