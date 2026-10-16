package appdb

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// HostRemap moves a recovered cluster onto this host: the new node takes
// over from every old one, pools and networks get this host's paths and
// bridges, and workloads and game servers are held stopped until their data
// is restored.
type HostRemap struct {
	Node     Node
	Pools    map[string]string // pool id -> root path
	Networks map[string]string // network id -> bridge
}

// nodeColumns are the columns that name a node.
var nodeColumns = []string{"node_id", "owner_node_id", "desired_node_id", "source_node_id", "dest_node_id", "target_node_id"}

// RemapRecoveredHost applies m in one transaction and returns the desired
// power each workload (by id) and game server (by "gs:" id) had.
func (p *Postgres) RemapRecoveredHost(ctx context.Context, clusterID string, m HostRemap) (map[string]string, error) {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	plat := m.Node.HostPlatform
	if len(plat) == 0 {
		plat = []byte(`{}`)
	}
	// The lost hosts keep their rows, renamed, so this host can take the name.
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET name = name || '-lost-' || left(id::text, 8) WHERE cluster_id=$1 AND id::text <> $2 AND name = $3`,
		clusterID, m.Node.ID, m.Node.Name); err != nil {
		return nil, fmt.Errorf("node: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO nodes (id, cluster_id, name, host_platform, role, hostname) VALUES ($1,$2,$3,$4,'control',$5)
ON CONFLICT (id) DO UPDATE SET cluster_id=EXCLUDED.cluster_id, name=EXCLUDED.name, host_platform=EXCLUDED.host_platform, role='control', hostname=EXCLUDED.hostname, revoked_at=NULL`,
		m.Node.ID, clusterID, m.Node.Name, plat, m.Node.Hostname); err != nil {
		return nil, fmt.Errorf("node: %w", err)
	}
	var old []string
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM nodes WHERE id::text <> $1`, m.Node.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		old = append(old, id)
	}
	rows.Close()
	if len(old) > 0 {
		cols, err := tx.QueryContext(ctx, `
