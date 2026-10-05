package appdb

import (
	"context"
	"errors"
)

// ConfigKind names a configuration record DeleteConfig can remove. The API
// checks dependencies first; the database still refuses a delete that would
// orphan a dependent row.
type ConfigKind string

const (
	ConfigStoragePool   ConfigKind = "storage_pool"
	ConfigLibraryItem   ConfigKind = "library_item"
	ConfigVMTemplate    ConfigKind = "vm_template"
	ConfigGroup         ConfigKind = "group"
	ConfigAlertRule     ConfigKind = "alert_rule"
	ConfigNotifyChannel ConfigKind = "notification_channel"
	ConfigRegistry      ConfigKind = "registry"
	ConfigNodeGroup     ConfigKind = "node_group"
	ConfigPolicy        ConfigKind = "policy"
	ConfigAIProvider    ConfigKind = "ai_provider"
	ConfigAIProfile     ConfigKind = "ai_profile"
	ConfigWGPeer        ConfigKind = "wg_peer"
	ConfigBackupTarget  ConfigKind = "backup_target"
)

// ErrConfigNotFound is returned when no record of that kind and id exists in
// the cluster.
var ErrConfigNotFound = errors.New("not found")

// configTables maps each kind to its table. Only these tables can be named.
var configTables = map[ConfigKind]string{
	ConfigStoragePool:   "storage_pools",
	ConfigLibraryItem:   "library_items",
	ConfigVMTemplate:    "vm_templates",
	ConfigGroup:         "groups",
	ConfigAlertRule:     "alert_rules",
	ConfigNotifyChannel: "notification_channels",
	ConfigRegistry:      "registries",
	ConfigNodeGroup:     "node_groups",
	ConfigPolicy:        "policies",
	ConfigAIProvider:    "ai_providers",
	ConfigAIProfile:     "ai_profiles",
	ConfigWGPeer:        "wg_peers",
	ConfigBackupTarget:  "backup_targets",
}

// DeleteConfig removes one configuration record. Rows that only exist for it
// (group members, saved credentials, a backup target's runs that produced no
// backup) go with it.
func (p *Postgres) DeleteConfig(ctx context.Context, kind ConfigKind, clusterID, id string) error {
	table, ok := configTables[kind]
	if !ok {
		return errors.New("unsupported record kind")
	}
	if !ValidUUID(id) {
		return ErrConfigNotFound
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	switch kind {
	case ConfigBackupTarget:
		if _, err := tx.ExecContext(ctx, `
DELETE FROM backup_runs r WHERE r.cluster_id=$1 AND r.target_id=$2
  AND NOT EXISTS (SELECT 1 FROM backup_artifacts a WHERE a.run_id = r.id)`, clusterID, id); err != nil {
			return err
		}
	case ConfigNodeGroup:
		if _, err := tx.ExecContext(ctx, `DELETE FROM node_group_members WHERE group_id=$1`, id); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConfigNotFound
	}
	return tx.Commit()
}

// DeleteConfig removes one configuration record.
func (m *Memory) DeleteConfig(_ context.Context, kind ConfigKind, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch kind {
	case ConfigStoragePool:
		if v, ok := m.pools[id]; ok && v.ClusterID == clusterID {
			delete(m.pools, id)
			return nil
		}
	case ConfigLibraryItem:
		if v, ok := m.library[id]; ok && v.ClusterID == clusterID {
			delete(m.library, id)
			return nil
		}
	case ConfigVMTemplate:
		if v, ok := m.vmTemplates[id]; ok && v.ClusterID == clusterID {
			delete(m.vmTemplates, id)
			return nil
		}
	case ConfigGroup:
		if v, ok := m.groups[id]; ok && v.ClusterID == clusterID {
			delete(m.groups, id)
			delete(m.groupMembers, id)
			delete(m.groupRoles, id)
			return nil
		}
	case ConfigAlertRule:
		if v, ok := m.alertRules[id]; ok && v.ClusterID == clusterID {
			delete(m.alertRules, id)
			return nil
		}
	case ConfigNotifyChannel:
		if v, ok := m.notifyChannels[id]; ok && v.ClusterID == clusterID {
			delete(m.notifyChannels, id)
			delete(m.notifySecrets, id)
			return nil
		}
	case ConfigRegistry:
		if v, ok := m.registries[id]; ok && v.ClusterID == clusterID {
			delete(m.registries, id)
			delete(m.registrySecrets, id)
			return nil
		}
	case ConfigNodeGroup:
		if v, ok := m.nodeGroups[id]; ok && v.ClusterID == clusterID {
			delete(m.nodeGroups, id)
			delete(m.nodeGroupMembers, id)
			return nil
		}
	case ConfigPolicy:
		if v, ok := m.policies[id]; ok && v.ClusterID == clusterID {
			delete(m.policies, id)
			return nil
		}
	case ConfigAIProvider:
		if v, ok := m.aiProviders[id]; ok && v.ClusterID == clusterID {
			delete(m.aiProviders, id)
			return nil
		}
	case ConfigAIProfile:
		if v, ok := m.aiProfiles[id]; ok && v.ClusterID == clusterID {
			delete(m.aiProfiles, id)
			return nil
		}
	case ConfigWGPeer:
		if v, ok := m.wgPeers[id]; ok && v.ClusterID == clusterID {
			delete(m.wgPeers, id)
			return nil
		}
	case ConfigBackupTarget:
		if v, ok := m.backupTargets[id]; ok && v.ClusterID == clusterID {
			for rid, run := range m.backupRuns {
				if run.ClusterID != clusterID || run.TargetID != id {
					continue
				}
				hasArtifact := false
				for _, a := range m.backupArtifacts {
					if a.RunID == rid {
						hasArtifact = true
						break
					}
				}
				if !hasArtifact {
					delete(m.backupRuns, rid)
				}
			}
			delete(m.backupTargets, id)
			delete(m.backupCreds, id)
			return nil
		}
	default:
		return errors.New("unsupported record kind")
	}
	return ErrConfigNotFound
}
