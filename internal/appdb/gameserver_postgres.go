package appdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (p *Postgres) CreateGameServer(ctx context.Context, s GameServer) error {
	if s.ID == "" || s.ClusterID == "" {
		return fmt.Errorf("game server identity is required")
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	s.UpdatedAt = s.CreatedAt
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO game_servers (
  id, cluster_id, workload_id, node_id, owner_user_id, name, notes, template_id, template_name,
  game, implementation, family, status, desired_power, image, image_label, startup, env_json, ports_json,
  cpus, memory_bytes, disk_bytes, capabilities, container_id, data_dir, install_phase, install_log,
  error_human, error_raw, pinned, created_at, updated_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32
)`,
		s.ID, s.ClusterID, s.WorkloadID, s.NodeID, s.OwnerUserID, s.Name, s.Notes, s.TemplateID, s.TemplateName,
		s.Game, s.Implementation, s.Family, s.Status, s.DesiredPower, s.Image, s.ImageLabel, s.Startup, nullStr(s.EnvJSON), nullStr(s.PortsJSON),
		s.CPUs, s.MemoryBytes, s.DiskBytes, nullStr(s.Capabilities), s.ContainerID, s.DataDir, s.InstallPhase, s.InstallLog,
		s.ErrorHuman, s.ErrorRaw, s.Pinned, s.CreatedAt, s.UpdatedAt)
	return err
}

func gameServerSelect() string {
	return `SELECT id::text, cluster_id::text, workload_id, node_id, owner_user_id, name, notes, template_id, template_name,
 game, implementation, family, status, desired_power, image, image_label, startup, env_json, ports_json,
 cpus, memory_bytes, disk_bytes, capabilities, container_id, data_dir, install_phase, install_log,
 error_human, error_raw, pinned, created_at, updated_at FROM game_servers`
}

func scanGameServer(row interface{ Scan(dest ...any) error }) (GameServer, error) {
	var s GameServer
	var env, ports, caps string
	err := row.Scan(&s.ID, &s.ClusterID, &s.WorkloadID, &s.NodeID, &s.OwnerUserID, &s.Name, &s.Notes, &s.TemplateID, &s.TemplateName,
		&s.Game, &s.Implementation, &s.Family, &s.Status, &s.DesiredPower, &s.Image, &s.ImageLabel, &s.Startup, &env, &ports,
		&s.CPUs, &s.MemoryBytes, &s.DiskBytes, &caps, &s.ContainerID, &s.DataDir, &s.InstallPhase, &s.InstallLog,
		&s.ErrorHuman, &s.ErrorRaw, &s.Pinned, &s.CreatedAt, &s.UpdatedAt)
	s.EnvJSON = []byte(env)
	s.PortsJSON = []byte(ports)
	s.Capabilities = []byte(caps)
	return s, err
}

func (p *Postgres) GetGameServer(ctx context.Context, clusterID, id string) (*GameServer, error) {
	s, err := scanGameServer(p.DB.QueryRowContext(ctx, gameServerSelect()+` WHERE cluster_id=$1 AND id=$2`, clusterID, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (p *Postgres) ListGameServers(ctx context.Context, clusterID string) ([]GameServer, error) {
	rows, err := p.DB.QueryContext(ctx, gameServerSelect()+` WHERE cluster_id=$1 ORDER BY name`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GameServer
	for rows.Next() {
		s, err := scanGameServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) UpdateGameServer(ctx context.Context, s GameServer) error {
	s.UpdatedAt = time.Now().UTC()
	res, err := p.DB.ExecContext(ctx, `
UPDATE game_servers SET workload_id=$3, node_id=$4, owner_user_id=$5, name=$6, notes=$7, template_id=$8, template_name=$9,
 game=$10, implementation=$11, family=$12, status=$13, desired_power=$14, image=$15, image_label=$16, startup=$17,
 env_json=$18, ports_json=$19, cpus=$20, memory_bytes=$21, disk_bytes=$22, capabilities=$23, container_id=$24, data_dir=$25,
 install_phase=$26, install_log=$27, error_human=$28, error_raw=$29, pinned=$30, updated_at=$31
WHERE cluster_id=$1 AND id=$2`,
		s.ClusterID, s.ID, s.WorkloadID, s.NodeID, s.OwnerUserID, s.Name, s.Notes, s.TemplateID, s.TemplateName,
		s.Game, s.Implementation, s.Family, s.Status, s.DesiredPower, s.Image, s.ImageLabel, s.Startup,
		nullStr(s.EnvJSON), nullStr(s.PortsJSON), s.CPUs, s.MemoryBytes, s.DiskBytes, nullStr(s.Capabilities), s.ContainerID, s.DataDir,
		s.InstallPhase, s.InstallLog, s.ErrorHuman, s.ErrorRaw, s.Pinned, s.UpdatedAt)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("game server not found")
	}
	return nil
}

func (p *Postgres) DeleteGameServer(ctx context.Context, clusterID, id string) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM game_servers WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("game server not found")
	}
	return nil
}

func (p *Postgres) UpsertGameTemplate(ctx context.Context, t GameTemplate) error {
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = time.Now().UTC()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = t.UpdatedAt
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO game_templates (id, cluster_id, body, created_at, updated_at) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (cluster_id, id) DO UPDATE SET body=EXCLUDED.body, updated_at=EXCLUDED.updated_at`,
		t.ID, t.ClusterID, string(t.Body), t.CreatedAt, t.UpdatedAt)
	return err
}

