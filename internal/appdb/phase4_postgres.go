package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (p *Postgres) CreateNetwork(ctx context.Context, n Network) error {
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	n.UpdatedAt = n.CreatedAt
	if n.PersistKind == "" {
		n.PersistKind = "systemd-networkd"
	}
	warn, _ := json.Marshal(n.Warnings)
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO networks (
  id, cluster_id, node_id, name, kind, status, reason, danger, bridge_name, uplink_ifname,
  ipv4_cidr, gateway, dhcp, dns, nat, persist_kind, warnings, management_ifindex, created_at, updated_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20
)`,
		n.ID, n.ClusterID, n.NodeID, n.Name, n.Kind, n.Status, n.Reason, n.Danger, n.BridgeName, n.UplinkIfName,
		n.IPv4CIDR, n.Gateway, n.DHCP, n.DNS, n.NAT, n.PersistKind, warn, n.ManagementIfIndex, n.CreatedAt, n.UpdatedAt)
	return err
}

func (p *Postgres) ListNetworks(ctx context.Context, clusterID string) ([]Network, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, node_id::text, name, kind, status, reason, danger, bridge_name, uplink_ifname,
       ipv4_cidr, gateway, dhcp, dns, nat, persist_kind, warnings, management_ifindex, created_at, updated_at
FROM networks WHERE cluster_id=$1 ORDER BY created_at`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Network
	for rows.Next() {
		n, err := scanNetwork(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (p *Postgres) GetNetwork(ctx context.Context, clusterID, id string) (*Network, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, node_id::text, name, kind, status, reason, danger, bridge_name, uplink_ifname,
       ipv4_cidr, gateway, dhcp, dns, nat, persist_kind, warnings, management_ifindex, created_at, updated_at
FROM networks WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	n, err := scanNetwork(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (p *Postgres) UpdateNetworkObserved(ctx context.Context, n Network) error {
	warn, _ := json.Marshal(n.Warnings)
	res, err := p.DB.ExecContext(ctx, `
UPDATE networks SET status=$2, reason=$3, warnings=$4, management_ifindex=$5, updated_at=now()
WHERE id=$1`, n.ID, n.Status, n.Reason, warn, n.ManagementIfIndex)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return errors.New("network not found")
	}
	return nil
}

func (p *Postgres) CreateAddress(ctx context.Context, a Address) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO addresses (id, cluster_id, network_id, family, cidr, role, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)`, a.ID, a.ClusterID, a.NetworkID, a.Family, a.CIDR, a.Role, a.CreatedAt)
	return err
}

func (p *Postgres) ListAddresses(ctx context.Context, clusterID, networkID string) ([]Address, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, network_id::text, family, cidr, role, created_at
FROM addresses WHERE cluster_id=$1 AND ($2='' OR network_id::text=$2) ORDER BY created_at`, clusterID, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Address
	for rows.Next() {
		var a Address
		if err := rows.Scan(&a.ID, &a.ClusterID, &a.NetworkID, &a.Family, &a.CIDR, &a.Role, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateReservation(ctx context.Context, r DHCPReservation) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO dhcp_reservations (id, cluster_id, network_id, mac, ipv4, hostname, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)`, r.ID, r.ClusterID, r.NetworkID, r.MAC, r.IPv4, r.Hostname, r.CreatedAt)
	return err
}

func (p *Postgres) ListReservations(ctx context.Context, clusterID, networkID string) ([]DHCPReservation, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, network_id::text, mac, ipv4, hostname, created_at
FROM dhcp_reservations WHERE cluster_id=$1 AND ($2='' OR network_id::text=$2) ORDER BY created_at`, clusterID, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DHCPReservation
	for rows.Next() {
		var r DHCPReservation
		if err := rows.Scan(&r.ID, &r.ClusterID, &r.NetworkID, &r.MAC, &r.IPv4, &r.Hostname, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type networkRow interface {
	Scan(dest ...any) error
}

func scanNetwork(row networkRow) (Network, error) {
	var n Network
	var warn []byte
	if err := row.Scan(
		&n.ID, &n.ClusterID, &n.NodeID, &n.Name, &n.Kind, &n.Status, &n.Reason, &n.Danger,
		&n.BridgeName, &n.UplinkIfName, &n.IPv4CIDR, &n.Gateway, &n.DHCP, &n.DNS, &n.NAT,
		&n.PersistKind, &warn, &n.ManagementIfIndex, &n.CreatedAt, &n.UpdatedAt,
	); err != nil {
		return n, err
	}
	if len(warn) > 0 {
		_ = json.Unmarshal(warn, &n.Warnings)
	}
	return n, nil
}
