package install

import (
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/hostos"
)

func TestUnsupportedHostMessage(t *testing.T) {
	_, err := hostos.DetectFrom(strings.NewReader("ID=fedora\nVERSION_ID=42\nPRETTY_NAME=\"Fedora Linux 42\"\n"), "x86_64")
	if err == nil {
		t.Fatal("expected unsupported")
	}
	if !strings.Contains(err.Error(), "does not currently support") {
		t.Fatal(err)
	}
	if !strings.Contains(err.Error(), "debian 13") {
		t.Fatal(err)
	}
}

func TestRecoverAdminRequiresRoot(t *testing.T) {
	if euid() == 0 {
		t.Skip("running as root")
	}
	err := RecoverAdmin("admin", "password12")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRecoverAdminSQLFailsClosedWhenUserMissing(t *testing.T) {
	sql := recoverAdminSQL("missing-admin", "hash", "audit-1")
	if !strings.Contains(sql, "RAISE EXCEPTION") || !strings.Contains(sql, "user not found") {
		t.Fatal(sql)
	}
	if !strings.Contains(sql, pgQuote("missing-admin")) {
		t.Fatal(sql)
	}
	if !strings.Contains(sql, "RAISE EXCEPTION") {
		t.Fatal("must fail closed when the user row is missing")
	}
}
