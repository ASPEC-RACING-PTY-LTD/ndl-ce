package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) DeleteVolume(ctx context.Context, clusterID, volumeID string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM volumes WHERE cluster_id=$1 AND id=$2`, clusterID, volumeID)
	return err
}

func (p *Postgres) CreateVMTemplate(ctx context.Context, t VMTemplate) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if len(t.SpecJSON) == 0 {
		t.SpecJSON = json.RawMessage(`{}`)
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO vm_templates (id, cluster_id, name, source_workload_id, snapshot_id, spec_json, created_at)
VALUES ($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,$7)`,
		t.ID, t.ClusterID, t.Name, t.SourceWorkloadID, t.SnapshotID, t.SpecJSON, t.CreatedAt)
	return err
}

func (p *Postgres) ListVMTemplates(ctx context.Context, clusterID string) ([]VMTemplate, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, COALESCE(source_workload_id::text, ''), COALESCE(snapshot_id::text, ''), spec_json, created_at
FROM vm_templates WHERE cluster_id=$1 ORDER BY name`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VMTemplate
	for rows.Next() {
		var t VMTemplate
		if err := rows.Scan(&t.ID, &t.ClusterID, &t.Name, &t.SourceWorkloadID, &t.SnapshotID, &t.SpecJSON, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Postgres) GetVMTemplate(ctx context.Context, clusterID, id string) (*VMTemplate, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, COALESCE(source_workload_id::text, ''), COALESCE(snapshot_id::text, ''), spec_json, created_at
FROM vm_templates WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var t VMTemplate
	if err := row.Scan(&t.ID, &t.ClusterID, &t.Name, &t.SourceWorkloadID, &t.SnapshotID, &t.SpecJSON, &t.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (p *Postgres) CreateUSBAttachment(ctx context.Context, a USBAttachment) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO usb_attachments (id, cluster_id, workload_id, address, vendor, product, exclusive, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		a.ID, a.ClusterID, a.WorkloadID, a.Address, a.Vendor, a.Product, a.Exclusive, a.CreatedAt)
	return err
}

func (p *Postgres) ListUSBAttachments(ctx context.Context, clusterID, workloadID string) ([]USBAttachment, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, workload_id::text, address, vendor, product, exclusive, created_at
FROM usb_attachments WHERE cluster_id=$1 AND ($2='' OR workload_id::text=$2)
ORDER BY address`, clusterID, workloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []USBAttachment
	for rows.Next() {
		var a USBAttachment
		if err := rows.Scan(&a.ID, &a.ClusterID, &a.WorkloadID, &a.Address, &a.Vendor, &a.Product, &a.Exclusive, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteUSBAttachment(ctx context.Context, clusterID, id string) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM usb_attachments WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("usb attachment not found")
	}
	return nil
}
