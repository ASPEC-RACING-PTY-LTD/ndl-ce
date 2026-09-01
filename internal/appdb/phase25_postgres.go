package appdb

import (
	"context"
	"database/sql"
	"errors"
)

func (p *Postgres) UpsertLVMVG(ctx context.Context, v LVMVG) error {
	if v.ThinPool == "" {
		v.ThinPool = "thinpool"
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO lvm_vgs (pool_id, vg_uuid, vg_name, thin_pool) VALUES ($1,$2,$3,$4)
ON CONFLICT (pool_id) DO UPDATE SET vg_uuid=EXCLUDED.vg_uuid, vg_name=EXCLUDED.vg_name, thin_pool=EXCLUDED.thin_pool`,
		v.PoolID, v.VGUUID, v.VGName, v.ThinPool)
	return err
}

func (p *Postgres) GetLVMVG(ctx context.Context, poolID string) (*LVMVG, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT pool_id::text, vg_uuid, vg_name, thin_pool FROM lvm_vgs WHERE pool_id=$1`, poolID)
	var v LVMVG
	if err := row.Scan(&v.PoolID, &v.VGUUID, &v.VGName, &v.ThinPool); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (p *Postgres) GetLVMVGByUUID(ctx context.Context, vgUUID string) (*LVMVG, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT pool_id::text, vg_uuid, vg_name, thin_pool FROM lvm_vgs WHERE vg_uuid=$1`, vgUUID)
	var v LVMVG
	if err := row.Scan(&v.PoolID, &v.VGUUID, &v.VGName, &v.ThinPool); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (p *Postgres) UpsertLVMLV(ctx context.Context, lv LVMLV) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO lvm_lvs (volume_id, lv_uuid, lv_name) VALUES ($1,$2,$3)
ON CONFLICT (volume_id) DO UPDATE SET lv_uuid=EXCLUDED.lv_uuid, lv_name=EXCLUDED.lv_name`,
		lv.VolumeID, lv.LVUUID, lv.LVName)
	return err
}

func (p *Postgres) GetLVMLV(ctx context.Context, volumeID string) (*LVMLV, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT volume_id::text, lv_uuid, lv_name FROM lvm_lvs WHERE volume_id=$1`, volumeID)
	var lv LVMLV
	if err := row.Scan(&lv.VolumeID, &lv.LVUUID, &lv.LVName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &lv, nil
}
