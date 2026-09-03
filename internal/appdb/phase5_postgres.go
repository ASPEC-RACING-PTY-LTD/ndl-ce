package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (p *Postgres) GetOperationByIdempotency(ctx context.Context, clusterID, key string) (*Operation, error) {
	if key == "" {
		return nil, nil
	}
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(node_id::text, ''), kind, state,
       COALESCE(idempotency_key, ''), progress, COALESCE(stage, ''), COALESCE(message, ''),
       created_at, COALESCE(updated_at, created_at)
FROM operations
WHERE cluster_id=$1 AND idempotency_key=$2`, clusterID, key)
	var op Operation
	var progress sql.NullInt64
	if err := row.Scan(&op.ID, &op.ClusterID, &op.NodeID, &op.Kind, &op.State,
		&op.IdempotencyKey, &progress, &op.Stage, &op.Message, &op.CreatedAt, &op.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if progress.Valid {
		n := int(progress.Int64)
		op.Progress = &n
	}
	return &op, nil
}

func (p *Postgres) CreateWorkload(ctx context.Context, w Workload) error {
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}
	w.UpdatedAt = w.CreatedAt
	if w.UIDMap == "" {
		w.UIDMap = "u 0 100000 65536"
	}
	if w.GIDMap == "" {
		w.GIDMap = "g 0 100000 65536"
	}
	if len(w.Devices) == 0 {
		w.Devices = json.RawMessage(`[]`)
	}
	if len(w.MigrateBlockers) == 0 {
		w.MigrateBlockers = json.RawMessage(`[]`)
	}
	warn, _ := json.Marshal(w.Warnings)
	if warn == nil {
		warn = []byte("[]")
	}
	if len(w.SpecJSON) == 0 {
		w.SpecJSON = json.RawMessage(`{}`)
	}
	if len(w.AppliedJSON) == 0 {
		w.AppliedJSON = json.RawMessage(`{}`)
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO workloads (
  id, cluster_id, node_id, owner_node_id, desired_node_id, name, kind, status, reason,
  desired_power, image_pin, image_verified, cpus, memory_bytes, privileged, uid_map, gid_map,
  pid, unit_active, migrate_ready, migrate_blockers, devices, warnings, created_at, updated_at,
  spec_json, applied_json, autostart, pending_restart, firmware, ownership_epoch
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31
)`,
		w.ID, w.ClusterID, w.NodeID, w.OwnerNodeID, w.DesiredNodeID, w.Name, w.Kind, w.Status, w.Reason,
		w.DesiredPower, w.ImagePin, w.ImageVerified, w.CPUs, w.MemoryBytes, w.Privileged, w.UIDMap, w.GIDMap,
		w.PID, w.UnitActive, w.MigrateReady, w.MigrateBlockers, w.Devices, warn, w.CreatedAt, w.UpdatedAt,
		w.SpecJSON, w.AppliedJSON, w.Autostart, w.PendingRestart, w.Firmware, w.OwnershipEpoch)
	return err
}

