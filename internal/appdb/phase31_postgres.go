package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (p *Postgres) CreateNodeGroup(ctx context.Context, g NodeGroup) error {
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO node_groups (id, cluster_id, name, created_at) VALUES ($1,$2,$3,$4)`,
		g.ID, g.ClusterID, g.Name, g.CreatedAt)
	return err
}

func (p *Postgres) ListNodeGroups(ctx context.Context, clusterID string) ([]NodeGroup, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id::text, cluster_id::text, name, created_at FROM node_groups WHERE cluster_id=$1 ORDER BY name, id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeGroup
	for rows.Next() {
		var g NodeGroup
		if err := rows.Scan(&g.ID, &g.ClusterID, &g.Name, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (p *Postgres) GetNodeGroup(ctx context.Context, clusterID, id string) (*NodeGroup, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id::text, cluster_id::text, name, created_at FROM node_groups WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var g NodeGroup
	if err := row.Scan(&g.ID, &g.ClusterID, &g.Name, &g.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &g, nil
}

func (p *Postgres) AddNodeGroupMember(ctx context.Context, clusterID, groupID, nodeID string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO node_group_members (group_id, node_id)
SELECT $1, $2
WHERE EXISTS (SELECT 1 FROM node_groups WHERE id=$1 AND cluster_id=$3)
ON CONFLICT DO NOTHING`, groupID, nodeID, clusterID)
	return err
}

func (p *Postgres) ListNodeGroupMembers(ctx context.Context, clusterID, groupID string) ([]string, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT m.node_id::text FROM node_group_members m
JOIN node_groups g ON g.id = m.group_id
WHERE g.cluster_id=$1 AND m.group_id=$2`, clusterID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (p *Postgres) SetNodeMaintenance(ctx context.Context, row NodeMaintenance) error {
	if row.Since.IsZero() {
		row.Since = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO node_maintenance (node_id, cluster_id, since, reason)
VALUES ($1,$2,$3,$4)
ON CONFLICT (node_id) DO UPDATE SET since = EXCLUDED.since, reason = EXCLUDED.reason`,
		row.NodeID, row.ClusterID, row.Since, row.Reason)
	return err
}

func (p *Postgres) ClearNodeMaintenance(ctx context.Context, clusterID, nodeID string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM node_maintenance WHERE cluster_id=$1 AND node_id=$2`, clusterID, nodeID)
	return err
}

func (p *Postgres) GetNodeMaintenance(ctx context.Context, clusterID, nodeID string) (*NodeMaintenance, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT node_id::text, cluster_id::text, since, reason FROM node_maintenance WHERE cluster_id=$1 AND node_id=$2`, clusterID, nodeID)
	var m NodeMaintenance
	if err := row.Scan(&m.NodeID, &m.ClusterID, &m.Since, &m.Reason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

func (p *Postgres) ListNodeMaintenance(ctx context.Context, clusterID string) ([]NodeMaintenance, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT node_id::text, cluster_id::text, since, reason FROM node_maintenance WHERE cluster_id=$1`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeMaintenance
	for rows.Next() {
		var m NodeMaintenance
		if err := rows.Scan(&m.NodeID, &m.ClusterID, &m.Since, &m.Reason); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *Postgres) UpsertWorkloadPlacement(ctx context.Context, pl WorkloadPlacement) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO workload_placements (workload_id, cluster_id, mode, node_group_id, require_gpu, require_storage_class, affinity_workload_id, anti_affinity_workload_id, priority)
VALUES ($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid,$9)
ON CONFLICT (workload_id) DO UPDATE SET
  mode = EXCLUDED.mode,
  node_group_id = EXCLUDED.node_group_id,
  require_gpu = EXCLUDED.require_gpu,
  require_storage_class = EXCLUDED.require_storage_class,
  affinity_workload_id = EXCLUDED.affinity_workload_id,
  anti_affinity_workload_id = EXCLUDED.anti_affinity_workload_id,
  priority = EXCLUDED.priority`,
		pl.WorkloadID, pl.ClusterID, pl.Mode, pl.NodeGroupID, pl.RequireGPU, pl.RequireStorageClass,
		pl.AffinityWorkloadID, pl.AntiAffinityWorkloadID, pl.Priority)
	return err
}

func (p *Postgres) GetWorkloadPlacement(ctx context.Context, clusterID, workloadID string) (*WorkloadPlacement, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT workload_id::text, cluster_id::text, mode, COALESCE(node_group_id::text, ''), require_gpu, require_storage_class,
       COALESCE(affinity_workload_id::text, ''), COALESCE(anti_affinity_workload_id::text, ''), priority
FROM workload_placements WHERE cluster_id=$1 AND workload_id=$2`, clusterID, workloadID)
	var pl WorkloadPlacement
	if err := row.Scan(&pl.WorkloadID, &pl.ClusterID, &pl.Mode, &pl.NodeGroupID, &pl.RequireGPU, &pl.RequireStorageClass,
		&pl.AffinityWorkloadID, &pl.AntiAffinityWorkloadID, &pl.Priority); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &pl, nil
}