func (p *Postgres) GetGameTemplate(ctx context.Context, clusterID, id string) (*GameTemplate, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id, cluster_id::text, body, created_at, updated_at FROM game_templates WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var t GameTemplate
	var body string
	if err := row.Scan(&t.ID, &t.ClusterID, &body, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	t.Body = []byte(body)
	return &t, nil
}

func (p *Postgres) ListGameTemplates(ctx context.Context, clusterID string) ([]GameTemplate, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id, cluster_id::text, body, created_at, updated_at FROM game_templates WHERE cluster_id=$1`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GameTemplate
	for rows.Next() {
		var t GameTemplate
		var body string
		if err := rows.Scan(&t.ID, &t.ClusterID, &body, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Body = []byte(body)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Postgres) UpsertGameSource(ctx context.Context, s GameSource) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO game_sources (id, cluster_id, name, url, kind, enabled, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (cluster_id, id) DO UPDATE SET name=EXCLUDED.name, url=EXCLUDED.url, kind=EXCLUDED.kind, enabled=EXCLUDED.enabled`,
		s.ID, s.ClusterID, s.Name, s.URL, s.Kind, s.Enabled, s.CreatedAt)
	return err
}

func (p *Postgres) ListGameSources(ctx context.Context, clusterID string) ([]GameSource, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id, cluster_id::text, name, url, kind, enabled, created_at FROM game_sources WHERE cluster_id=$1`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GameSource
	for rows.Next() {
		var s GameSource
		if err := rows.Scan(&s.ID, &s.ClusterID, &s.Name, &s.URL, &s.Kind, &s.Enabled, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteGameSource(ctx context.Context, clusterID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM game_sources WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	return err
}

func (p *Postgres) UpsertGameACL(ctx context.Context, a GameACL) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO game_acl (server_id, user_id, grants, created_at) VALUES ($1,$2,$3,$4)
ON CONFLICT (server_id, user_id) DO UPDATE SET grants=EXCLUDED.grants`,
		a.ServerID, a.UserID, string(a.Grants), a.CreatedAt)
	return err
}

func (p *Postgres) ListGameACL(ctx context.Context, serverID string) ([]GameACL, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT server_id::text, user_id, grants, created_at FROM game_acl WHERE server_id=$1`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GameACL
	for rows.Next() {
		var a GameACL
		var grants string
		if err := rows.Scan(&a.ServerID, &a.UserID, &grants, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Grants = []byte(grants)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) GetGameACL(ctx context.Context, serverID, userID string) (*GameACL, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT server_id::text, user_id, grants, created_at FROM game_acl WHERE server_id=$1 AND user_id=$2`, serverID, userID)
	var a GameACL
	var grants string
	if err := row.Scan(&a.ServerID, &a.UserID, &grants, &a.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	a.Grants = []byte(grants)
	return &a, nil
}

