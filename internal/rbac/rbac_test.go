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
	if Authorize(nil, NodeRead) {
		t.Fatal("deny by default")
	}
}
