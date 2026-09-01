package rbac

import "testing"

func TestAuthorizeMatrix(t *testing.T) {
	c := New()
	admin := c.PermissionsForRole(Admin)
	op := c.PermissionsForRole(Operator)
	view := c.PermissionsForRole(Viewer)
	if !Authorize(admin, IdentityTokenCreate) {
		t.Fatal("admin")
	}
	if !Authorize(op, IdentityTokenCreate) {
		t.Fatal("operator tokens")
	}
	if Authorize(view, IdentityTokenCreate) {
		t.Fatal("viewer must be denied token create")
	}
	if !Authorize(view, IdentityRead) {
		t.Fatal("viewer read")
	}
	if !Authorize(view, NodeRead) || !Authorize(view, EventsRead) || !Authorize(view, MetricsRead) {
		t.Fatal("viewer phase 2 reads")
	}
	if !Authorize(op, NodeRead) || !Authorize(op, EventsRead) || !Authorize(op, MetricsRead) {
		t.Fatal("operator phase 2 reads")
	}
	if !Authorize(view, StorageRead) || Authorize(view, StoragePoolCreate) || Authorize(view, StorageVolumeCreate) || Authorize(view, StorageImageUpload) {
		t.Fatal("viewer storage is read-only")
	}
	if !Authorize(op, StoragePoolCreate) || !Authorize(op, StorageVolumeCreate) || !Authorize(op, StorageImageUpload) {
		t.Fatal("operator storage mutations")
	}
	if !Authorize(view, NetworkRead) || Authorize(view, NetworkCreate) || Authorize(view, NetworkApply) {
		t.Fatal("viewer network is read-only")
	}
	if !Authorize(op, NetworkCreate) || !Authorize(op, NetworkApply) {
		t.Fatal("operator isolated network mutations")
	}
	if !Authorize(view, ComputeRead) || Authorize(view, ComputeCreate) || Authorize(view, ComputeLifecycle) {
		t.Fatal("viewer compute is read-only")
	}
	if !Authorize(op, ComputeCreate) || !Authorize(op, ComputeLifecycle) {
		t.Fatal("operator unprivileged create and lifecycle")
	}
	if Authorize(nil, NodeRead) {
		t.Fatal("deny by default")
	}
}
