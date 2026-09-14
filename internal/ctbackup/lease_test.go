package ctbackup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecoverOwnedFreezesThawsOnlyLeasedUnits(t *testing.T) {
	prevRoot := cgroupRoot
	prevLease := freezeLeaseDir
	t.Cleanup(func() { cgroupRoot = prevRoot; freezeLeaseDir = prevLease })
	cgroupRoot = t.TempDir()
	freezeLeaseDir = t.TempDir()

	owned := "nodal-ct@11111111-1111-4111-8111-111111111111.service"
	other := "nodal-ct@22222222-2222-4222-8222-222222222222.service"
	ownedPath := filepath.Join(cgroupRoot, "lxc.payload."+freezeUnitID(owned), "cgroup.freeze")
	otherPath := filepath.Join(cgroupRoot, "lxc.payload."+freezeUnitID(other), "cgroup.freeze")
	if err := os.MkdirAll(filepath.Dir(ownedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(otherPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownedPath, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFreezeLease(owned); err != nil {
		t.Fatal(err)
	}

	if n := RecoverOwnedFreezes(); n != 1 {
		t.Fatalf("recovered %d", n)
	}
	if strings.TrimSpace(string(mustRead(t, ownedPath))) != "0" {
		t.Fatal("owned freeze must thaw")
	}
	if strings.TrimSpace(string(mustRead(t, otherPath))) != "1" {
		t.Fatal("unleased freeze must stay frozen")
	}
	if _, err := os.Stat(leasePath(owned)); !os.IsNotExist(err) {
		t.Fatal("lease must be removed after recover")
	}
}

func TestUnfreezeRemovesLease(t *testing.T) {
	prevRoot := cgroupRoot
	prevLease := freezeLeaseDir
	t.Cleanup(func() { cgroupRoot = prevRoot; freezeLeaseDir = prevLease })
	cgroupRoot = t.TempDir()
	freezeLeaseDir = t.TempDir()
	unit := "nodal-ct@11111111-1111-4111-8111-111111111111.service"
	dir := filepath.Join(cgroupRoot, "lxc.payload.11111111-1111-4111-8111-111111111111")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.freeze"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unfreeze, err := freezeUnitIfPresent(unit, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leasePath(unit)); err != nil {
		t.Fatalf("lease missing while frozen: %v", err)
	}
	unfreeze()
	if _, err := os.Stat(leasePath(unit)); !os.IsNotExist(err) {
		t.Fatal("lease must be removed after unfreeze")
	}
}

func TestExpiredLeaseWatchdogThaws(t *testing.T) {
	prevRoot := cgroupRoot
	prevLease := freezeLeaseDir
	prevMax := maxFreeze
	t.Cleanup(func() {
		cgroupRoot = prevRoot
		freezeLeaseDir = prevLease
		maxFreeze = prevMax
	})
	cgroupRoot = t.TempDir()
	freezeLeaseDir = t.TempDir()
	maxFreeze = time.Millisecond
	unit := "nodal-ct@11111111-1111-4111-8111-111111111111.service"
	path := filepath.Join(cgroupRoot, "lxc.payload.11111111-1111-4111-8111-111111111111", "cgroup.freeze")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFreezeLease(unit); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	if n := recoverExpiredFreezes(time.Now().UTC()); n != 1 {
		t.Fatalf("expired %d", n)
	}
	if strings.TrimSpace(string(mustRead(t, path))) != "0" {
		t.Fatal("expired lease must thaw")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
