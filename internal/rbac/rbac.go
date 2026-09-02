package rbac

import "strings"

// Built-in roles.
const (
	Admin    = "admin"
	Operator = "operator"
	Viewer   = "viewer"
)

// Permissions used in Phase 1. Later phases add more names.
const (
	IdentityRead        = "identity.read"
	IdentityTokenCreate = "identity.token.create"
	IdentityTokenRevoke = "identity.token.revoke"
	IdentityRecover     = "identity.recover"
	IdentityMFA         = "identity.mfa"
	IdentityGroupManage = "identity.group.manage"
	IdentityService     = "identity.service"
	SecretReveal        = "secret.reveal"
	SecretUse           = "secret.use"
	ClusterDestroy      = "cluster.destroy"
	AuditRead           = "audit.read"
	AlertRead           = "alert.read"
	AlertManage         = "alert.manage"
	ClusterRead         = "cluster.read"
	NodeRead            = "node.read"
	EventsRead          = "events.read"
	MetricsRead         = "metrics.read"
	StorageRead         = "storage.read"
	StoragePoolCreate   = "storage.pool.create"
	StorageVolumeCreate = "storage.volume.create"
	StorageImageUpload  = "storage.image.upload"
	NetworkRead         = "network.read"
	NetworkCreate       = "network.create"
	NetworkApply        = "network.apply"
	ComputeRead         = "compute.read"
	ComputeCreate       = "compute.create"
	ComputeLifecycle    = "compute.lifecycle"
	ComputeModify       = "compute.modify"
	ComputeGPUAssign    = "compute.gpu.assign"
	ComputeStart        = "compute.start"
	ComputeStop         = "compute.stop"
	ComputeDelete       = "compute.delete"
	ComputeConsole      = "compute.console"
	ComputeSnapshot     = "compute.snapshot"
	ComputeMigrate      = "compute.migrate"
	StorageSnapshot     = "storage.snapshot"
	BackupRead          = "backup.read"
	BackupCreate        = "backup.create"
	BackupRestore       = "backup.restore"
	NodeUpdate          = "node.update"
	NodeRevoke          = "node.revoke"
	ClusterJoin         = "cluster.join"
	ClusterPromote      = "cluster.promote"
	TerminalOpen        = "terminal.open"
	FilesRead           = "files.read"
	FilesDownload       = "files.download"
	FilesUpload         = "files.upload"
	FilesCreate         = "files.create"
	FilesModify         = "files.modify"
	FilesDelete         = "files.delete"
	FilesPermissions    = "files.permissions"
	FilesOwnership      = "files.ownership"
	SettingsTLSRead     = "settings.tls.read"
	SettingsTLSManage   = "settings.tls.manage"
	FeatureRead         = "feature.read"
	FeatureManage       = "feature.manage"
	StoreRead           = "store.read"
	StoreInstall        = "store.install"
	All                 = "*"
)

// Catalog is deny-by-default.
type Catalog struct{}

// New returns the Phase 1 catalog.
func New() Catalog { return Catalog{} }

// PermissionsForRole returns the built-in grant list.
func (Catalog) PermissionsForRole(role string) []string {
	switch role {
	case Admin:
		return []string{All}
	case Operator:
		return []string{
			IdentityRead, IdentityTokenCreate, IdentityTokenRevoke, IdentityMFA, IdentityGroupManage, ClusterRead,
			NodeRead, EventsRead, MetricsRead, AlertRead, AlertManage,
			StorageRead, StoragePoolCreate, StorageVolumeCreate, StorageImageUpload,
			NetworkRead, NetworkCreate, NetworkApply,
			ComputeRead, ComputeCreate, ComputeLifecycle,
			ComputeModify, ComputeStart, ComputeStop, ComputeDelete, ComputeConsole, ComputeSnapshot, StorageSnapshot, ComputeGPUAssign, ComputeMigrate,
			BackupRead, BackupCreate, BackupRestore, NodeUpdate, ClusterJoin, NodeRevoke,
			TerminalOpen, FilesRead, FilesDownload, FilesUpload, FilesCreate, FilesModify, FilesDelete,
			SettingsTLSRead, FeatureRead, FeatureManage, StoreRead, StoreInstall,
		}
	case Viewer:
		return []string{IdentityRead, IdentityMFA, ClusterRead, NodeRead, EventsRead, MetricsRead, AlertRead, StorageRead, NetworkRead, ComputeRead, FilesRead, SettingsTLSRead, BackupRead, FeatureRead, StoreRead}
	default:
		return nil
	}
}

// Authorize reports whether grants include permission.
func Authorize(grants []string, permission string) bool {
	for _, g := range grants {
		if g == All || g == permission {
			return true
		}
		if strings.HasSuffix(g, ".*") && strings.HasPrefix(permission, strings.TrimSuffix(g, "*")) {
			return true
		}
	}
	return false
}

// SeedRoles is the Phase 1 built-in set.
func SeedRoles() map[string][]string {
	c := New()
	return map[string][]string{
		Admin:    c.PermissionsForRole(Admin),
		Operator: c.PermissionsForRole(Operator),
		Viewer:   c.PermissionsForRole(Viewer),
	}
}
