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
	AuditRead           = "audit.read"
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
			IdentityRead, IdentityTokenCreate, IdentityTokenRevoke, ClusterRead,
			NodeRead, EventsRead, MetricsRead,
			StorageRead, StoragePoolCreate, StorageVolumeCreate, StorageImageUpload,
			NetworkRead, NetworkCreate, NetworkApply,
			ComputeRead, ComputeCreate, ComputeLifecycle,
		}
	case Viewer:
		return []string{IdentityRead, ClusterRead, NodeRead, EventsRead, MetricsRead, StorageRead, NetworkRead, ComputeRead}
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