func (p *Postgres) DeleteGameACL(ctx context.Context, serverID, userID string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM game_acl WHERE server_id=$1 AND user_id=$2`, serverID, userID)
	return err
}

func (p *Postgres) InsertGameEvent(ctx context.Context, e GameEvent) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO game_events (id, server_id, cluster_id, user_id, kind, summary, detail, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, e.ID, e.ServerID, e.ClusterID, e.UserID, e.Kind, e.Summary, e.Detail, e.CreatedAt)
	return err
}

func (p *Postgres) ListGameEvents(ctx context.Context, clusterID, serverID string, limit int) ([]GameEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id::text, server_id, cluster_id::text, user_id, kind, summary, detail, created_at FROM game_events WHERE cluster_id=$1`
	args := []any{clusterID}
	if serverID != "" {
		q += ` AND server_id=$2 ORDER BY created_at DESC LIMIT $3`
		args = append(args, serverID, limit)
	} else {
		q += ` ORDER BY created_at DESC LIMIT $2`
		args = append(args, limit)
	}
	rows, err := p.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GameEvent
	for rows.Next() {
		var e GameEvent
		if err := rows.Scan(&e.ID, &e.ServerID, &e.ClusterID, &e.UserID, &e.Kind, &e.Summary, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateGameSchedule(ctx context.Context, s GameSchedule) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO game_schedules (id, server_id, name, action, cron, payload, enabled, last_run, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, s.ID, s.ServerID, s.Name, s.Action, s.Cron, s.Payload, s.Enabled, nullableTime(s.LastRun), s.CreatedAt)
	return err
}

func (p *Postgres) ListGameSchedules(ctx context.Context, serverID string) ([]GameSchedule, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id::text, server_id::text, name, action, cron, payload, enabled, last_run, created_at FROM game_schedules WHERE server_id=$1`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GameSchedule
	for rows.Next() {
		var s GameSchedule
		var last sql.NullTime
		if err := rows.Scan(&s.ID, &s.ServerID, &s.Name, &s.Action, &s.Cron, &s.Payload, &s.Enabled, &last, &s.CreatedAt); err != nil {
			return nil, err
		}
		if last.Valid {
			s.LastRun = last.Time
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteGameSchedule(ctx context.Context, serverID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM game_schedules WHERE server_id=$1 AND id=$2`, serverID, id)
	return err
}

func (p *Postgres) UpdateGameSchedule(ctx context.Context, s GameSchedule) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE game_schedules SET name=$3, action=$4, cron=$5, payload=$6, enabled=$7, last_run=$8 WHERE server_id=$1 AND id=$2`,
		s.ServerID, s.ID, s.Name, s.Action, s.Cron, s.Payload, s.Enabled, nullableTime(s.LastRun))
	return err
}

func (p *Postgres) CreateGameBackup(ctx context.Context, b GameBackup) error {
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO game_backups (id, server_id, name, reason, path, bytes, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		b.ID, b.ServerID, b.Name, b.Reason, b.Path, b.Bytes, b.CreatedAt)
	return err
}

func (p *Postgres) ListGameBackups(ctx context.Context, serverID string) ([]GameBackup, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id::text, server_id::text, name, reason, path, bytes, created_at FROM game_backups WHERE server_id=$1 ORDER BY created_at DESC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GameBackup
	for rows.Next() {
		var b GameBackup
		if err := rows.Scan(&b.ID, &b.ServerID, &b.Name, &b.Reason, &b.Path, &b.Bytes, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (p *Postgres) GetGameBackup(ctx context.Context, serverID, id string) (*GameBackup, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id::text, server_id::text, name, reason, path, bytes, created_at FROM game_backups WHERE server_id=$1 AND id=$2`, serverID, id)
	var b GameBackup
	if err := row.Scan(&b.ID, &b.ServerID, &b.Name, &b.Reason, &b.Path, &b.Bytes, &b.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

func (p *Postgres) DeleteGameBackup(ctx context.Context, serverID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM game_backups WHERE server_id=$1 AND id=$2`, serverID, id)
	return err
}

func (p *Postgres) UpsertGameContent(ctx context.Context, c GameContent) error {
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO game_content (id, server_id, body, created_at, updated_at) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (id) DO UPDATE SET body=EXCLUDED.body, updated_at=EXCLUDED.updated_at`,
		c.ID, c.ServerID, string(c.Body), c.CreatedAt, c.UpdatedAt)
	return err
}

