package appdb

import (
	"context"
	"sort"
	"time"
)

// DockerPref is a saved choice about one Docker container or Compose project.
type DockerPref struct {
	ClusterID string
	MachineID string
	Scope     string // container or project
	Name      string
	Ignored   bool
	Note      string
	UpdatedBy string
	UpdatedAt time.Time
}

func dockerPrefKey(p DockerPref) string {
	return p.ClusterID + "\x00" + p.MachineID + "\x00" + p.Scope + "\x00" + p.Name
}

func (m *Memory) ListDockerPrefs(_ context.Context, clusterID string) ([]DockerPref, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DockerPref
	for _, p := range m.dockerPrefs {
		if p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return dockerPrefKey(out[i]) < dockerPrefKey(out[j]) })
	return out, nil
}

func (m *Memory) PutDockerPref(_ context.Context, p DockerPref) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dockerPrefs == nil {
		m.dockerPrefs = map[string]DockerPref{}
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now().UTC()
	}
	m.dockerPrefs[dockerPrefKey(p)] = p
	return nil
}

func (m *Memory) DeleteDockerPref(_ context.Context, clusterID, machineID, scope, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dockerPrefs, dockerPrefKey(DockerPref{ClusterID: clusterID, MachineID: machineID, Scope: scope, Name: name}))
	return nil
}

func (p *Postgres) ListDockerPrefs(ctx context.Context, clusterID string) ([]DockerPref, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT cluster_id::text, machine_id, scope, name, ignored, note, updated_by, updated_at
FROM docker_prefs WHERE cluster_id=$1 ORDER BY machine_id, scope, name`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DockerPref
	for rows.Next() {
		var d DockerPref
		if err := rows.Scan(&d.ClusterID, &d.MachineID, &d.Scope, &d.Name, &d.Ignored, &d.Note, &d.UpdatedBy, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (p *Postgres) PutDockerPref(ctx context.Context, d DockerPref) error {
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO docker_prefs (cluster_id, machine_id, scope, name, ignored, note, updated_by, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,now())
ON CONFLICT (cluster_id, machine_id, scope, name) DO UPDATE
SET ignored=EXCLUDED.ignored, note=EXCLUDED.note, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		d.ClusterID, d.MachineID, d.Scope, d.Name, d.Ignored, d.Note, d.UpdatedBy)
	return err
}

func (p *Postgres) DeleteDockerPref(ctx context.Context, clusterID, machineID, scope, name string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM docker_prefs WHERE cluster_id=$1 AND machine_id=$2 AND scope=$3 AND name=$4`,
		clusterID, machineID, scope, name)
	return err
}
