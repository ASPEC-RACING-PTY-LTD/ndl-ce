package appdb

import (
	"context"
	"fmt"
)

// ErrNodeHasWorkloads refuses deleting a node that still runs workloads.
type ErrNodeHasWorkloads struct{ Count int }

func (e ErrNodeHasWorkloads) Error() string {
	return fmt.Sprintf("%d workload(s) are still placed on this node; delete or move them first", e.Count)
}

// DeleteNode removes a node that is not the control node, with the records
// that only existed for it: its inventory and observations, storage pools,
// volumes and library items, networks, maintenance and group membership,
// and finished migration and rolling-update steps. Events and operations
// stay and lose the node reference. A node that still has workloads is
// refused with ErrNodeHasWorkloads.
func (m *Memory) DeleteNode(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[id]
	if !ok || n.ClusterID != clusterID {
		return ErrNodeNotFound
	}
	count := 0
	for _, w := range m.workloads {
		if w.NodeID == id || w.OwnerNodeID == id || w.DesiredNodeID == id {
			count++
		}
	}
	if count > 0 {
		return ErrNodeHasWorkloads{Count: count}
	}
	delete(m.inventory, id)
	kept := m.observations[:0]
	for _, o := range m.observations {
		if o.NodeID != id {
			kept = append(kept, o)
		}
	}
	m.observations = kept
	for i := range m.operations {
		if m.operations[i].NodeID == id {
			m.operations[i].NodeID = ""
		}
	}
	for i := range m.events {
		if m.events[i].NodeID == id {
			m.events[i].NodeID = ""
		}
	}
	for k, v := range m.volumes {
		if v.NodeID == id {
			delete(m.volumes, k)
		}
	}
	for k, v := range m.library {
		if v.NodeID == id {
			delete(m.library, k)
		}
	}
	for k, v := range m.pools {
		if v.NodeID == id {
			delete(m.pools, k)
		}
	}
	for k, v := range m.networks {
		if v.NodeID == id {
			delete(m.networks, k)
		}
	}
	for k, v := range m.migrateJobs {
		if v.SourceNodeID == id || v.DestNodeID == id {
			delete(m.migrateJobs, k)
		}
	}
	for k, v := range m.rollingSteps {
		if v.NodeID == id {
			delete(m.rollingSteps, k)
		}
	}
	for k, v := range m.distributedOSDs {
		if v.NodeID == id {
			delete(m.distributedOSDs, k)
		}
	}
	delete(m.nodeMaint, id)
	for g, members := range m.nodeGroupMembers {
		out := members[:0]
		for _, mid := range members {
			if mid != id {
				out = append(out, mid)
			}
		}
		m.nodeGroupMembers[g] = out
	}
	delete(m.nodes, id)
	return nil
}

// DeleteNode is the Postgres DeleteNode, in one transaction.
func (p *Postgres) DeleteNode(ctx context.Context, clusterID, id string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM nodes WHERE cluster_id=$1 AND id=$2)`, clusterID, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNodeNotFound
	}
	// Workloads on the node, and workloads elsewhere that use its disks or
	// networks, keep it.
	var count int
	if err := tx.QueryRowContext(ctx, `
SELECT count(DISTINCT w.id) FROM workloads w
WHERE w.cluster_id=$1 AND (w.node_id=$2 OR w.owner_node_id=$2 OR w.desired_node_id=$2
   OR EXISTS (SELECT 1 FROM workload_disks d JOIN volumes v ON v.id=d.volume_id WHERE d.workload_id=w.id AND v.node_id=$2)
   OR EXISTS (SELECT 1 FROM workload_nics n JOIN networks nw ON nw.id=n.network_id WHERE n.workload_id=w.id AND nw.node_id=$2))`, clusterID, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrNodeHasWorkloads{Count: count}
	}
	stmts := []string{
		`UPDATE operations SET node_id=NULL WHERE node_id=$1`,
		`UPDATE events SET node_id=NULL WHERE node_id=$1`,
		`UPDATE join_tokens SET consumed_node_id=NULL WHERE consumed_node_id=$1`,
		`DELETE FROM hardware_inventory WHERE node_id=$1`,
		`DELETE FROM node_observations WHERE node_id=$1`,
		`DELETE FROM rolling_steps WHERE node_id=$1`,
		`DELETE FROM migrate_jobs WHERE source_node_id=$1 OR dest_node_id=$1`,
		`DELETE FROM distributed_osds WHERE node_id=$1`,
		`DELETE FROM snapshots WHERE volume_id IN (SELECT id FROM volumes WHERE node_id=$1)`,
		`DELETE FROM addresses WHERE network_id IN (SELECT id FROM networks WHERE node_id=$1)`,
		`DELETE FROM dhcp_reservations WHERE network_id IN (SELECT id FROM networks WHERE node_id=$1)`,
		`DELETE FROM library_items WHERE node_id=$1`,
		`DELETE FROM volumes WHERE node_id=$1`,
		`DELETE FROM storage_pools WHERE node_id=$1`,
		`DELETE FROM networks WHERE node_id=$1`,
		`DELETE FROM nodes WHERE id=$1`,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("delete node: %w", err)
		}
	}
	return tx.Commit()
}

// DeleteRemoteNode removes a remote worker record and its sessions.
func (m *Memory) DeleteRemoteNode(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.remoteNodes[id]
	if !ok || n.ClusterID != clusterID {
		return ErrNodeNotFound
	}
	delete(m.remoteNodes, id)
	for k, s := range m.remoteSessions {
		if s.NodeID == id {
			delete(m.remoteSessions, k)
		}
	}
	return nil
}

// DeleteRemoteNode removes a remote worker record; its sessions cascade.
func (p *Postgres) DeleteRemoteNode(ctx context.Context, clusterID, id string) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM remote_nodes WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNodeNotFound
	}
	return nil
}
