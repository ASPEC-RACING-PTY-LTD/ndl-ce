package rbac

import "strings"

// Built-in roles. Each id matches the name the UI shows: SuperAdmin is the
// highest role, Admin runs the appliance, User reads it.
const (
	SuperAdmin = "superadmin"
	// IAMManager ranks between SuperAdmin and Admin: Admin's permissions
	// plus managing the people, groups and roles ranked below it, and
	// assigning the permissions it holds to them.
	IAMManager = "iam-manager"
	Admin      = "admin"
	User       = "user"
	// Automation labels the identity flows run as. It is not a login role.
	Automation = "automation"
	// NoRole is what the user forms send for a person with no sign-in role:
	// they hold only grants on single items (workloads, game servers, Docker
	// containers, nodes) given to them or their groups.
	NoRole = "none"
)

// Permissions used in Phase 1. Later phases add more names.
const (
	IdentityRead           = "identity.read"
	IdentityTokenCreate    = "identity.token.create"
	IdentityTokenRevoke    = "identity.token.revoke"
	IdentityRecover        = "identity.recover"
	IdentityMFA            = "identity.mfa"
	IdentityGroupManage    = "identity.group.manage"
	IdentityService        = "identity.service"
	SecretReveal           = "secret.reveal"
	SecretUse              = "secret.use"
	ClusterDestroy         = "cluster.destroy"
	AuditRead              = "audit.read"
	AlertRead              = "alert.read"
	AlertManage            = "alert.manage"
	ClusterRead            = "cluster.read"
	NodeRead               = "node.read"
	EventsRead             = "events.read"
	MetricsRead            = "metrics.read"
	StorageRead            = "storage.read"
	StoragePoolCreate      = "storage.pool.create"
	StorageVolumeCreate    = "storage.volume.create"
	StorageImageUpload     = "storage.image.upload"
	NetworkRead            = "network.read"
	NetworkCreate          = "network.create"
	NetworkApply           = "network.apply"
	ComputeRead            = "compute.read"
	ComputeCreate          = "compute.create"
	ComputeLifecycle       = "compute.lifecycle"
	ComputeModify          = "compute.modify"
	ComputeGPUAssign       = "compute.gpu.assign"
	ComputeStart           = "compute.start"
	ComputeStop            = "compute.stop"
	ComputeDelete          = "compute.delete"
	ComputeConsole         = "compute.console"
	ComputeSnapshot        = "compute.snapshot"
	ComputeMigrate         = "compute.migrate"
	StorageSnapshot        = "storage.snapshot"
	BackupRead             = "backup.read"
	BackupCreate           = "backup.create"
	BackupRestore          = "backup.restore"
	NodeUpdate             = "node.update"
	NodeRevoke             = "node.revoke"
	ClusterJoin            = "cluster.join"
	ClusterPromote         = "cluster.promote"
	TerminalOpen           = "terminal.open"
	FilesRead              = "files.read"
	FilesDownload          = "files.download"
	FilesUpload            = "files.upload"
	FilesCreate            = "files.create"
	FilesModify            = "files.modify"
	FilesDelete            = "files.delete"
	FilesPermissions       = "files.permissions"
	FilesOwnership         = "files.ownership"
	SettingsTLSRead        = "settings.tls.read"
	SettingsTLSManage      = "settings.tls.manage"
	FeatureRead            = "feature.read"
	FeatureManage          = "feature.manage"
	StoreRead              = "store.read"
	StoreInstall           = "store.install"
	StoreVerify            = "store.verify"
	PolicyRead             = "policy.read"
	PolicyApply            = "policy.apply"
	PolicyRun              = "policy.run"
	AIAsk                  = "ai.ask"
	AIManage               = "ai.manage"
	SettingsLicenseRead    = "settings.license.read"
	SettingsLicenseManage  = "settings.license.manage"
	MigrationRead          = "migration.read"
	MigrationImport        = "migration.import"
	MigrationExport        = "migration.export"
	MigrationManage        = "migration.manage"
	UsersRead              = "users.read"
	UsersCreate            = "users.create"
	UsersUpdate            = "users.update"
	UsersDelete            = "users.delete"
	UsersRolesManage       = "users.roles.manage"
	UsersSessionsRevoke    = "users.sessions.revoke"
	APIAccessManage        = "api_access.manage"
	UpdatesManage          = "updates.manage"
	RolesManage            = "roles.manage"
	SettingsSecurityManage = "settings.security.manage"
	GameServerRead         = "gameserver.read"
	GameServerCreate       = "gameserver.create"
	GameServerPower        = "gameserver.power"
	GameServerConsole      = "gameserver.console"
	GameServerFiles        = "gameserver.files"
	GameServerConfig       = "gameserver.config"
	GameServerContent      = "gameserver.content"
	GameServerBackup       = "gameserver.backup"
	GameServerSchedule     = "gameserver.schedule"
	GameServerNetwork      = "gameserver.network"
	GameServerUsers        = "gameserver.users"
	GameServerReinstall    = "gameserver.reinstall"
	GameServerDelete       = "gameserver.delete"
	GameServerAdmin        = "gameserver.admin"
	// Cloudflare: read the unified view; manage tunnels, routes, DNS and
	// connectors; add, rotate and remove API tokens (admin only by default).
	CloudflareRead        = "cloudflare.read"
	CloudflareManage      = "cloudflare.manage"
	CloudflareCredentials = "cloudflare.credentials"
	// DDoS protection: see protected targets, traffic and attacks; change
	// targets, limits, trusted and blocked networks, and ban or unban.
	DDoSRead   = "ddos.read"
	DDoSManage = "ddos.manage"
	// Approvals: see and decide automation flow versions that hold
	// protected steps. SuperAdmins have both; they give them to other roles
	// on the Approvals page only, never in the role editor.
	ApprovalsView   = "approvals.view"
	ApprovalsDecide = "approvals.decide"
	// Search settings and indexing (Admin by default); appearance settings:
	// the global theme and themes per role or group (SuperAdmin by default).
	SearchManage     = "search.manage"
	AppearanceManage = "settings.appearance.manage"
	// Assigning permissions directly (resource access): hand out
	// permissions you hold to users, groups and roles ranked below you, on
	// one item or everywhere. The override lifts those limits; only a
	// SuperAdmin can give it.
	PermissionsAssign         = "permissions.assign"
	PermissionsAssignOverride = "permissions.assign.override"
	// Capabilities that used to be SuperAdmin only. The SuperAdmin role
	// holds them through All; anyone else can be given them.
	ComputePrivileged      = "compute.privileged"
	NodeHostAccess         = "node.host"
	TerminalSessionsManage = "terminal.sessions.manage"
	LabManage              = "lab.manage"
	MigrationPrivileged    = "migration.privileged"
	BackupGitManage        = "backup.git.manage"
	PlatformBackupManage   = "platform_backup.manage"
	CloudflareHostTunnel   = "cloudflare.host_tunnel"
	DockerScriptsAdmin     = "docker.scripts.admin"
	DeployConfigure        = "deploy.configure"
	StorageProtectedDelete = "storage.protected.delete"
	AutomationSkipTest     = "automation.skip_test"
	UpdatesWipe            = "updates.wipe"
	SearchHostFiles        = "search.host_files"
	IdentityServiceForce   = "identity.service.force"
	AppearanceLimit        = "settings.appearance.limit"
	All                    = "*"

	TokenPresetReadonlyDebug = "readonly-debug"
	TokenPresetFullAudit     = "full-audit"
)

