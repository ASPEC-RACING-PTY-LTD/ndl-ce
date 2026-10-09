package hostos

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/hostos/debian"
)

// scriptedHost answers apt-cache policy with an available upgrade and lets a
// test fail single commands.
type scriptedHost struct {
	fakeHost
	fail  map[string]error
	out   map[string]string
	unit  string
	held  string
	audit string
}

func (h *scriptedHost) exec(ctx context.Context, argv []string) (string, error) {
	h.calls = append(h.calls, append([]string{}, argv...))
	joined := strings.Join(argv, " ")
	for key, err := range h.fail {
		if strings.Contains(joined, key) {
			return h.out[key], err
		}
	}
	switch {
	case argv[0] == "/usr/bin/systemctl" && argv[1] == "show":
		return h.unit, nil
	case argv[0] == "/usr/bin/apt-cache":
		return argv[len(argv)-1] + ":\n  Installed: 1.1.4\n  Candidate: 1.1.5\n", nil
	case argv[0] == "/usr/bin/apt-mark":
		return h.held, nil
	case argv[0] == "/usr/bin/dpkg":
		return h.audit, nil
	}
	return "", nil
}

func withProbes(t *testing.T, free uint64, locked bool) {
	t.Helper()
	prevFree, prevLock := freeBytes, lockHeld
	freeBytes = func(string) (uint64, error) { return free, nil }
	lockHeld = func(string) bool { return locked }
	t.Cleanup(func() { freeBytes, lockHeld = prevFree, prevLock })
}

func checkStatus(res UpdateResult, name string) string {
	for _, c := range res.Checks {
		if c.Name == name && c.Status != "ok" {
			return c.Status
		}
	}
	for _, c := range res.Checks {
		if c.Name == name {
			return c.Status
		}
	}
	return ""
}

func TestPreflightPassesAndReportsTheVersionToInstall(t *testing.T) {
	withRepoRoot(t, true)
	withProbes(t, 50<<30, false)
	host := &scriptedHost{}
	res, _ := RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "preflight"}, host.exec)
	if !res.PreflightOK || res.CandidateVersion != "1.1.5" {
		t.Fatalf("healthy host must pass with the candidate version: %+v", res)
	}
	for _, name := range []string{"disk_space", "package_manager", "package_database", "held_packages", "repository", "update_available"} {
		if checkStatus(res, name) != "ok" {
			t.Fatalf("%s: %+v", name, res.Checks)
		}
	}
}

func TestPreflightFailsClosed(t *testing.T) {
	cases := map[string]struct {
		free   uint64
		locked bool
		host   *scriptedHost
		check  string
	}{
		"low disk":       {1 << 30, false, &scriptedHost{}, "disk_space"},
		"apt locked":     {50 << 30, true, &scriptedHost{}, "package_manager"},
		"update running": {50 << 30, false, &scriptedHost{unit: "LoadState=loaded\nActiveState=active\nSubState=running\n"}, "package_manager"},
		"held package":   {50 << 30, false, &scriptedHost{held: "ndl-agent\n"}, "held_packages"},
		"broken dpkg":    {50 << 30, false, &scriptedHost{audit: "The following packages are only half configured"}, "package_database"},
		"offline":        {50 << 30, false, &scriptedHost{fail: map[string]error{" update": errors.New("exit 100")}}, "repository"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			withRepoRoot(t, true)
			withProbes(t, tc.free, tc.locked)
			res, _ := RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "preflight"}, tc.host.exec)
			if res.PreflightOK || res.Status != "failed" || checkStatus(res, tc.check) != "failed" || res.Reason == "" {
				t.Fatalf("%s must fail preflight: %+v", tc.check, res)
			}
		})
	}
}

