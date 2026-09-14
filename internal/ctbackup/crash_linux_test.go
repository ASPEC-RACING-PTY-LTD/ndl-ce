package ctbackup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestCrashRecoveryThawsOwnedDisposableCgroup freezes a throwaway process in
// a real LXC-style payload cgroup, skips unfreeze (agent death), then recovers.
func TestCrashRecoveryThawsOwnedDisposableCgroup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root required")
	}
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		t.Skip("cgroup2 required")
	}
	id := uuid.NewString()
	payload := filepath.Join("/sys/fs/cgroup", "lxc.payload."+id)
	if err := os.Mkdir(payload, 0o755); err != nil {
		t.Skipf("cannot create disposable payload cgroup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(payload, "cgroup.freeze")); err != nil {
		_ = os.Remove(payload)
		t.Skip("freezer not available on disposable cgroup")
	}
	cmd := exec.Command("sleep", "90")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(payload)
		t.Fatal(err)
	}
	cleanup := func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
		}
		_ = os.WriteFile(filepath.Join(payload, "cgroup.freeze"), []byte("0\n"), 0o644)
		if cmd.Process != nil {
			_ = os.WriteFile("/sys/fs/cgroup/cgroup.procs", []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
		}
		_ = os.Remove(payload)
	}
	t.Cleanup(cleanup)

	if err := os.WriteFile(filepath.Join(payload, "cgroup.procs"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Skipf("cannot move disposable process into payload cgroup: %v", err)
	}

	prevLease := freezeLeaseDir
	freezeLeaseDir = t.TempDir()
	t.Cleanup(func() { freezeLeaseDir = prevLease })

	unit := "nodal-ct@" + id + ".service"
	unfreeze, err := freezeUnitIfPresent(unit, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = unfreeze
	got, err := os.ReadFile(filepath.Join(payload, "cgroup.freeze"))
	if err != nil || strings.TrimSpace(string(got)) != "1" {
		t.Fatalf("disposable cgroup must freeze: %s %v", got, err)
	}
	if _, err := os.Stat(leasePath(unit)); err != nil {
		t.Fatalf("lease missing after freeze: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	if n := RecoverOwnedFreezes(); n != 1 {
		t.Fatalf("recovered %d", n)
	}
	got, err = os.ReadFile(filepath.Join(payload, "cgroup.freeze"))
	if err != nil || strings.TrimSpace(string(got)) != "0" {
		t.Fatalf("crash recover must thaw owned freeze: %s %v", got, err)
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("disposable process should still exist after thaw: %v", err)
	}
	if err := syscall.Kill(cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatalf("thawed process must accept SIGKILL: %v", err)
	}
}
