package appdb

import (
	"context"
	"database/sql"
	"errors"
)

func (p *Postgres) UpsertZFSPool(ctx context.Context, z ZFSPool) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO zfs_pools (pool_id, zpool_guid, zpool_name) VALUES ($1,$2,$3)
ON CONFLICT (pool_id) DO UPDATE SET zpool_guid=EXCLUDED.zpool_guid, zpool_name=EXCLUDED.zpool_name`,
		z.PoolID, z.ZPoolGUID, z.ZPoolName)
	return err
}

func (p *Postgres) GetZFSPool(ctx context.Context, poolID string) (*ZFSPool, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT pool_id::text, zpool_guid, zpool_name FROM zfs_pools WHERE pool_id=$1`, poolID)
	var z ZFSPool
	if err := row.Scan(&z.PoolID, &z.ZPoolGUID, &z.ZPoolName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &z, nil
}

func (p *Postgres) GetZFSPoolByGUID(ctx context.Context, guid string) (*ZFSPool, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT pool_id::text, zpool_guid, zpool_name FROM zfs_pools WHERE zpool_guid=$1`, guid)
	var z ZFSPool
	if err := row.Scan(&z.PoolID, &z.ZPoolGUID, &z.ZPoolName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &z, nil
}

func (p *Postgres) UpsertZFSDataset(ctx context.Context, d ZFSDataset) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO zfs_datasets (volume_id, dataset_guid, dataset_name) VALUES ($1,$2,$3)
ON CONFLICT (volume_id) DO UPDATE SET dataset_guid=EXCLUDED.dataset_guid, dataset_name=EXCLUDED.dataset_name`,
		d.VolumeID, d.DatasetGUID, d.DatasetName)
	return err
}

func (p *Postgres) GetZFSDataset(ctx context.Context, volumeID string) (*ZFSDataset, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT volume_id::text, dataset_guid, dataset_name FROM zfs_datasets WHERE volume_id=$1`, volumeID)
	var d ZFSDataset
	if err := row.Scan(&d.VolumeID, &d.DatasetGUID, &d.DatasetName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}
