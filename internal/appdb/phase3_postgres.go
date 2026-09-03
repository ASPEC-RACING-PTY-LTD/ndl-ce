package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (p *Postgres) CreateStoragePool(ctx context.Context, pool StoragePool) error {
	if pool.CreatedAt.IsZero() {
		pool.CreatedAt = time.Now().UTC()
	}
	pool.UpdatedAt = pool.CreatedAt
	if len(pool.Backing) == 0 {
		pool.Backing = json.RawMessage(`{}`)
	}
	if len(pool.Capabilities) == 0 {
		pool.Capabilities = json.RawMessage(`{}`)
	}
	warnJSON, textJSON := warningJSON(pool.Warnings, pool.WarningText)
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO storage_pools (
  id, cluster_id, node_id, name, backend_type, status, reason, root_path, backing,
  warnings, warning_text, capabilities, usable_bytes, allocated_bytes, provisioned_bytes,
  total_bytes, adopted, created_at, updated_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19
)`,
		pool.ID, pool.ClusterID, pool.NodeID, pool.Name, pool.BackendType, pool.Status, pool.Reason,
		pool.RootPath, pool.Backing, warnJSON, textJSON, pool.Capabilities,
		pool.UsableBytes, pool.AllocatedBytes, pool.ProvisionedBytes, pool.TotalBytes,
		pool.Adopted, pool.CreatedAt, pool.UpdatedAt)
	return err
}

func (p *Postgres) ListStoragePools(ctx context.Context, clusterID string) ([]StoragePool, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, node_id::text, name, backend_type, status, reason, root_path,
       backing, warnings, warning_text, capabilities, usable_bytes, allocated_bytes, provisioned_bytes,
       total_bytes, adopted, created_at, updated_at
FROM storage_pools WHERE cluster_id=$1 ORDER BY created_at, id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoragePool
	for rows.Next() {
		pool, err := scanPool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pool)
	}
	return out, rows.Err()
}

func (p *Postgres) GetStoragePool(ctx context.Context, clusterID, id string) (*StoragePool, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, node_id::text, name, backend_type, status, reason, root_path,
       backing, warnings, warning_text, capabilities, usable_bytes, allocated_bytes, provisioned_bytes,
       total_bytes, adopted, created_at, updated_at
FROM storage_pools WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	pool, err := scanPool(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pool, nil
}

func (p *Postgres) UpdateStoragePoolObserved(ctx context.Context, pool StoragePool) error {
	if len(pool.Capabilities) == 0 {
		pool.Capabilities = json.RawMessage(`{}`)
	}
	warnJSON, textJSON := warningJSON(pool.Warnings, pool.WarningText)
	_, err := p.DB.ExecContext(ctx, `
UPDATE storage_pools SET
  status=$2, reason=$3, warnings=$4, warning_text=$5, capabilities=$6,
  usable_bytes=$7, allocated_bytes=$8, provisioned_bytes=$9, total_bytes=$10, updated_at=now()
WHERE id=$1`,
		pool.ID, pool.Status, pool.Reason, warnJSON, textJSON, pool.Capabilities,
		pool.UsableBytes, pool.AllocatedBytes, pool.ProvisionedBytes, pool.TotalBytes)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPool(row rowScanner) (StoragePool, error) {
	var p StoragePool
	var usable, alloc, prov, total sql.NullInt64
	var warnings, texts []byte
	if err := row.Scan(&p.ID, &p.ClusterID, &p.NodeID, &p.Name, &p.BackendType, &p.Status, &p.Reason,
		&p.RootPath, &p.Backing, &warnings, &texts, &p.Capabilities, &usable, &alloc, &prov, &total,
		&p.Adopted, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return p, err
	}
	if len(warnings) > 0 {
		_ = json.Unmarshal(warnings, &p.Warnings)
	}
	if len(texts) > 0 {
		_ = json.Unmarshal(texts, &p.WarningText)
	}
	if usable.Valid {
		p.UsableBytes = &usable.Int64
	}
	if alloc.Valid {
		p.AllocatedBytes = &alloc.Int64
	}
	if prov.Valid {
		p.ProvisionedBytes = &prov.Int64
	}
	if total.Valid {
		p.TotalBytes = &total.Int64
	}
	return p, nil
}

func (p *Postgres) CreateVolume(ctx context.Context, v Volume) error {
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	v.UpdatedAt = v.CreatedAt
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO volumes (
  id, cluster_id, node_id, pool_id, class, kind, format, size_bytes, status,
  backend_type, backend_ref, xattr_state, allocated_bytes, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		v.ID, v.ClusterID, v.NodeID, v.PoolID, v.Class, v.Kind, v.Format, v.SizeBytes, v.Status,
		v.BackendType, v.BackendRef, v.XattrState, v.AllocatedBytes, v.CreatedAt, v.UpdatedAt)
	return err
}

func (p *Postgres) ListVolumes(ctx context.Context, clusterID, poolID string) ([]Volume, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, node_id::text, pool_id::text, class, kind, format, size_bytes,
       status, backend_type, backend_ref, xattr_state, allocated_bytes, created_at, updated_at
FROM volumes WHERE cluster_id=$1 AND ($2='' OR pool_id::text=$2)
ORDER BY created_at, id`, clusterID, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Volume
	for rows.Next() {
		v, err := scanVolume(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) GetVolume(ctx context.Context, clusterID, id string) (*Volume, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, node_id::text, pool_id::text, class, kind, format, size_bytes,
       status, backend_type, backend_ref, xattr_state, allocated_bytes, created_at, updated_at
FROM volumes WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	v, err := scanVolume(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (p *Postgres) UpdateVolumeObserved(ctx context.Context, v Volume) error {
	_, err := p.DB.ExecContext(ctx, `
UPDATE volumes SET status=$2, xattr_state=$3, allocated_bytes=$4, updated_at=now() WHERE id=$1`,
		v.ID, v.Status, v.XattrState, v.AllocatedBytes)
	return err
}

func scanVolume(row rowScanner) (Volume, error) {
	var v Volume
	var alloc sql.NullInt64
	if err := row.Scan(&v.ID, &v.ClusterID, &v.NodeID, &v.PoolID, &v.Class, &v.Kind, &v.Format,
		&v.SizeBytes, &v.Status, &v.BackendType, &v.BackendRef, &v.XattrState, &alloc, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return v, err
	}
	if alloc.Valid {
		v.AllocatedBytes = &alloc.Int64
	}
	return v, nil
}

func (p *Postgres) CreateLibraryItem(ctx context.Context, item LibraryItem) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	item.UpdatedAt = item.CreatedAt
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO library_items (
  id, cluster_id, node_id, pool_id, kind, display_name, backend_ref, size_bytes,
  checksum_sha256, status, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		item.ID, item.ClusterID, item.NodeID, item.PoolID, item.Kind, item.DisplayName, item.BackendRef,
		item.SizeBytes, item.ChecksumSHA256, item.Status, item.CreatedAt, item.UpdatedAt)
	return err
}

func (p *Postgres) ListLibraryItems(ctx context.Context, clusterID, poolID string) ([]LibraryItem, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, node_id::text, pool_id::text, kind, display_name, backend_ref,
       size_bytes, checksum_sha256, status, created_at, updated_at
FROM library_items WHERE cluster_id=$1 AND ($2='' OR pool_id::text=$2)
ORDER BY created_at, id`, clusterID, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LibraryItem
	for rows.Next() {
		item, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (p *Postgres) GetLibraryItem(ctx context.Context, clusterID, id string) (*LibraryItem, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, node_id::text, pool_id::text, kind, display_name, backend_ref,
       size_bytes, checksum_sha256, status, created_at, updated_at
FROM library_items WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	item, err := scanLibrary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (p *Postgres) GetLibraryByChecksum(ctx context.Context, poolID, checksum string) (*LibraryItem, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, node_id::text, pool_id::text, kind, display_name, backend_ref,
       size_bytes, checksum_sha256, status, created_at, updated_at
FROM library_items WHERE pool_id=$1 AND checksum_sha256=$2`, poolID, checksum)
	item, err := scanLibrary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (p *Postgres) UpdateLibraryObserved(ctx context.Context, item LibraryItem) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE library_items SET status=$2, updated_at=now() WHERE id=$1`, item.ID, item.Status)
	return err
}

func warningJSON(warnings, texts []string) ([]byte, []byte) {
	if warnings == nil {
		warnings = []string{}
	}
	if texts == nil {
		texts = []string{}
	}
	w, _ := json.Marshal(warnings)
	t, _ := json.Marshal(texts)
	return w, t
}

func scanLibrary(row rowScanner) (LibraryItem, error) {
	var item LibraryItem
	err := row.Scan(&item.ID, &item.ClusterID, &item.NodeID, &item.PoolID, &item.Kind, &item.DisplayName,
		&item.BackendRef, &item.SizeBytes, &item.ChecksumSHA256, &item.Status, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}