// Catalog is deny-by-default.
type Catalog struct{}

// New returns the Phase 1 catalog.
func New() Catalog { return Catalog{} }

// PermissionsForRole returns the built-in grant list.
func (Catalog) PermissionsForRole(role string) []string {
	switch role {
	case SuperAdmin:
		return []string{All}
	case Admin:
		return []string{
			IdentityRead, IdentityTokenCreate, IdentityTokenRevoke, IdentityMFA, IdentityGroupManage, ClusterRead,
			NodeRead, EventsRead, MetricsRead, AlertRead, AlertManage,
			StorageRead, StoragePoolCreate, StorageVolumeCreate, StorageImageUpload,
			NetworkRead, NetworkCreate, NetworkApply,
			ComputeRead, ComputeCreate, ComputeLifecycle,
			ComputeModify, ComputeStart, ComputeStop, ComputeDelete, ComputeConsole, ComputeSnapshot, StorageSnapshot, ComputeGPUAssign, ComputeMigrate,
			BackupRead, BackupCreate, BackupRestore, NodeUpdate, ClusterJoin, NodeRevoke,
			TerminalOpen, FilesRead, FilesDownload, FilesUpload, FilesCreate, FilesModify, FilesDelete,
			FilesPermissions, FilesOwnership,
			SettingsTLSRead, FeatureRead, FeatureManage, StoreRead, StoreInstall, StoreVerify, PolicyRead, PolicyApply, PolicyRun, AIAsk, AIManage,
			SettingsLicenseRead,
			MigrationRead, MigrationImport, MigrationExport, MigrationManage,
			APIAccessManage, UpdatesManage,
			GameServerRead, GameServerCreate, GameServerPower, GameServerConsole, GameServerFiles, GameServerConfig,
			GameServerContent, GameServerBackup, GameServerSchedule, GameServerNetwork, GameServerUsers,
			GameServerReinstall, GameServerDelete, GameServerAdmin,
			CloudflareRead, CloudflareManage, SearchManage,
			DDoSRead, DDoSManage,
		}
	case IAMManager:
		return append(Catalog{}.PermissionsForRole(Admin),
			UsersRead, UsersCreate, UsersUpdate, UsersDelete, UsersRolesManage, UsersSessionsRevoke,
			RolesManage, PermissionsAssign, AuditRead,
		)
	case User:
		return []string{IdentityRead, IdentityMFA, ClusterRead, NodeRead, EventsRead, MetricsRead, AlertRead, StorageRead, NetworkRead, ComputeRead, FilesRead, SettingsTLSRead, BackupRead, FeatureRead, StoreRead, PolicyRead, AIAsk, SettingsLicenseRead, MigrationRead, GameServerRead, CloudflareRead, DDoSRead}
	case Automation:
		// Flows run as the system: a flow does exactly what it was built to
		// do. Protected steps are held for approval instead (see httpapi).
		return []string{All}
	default:
		return nil
	}
}

