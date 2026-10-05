package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (p *Postgres) GetUserPrefs(ctx context.Context, userID string) (*UserPrefs, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT user_id::text, cluster_id::text, ux_level, expert_ack_at, workload_sort, workload_order, updated_at
FROM user_prefs WHERE user_id=$1`, userID)
	var pref UserPrefs
	var ack sql.NullTime
	var order []byte
	if err := row.Scan(&pref.UserID, &pref.ClusterID, &pref.UXLevel, &ack, &pref.WorkloadSort, &order, &pref.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if ack.Valid {
		t := ack.Time.UTC()
		pref.ExpertAckAt = &t
	}
	if len(order) > 0 {
		_ = json.Unmarshal(order, &pref.WorkloadOrder)
	}
	return &pref, nil
}

func (p *Postgres) UpsertUserPrefs(ctx context.Context, pref UserPrefs) error {
	if pref.UpdatedAt.IsZero() {
		pref.UpdatedAt = time.Now().UTC()
	}
	if pref.WorkloadOrder == nil {
		pref.WorkloadOrder = []string{}
	}
	order, err := json.Marshal(pref.WorkloadOrder)
	if err != nil {
		return err
	}
	_, err = p.DB.ExecContext(ctx, `
INSERT INTO user_prefs (user_id, cluster_id, ux_level, expert_ack_at, workload_sort, workload_order, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (user_id) DO UPDATE SET
	ux_level=EXCLUDED.ux_level,
	expert_ack_at=EXCLUDED.expert_ack_at,
	workload_sort=EXCLUDED.workload_sort,
	workload_order=EXCLUDED.workload_order,
	updated_at=EXCLUDED.updated_at`,
		pref.UserID, pref.ClusterID, pref.UXLevel, pref.ExpertAckAt, pref.WorkloadSort, order, pref.UpdatedAt)
	return err
}
