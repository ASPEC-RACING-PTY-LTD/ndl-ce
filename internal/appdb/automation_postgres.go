package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

func autoJSON(raw json.RawMessage) string {
	if len(raw) == 0 || !json.Valid(raw) {
		return "{}"
	}
	return string(raw)
}

func autoTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func autoTimePtr(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time
	return &t
}

func autoUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const autoFlowCols = `id::text, cluster_id::text, name, description, draft::text, published_version, enabled, trigger_kind, event_kind,
limits::text, dry_run_hash, consecutive_failures, disabled_reason, COALESCE(legacy_policy_id::text, ''), pending_version, draft_tainted,
created_by, created_at, updated_at`

func scanAutoFlow(s interface{ Scan(...any) error }) (AutomationFlow, error) {
	var f AutomationFlow
	var draft, limits string
	err := s.Scan(&f.ID, &f.ClusterID, &f.Name, &f.Description, &draft, &f.PublishedVersion, &f.Enabled, &f.TriggerKind, &f.EventKind,
		&limits, &f.DryRunHash, &f.ConsecutiveFailures, &f.DisabledReason, &f.LegacyPolicyID, &f.PendingVersion, &f.DraftTainted,
		&f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	f.Draft, f.Limits = json.RawMessage(draft), json.RawMessage(limits)
	return f, err
}

func (p *Postgres) CreateAutomationFlow(ctx context.Context, f AutomationFlow) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO automation_flows (id, cluster_id, name, description, draft, published_version, enabled, trigger_kind, event_kind,
  limits, dry_run_hash, consecutive_failures, disabled_reason, legacy_policy_id, created_by, pending_version, draft_tainted)
VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10::jsonb,$11,$12,$13,$14,$15,$16,$17)`,
		f.ID, f.ClusterID, f.Name, f.Description, autoJSON(f.Draft), f.PublishedVersion, f.Enabled, f.TriggerKind, f.EventKind,
		autoJSON(f.Limits), f.DryRunHash, f.ConsecutiveFailures, f.DisabledReason, autoUUID(f.LegacyPolicyID), f.CreatedBy,
		f.PendingVersion, f.DraftTainted)
	return err
}

func (p *Postgres) GetAutomationFlow(ctx context.Context, clusterID, id string) (*AutomationFlow, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	f, err := scanAutoFlow(p.DB.QueryRowContext(ctx, `SELECT `+autoFlowCols+` FROM automation_flows WHERE cluster_id=$1 AND id=$2`, clusterID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (p *Postgres) listAutoFlows(ctx context.Context, q string, args ...any) ([]AutomationFlow, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT `+autoFlowCols+` FROM automation_flows `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationFlow
	for rows.Next() {
		f, err := scanAutoFlow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (p *Postgres) ListAutomationFlows(ctx context.Context, clusterID string) ([]AutomationFlow, error) {
	if clusterID == "" {
		return p.listAutoFlows(ctx, `ORDER BY created_at`)
	}
	return p.listAutoFlows(ctx, `WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
}

func (p *Postgres) ListAutomationFlowsByTrigger(ctx context.Context, clusterID, kind, eventKind string) ([]AutomationFlow, error) {
	return p.listAutoFlows(ctx, `WHERE cluster_id=$1 AND enabled AND published_version > 0 AND trigger_kind=$2 AND ($3 = '' OR event_kind=$3) ORDER BY created_at`,
		clusterID, kind, eventKind)
}

func (p *Postgres) UpdateAutomationFlow(ctx context.Context, f AutomationFlow) error {
	res, err := p.DB.ExecContext(ctx, `
UPDATE automation_flows SET name=$3, description=$4, draft=$5::jsonb, published_version=$6, enabled=$7, trigger_kind=$8, event_kind=$9,
  limits=$10::jsonb, dry_run_hash=$11, consecutive_failures=$12, disabled_reason=$13, legacy_policy_id=$14,
  pending_version=$15, draft_tainted=$16, updated_at=now()
WHERE cluster_id=$1 AND id=$2`,
		f.ClusterID, f.ID, f.Name, f.Description, autoJSON(f.Draft), f.PublishedVersion, f.Enabled, f.TriggerKind, f.EventKind,
		autoJSON(f.Limits), f.DryRunHash, f.ConsecutiveFailures, f.DisabledReason, autoUUID(f.LegacyPolicyID),
		f.PendingVersion, f.DraftTainted)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("flow not found")
	}
	return nil
}

func (p *Postgres) DeleteAutomationFlow(ctx context.Context, clusterID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM automation_flows WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	return err
}

const autoVersionCols = `flow_id::text, cluster_id::text, version, definition::text, published_by, note, status, definition_hash,
needs_approval, protected_steps::text, exempt, requested_by_id, decided_by, decided_at, decision_note, created_at`

func scanAutoVersion(s interface{ Scan(...any) error }) (AutomationFlowVersion, error) {
	var v AutomationFlowVersion
	var def, prot string
	var decided sql.NullTime
	err := s.Scan(&v.FlowID, &v.ClusterID, &v.Version, &def, &v.PublishedBy, &v.Note, &v.Status, &v.Hash,
		&v.NeedsApproval, &prot, &v.Exempt, &v.RequestedByID, &v.DecidedBy, &decided, &v.DecisionNote, &v.CreatedAt)
	v.Definition, v.Protected, v.DecidedAt = json.RawMessage(def), json.RawMessage(prot), autoTimePtr(decided)
	return v, err
}

func autoJSONArray(raw json.RawMessage) string {
	if len(raw) == 0 || !json.Valid(raw) {
		return "[]"
	}
	return string(raw)
}

func (p *Postgres) CreateAutomationFlowVersion(ctx context.Context, v AutomationFlowVersion) error {
	if v.Status == "" {
		v.Status = FlowVersionApproved
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO automation_flow_versions (flow_id, cluster_id, version, definition, published_by, note, status, definition_hash,
  needs_approval, protected_steps, exempt, requested_by_id, decided_by, decided_at, decision_note)
VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,$13,$14,$15)`,
		v.FlowID, v.ClusterID, v.Version, autoJSON(v.Definition), v.PublishedBy, v.Note, v.Status, v.Hash,
		v.NeedsApproval, autoJSONArray(v.Protected), v.Exempt, v.RequestedByID, v.DecidedBy, autoTime(v.DecidedAt), v.DecisionNote)
	return err
}

func (p *Postgres) GetAutomationFlowVersion(ctx context.Context, clusterID, flowID string, version int) (*AutomationFlowVersion, error) {
	if !ValidUUID(flowID) {
		return nil, nil
	}
	v, err := scanAutoVersion(p.DB.QueryRowContext(ctx, `SELECT `+autoVersionCols+`
FROM automation_flow_versions WHERE cluster_id=$1 AND flow_id=$2 AND version=$3`, clusterID, flowID, version))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (p *Postgres) listAutoVersions(ctx context.Context, q string, args ...any) ([]AutomationFlowVersion, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT `+autoVersionCols+` FROM automation_flow_versions `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationFlowVersion
	for rows.Next() {
		v, err := scanAutoVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) ListAutomationFlowVersions(ctx context.Context, clusterID, flowID string) ([]AutomationFlowVersion, error) {
	if !ValidUUID(flowID) {
		return nil, nil
	}
	return p.listAutoVersions(ctx, `WHERE cluster_id=$1 AND flow_id=$2 ORDER BY version DESC`, clusterID, flowID)
}

func (p *Postgres) DecideAutomationFlowVersion(ctx context.Context, clusterID, flowID string, version int, d FlowVersionDecision) error {
	res, err := p.DB.ExecContext(ctx, `
UPDATE automation_flow_versions SET status=$4, decided_by=$5, decided_at=$6, decision_note=$7
WHERE cluster_id=$1 AND flow_id=$2 AND version=$3 AND status='pending'`,
		clusterID, flowID, version, d.Status, d.DecidedBy, d.DecidedAt, d.Note)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotPending
	}
	return nil
}

func (p *Postgres) ListAutomationApprovals(ctx context.Context, clusterID, status string, limit int) ([]AutomationFlowVersion, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	return p.listAutoVersions(ctx, `WHERE cluster_id=$1 AND needs_approval AND ($2 = '' OR status=$2) ORDER BY created_at DESC, version DESC LIMIT $3`,
		clusterID, status, limit)
}

const autoConnCols = `id::text, cluster_id::text, name, kind, config::text, sealed, created_by, created_at, updated_at`

func scanAutoConn(s interface{ Scan(...any) error }) (AutomationConnection, error) {
	var c AutomationConnection
	var cfg string
	err := s.Scan(&c.ID, &c.ClusterID, &c.Name, &c.Kind, &cfg, &c.Sealed, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	c.Config = json.RawMessage(cfg)
	return c, err
}

func (p *Postgres) CreateAutomationConnection(ctx context.Context, c AutomationConnection) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO automation_connections (id, cluster_id, name, kind, config, sealed, created_by) VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7)`,
		c.ID, c.ClusterID, c.Name, c.Kind, autoJSON(c.Config), c.Sealed, c.CreatedBy)
	return err
}

func (p *Postgres) GetAutomationConnection(ctx context.Context, clusterID, id string) (*AutomationConnection, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	c, err := scanAutoConn(p.DB.QueryRowContext(ctx, `SELECT `+autoConnCols+` FROM automation_connections WHERE cluster_id=$1 AND id=$2`, clusterID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (p *Postgres) ListAutomationConnections(ctx context.Context, clusterID string) ([]AutomationConnection, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT `+autoConnCols+` FROM automation_connections WHERE cluster_id=$1 ORDER BY name`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationConnection
	for rows.Next() {
		c, err := scanAutoConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateAutomationConnection(ctx context.Context, c AutomationConnection) error {
	_, err := p.DB.ExecContext(ctx, `
UPDATE automation_connections SET name=$3, kind=$4, config=$5::jsonb, sealed=$6, updated_at=now() WHERE cluster_id=$1 AND id=$2`,
		c.ClusterID, c.ID, c.Name, c.Kind, autoJSON(c.Config), c.Sealed)
	return err
}

func (p *Postgres) DeleteAutomationConnection(ctx context.Context, clusterID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM automation_connections WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	return err
}

func (p *Postgres) PutAutomationWebhook(ctx context.Context, w AutomationWebhook) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO automation_webhook_endpoints (flow_id, cluster_id, token_hash, secret_hash, rotated_at) VALUES ($1,$2,$3,$4,now())
ON CONFLICT (flow_id) DO UPDATE SET token_hash=EXCLUDED.token_hash, secret_hash=EXCLUDED.secret_hash, rotated_at=now()`,
		w.FlowID, w.ClusterID, w.TokenHash, w.SecretHash)
	return err
}

func (p *Postgres) getAutoWebhook(ctx context.Context, q string, args ...any) (*AutomationWebhook, error) {
	var w AutomationWebhook
	err := p.DB.QueryRowContext(ctx, `SELECT flow_id::text, cluster_id::text, token_hash, secret_hash, rotated_at FROM automation_webhook_endpoints `+q, args...).
		Scan(&w.FlowID, &w.ClusterID, &w.TokenHash, &w.SecretHash, &w.RotatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (p *Postgres) GetAutomationWebhook(ctx context.Context, clusterID, flowID string) (*AutomationWebhook, error) {
	return p.getAutoWebhook(ctx, `WHERE cluster_id=$1 AND flow_id=$2`, clusterID, flowID)
}

func (p *Postgres) GetAutomationWebhookByToken(ctx context.Context, tokenHash string) (*AutomationWebhook, error) {
	return p.getAutoWebhook(ctx, `WHERE token_hash=$1`, tokenHash)
}

func (p *Postgres) AddAutomationWebhookDelivery(ctx context.Context, d AutomationWebhookDelivery, keep int) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if _, err := p.DB.ExecContext(ctx, `
INSERT INTO automation_webhook_deliveries (id, cluster_id, flow_id, status, run_id, error, payload) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)`,
		d.ID, d.ClusterID, d.FlowID, d.Status, d.RunID, d.Error, autoJSON(d.Payload)); err != nil {
		return err
	}
	if keep > 0 {
		_, err := p.DB.ExecContext(ctx, `
DELETE FROM automation_webhook_deliveries WHERE flow_id=$1 AND id NOT IN (
  SELECT id FROM automation_webhook_deliveries WHERE flow_id=$1 ORDER BY created_at DESC LIMIT $2)`, d.FlowID, keep)
		return err
	}
	return nil
}

func (p *Postgres) ListAutomationWebhookDeliveries(ctx context.Context, clusterID, flowID string, limit int) ([]AutomationWebhookDelivery, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, flow_id::text, status, run_id, error, payload::text, created_at FROM automation_webhook_deliveries
WHERE cluster_id=$1 AND ($2 = '' OR flow_id::text=$2) ORDER BY created_at DESC LIMIT $3`, clusterID, flowID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationWebhookDelivery
	for rows.Next() {
		var d AutomationWebhookDelivery
		var payload string
		if err := rows.Scan(&d.ID, &d.ClusterID, &d.FlowID, &d.Status, &d.RunID, &d.Error, &payload, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Payload = json.RawMessage(payload)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (p *Postgres) PutAutomationSchedule(ctx context.Context, s AutomationSchedule) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO automation_schedules (flow_id, cluster_id, preset, cron, timezone, next_run_at, last_key) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (flow_id) DO UPDATE SET preset=EXCLUDED.preset, cron=EXCLUDED.cron, timezone=EXCLUDED.timezone,
  next_run_at=EXCLUDED.next_run_at, last_key=EXCLUDED.last_key`,
		s.FlowID, s.ClusterID, s.Preset, s.Cron, s.Timezone, autoTime(s.NextRunAt), s.LastKey)
	return err
}

func (p *Postgres) DeleteAutomationSchedule(ctx context.Context, clusterID, flowID string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM automation_schedules WHERE cluster_id=$1 AND flow_id=$2`, clusterID, flowID)
	return err
}

func (p *Postgres) ListDueAutomationSchedules(ctx context.Context, now time.Time, limit int) ([]AutomationSchedule, error) {
	if limit <= 0 {
		limit = 8
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT flow_id::text, cluster_id::text, preset, cron, timezone, next_run_at, last_key FROM automation_schedules
WHERE next_run_at IS NOT NULL AND next_run_at <= $1 ORDER BY next_run_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationSchedule
	for rows.Next() {
		var s AutomationSchedule
		var next sql.NullTime
		if err := rows.Scan(&s.FlowID, &s.ClusterID, &s.Preset, &s.Cron, &s.Timezone, &next, &s.LastKey); err != nil {
			return nil, err
		}
		s.NextRunAt = autoTimePtr(next)
		out = append(out, s)
	}
	return out, rows.Err()
}

const autoRunCols = `id::text, cluster_id::text, flow_id::text, version, mode, status, trigger_kind, trigger_payload::text, inputs::text,
definition::text, state::text, report::text, idempotency_key, correlation_id, caused_by_run, chain_depth, retry_of, actor_id, started_by,
error, wake_at, lease_owner, lease_until, started_at, finished_at, created_at, updated_at`

func scanAutoRun(s interface{ Scan(...any) error }) (AutomationRun, error) {
	var r AutomationRun
	var payload, inputs, def, state, report string
	var wake, lease, started, finished sql.NullTime
	err := s.Scan(&r.ID, &r.ClusterID, &r.FlowID, &r.Version, &r.Mode, &r.Status, &r.TriggerKind, &payload, &inputs,
		&def, &state, &report, &r.IdempotencyKey, &r.CorrelationID, &r.CausedByRun, &r.ChainDepth, &r.RetryOf, &r.ActorID, &r.StartedBy,
		&r.Error, &wake, &r.LeaseOwner, &lease, &started, &finished, &r.CreatedAt, &r.UpdatedAt)
	r.TriggerPayload, r.Inputs, r.Definition = json.RawMessage(payload), json.RawMessage(inputs), json.RawMessage(def)
	r.State, r.Report = json.RawMessage(state), json.RawMessage(report)
	r.WakeAt, r.LeaseUntil, r.StartedAt, r.FinishedAt = autoTimePtr(wake), autoTimePtr(lease), autoTimePtr(started), autoTimePtr(finished)
	return r, err
}

func (p *Postgres) CreateAutomationRun(ctx context.Context, r AutomationRun) error {
	created := r.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO automation_runs (id, cluster_id, flow_id, version, mode, status, trigger_kind, trigger_payload, inputs, definition, state, report,
  idempotency_key, correlation_id, caused_by_run, chain_depth, retry_of, actor_id, started_by, error, wake_at, started_at, finished_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,$10::jsonb,$11::jsonb,$12::jsonb,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
		r.ID, r.ClusterID, r.FlowID, r.Version, r.Mode, r.Status, r.TriggerKind, autoJSON(r.TriggerPayload), autoJSON(r.Inputs),
		autoJSON(r.Definition), autoJSON(r.State), autoJSON(r.Report), r.IdempotencyKey, r.CorrelationID, r.CausedByRun, r.ChainDepth,
		r.RetryOf, r.ActorID, r.StartedBy, r.Error, autoTime(r.WakeAt), autoTime(r.StartedAt), autoTime(r.FinishedAt), created)
	return err
}

func (p *Postgres) GetAutomationRun(ctx context.Context, clusterID, id string) (*AutomationRun, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	r, err := scanAutoRun(p.DB.QueryRowContext(ctx, `SELECT `+autoRunCols+` FROM automation_runs WHERE ($1 = '' OR cluster_id::text=$1) AND id=$2`, clusterID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (p *Postgres) UpdateAutomationRun(ctx context.Context, r AutomationRun) error {
	_, err := p.DB.ExecContext(ctx, `
UPDATE automation_runs SET status=$3, state=$4::jsonb, report=$5::jsonb, error=$6, wake_at=$7, started_at=$8, finished_at=$9,
  inputs=$10::jsonb, updated_at=now()
WHERE cluster_id=$1 AND id=$2`,
		r.ClusterID, r.ID, r.Status, autoJSON(r.State), autoJSON(r.Report), r.Error, autoTime(r.WakeAt), autoTime(r.StartedAt), autoTime(r.FinishedAt),
		autoJSON(r.Inputs))
	return err
}

func (p *Postgres) ListAutomationRuns(ctx context.Context, clusterID string, f AutomationRunFilter) ([]AutomationRun, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := p.DB.QueryContext(ctx, `SELECT `+autoRunCols+` FROM automation_runs
WHERE cluster_id=$1 AND ($2 = '' OR flow_id::text=$2) AND ($3 = '' OR status=$3) AND ($4 = '' OR mode=$4)
ORDER BY created_at DESC LIMIT $5`, clusterID, f.FlowID, f.Status, f.Mode, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		r, err := scanAutoRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) FindAutomationRunByKey(ctx context.Context, clusterID, flowID, key string, since time.Time) (*AutomationRun, error) {
	if key == "" {
		return nil, nil
	}
	r, err := scanAutoRun(p.DB.QueryRowContext(ctx, `SELECT `+autoRunCols+` FROM automation_runs
WHERE cluster_id=$1 AND flow_id=$2 AND idempotency_key=$3 AND created_at >= $4 AND mode='live' ORDER BY created_at DESC LIMIT 1`,
		clusterID, flowID, key, since))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (p *Postgres) CountAutomationRunsSince(ctx context.Context, clusterID, flowID string, since time.Time) (int, error) {
	var n int
	err := p.DB.QueryRowContext(ctx, `SELECT count(*) FROM automation_runs
WHERE cluster_id=$1 AND flow_id=$2 AND mode='live' AND trigger_kind <> 'manual' AND status <> 'dropped' AND created_at >= $3`, clusterID, flowID, since).Scan(&n)
	return n, err
}

func (p *Postgres) ListDueAutomationRuns(ctx context.Context, now time.Time, limit int) ([]AutomationRun, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := p.DB.QueryContext(ctx, `SELECT `+autoRunCols+` FROM automation_runs
WHERE status IN ('pending', 'running', 'waiting') AND (lease_until IS NULL OR lease_until <= $1)
  AND (status <> 'waiting' OR (wake_at IS NOT NULL AND wake_at <= $1))
ORDER BY created_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationRun
	for rows.Next() {
		r, err := scanAutoRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClaimAutomationRun locks the row (SELECT ... FOR UPDATE inside one
// UPDATE) and takes the lease only when it is free, expired or already ours.
func (p *Postgres) ClaimAutomationRun(ctx context.Context, clusterID, id, owner string, now, until time.Time) (bool, error) {
	res, err := p.DB.ExecContext(ctx, `
UPDATE automation_runs SET lease_owner=$3, lease_until=$5
WHERE id = (SELECT id FROM automation_runs WHERE cluster_id=$1 AND id=$2 FOR UPDATE)
  AND (lease_owner = '' OR lease_owner = $3 OR lease_until IS NULL OR lease_until <= $4)`,
		clusterID, id, owner, now, until)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (p *Postgres) ReleaseAutomationRun(ctx context.Context, clusterID, id, owner string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE automation_runs SET lease_owner='', lease_until=NULL WHERE cluster_id=$1 AND id=$2 AND lease_owner=$3`,
		clusterID, id, owner)
	return err
}

func (p *Postgres) UpsertAutomationNodeRun(ctx context.Context, n AutomationNodeRun) error {
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO automation_node_runs (id, cluster_id, run_id, node_id, scope, type, status, port, input, output, error, attempts, started_at, finished_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10::jsonb,$11,$12,$13,$14)
ON CONFLICT (run_id, scope, node_id) DO UPDATE SET type=EXCLUDED.type, status=EXCLUDED.status, port=EXCLUDED.port,
  input=CASE WHEN EXCLUDED.input = '{}'::jsonb THEN automation_node_runs.input ELSE EXCLUDED.input END,
  output=EXCLUDED.output, error=EXCLUDED.error, attempts=EXCLUDED.attempts, started_at=EXCLUDED.started_at, finished_at=EXCLUDED.finished_at`,
		n.ID, n.ClusterID, n.RunID, n.NodeID, n.Scope, n.Type, n.Status, n.Port, autoJSON(n.Input), autoJSON(n.Output), n.Error, n.Attempts,
		autoTime(n.StartedAt), autoTime(n.FinishedAt))
	return err
}

func (p *Postgres) ListAutomationNodeRuns(ctx context.Context, clusterID, runID string) ([]AutomationNodeRun, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, run_id::text, node_id, scope, type, status, port, input::text, output::text, error, attempts, started_at, finished_at
FROM automation_node_runs WHERE cluster_id=$1 AND run_id=$2 ORDER BY started_at NULLS LAST, node_id`, clusterID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationNodeRun
	for rows.Next() {
		var n AutomationNodeRun
		var in, outp string
		var started, finished sql.NullTime
		if err := rows.Scan(&n.ID, &n.ClusterID, &n.RunID, &n.NodeID, &n.Scope, &n.Type, &n.Status, &n.Port, &in, &outp, &n.Error, &n.Attempts, &started, &finished); err != nil {
			return nil, err
		}
		n.Input, n.Output = json.RawMessage(in), json.RawMessage(outp)
		n.StartedAt, n.FinishedAt = autoTimePtr(started), autoTimePtr(finished)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (p *Postgres) AddAutomationNodeAttempt(ctx context.Context, a AutomationNodeAttempt) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO automation_node_attempts (id, cluster_id, run_id, node_run_id, attempt, status, error, started_at, finished_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (id) DO UPDATE SET status=EXCLUDED.status, error=EXCLUDED.error, finished_at=EXCLUDED.finished_at`,
		a.ID, a.ClusterID, a.RunID, autoUUID(a.NodeRunID), a.Attempt, a.Status, a.Error, a.StartedAt, autoTime(a.FinishedAt))
	return err
}

func (p *Postgres) ListAutomationNodeAttempts(ctx context.Context, clusterID, runID string) ([]AutomationNodeAttempt, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, run_id::text, node_run_id::text, attempt, status, error, started_at, finished_at
FROM automation_node_attempts WHERE cluster_id=$1 AND run_id=$2 ORDER BY started_at`, clusterID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationNodeAttempt
	for rows.Next() {
		var a AutomationNodeAttempt
		var finished sql.NullTime
		if err := rows.Scan(&a.ID, &a.ClusterID, &a.RunID, &a.NodeRunID, &a.Attempt, &a.Status, &a.Error, &a.StartedAt, &finished); err != nil {
			return nil, err
		}
		a.FinishedAt = autoTimePtr(finished)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) AddAutomationRunLog(ctx context.Context, l AutomationRunLog) error {
	if l.ID == "" {
		l.ID = uuid.NewString()
	}
	at := l.CreatedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO automation_run_logs (id, cluster_id, run_id, node_id, level, message, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		l.ID, l.ClusterID, l.RunID, l.NodeID, l.Level, l.Message, at)
	return err
}

func (p *Postgres) ListAutomationRunLogs(ctx context.Context, clusterID, runID string, limit int) ([]AutomationRunLog, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, run_id::text, node_id, level, message, created_at FROM (
  SELECT * FROM automation_run_logs WHERE cluster_id=$1 AND run_id=$2 ORDER BY created_at DESC LIMIT $3
) newest ORDER BY created_at`, clusterID, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationRunLog
	for rows.Next() {
		var l AutomationRunLog
		if err := rows.Scan(&l.ID, &l.ClusterID, &l.RunID, &l.NodeID, &l.Level, &l.Message, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (p *Postgres) AddAutomationLedger(ctx context.Context, e AutomationLedgerEntry) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	at := e.CreatedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO automation_action_ledger (id, cluster_id, action, target, flow_id, run_id, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		e.ID, e.ClusterID, e.Action, e.Target, e.FlowID, e.RunID, at)
	return err
}

func (p *Postgres) CountAutomationLedger(ctx context.Context, clusterID, action, target string, since time.Time) (int, error) {
	var n int
	err := p.DB.QueryRowContext(ctx, `SELECT count(*) FROM automation_action_ledger WHERE cluster_id=$1 AND action=$2 AND target=$3 AND created_at >= $4`,
		clusterID, action, target, since).Scan(&n)
	return n, err
}
