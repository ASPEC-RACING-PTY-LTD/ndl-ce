package appdb

import (
	"context"
	"database/sql"
	"errors"
)

func (p *Postgres) UpsertDistributedPool(ctx context.Context, d DistributedPool) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO distributed_pools (pool_id, cluster_id, locator, ceph_pool, ceph_user, fsid)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (pool_id) DO UPDATE SET locator=EXCLUDED.locator, ceph_pool=EXCLUDED.ceph_pool, ceph_user=EXCLUDED.ceph_user, fsid=EXCLUDED.fsid`,
		d.PoolID, d.ClusterID, d.Locator, d.CephPool, d.CephUser, d.FSID)
	return err
}

func (p *Postgres) GetDistributedPool(ctx context.Context, poolID string) (*DistributedPool, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT pool_id::text, cluster_id::text, locator, ceph_pool, ceph_user, fsid
FROM distributed_pools WHERE pool_id=$1`, poolID)
	var d DistributedPool
	if err := row.Scan(&d.PoolID, &d.ClusterID, &d.Locator, &d.CephPool, &d.CephUser, &d.FSID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

func (p *Postgres) UpsertDistributedSecret(ctx context.Context, poolID, cephxKey string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO secrets.distributed_credentials (pool_id, cluster_id, cephx_key, updated_at)
SELECT $1, p.cluster_id, $2, now() FROM storage_pools p WHERE p.id=$1
ON CONFLICT (pool_id) DO UPDATE SET cephx_key=EXCLUDED.cephx_key, updated_at=now()`,
		poolID, cephxKey)
	return err
}

func (p *Postgres) DistributedSecret(ctx context.Context, poolID string) (string, error) {
	var key string
	err := p.DB.QueryRowContext(ctx, `SELECT cephx_key FROM secrets.distributed_credentials WHERE pool_id=$1`, poolID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return key, err
}

func (p *Postgres) CreateDistributedOSD(ctx context.Context, o DistributedOSD) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO distributed_osds (id, cluster_id, node_id, pool_id, disk, osd_id, status, reason, created_at, updated_at)
VALUES ($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,now(),now())`,
		o.ID, o.ClusterID, o.NodeID, o.PoolID, o.Disk, o.OSDNumber, o.Status, o.Reason)
	return err
}

func (p *Postgres) ListDistributedOSDs(ctx context.Context, clusterID string) ([]DistributedOSD, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, node_id::text, COALESCE(pool_id::text, ''), disk, osd_id, status, reason, created_at, updated_at
FROM distributed_osds WHERE cluster_id=$1 ORDER BY created_at, id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DistributedOSD
	for rows.Next() {
		var o DistributedOSD
		if err := rows.Scan(&o.ID, &o.ClusterID, &o.NodeID, &o.PoolID, &o.Disk, &o.OSDNumber, &o.Status, &o.Reason, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateDistributedOSD(ctx context.Context, o DistributedOSD) error {
	_, err := p.DB.ExecContext(ctx, `
UPDATE distributed_osds SET status=$2, reason=$3, osd_id=$4, updated_at=now() WHERE id=$1`,
		o.ID, o.Status, o.Reason, o.OSDNumber)
	return err
}