// PermissionsForTokenPreset returns a scoped REST grant list. Unknown presets return nil.
func PermissionsForTokenPreset(preset string) []string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case TokenPresetReadonlyDebug:
		return []string{
			ClusterRead, NodeRead, EventsRead, MetricsRead, AlertRead, StorageRead, NetworkRead,
			ComputeRead, FilesRead, BackupRead, PolicyRead, FeatureRead, SettingsLicenseRead,
			SettingsTLSRead, MigrationRead,
		}
	case TokenPresetFullAudit:
		return append(PermissionsForTokenPreset(TokenPresetReadonlyDebug), AuditRead)
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
		SuperAdmin: c.PermissionsForRole(SuperAdmin),
		IAMManager: c.PermissionsForRole(IAMManager),
		Admin:      c.PermissionsForRole(Admin),
		User:       c.PermissionsForRole(User),
		Automation: c.PermissionsForRole(Automation),
	}
}

// RoleMeta describes a built-in role for Management. Custom roles are not
// assigned at request time; authz uses this catalog, not roles.permissions.
type RoleMeta struct {
	Name      string
	Title     string
	Summary   string
	Login     bool
	Immutable bool
}

// BuiltInRoles is the immutable role catalog.
func BuiltInRoles() []RoleMeta {
	return []RoleMeta{
		{
			Name:      User,
			Title:     "User",
			Summary:   "Read the appliance. Personal account and MFA stay available. Management is hidden.",
			Login:     true,
			Immutable: true,
		},
		{
			Name:      Admin,
			Title:     "Admin",
			Summary:   "Operate workloads, features, updates, groups, and API tokens. Cannot manage users, roles, license, audit, or security policy.",
			Login:     true,
			Immutable: true,
		},
		{
			Name:      IAMManager,
			Title:     "IAM Manager",
			Summary:   "Everything Admin can do, plus managing the users, groups and roles ranked below it and assigning them the permissions it holds.",
			Login:     true,
			Immutable: true,
		},
		{
			Name:      SuperAdmin,
			Title:     "SuperAdmin",
			Summary:   "The highest role. Full control including users, roles, license, audit, and security policy. The last SuperAdmin cannot be removed, disabled, or demoted.",
			Login:     true,
			Immutable: true,
		},
		{
			Name:      Automation,
			Title:     "Automation",
			Summary:   "The identity flows run as. Flows do what they were built to do; protected steps need approval. Not a sign-in role and cannot be assigned to a person.",
			Login:     false,
			Immutable: true,
		},
	}
}

// LoginRoles are the roles a person account may be assigned.
func LoginRoles() []string {
	return []string{User, Admin, IAMManager, SuperAdmin}
}

// IsLoginRole reports whether name is a person sign-in role.
func IsLoginRole(name string) bool {
	switch name {
	case User, Admin, IAMManager, SuperAdmin:
		return true
	default:
		return false
	}
}

// GrantsForRoles unions catalog grants for the named roles.
func GrantsForRoles(roles []string) []string {
	c := New()
	seen := map[string]struct{}{}
	var out []string
	for _, role := range roles {
		for _, perm := range c.PermissionsForRole(role) {
			if _, ok := seen[perm]; ok {
				continue
			}
			seen[perm] = struct{}{}
			out = append(out, perm)
		}
	}
	return out
}