func (p *Postgres) ListGameContent(ctx context.Context, serverID string) ([]GameContent, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id, server_id::text, body, created_at, updated_at FROM game_content WHERE server_id=$1`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GameContent
	for rows.Next() {
		var c GameContent
		var body string
		if err := rows.Scan(&c.ID, &c.ServerID, &body, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Body = []byte(body)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *Postgres) GetGameContent(ctx context.Context, serverID, id string) (*GameContent, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT id, server_id::text, body, created_at, updated_at FROM game_content WHERE server_id=$1 AND id=$2`, serverID, id)
	var c GameContent
	var body string
	if err := row.Scan(&c.ID, &c.ServerID, &body, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	c.Body = []byte(body)
	return &c, nil
}

func (p *Postgres) DeleteGameContent(ctx context.Context, serverID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM game_content WHERE server_id=$1 AND id=$2`, serverID, id)
	return err
}

func (p *Postgres) UpsertGameContentProfile(ctx context.Context, pr GameContentProfile) error {
	if pr.CreatedAt.IsZero() {
		pr.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO game_content_profiles (id, server_id, name, items, created_at) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, items=EXCLUDED.items`,
		pr.ID, pr.ServerID, pr.Name, string(pr.Items), pr.CreatedAt)
	return err
}

func (p *Postgres) ListGameContentProfiles(ctx context.Context, serverID string) ([]GameContentProfile, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id::text, server_id::text, name, items, created_at FROM game_content_profiles WHERE server_id=$1`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GameContentProfile
	for rows.Next() {
		var pr GameContentProfile
		var items string
		if err := rows.Scan(&pr.ID, &pr.ServerID, &pr.Name, &items, &pr.CreatedAt); err != nil {
			return nil, err
		}
		pr.Items = []byte(items)
		out = append(out, pr)
	}
	return out, rows.Err()
}

func (p *Postgres) UpsertGameConsoleFav(ctx context.Context, f GameConsoleFav) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO game_console_favs (id, server_id, name, command) VALUES ($1,$2,$3,$4)
ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, command=EXCLUDED.command`,
		f.ID, f.ServerID, f.Name, f.Command)
	return err
}

func (p *Postgres) ListGameConsoleFavs(ctx context.Context, serverID string) ([]GameConsoleFav, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id::text, server_id::text, name, command FROM game_console_favs WHERE server_id=$1`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GameConsoleFav
	for rows.Next() {
		var f GameConsoleFav
		if err := rows.Scan(&f.ID, &f.ServerID, &f.Name, &f.Command); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteGameConsoleFav(ctx context.Context, serverID, id string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM game_console_favs WHERE server_id=$1 AND id=$2`, serverID, id)
	return err
}

func (p *Postgres) AppendGameConsoleHist(ctx context.Context, serverID, command string) error {
	_, err := p.DB.ExecContext(ctx, `INSERT INTO game_console_hist (server_id, command) VALUES ($1,$2)`, serverID, command)
	return err
}

func (p *Postgres) ListGameConsoleHist(ctx context.Context, serverID string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.DB.QueryContext(ctx, `SELECT command FROM game_console_hist WHERE server_id=$1 ORDER BY id DESC LIMIT $2`, serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rev []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		rev = append(rev, c)
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, rows.Err()
}

func (p *Postgres) GetGameUserPref(ctx context.Context, clusterID, userID, key string) (string, error) {
	row := p.DB.QueryRowContext(ctx, `SELECT value FROM game_user_prefs WHERE cluster_id=$1 AND user_id=$2 AND key=$3`, clusterID, userID, key)
	var v string
	if err := row.Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

func (p *Postgres) SetGameUserPref(ctx context.Context, clusterID, userID, key, value string) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO game_user_prefs (cluster_id, user_id, key, value) VALUES ($1,$2,$3,$4)
ON CONFLICT (cluster_id, user_id, key) DO UPDATE SET value=EXCLUDED.value`, clusterID, userID, key, value)
	return err
}

func nullStr(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return string(b)
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
