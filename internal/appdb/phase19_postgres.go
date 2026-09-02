package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (p *Postgres) UpsertGuestObservation(ctx context.Context, g GuestObservation) error {
	if g.ObservedAt.IsZero() {
		g.ObservedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO guest_observations (
  workload_id, cluster_id, qemu_ga_state, nodal_ga_state, nodal_ga_version,
  guest_os, guest_arch, guest_ipv4, observed_at, stale
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (workload_id) DO UPDATE SET
  qemu_ga_state=EXCLUDED.qemu_ga_state,
  nodal_ga_state=EXCLUDED.nodal_ga_state,
  nodal_ga_version=EXCLUDED.nodal_ga_version,
  guest_os=EXCLUDED.guest_os,
  guest_arch=EXCLUDED.guest_arch,
  guest_ipv4=EXCLUDED.guest_ipv4,
  observed_at=EXCLUDED.observed_at,
  stale=EXCLUDED.stale`,
		g.WorkloadID, g.ClusterID, g.QEMUGAState, g.NodalGAState, g.NodalGAVersion,
		g.GuestOS, g.GuestArch, g.GuestIPv4, g.ObservedAt, g.Stale)
	return err
}

func (p *Postgres) GetGuestObservation(ctx context.Context, clusterID, workloadID string) (*GuestObservation, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT workload_id::text, cluster_id::text, qemu_ga_state, nodal_ga_state, COALESCE(nodal_ga_version, ''),
  COALESCE(guest_os, ''), COALESCE(guest_arch, ''), COALESCE(guest_ipv4, ''), observed_at, stale
FROM guest_observations WHERE cluster_id=$1 AND workload_id=$2`, clusterID, workloadID)
	var g GuestObservation
	if err := row.Scan(&g.WorkloadID, &g.ClusterID, &g.QEMUGAState, &g.NodalGAState, &g.NodalGAVersion,
		&g.GuestOS, &g.GuestArch, &g.GuestIPv4, &g.ObservedAt, &g.Stale); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &g, nil
}
