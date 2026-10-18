package appdb

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// RoleDef is a role as the Access control page edits it. Rank 0 is the
// highest; a role may only act on roles ranked below it.
type RoleDef struct {
	ID          string
	ClusterID   string
	Name        string
	Title       string
	Summary     string
	Permissions []string
	Rank        int
	BuiltIn     bool
	Login       bool
	// Customized is set once a built-in role's permissions were edited.
	Customized bool
}

func (p *Postgres) ListRoleDefs(ctx context.Context, clusterID string) ([]RoleDef, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id::text, cluster_id::text, name, title, summary, COALESCE(array_to_string(permissions, ','), ''), rank, builtin, login, customized
		FROM roles WHERE cluster_id=$1 ORDER BY rank, name`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoleDef
	for rows.Next() {
		var d RoleDef
		var perms string
		if err := rows.Scan(&d.ID, &d.ClusterID, &d.Name, &d.Title, &d.Summary, &perms, &d.Rank, &d.BuiltIn, &d.Login, &d.Customized); err != nil {
			return nil, err
		}
		if perms != "" {
			d.Permissions = strings.Split(perms, ",")
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (p *Postgres) SaveRoleDef(ctx context.Context, d RoleDef) error {
	id := d.ID
	if id == "" {
		id = uuid.NewString()
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO roles (id, cluster_id, name, title, summary, permissions, rank, builtin, login, customized)
		VALUES ($1,$2,$3,$4,$5,COALESCE(string_to_array(NULLIF($6, ''), ','), '{}'),$7,$8,$9,$10)
		ON CONFLICT (cluster_id, name) DO UPDATE SET title=EXCLUDED.title, summary=EXCLUDED.summary, permissions=EXCLUDED.permissions,
			login=EXCLUDED.login, customized=EXCLUDED.customized`,
		id, d.ClusterID, d.Name, d.Title, d.Summary, strings.Join(d.Permissions, ","), d.Rank, d.BuiltIn, d.Login, d.Customized)
	return err
}

func (p *Postgres) DeleteRoleDef(ctx context.Context, clusterID, name string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM role_bindings b USING roles r WHERE b.role_id=r.id AND r.cluster_id=$1 AND r.name=$2`,
		`DELETE FROM group_role_bindings b USING roles r WHERE b.role_id=r.id AND r.cluster_id=$1 AND r.name=$2`,
		`DELETE FROM roles WHERE cluster_id=$1 AND name=$2`,
	} {
		if _, err := tx.ExecContext(ctx, q, clusterID, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *Postgres) SetRoleOrder(ctx context.Context, clusterID string, names []string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, n := range names {
		if _, err := tx.ExecContext(ctx, `UPDATE roles SET rank=$3 WHERE cluster_id=$1 AND name=$2`, clusterID, n, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *Postgres) SetGroup(ctx context.Context, clusterID, groupID, name, description string, members, roles []string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE groups SET name=COALESCE(NULLIF($3, ''), name), description=$4 WHERE id=$1 AND cluster_id=$2`, groupID, clusterID, name, description)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("group not found")
	}
	if members != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM group_members WHERE group_id=$1`, groupID); err != nil {
			return err
		}
		for _, u := range members {
			if _, err := tx.ExecContext(ctx, `INSERT INTO group_members (group_id, user_id) SELECT $1, u.id FROM users u WHERE u.id::text=$2 AND u.cluster_id=$3 ON CONFLICT DO NOTHING`, groupID, u, clusterID); err != nil {
				return err
			}
		}
	}
	if roles != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM group_role_bindings WHERE group_id=$1`, groupID); err != nil {
			return err
		}
		for _, rn := range roles {
			if _, err := tx.ExecContext(ctx, `INSERT INTO group_role_bindings (group_id, role_id) SELECT $1, r.id FROM roles r WHERE r.cluster_id=$2 AND r.name=$3 ON CONFLICT DO NOTHING`, groupID, clusterID, rn); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Memory keeps role metadata beside its existing name -> permissions map.

func (m *Memory) ListRoleDefs(_ context.Context, clusterID string) ([]RoleDef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RoleDef
	for key, perms := range m.roles {
		if !strings.HasPrefix(key, clusterID+"/") {
			continue
		}
		name := strings.TrimPrefix(key, clusterID+"/")
		d := m.roleMeta[key]
		d.ClusterID, d.Name, d.Permissions = clusterID, name, append([]string(nil), perms...)
		if d.ID == "" {
			d.ID = key
			d.Rank, d.BuiltIn, d.Login, d.Title = builtinRoleDefaults(name)
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func builtinRoleDefaults(name string) (int, bool, bool, string) {
	switch name {
	case "admin":
		return 0, true, true, "Owner"
	case "operator":
		return 1, true, true, "Admin"
	case "automation":
		return 2, true, false, "Automation"
	case "viewer":
		return 3, true, true, "User"
	}
	return 100, false, true, name
}

func (m *Memory) SaveRoleDef(_ context.Context, d RoleDef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.roles == nil {
		m.roles = map[string][]string{}
	}
	if m.roleMeta == nil {
		m.roleMeta = map[string]RoleDef{}
	}
	key := d.ClusterID + "/" + d.Name
	if prev, ok := m.roleMeta[key]; ok {
		d.Rank = prev.Rank
		d.ID = prev.ID
	} else if _, exists := m.roles[key]; exists {
		d.Rank, d.BuiltIn, _, _ = builtinRoleDefaults(d.Name)
	}
	if d.ID == "" {
		d.ID = key
	}
	m.roles[key] = append([]string(nil), d.Permissions...)
	m.roleMeta[key] = d
	return nil
}

func (m *Memory) DeleteRoleDef(_ context.Context, clusterID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := clusterID + "/" + name
	delete(m.roles, key)
	delete(m.roleMeta, key)
	for uid, roles := range m.binds {
		m.binds[uid] = removeString(roles, name)
	}
	for gid, roles := range m.groupRoles {
		m.groupRoles[gid] = removeString(roles, name)
	}
	return nil
}

func (m *Memory) SetRoleOrder(_ context.Context, clusterID string, names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.roleMeta == nil {
		m.roleMeta = map[string]RoleDef{}
	}
	for i, n := range names {
		key := clusterID + "/" + n
		d, ok := m.roleMeta[key]
		if !ok {
			d = RoleDef{ID: key, ClusterID: clusterID, Name: n}
			_, d.BuiltIn, d.Login, d.Title = builtinRoleDefaults(n)
		}
		d.Rank = i
		m.roleMeta[key] = d
	}
	return nil
}

func (m *Memory) SetGroup(_ context.Context, _, groupID, _, _ string, members, roles []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if members != nil {
		if m.groupMembers == nil {
			m.groupMembers = map[string][]string{}
		}
		m.groupMembers[groupID] = append([]string(nil), members...)
	}
	if roles != nil {
		if m.groupRoles == nil {
			m.groupRoles = map[string][]string{}
		}
		m.groupRoles[groupID] = append([]string(nil), roles...)
	}
	return nil
}

func removeString(list []string, v string) []string {
	out := list[:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
