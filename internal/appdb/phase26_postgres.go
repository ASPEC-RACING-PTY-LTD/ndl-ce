package appdb

import (
	"context"
	"database/sql"
	"errors"
)

func (p *Postgres) UpsertDatastore(ctx context.Context, d Datastore) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO datastores (pool_id, kind, locator, portal, iqn) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (pool_id) DO UPDATE SET kind=EXCLUDED.kind, locator=EXCLUDED.locator, portal=EXCLUDED.portal, iqn=EXCLUDED.iqn`,
		d.PoolID, d.Kind, d.Locator, d.Portal, d.IQN)
	return err
}

func (p *Postgres) GetDatastore(ctx context.Context, poolID string) (*Datastore, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT pool_id::text, kind, locator, portal, iqn FROM datastores WHERE pool_id=$1`, poolID)
	var d Datastore
	if err := row.Scan(&d.PoolID, &d.Kind, &d.Locator, &d.Portal, &d.IQN); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

func (p *Postgres) UpsertDatastoreSecret(ctx context.Context, poolID, username, password string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO secrets.datastore_credentials (pool_id, cluster_id, username, password, updated_at)
SELECT $1, p.cluster_id, $2, $3, now() FROM storage_pools p WHERE p.id=$1
ON CONFLICT (pool_id) DO UPDATE SET username=EXCLUDED.username, password=EXCLUDED.password, updated_at=now()`,
		poolID, username, password)
	return err
}

func (p *Postgres) DatastoreSecret(ctx context.Context, poolID string) (username, password string, err error) {
	err = p.DB.QueryRowContext(ctx, `SELECT username, password FROM secrets.datastore_credentials WHERE pool_id=$1`, poolID).Scan(&username, &password)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return username, password, err
}
