package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) UpsertInventory(ctx context.Context, row HardwareInventory) error {
	if len(row.Payload) == 0 {
		row.Payload = json.RawMessage(`{}`)
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO hardware_inventory (node_id, cluster_id, payload, observed_at, stale)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (node_id) DO UPDATE SET
  payload = EXCLUDED.payload,
  observed_at = EXCLUDED.observed_at,
  stale = EXCLUDED.stale`,
		row.NodeID, row.ClusterID, row.Payload, row.ObservedAt, row.Stale)
	return err
}

func (p *Postgres) GetInventory(ctx context.Context, nodeID string) (*HardwareInventory, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT node_id::text, cluster_id::text, payload, observed_at, stale
FROM hardware_inventory WHERE node_id=$1`, nodeID)
	var h HardwareInventory
	if err := row.Scan(&h.NodeID, &h.ClusterID, &h.Payload, &h.ObservedAt, &h.Stale); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &h, nil
}

func (p *Postgres) MarkInventoryStale(ctx context.Context, nodeID string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE hardware_inventory SET stale=true WHERE node_id=$1`, nodeID)
	return err
}

func (p *Postgres) InsertObservation(ctx context.Context, o NodeObservation) error {
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO node_observations (id, cluster_id, node_id, kind, observed_at, stale)
VALUES ($1,$2,$3,$4,$5,$6)`,
		o.ID, o.ClusterID, o.NodeID, o.Kind, o.ObservedAt, o.Stale)
	return err
}

func (p *Postgres) UpsertOperation(ctx context.Context, op Operation) error {
	if op.ID == "" {
		op.ID = uuid.NewString()
	}
	if op.UpdatedAt.IsZero() {
		op.UpdatedAt = time.Now().UTC()
	}
	if op.CreatedAt.IsZero() {
		op.CreatedAt = op.UpdatedAt
	}
	key := any(nil)
	if op.IdempotencyKey != "" {
		key = op.IdempotencyKey
	}
	node := any(nil)
	if op.NodeID != "" {
		node = op.NodeID
	}
	progress := any(nil)
	if op.Progress != nil {
		progress = *op.Progress
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO operations (id, cluster_id, node_id, kind, state, idempotency_key, progress, stage, message, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (cluster_id, idempotency_key) WHERE idempotency_key IS NOT NULL
DO UPDATE SET
  state = EXCLUDED.state,
  progress = EXCLUDED.progress,
  stage = EXCLUDED.stage,
  message = EXCLUDED.message,
  updated_at = EXCLUDED.updated_at,
  node_id = EXCLUDED.node_id`,
		op.ID, op.ClusterID, node, op.Kind, op.State, key, progress, nullIfEmpty(op.Stage), nullIfEmpty(op.Message), op.CreatedAt, op.UpdatedAt)
	return err
}

func (p *Postgres) ListOperations(ctx context.Context, clusterID string, limit int) ([]Operation, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(node_id::text, ''), kind, state,
       COALESCE(idempotency_key, ''), progress, COALESCE(stage, ''), COALESCE(message, ''),
       created_at, COALESCE(updated_at, created_at)
FROM operations
WHERE cluster_id=$1
ORDER BY COALESCE(updated_at, created_at) DESC
LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Operation
	for rows.Next() {
		var op Operation
		var progress sql.NullInt64
		if err := rows.Scan(&op.ID, &op.ClusterID, &op.NodeID, &op.Kind, &op.State,
			&op.IdempotencyKey, &progress, &op.Stage, &op.Message, &op.CreatedAt, &op.UpdatedAt); err != nil {
			return nil, err
		}
		if progress.Valid {
			n := int(progress.Int64)
			op.Progress = &n
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func (p *Postgres) InsertEvent(ctx context.Context, e Event) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	node := any(nil)
	if e.NodeID != "" {
		node = e.NodeID
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO events (id, cluster_id, node_id, type, payload, created_at)
VALUES ($1,$2,$3,$4,$5,$6)`,
		e.ID, e.ClusterID, node, e.Type, e.Payload, e.CreatedAt)
	return err
}

func (p *Postgres) ListEvents(ctx context.Context, clusterID string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(node_id::text, ''), type, payload, created_at
FROM events
WHERE cluster_id=$1
ORDER BY created_at DESC
LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.ClusterID, &e.NodeID, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