func (p *Postgres) ListWorkloads(ctx context.Context, clusterID string) ([]Workload, error) {
	rows, err := p.DB.QueryContext(ctx, workloadSelect+` WHERE cluster_id=$1 ORDER BY created_at, id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workload
	for rows.Next() {
		w, err := scanWorkload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (p *Postgres) GetWorkload(ctx context.Context, clusterID, id string) (*Workload, error) {
	row := p.DB.QueryRowContext(ctx, workloadSelect+` WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	w, err := scanWorkload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (p *Postgres) GetWorkloadByName(ctx context.Context, clusterID, name string) (*Workload, error) {
	row := p.DB.QueryRowContext(ctx, workloadSelect+` WHERE cluster_id=$1 AND name=$2`, clusterID, name)
	w, err := scanWorkload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (p *Postgres) GetWorkloadByIdempotency(ctx context.Context, clusterID, key string) (*Workload, error) {
	op, err := p.GetOperationByIdempotency(ctx, clusterID, key)
	if err != nil || op == nil || op.Message == "" {
		return nil, err
	}
	var payload struct {
		WorkloadID string `json:"workload_id"`
	}
	if json.Unmarshal([]byte(op.Message), &payload) != nil || payload.WorkloadID == "" {
		return nil, nil
	}
	return p.GetWorkload(ctx, clusterID, payload.WorkloadID)
}

func (p *Postgres) UpdateWorkloadObserved(ctx context.Context, w Workload) error {
	warn, _ := json.Marshal(w.Warnings)
	if warn == nil {
		warn = []byte("[]")
	}
	if len(w.MigrateBlockers) == 0 {
		w.MigrateBlockers = json.RawMessage(`[]`)
	}
	_, err := p.DB.ExecContext(ctx, `
UPDATE workloads SET status=$2, reason=$3, pid=$4, unit_active=$5, image_verified=$6,
  warnings=$7, migrate_ready=$8, migrate_blockers=$9, updated_at=now()
WHERE id=$1`, w.ID, w.Status, w.Reason, w.PID, w.UnitActive, w.ImageVerified,
		warn, w.MigrateReady, w.MigrateBlockers)
	return err
}

func (p *Postgres) UpdateWorkloadSpec(ctx context.Context, w Workload) error {
	spec := w.SpecJSON
	if len(spec) == 0 {
		spec = nil
	}
	applied := w.AppliedJSON
	if len(applied) == 0 {
		applied = nil
	}
	_, err := p.DB.ExecContext(ctx, `
UPDATE workloads SET cpus=COALESCE(NULLIF($2,0), cpus),
  memory_bytes=COALESCE(NULLIF($3,0), memory_bytes),
  desired_power=COALESCE(NULLIF($4,''), desired_power),
  spec_json=COALESCE($5, spec_json),
  applied_json=COALESCE($6, applied_json),
  autostart=$7,
  pending_restart=$8,
  firmware=COALESCE(NULLIF($9,''), firmware),
  updated_at=now()
WHERE id=$1`, w.ID, w.CPUs, w.MemoryBytes, w.DesiredPower, spec, applied, w.Autostart, w.PendingRestart, w.Firmware)
	return err
}

func (p *Postgres) CreateWorkloadDisk(ctx context.Context, d WorkloadDisk) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if d.Role == "" {
		d.Role = "root"
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO workload_disks (id, cluster_id, workload_id, volume_id, role, slot, bus_addr, read_only, format, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, d.ID, d.ClusterID, d.WorkloadID, d.VolumeID, d.Role, d.Slot, d.BusAddr, d.ReadOnly, d.Format, d.CreatedAt)
	return err
}

func (p *Postgres) ListWorkloadDisks(ctx context.Context, clusterID, workloadID string) ([]WorkloadDisk, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, workload_id::text, volume_id::text, role, slot, COALESCE(bus_addr,''), read_only, COALESCE(format,''), created_at
FROM workload_disks WHERE cluster_id=$1 AND ($2='' OR workload_id::text=$2)
ORDER BY slot, created_at, id`, clusterID, workloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkloadDisk
	for rows.Next() {
		var d WorkloadDisk
		if err := rows.Scan(&d.ID, &d.ClusterID, &d.WorkloadID, &d.VolumeID, &d.Role, &d.Slot, &d.BusAddr, &d.ReadOnly, &d.Format, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateWorkloadNIC(ctx context.Context, n WorkloadNIC) error {
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO workload_nics (id, cluster_id, workload_id, network_id, mac, ipv4, pci_addr, model, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, n.ID, n.ClusterID, n.WorkloadID, n.NetworkID, n.MAC, n.IPv4, n.PCIAddr, n.Model, n.CreatedAt)
	return err
}

func (p *Postgres) ListWorkloadNICs(ctx context.Context, clusterID, workloadID string) ([]WorkloadNIC, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, workload_id::text, network_id::text, mac, ipv4, COALESCE(pci_addr,''), COALESCE(model,''), created_at
FROM workload_nics WHERE cluster_id=$1 AND ($2='' OR workload_id::text=$2)
ORDER BY created_at, id`, clusterID, workloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkloadNIC
	for rows.Next() {
		var n WorkloadNIC
		if err := rows.Scan(&n.ID, &n.ClusterID, &n.WorkloadID, &n.NetworkID, &n.MAC, &n.IPv4, &n.PCIAddr, &n.Model, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateWorkloadNIC(ctx context.Context, n WorkloadNIC) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE workload_nics SET ipv4=$2, pci_addr=COALESCE(NULLIF($3,''), pci_addr), model=COALESCE(NULLIF($4,''), model) WHERE id=$1`, n.ID, n.IPv4, n.PCIAddr, n.Model)
	return err
}

const workloadSelect = `
SELECT id::text, cluster_id::text, node_id::text, owner_node_id::text, desired_node_id::text,
       name, kind, status, reason, desired_power, image_pin, image_verified, cpus, memory_bytes,
       privileged, uid_map, gid_map, pid, unit_active, migrate_ready, migrate_blockers, devices,
       warnings, created_at, updated_at,
       COALESCE(spec_json, '{}'::jsonb), COALESCE(applied_json, '{}'::jsonb), autostart, pending_restart, COALESCE(firmware, 'bios'),
       COALESCE(ownership_epoch, 0)
FROM workloads`

func scanWorkload(row rowScanner) (Workload, error) {
	var w Workload
	var pid sql.NullInt64
	var blockers, devices, warn, specJSON, appliedJSON []byte
	err := row.Scan(&w.ID, &w.ClusterID, &w.NodeID, &w.OwnerNodeID, &w.DesiredNodeID,
		&w.Name, &w.Kind, &w.Status, &w.Reason, &w.DesiredPower, &w.ImagePin, &w.ImageVerified,
		&w.CPUs, &w.MemoryBytes, &w.Privileged, &w.UIDMap, &w.GIDMap, &pid, &w.UnitActive,
		&w.MigrateReady, &blockers, &devices, &warn, &w.CreatedAt, &w.UpdatedAt,
		&specJSON, &appliedJSON, &w.Autostart, &w.PendingRestart, &w.Firmware, &w.OwnershipEpoch)
	if err != nil {
		return w, err
	}
	if pid.Valid {
		n := int(pid.Int64)
		w.PID = &n
	}
	w.MigrateBlockers = json.RawMessage(blockers)
	w.Devices = json.RawMessage(devices)
	w.SpecJSON = json.RawMessage(specJSON)
	w.AppliedJSON = json.RawMessage(appliedJSON)
	if len(warn) > 0 {
		_ = json.Unmarshal(warn, &w.Warnings)
	}
	return w, nil
}
