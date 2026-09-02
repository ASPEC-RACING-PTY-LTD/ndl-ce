package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (p *Postgres) CreateNetworkVLAN(ctx context.Context, v NetworkVLAN) error {
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO network_vlans (id, cluster_id, network_id, name, vid, parent_ifname, access_ifname, mode, locator, status, reason, created_at)
VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		v.ID, v.ClusterID, v.NetworkID, v.Name, v.VID, v.ParentIfName, v.AccessIfName, v.Mode, v.Locator, v.Status, v.Reason, v.CreatedAt)
	return err
}

func (p *Postgres) ListNetworkVLANs(ctx context.Context, clusterID string) ([]NetworkVLAN, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, COALESCE(network_id::text,''), name, vid, parent_ifname, access_ifname, mode, locator, status, reason, created_at
FROM network_vlans WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NetworkVLAN
	for rows.Next() {
		var v NetworkVLAN
		if err := rows.Scan(&v.ID, &v.ClusterID, &v.NetworkID, &v.Name, &v.VID, &v.ParentIfName, &v.AccessIfName, &v.Mode, &v.Locator, &v.Status, &v.Reason, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateNetworkBond(ctx context.Context, b NetworkBond) error {
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now().UTC()
	}
	members, _ := json.Marshal(b.Members)
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO network_bonds (id, cluster_id, name, mode, members, locator, status, reason, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, b.ID, b.ClusterID, b.Name, b.Mode, members, b.Locator, b.Status, b.Reason, b.CreatedAt)
	return err
}

func (p *Postgres) ListNetworkBonds(ctx context.Context, clusterID string) ([]NetworkBond, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, mode, members, locator, status, reason, created_at
FROM network_bonds WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NetworkBond
	for rows.Next() {
		var b NetworkBond
		var raw []byte
		if err := rows.Scan(&b.ID, &b.ClusterID, &b.Name, &b.Mode, &raw, &b.Locator, &b.Status, &b.Reason, &b.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &b.Members)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateNetworkPolicy(ctx context.Context, pol NetworkPolicy) error {
	if pol.CreatedAt.IsZero() {
		pol.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO network_policies (id, cluster_id, name, action, src_workload_id, dst_workload_id, src_mac, dst_mac, status, reason, created_at)
VALUES ($1,$2,$3,$4,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11)`,
		pol.ID, pol.ClusterID, pol.Name, pol.Action, pol.SrcWorkloadID, pol.DstWorkloadID, pol.SrcMAC, pol.DstMAC, pol.Status, pol.Reason, pol.CreatedAt)
	return err
}

func (p *Postgres) ListNetworkPolicies(ctx context.Context, clusterID string) ([]NetworkPolicy, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, action, COALESCE(src_workload_id::text,''), COALESCE(dst_workload_id::text,''), src_mac, dst_mac, status, reason, created_at
FROM network_policies WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NetworkPolicy
	for rows.Next() {
		var pol NetworkPolicy
		if err := rows.Scan(&pol.ID, &pol.ClusterID, &pol.Name, &pol.Action, &pol.SrcWorkloadID, &pol.DstWorkloadID, &pol.SrcMAC, &pol.DstMAC, &pol.Status, &pol.Reason, &pol.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pol)
	}
	return out, rows.Err()
}

func (p *Postgres) GetNetworkPolicy(ctx context.Context, clusterID, id string) (*NetworkPolicy, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, action, COALESCE(src_workload_id::text,''), COALESCE(dst_workload_id::text,''), src_mac, dst_mac, status, reason, created_at
FROM network_policies WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	var pol NetworkPolicy
	if err := row.Scan(&pol.ID, &pol.ClusterID, &pol.Name, &pol.Action, &pol.SrcWorkloadID, &pol.DstWorkloadID, &pol.SrcMAC, &pol.DstMAC, &pol.Status, &pol.Reason, &pol.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &pol, nil
}

func (p *Postgres) UpdateNetworkPolicyStatus(ctx context.Context, clusterID, id, status, reason string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE network_policies SET status=$3, reason=$4 WHERE cluster_id=$1 AND id=$2`, clusterID, id, status, reason)
	return err
}

func (p *Postgres) CreateNetworkOverlay(ctx context.Context, o NetworkOverlay) error {
	if o.CreatedAt.IsZero() {
		o.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO network_overlays (id, cluster_id, name, vni, locator, status, reason, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, o.ID, o.ClusterID, o.Name, o.VNI, o.Locator, o.Status, o.Reason, o.CreatedAt)
	return err
}

func (p *Postgres) ListNetworkOverlays(ctx context.Context, clusterID string) ([]NetworkOverlay, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, vni, locator, status, reason, created_at
FROM network_overlays WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NetworkOverlay
	for rows.Next() {
		var o NetworkOverlay
		if err := rows.Scan(&o.ID, &o.ClusterID, &o.Name, &o.VNI, &o.Locator, &o.Status, &o.Reason, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
