package appdb

import (
	"context"
	"strings"
	"time"
)

// ClearAuditEvents deletes audit events of a cluster created before before,
// or all of them when before is zero. It returns how many were deleted.
func (m *Memory) ClearAuditEvents(_ context.Context, clusterID string, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.audit[:0]
	n := 0
	for _, e := range m.audit {
		if e.ClusterID == clusterID && (before.IsZero() || e.CreatedAt.Before(before)) {
			n++
			continue
		}
		kept = append(kept, e)
	}
	m.audit = kept
	return n, nil
}

// ClearOperations deletes finished operations (tasks) of a cluster last
// updated before before, or all finished ones when before is zero. Running
// work is never removed.
func (m *Memory) ClearOperations(_ context.Context, clusterID string, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.operations[:0]
	n := 0
	for _, op := range m.operations {
		if op.ClusterID == clusterID && !operationActive(op.State) && (before.IsZero() || op.UpdatedAt.Before(before)) {
			n++
			continue
		}
		kept = append(kept, op)
	}
	m.operations = kept
	return n, nil
}

func operationActive(state string) bool {
	s := strings.ToLower(strings.TrimSpace(state))
	return s == OpStateRunning || s == OpStateCanceling || s == "queued" || s == "pending"
}

func (p *Postgres) ClearAuditEvents(ctx context.Context, clusterID string, before time.Time) (int, error) {
	q := `DELETE FROM audit_events WHERE cluster_id=$1`
	args := []any{clusterID}
	if !before.IsZero() {
		q += ` AND created_at < $2`
		args = append(args, before)
	}
	res, err := p.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (p *Postgres) ClearOperations(ctx context.Context, clusterID string, before time.Time) (int, error) {
	q := `DELETE FROM operations WHERE cluster_id=$1 AND lower(state) NOT IN ('running', 'canceling', 'queued', 'pending')`
	args := []any{clusterID}
	if !before.IsZero() {
		q += ` AND updated_at < $2`
		args = append(args, before)
	}
	res, err := p.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