SELECT table_schema, table_name, column_name, udt_name FROM information_schema.columns
WHERE table_schema IN ('public','secrets') AND column_name = ANY($1) AND table_name <> 'nodes'
ORDER BY table_schema, table_name, column_name`, nodeColumns)
		if err != nil {
			return nil, err
		}
		type col struct{ schema, table, column, udt string }
		var all []col
		for cols.Next() {
			var c col
			if err := cols.Scan(&c.schema, &c.table, &c.column, &c.udt); err != nil {
				cols.Close()
				return nil, err
			}
			all = append(all, c)
		}
		cols.Close()
		for i, c := range all {
			sp := fmt.Sprintf("remap_%d", i)
			if _, err := tx.ExecContext(ctx, "SAVEPOINT "+sp); err != nil {
				return nil, err
			}
			q := fmt.Sprintf(`UPDATE %q.%q SET %q = CAST($1 AS %s) WHERE %q::text = ANY($2)`, c.schema, c.table, c.column, c.udt, c.column)
			if _, err := tx.ExecContext(ctx, q, m.Node.ID, old); err != nil {
				// A row per node that cannot move (a unique key) is
				// per-host state of the old host: it goes.
				if _, rerr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+sp); rerr != nil {
					return nil, rerr
				}
				del := fmt.Sprintf(`DELETE FROM %q.%q WHERE %q::text = ANY($1)`, c.schema, c.table, c.column)
				if _, err := tx.ExecContext(ctx, del, old); err != nil {
					return nil, fmt.Errorf("move %s.%s off the old hosts: %w", c.table, c.column, err)
				}
			}
			if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+sp); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE nodes SET revoked_at = COALESCE(revoked_at, now()) WHERE id::text = ANY($1)`, old); err != nil {
			return nil, err
		}
	}
	for id, path := range m.Pools {
		if _, err := tx.ExecContext(ctx, `UPDATE storage_pools SET root_path=$3 WHERE cluster_id=$1 AND id::text=$2`, clusterID, id, path); err != nil {
			return nil, fmt.Errorf("pool %s: %w", id, err)
		}
	}
	for id, bridge := range m.Networks {
		if _, err := tx.ExecContext(ctx, `UPDATE networks SET bridge_name=$3 WHERE cluster_id=$1 AND id::text=$2`, clusterID, id, bridge); err != nil {
			return nil, fmt.Errorf("network %s: %w", id, err)
		}
	}
	held := map[string]string{}
	if err := collectPower(ctx, tx, `SELECT id::text, desired_power FROM workloads WHERE cluster_id=$1`, clusterID, "", held); err != nil {
		return nil, err
	}
	if err := collectPower(ctx, tx, `SELECT id::text, desired_power FROM game_servers WHERE cluster_id=$1`, clusterID, "gs:", held); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workloads SET desired_power='stopped' WHERE cluster_id=$1`, clusterID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_servers SET desired_power='stopped' WHERE cluster_id=$1`, clusterID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = COALESCE(revoked_at, now())`); err != nil {
		return nil, err
	}
	return held, tx.Commit()
}

func collectPower(ctx context.Context, tx *sql.Tx, q, clusterID, prefix string, out map[string]string) error {
	rows, err := tx.QueryContext(ctx, q, clusterID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, power string
		if err := rows.Scan(&id, &power); err != nil {
			return err
		}
		out[prefix+id] = power
	}
	return rows.Err()
}

// ReplaceBackupTargetConnection points a target at new credentials, keeping
// its encryption key, after the old credentials were revoked.
func (p *Postgres) ReplaceBackupTargetConnection(ctx context.Context, clusterID, id, endpoint, region, username, password string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE backup_targets SET endpoint=$3, region=$4, username=$5, status='untested', updated_at=now() WHERE cluster_id=$1 AND id=$2`,
		clusterID, id, endpoint, region, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("backup target not found")
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets.backup_credentials (target_id, cluster_id, password, encryption_key, updated_at) VALUES ($1,$2,$3,'',now())
ON CONFLICT (target_id) DO UPDATE SET password=EXCLUDED.password, updated_at=now()`, id, clusterID, password); err != nil {
		return err
	}
	return tx.Commit()
}

// RemapRecoveredHost applies m.
func (m *Memory) RemapRecoveredHost(_ context.Context, clusterID string, r HostRemap) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nodes == nil {
		m.nodes = map[string]Node{}
	}
	now := time.Now().UTC()
	r.Node.ClusterID = clusterID
	r.Node.Role = "control"
	for id, n := range m.nodes {
		if id != r.Node.ID && n.RevokedAt == nil {
			n.RevokedAt = &now
			m.nodes[id] = n
		}
	}
	m.nodes[r.Node.ID] = r.Node
	for id, path := range r.Pools {
		if p, ok := m.pools[id]; ok {
			p.RootPath = path
			p.NodeID = r.Node.ID
			m.pools[id] = p
		}
	}
	for id, br := range r.Networks {
		if n, ok := m.networks[id]; ok {
			n.BridgeName = br
			n.NodeID = r.Node.ID
			m.networks[id] = n
		}
	}
	held := map[string]string{}
	for id, w := range m.workloads {
		if w.ClusterID != clusterID {
			continue
		}
		held[id] = w.DesiredPower
		w.DesiredPower = "stopped"
		w.NodeID = r.Node.ID
		m.workloads[id] = w
	}
	for id, g := range m.gameServers {
		held["gs:"+id] = g.DesiredPower
		g.DesiredPower = "stopped"
		m.gameServers[id] = g
	}
	return held, nil
}

// ReplaceBackupTargetConnection points a target at new credentials.
func (m *Memory) ReplaceBackupTargetConnection(_ context.Context, clusterID, id, endpoint, region, username, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.backupTargets[id]
	if !ok || t.ClusterID != clusterID {
		return fmt.Errorf("backup target not found")
	}
	t.Endpoint, t.Region, t.Username, t.Status, t.UpdatedAt = endpoint, region, username, BackupUntested, time.Now().UTC()
	m.backupTargets[id] = t
	pair := m.backupCreds[id]
	pair[0] = password
	m.backupCreds[id] = pair
	return nil
}

// BackupWorkloadOptions are one workload's backup options.
type BackupWorkloadOptions struct {
	ClusterID  string
	WorkloadID string
	// KeepImages keeps a copy of the workload's container images in its
	// backups.
	KeepImages bool
}

func (p *Postgres) ListBackupWorkloadOptions(ctx context.Context, clusterID string) (map[string]BackupWorkloadOptions, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT workload_id, keep_images FROM backup_workload_options WHERE cluster_id=$1`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]BackupWorkloadOptions{}
	for rows.Next() {
		o := BackupWorkloadOptions{ClusterID: clusterID}
		if err := rows.Scan(&o.WorkloadID, &o.KeepImages); err != nil {
			return nil, err
		}
		out[o.WorkloadID] = o
	}
	return out, rows.Err()
}

func (p *Postgres) SetBackupWorkloadOptions(ctx context.Context, o BackupWorkloadOptions) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO backup_workload_options (cluster_id, workload_id, keep_images, updated_at) VALUES ($1,$2,$3,now())
ON CONFLICT (cluster_id, workload_id) DO UPDATE SET keep_images=EXCLUDED.keep_images, updated_at=now()`, o.ClusterID, o.WorkloadID, o.KeepImages)
	return err
}

func (m *Memory) ListBackupWorkloadOptions(_ context.Context, clusterID string) (map[string]BackupWorkloadOptions, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]BackupWorkloadOptions{}
	for _, o := range m.backupWorkloadOpts {
		if o.ClusterID == clusterID {
			out[o.WorkloadID] = o
		}
	}
	return out, nil
}

func (m *Memory) SetBackupWorkloadOptions(_ context.Context, o BackupWorkloadOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backupWorkloadOpts == nil {
		m.backupWorkloadOpts = map[string]BackupWorkloadOptions{}
	}
	m.backupWorkloadOpts[o.ClusterID+"/"+o.WorkloadID] = o
	return nil
}
