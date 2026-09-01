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
	if !Authorize(op, ComputeModify) || !Authorize(op, ComputeStart) || !Authorize(op, ComputeStop) || !Authorize(op, ComputeDelete) || !Authorize(op, ComputeConsole) {
		t.Fatal("operator phase 8 compute permissions")
	}
	if Authorize(view, ComputeModify) || Authorize(view, ComputeStart) || Authorize(view, ComputeConsole) || Authorize(view, ComputeDelete) {
		t.Fatal("viewer compute remains read-only")
	}
	if !Authorize(op, ComputeGPUAssign) || Authorize(view, ComputeGPUAssign) {
		t.Fatal("operator gpu assign; viewer not")
	}
	if Authorize(view, TerminalOpen) {
		t.Fatal("viewer must not have terminal")
	}
	if !Authorize(view, FilesRead) || Authorize(view, FilesDownload) || Authorize(view, TerminalOpen) {
		t.Fatal("viewer files.read only, no download, no terminal")
	}
	if !Authorize(op, TerminalOpen) || !Authorize(op, FilesUpload) || !Authorize(op, FilesDelete) {
		t.Fatal("operator CT terminal and files")
	}
	if Authorize(view, SettingsTLSManage) || Authorize(op, SettingsTLSManage) {
		t.Fatal("only admin may manage TLS")
	}
	if !Authorize(view, SettingsTLSRead) || !Authorize(op, SettingsTLSRead) {
		t.Fatal("tls status is readable")
	}
	if !Authorize(op, ComputeSnapshot) || !Authorize(op, StorageSnapshot) {
		t.Fatal("operator snapshots")
	}
	if Authorize(view, ComputeSnapshot) || Authorize(view, StorageSnapshot) {
		t.Fatal("viewer must not snapshot")
	}
	if !Authorize(view, BackupRead) || Authorize(view, BackupCreate) || Authorize(view, BackupRestore) {
		t.Fatal("viewer backup is read-only")
	}
	if !Authorize(op, BackupRead) || !Authorize(op, BackupCreate) || !Authorize(op, BackupRestore) {
		t.Fatal("operator backup run and restore")
	}
	if !Authorize(op, NodeUpdate) || Authorize(view, NodeUpdate) {
		t.Fatal("operator may update the node; viewer may not")
	}
	if Authorize(view, AuditRead) || Authorize(op, AuditRead) {
		t.Fatal("viewer and operator must not read audit")
	}
	if !Authorize(op, IdentityMFA) || !Authorize(view, IdentityMFA) {
		t.Fatal("people may enroll MFA")
	}
	if !Authorize(op, IdentityGroupManage) || Authorize(view, IdentityGroupManage) {
		t.Fatal("operator groups; viewer not")
	}
	if Authorize(op, SecretReveal) || Authorize(op, ClusterDestroy) || Authorize(op, IdentityService) {
		t.Fatal("step-up and service principals stay admin")
	}
	if !Authorize(view, AlertRead) || Authorize(view, AlertManage) {
		t.Fatal("viewer alert is read-only")
	}
	if !Authorize(op, AlertRead) || !Authorize(op, AlertManage) {
		t.Fatal("operator may manage alerts")
	}
}