func TestCheckpointDumpsAsTheDatabaseOwnerAndCleansUpFailures(t *testing.T) {
	dir := t.TempDir()
	prev := checkpointDir
	checkpointDir = dir
	t.Cleanup(func() { checkpointDir = prev })

	ok := &scriptedHost{}
	res, _ := RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "checkpoint", CheckpointID: "good"}, ok.exec)
	if res.Status != "succeeded" || !res.PostgresDump {
		t.Fatalf("%+v", res)
	}
	if !ok.ran("/usr/sbin/runuser", "-u", debian.DBOwner, "--", "/usr/bin/pg_dump") {
		t.Fatalf("pg_dump must run as the database owner, root has no role: %v", ok.calls)
	}
	if !ok.ran("/usr/bin/install", "-o", debian.DBOwner) || !ok.ran("/usr/bin/chmod", "0600") {
		t.Fatalf("dump file must belong to the owner and the archive be private: %v", ok.calls)
	}

	for _, ext := range []string{".tar", ".sql"} {
		_ = os.WriteFile(filepath.Join(dir, "bad"+ext), []byte("partial"), 0o600)
	}
	bad := &scriptedHost{fail: map[string]error{"pg_dump": errors.New("exit 1")}}
	res, _ = RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "checkpoint", CheckpointID: "bad"}, bad.exec)
	if res.Status != "failed" || res.PostgresDump {
		t.Fatalf("%+v", res)
	}
	for _, ext := range []string{".tar", ".sql"} {
		if _, err := os.Stat(filepath.Join(dir, "bad"+ext)); !os.IsNotExist(err) {
			t.Fatalf("a failed checkpoint must not leave %s behind", ext)
		}
	}

	changed := &scriptedHost{fail: map[string]error{"/usr/bin/tar": exitErr(1)}}
	res, _ = RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "checkpoint", CheckpointID: "busy"}, changed.exec)
	if res.Status != "succeeded" {
		t.Fatalf("tar exit 1 (a file changed while read) must not fail the checkpoint: %+v", res)
	}
}

type exitErr int

func (e exitErr) Error() string { return "exit status" }
func (e exitErr) ExitCode() int { return int(e) }

func TestRollbackMovesEveryPackageAndRestoresTheDatabase(t *testing.T) {
	dir := t.TempDir()
	prev := checkpointDir
	checkpointDir = dir
	t.Cleanup(func() { checkpointDir = prev })

	missing := &scriptedHost{}
	res, _ := RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "rollback", Version: "1.1.4", CheckpointID: "cp"}, missing.exec)
	if res.Status != "failed" || !strings.Contains(res.Reason, "checkpoint") || missing.ran("/usr/bin/systemd-run") {
		t.Fatalf("a rollback that needs a missing dump must not start: %+v", res)
	}

	_ = os.WriteFile(filepath.Join(dir, "cp.sql"), []byte("-- dump"), 0o600)
	host := &scriptedHost{}
	res, _ = RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "rollback", Version: "1.1.4", CheckpointID: "cp"}, host.exec)
	if res.Status != "running" {
		t.Fatalf("%+v", res)
	}
	var launch string
	for _, c := range host.calls {
		if c[0] == "/usr/bin/systemd-run" {
			launch = strings.Join(c, " ")
		}
	}
	for _, want := range []string{"--allow-downgrades", "ExecStartPre=/usr/bin/systemctl stop ndl-control.socket ndl-control.service",
		"--single-transaction", "DROP OWNED BY CURRENT_USER", "ExecStopPost=/usr/bin/systemctl start"} {
		if !strings.Contains(launch, want) {
			t.Fatalf("rollback unit missing %q: %s", want, launch)
		}
	}
	for _, name := range PackageNames {
		if !strings.Contains(launch, name+"=1.1.4") {
			t.Fatalf("rollback must move %s too: %s", name, launch)
		}
	}

	noDB := &scriptedHost{}
	_, _ = RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "rollback", Version: "1.1.4"}, noDB.exec)
	for _, c := range noDB.calls {
		if c[0] == "/usr/bin/systemd-run" && strings.Contains(strings.Join(c, " "), "psql") {
			t.Fatal("without a checkpoint the database must be left alone")
		}
	}

	unavailable := &scriptedHost{fail: map[string]error{"--dry-run": errors.New("exit 100")}}
	res, _ = RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "rollback", Version: "1.1.4"}, unavailable.exec)
	if res.Status != "failed" || unavailable.ran("/usr/bin/systemd-run") {
		t.Fatalf("a version the repository no longer has must not start a rollback: %+v", res)
	}
}
