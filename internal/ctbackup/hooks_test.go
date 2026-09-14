package ctbackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyTreeDoesNotFreezeLiveRoot(t *testing.T) {
	prevRoot := cgroupRoot
	prevLease := freezeLeaseDir
	prevCopy := copyTreeCmd
	t.Cleanup(func() {
		cgroupRoot = prevRoot
		freezeLeaseDir = prevLease
		copyTreeCmd = prevCopy
	})
	cgroupRoot = t.TempDir()
	freezeLeaseDir = t.TempDir()
	id := "11111111-1111-4111-8111-111111111111"
	unit := "nodal-ct@" + id + ".service"
	payload := filepath.Join(cgroupRoot, "lxc.payload."+id)
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	freeze := filepath.Join(payload, "cgroup.freeze")
	if err := os.WriteFile(freeze, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(filepath.Join(src, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "etc", "marker"), []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "tree")
	copyTreeCmd = func(ctx context.Context, srcRoot, destRoot string) error {
		body, _ := os.ReadFile(freeze)
		if strings.TrimSpace(string(body)) != "0" {
			t.Fatalf("live copy must not freeze: %s", body)
		}
		return copyTreeDefault(ctx, srcRoot, destRoot)
	}
	if err := CopyTree(context.Background(), src, dest, unit); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "etc", "marker"))
	if err != nil || string(got) != "live" {
		t.Fatalf("copied %s %v", got, err)
	}
	body, _ := os.ReadFile(freeze)
	if strings.TrimSpace(string(body)) != "0" {
		t.Fatalf("guest must stay running after copy: %s", body)
	}
}

func TestCopyTreeRunsGuestHooksWithoutFreeze(t *testing.T) {
	prev := hookRunner
	var got []string
	hookRunner = func(_ context.Context, unit, _, guestPath string) error {
		if !strings.HasPrefix(unit, "nodal-ct@") {
			t.Fatalf("unit %s", unit)
		}
		got = append(got, guestPath)
		return nil
	}
	t.Cleanup(func() { hookRunner = prev })

	src := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "tree")
	unit := "nodal-ct@11111111-1111-4111-8111-111111111111.service"
	if err := CopyTree(context.Background(), src, dest, unit); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != GuestHookPre || got[1] != GuestHookPost {
		t.Fatalf("hooks %v", got)
	}
}

func TestCopyTreePreHookFailureStillRunsPost(t *testing.T) {
	prev := hookRunner
	var got []string
	hookRunner = func(_ context.Context, _, _, guestPath string) error {
		got = append(got, guestPath)
		if guestPath == GuestHookPre {
			return errors.New("quiesce failed")
		}
		return nil
	}
	t.Cleanup(func() { hookRunner = prev })
	src := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	err := CopyTree(context.Background(), src, filepath.Join(t.TempDir(), "tree"), "nodal-ct@11111111-1111-4111-8111-111111111111.service")
	if err == nil || !strings.Contains(err.Error(), "quiesce failed") {
		t.Fatalf("pre-hook error %v", err)
	}
	if len(got) != 2 || got[0] != GuestHookPre || got[1] != GuestHookPost {
		t.Fatalf("hooks %v", got)
	}
}

func TestMissingGuestHooksAreSkipped(t *testing.T) {
	src := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGuestHookDefault(context.Background(), "nodal-ct@11111111-1111-4111-8111-111111111111.service", src, GuestHookPre); err != nil {
		t.Fatal(err)
	}
}

func TestGuestHookRejectsPathEscape(t *testing.T) {
	src := t.TempDir()
	err := runGuestHookDefault(context.Background(), "nodal-ct@11111111-1111-4111-8111-111111111111.service", src, "/tmp/evil")
	if err == nil || !strings.Contains(err.Error(), "unsupported backup hook") {
		t.Fatalf("escape %v", err)
	}
}
