package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) CreateStack(ctx context.Context, s Stack) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	s.UpdatedAt = s.CreatedAt
	if s.Status == "" {
		s.Status = StackStatusDraft
	}
	if len(s.DesiredJSON) == 0 {
		s.DesiredJSON = json.RawMessage(`{}`)
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO stacks (id, cluster_id, name, status, desired_json, source_compose, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		s.ID, s.ClusterID, s.Name, s.Status, []byte(s.DesiredJSON), s.SourceCompose, s.CreatedAt, s.UpdatedAt)
	return err
}

func (p *Postgres) ListStacks(ctx context.Context, clusterID string) ([]Stack, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, status, desired_json, COALESCE(source_compose, ''), created_at, updated_at
FROM stacks WHERE cluster_id=$1 ORDER BY created_at ASC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stack
	for rows.Next() {
		s, err := scanStack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) GetStack(ctx context.Context, clusterID, id string) (*Stack, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, status, desired_json, COALESCE(source_compose, ''), created_at, updated_at
FROM stacks WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	s, err := scanStack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (p *Postgres) UpdateStack(ctx context.Context, s Stack) error {
	cur, err := p.GetStack(ctx, s.ClusterID, s.ID)
	if err != nil {
		return err
	}
	if cur == nil {
		return errors.New("stack not found")
	}
	name := cur.Name
	if s.Name != "" {
		name = s.Name
	}
	status := cur.Status
	if s.Status != "" {
		status = s.Status
	}
	desired := cur.DesiredJSON
	if len(s.DesiredJSON) > 0 {
		desired = s.DesiredJSON
	}
	source := cur.SourceCompose
	if s.SourceCompose != "" {
		source = s.SourceCompose
	}
	_, err = p.DB.ExecContext(ctx, `
UPDATE stacks SET name=$3, status=$4, desired_json=$5, source_compose=$6, updated_at=$7
WHERE cluster_id=$1 AND id=$2`,
		s.ClusterID, s.ID, name, status, []byte(desired), source, time.Now().UTC())
	return err
}

func (p *Postgres) DeleteStack(ctx context.Context, clusterID, id string) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM stacks WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("stack not found")
	}
	return nil
}

func (p *Postgres) CreateStackMember(ctx context.Context, mem StackMember) error {
	if mem.ID == "" {
		mem.ID = uuid.NewString()
	}
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = time.Now().UTC()
	}
	mem.UpdatedAt = mem.CreatedAt
	if mem.Status == "" {
		mem.Status = MemberStatusPending
	}
	if len(mem.DesiredJSON) == 0 {
		mem.DesiredJSON = json.RawMessage(`{}`)
	}
	var workload any
	if mem.WorkloadID != "" {
		workload = mem.WorkloadID
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO stack_members (id, cluster_id, stack_id, service_name, workload_id, desired_json, status, sort_order, reason, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		mem.ID, mem.ClusterID, mem.StackID, mem.ServiceName, workload, []byte(mem.DesiredJSON),
		mem.Status, mem.SortOrder, mem.Reason, mem.CreatedAt, mem.UpdatedAt)
	return err
}

func (p *Postgres) ListStackMembers(ctx context.Context, clusterID, stackID string) ([]StackMember, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, stack_id::text, service_name, COALESCE(workload_id::text, ''), desired_json,
       status, sort_order, COALESCE(reason, ''), created_at, updated_at
FROM stack_members WHERE cluster_id=$1 AND stack_id=$2 ORDER BY sort_order ASC, service_name ASC`, clusterID, stackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StackMember
	for rows.Next() {
		mem, err := scanStackMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, mem)
	}
	return out, rows.Err()
}

func (p *Postgres) GetStackMember(ctx context.Context, clusterID, id string) (*StackMember, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, stack_id::text, service_name, COALESCE(workload_id::text, ''), desired_json,
       status, sort_order, COALESCE(reason, ''), created_at, updated_at
FROM stack_members WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	mem, err := scanStackMember(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &mem, nil
}

func (p *Postgres) GetStackMemberByService(ctx context.Context, clusterID, stackID, service string) (*StackMember, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, stack_id::text, service_name, COALESCE(workload_id::text, ''), desired_json,
       status, sort_order, COALESCE(reason, ''), created_at, updated_at
FROM stack_members WHERE cluster_id=$1 AND stack_id=$2 AND service_name=$3`, clusterID, stackID, service)
	mem, err := scanStackMember(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &mem, nil
}

func (p *Postgres) UpdateStackMember(ctx context.Context, mem StackMember) error {
	cur, err := p.GetStackMember(ctx, mem.ClusterID, mem.ID)
	if err != nil {
		return err
	}
	if cur == nil {
		return errors.New("stack member not found")
	}
	workload := cur.WorkloadID
	if mem.WorkloadID != "" {
		workload = mem.WorkloadID
	}
	desired := cur.DesiredJSON
	if len(mem.DesiredJSON) > 0 {
		desired = mem.DesiredJSON
	}
	status := cur.Status
	if mem.Status != "" {
		status = mem.Status
	}
	var wl any
	if workload != "" {
		wl = workload
	}
	_, err = p.DB.ExecContext(ctx, `
UPDATE stack_members SET workload_id=$3, desired_json=$4, status=$5, reason=$6, updated_at=$7
WHERE cluster_id=$1 AND id=$2`,
		mem.ClusterID, mem.ID, wl, []byte(desired), status, mem.Reason, time.Now().UTC())
	return err
}

type stackScanner interface {
	Scan(dest ...any) error
}

func scanStack(s stackScanner) (Stack, error) {
	var row Stack
	var desired []byte
	err := s.Scan(&row.ID, &row.ClusterID, &row.Name, &row.Status, &desired, &row.SourceCompose, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return row, err
	}
	row.DesiredJSON = json.RawMessage(desired)
	return row, nil
}

func scanStackMember(s stackScanner) (StackMember, error) {
	var row StackMember
	var desired []byte
	err := s.Scan(&row.ID, &row.ClusterID, &row.StackID, &row.ServiceName, &row.WorkloadID, &desired,
		&row.Status, &row.SortOrder, &row.Reason, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return row, err
	}
	row.DesiredJSON = json.RawMessage(desired)
	return row, nil
}
