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

func debian13(t *testing.T) Platform {
	t.Helper()
	p, err := DetectFrom(strings.NewReader("ID=debian\nVERSION_ID=13\nPRETTY_NAME=\"Debian GNU/Linux 13\"\n"), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// withRepoRoot points repository files at a temp dir for one test.
func withRepoRoot(t *testing.T, configured bool) string {
	t.Helper()
	root := t.TempDir()
	prevRoot, prevFetch := repoRoot, fetchRepositoryKey
	repoRoot = root
	t.Cleanup(func() { repoRoot, fetchRepositoryKey = prevRoot, prevFetch })
	fetchRepositoryKey = func(context.Context) ([]byte, error) { return nil, errors.New("no network in tests") }
	if configured {
		path := filepath.Join(root, repositorySource)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(repositorySources(keyringArmored)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

type fakeHost struct {
	calls [][]string
	unit  string
}

func (f *fakeHost) exec(_ context.Context, argv []string) (string, error) {
	f.calls = append(f.calls, append([]string{}, argv...))
	switch {
	case argv[0] == "/usr/bin/systemctl" && argv[1] == "show":
		return f.unit, nil
	case argv[0] == "/usr/bin/apt-cache":
		return argv[len(argv)-1] + ":\n  Installed: 1.1.1\n  Candidate: 1.1.1\n", nil
	case argv[0] == "/usr/bin/journalctl":
		return "Setting up ndl-control (1.1.1) ...", nil
	}
	return "", nil
}

func (f *fakeHost) ran(prefix ...string) bool {
	for _, c := range f.calls {
		if len(c) >= len(prefix) && strings.Join(c[:len(prefix)], " ") == strings.Join(prefix, " ") {
			return true
		}
	}
	return false
}

func TestApplyRunsDetachedAndUpgradesEveryCorePackage(t *testing.T) {
	withRepoRoot(t, true)
	host := &fakeHost{unit: "LoadState=not-found\nActiveState=inactive\nSubState=dead\nResult=success\n"}
	res, err := RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "apply"}, host.exec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "running" {
		t.Fatalf("apply must report running until the unit finishes: %+v", res)
	}
	var launch []string
	for _, c := range host.calls {
		if c[0] == "/usr/bin/systemd-run" {
			launch = c
		}
	}
	if launch == nil {
		t.Fatalf("apply must run in its own unit so agent restarts cannot kill dpkg: %v", host.calls)
	}
	joined := strings.Join(launch, " ")
	for _, want := range append([]string{"--unit=" + debian.ApplyUnit, "RemainAfterExit=yes", "AllowUnauthenticated=false"}, PackageNames...) {
		if !strings.Contains(joined, want) {
			t.Fatalf("launch argv missing %q: %s", want, joined)
		}
	}
	if !host.ran("/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false", "update") {
		t.Fatal("apply must refresh the signed indexes first")
	}
	if host.ran("/usr/bin/systemctl", "stop") {
		t.Fatal("no earlier unit was loaded, nothing to stop")
	}
}

func TestApplyClearsFinishedUnitAndRefusesRunningOne(t *testing.T) {
	withRepoRoot(t, true)
	done := &fakeHost{unit: "LoadState=loaded\nActiveState=active\nSubState=exited\nResult=success\n"}
	if res, _ := RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "apply"}, done.exec); res.Status != "running" {
		t.Fatalf("%+v", res)
	}
	if !done.ran("/usr/bin/systemctl", "stop", debian.ApplyUnit) || !done.ran("/usr/bin/systemctl", "reset-failed", debian.ApplyUnit) {
		t.Fatalf("finished unit must be cleared: %v", done.calls)
	}

	busy := &fakeHost{unit: "LoadState=loaded\nActiveState=active\nSubState=running\nResult=success\n"}
	res, _ := RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: "apply"}, busy.exec)
	if res.Status != "failed" || !strings.Contains(res.Reason, "already running") {
		t.Fatalf("%+v", res)
	}
	if busy.ran("/usr/bin/systemd-run") {
		t.Fatal("must not start a second update")
	}
}

func TestApplyStatusReadsUnitResult(t *testing.T) {
	withRepoRoot(t, true)
	cases := map[string]string{
		"LoadState=loaded\nActiveState=active\nSubState=running\nResult=success\n":                                                 "running",
		"LoadState=loaded\nActiveState=active\nSubState=exited\nResult=success\nInvocationID=0123456789abcdef0123456789abcdef\n":   "succeeded",
		"LoadState=loaded\nActiveState=failed\nSubState=failed\nResult=exit-code\nInvocationID=0123456789abcdef0123456789abcdef\n": "failed",
		"LoadState=not-found\nActiveState=inactive\nSubState=dead\nResult=success\n":                                               "not_reported",
	}
	for unit, want := range cases {
		host := &fakeHost{unit: unit}
		res, err := RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: UpdateApplyStatus}, host.exec)
		if err != nil || res.Status != want {
			t.Fatalf("unit %q: status %q want %q (%v)", unit, res.Status, want, err)
		}
		if want == "succeeded" && res.Version != "1.1.1" {
			t.Fatalf("installed version not reported: %+v", res)
		}
		if want == "failed" && !strings.Contains(res.Log, "Setting up") {
			t.Fatalf("failure must carry the package output: %+v", res)
		}
	}
}

func TestCheckAndApplyNeedTheReleaseRepository(t *testing.T) {
	withRepoRoot(t, false)
	host := &fakeHost{}
	for _, action := range []string{"check", "apply"} {
		res, _ := RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: action}, host.exec)
		if res.Status != "failed" || res.Reason != RepositoryNotConfigured || res.RepositoryConfigured {
			t.Fatalf("%s: %+v", action, res)
		}
	}
	if len(host.calls) != 0 {
		t.Fatalf("nothing must run without the repository: %v", host.calls)
	}
}

func TestEnableRepositoryWritesKeyAndSource(t *testing.T) {
	root := withRepoRoot(t, false)
	key := []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmQENBF...\n-----END PGP PUBLIC KEY BLOCK-----\n")
	fetchRepositoryKey = func(context.Context) ([]byte, error) { return key, nil }
	host := &fakeHost{}
	res, err := RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: UpdateRepoEnable}, host.exec)
	if err != nil || !res.RepositoryConfigured || res.Status == "failed" {
		t.Fatalf("%+v %v", res, err)
	}
	got, err := os.ReadFile(filepath.Join(root, keyringArmored))
	if err != nil || string(got) != string(key) {
		t.Fatalf("key not written: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, repositorySource))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"URIs: " + RepositoryURL, "Suites: trixie", "Signed-By: /" + keyringArmored} {
		if !strings.Contains(string(src), want) {
			t.Fatalf("source missing %q:\n%s", want, src)
		}
	}
	if !RepositoryConfigured() {
		t.Fatal("repository must now be configured")
	}
}

func TestEnableRepositoryRejectsNonKeys(t *testing.T) {
	root := withRepoRoot(t, false)
	fetchRepositoryKey = func(context.Context) ([]byte, error) { return []byte("<html>not a key</html>"), nil }
	res, _ := RunUpdate(context.Background(), debian13(t), UpdateRequest{Action: UpdateRepoEnable}, (&fakeHost{}).exec)
	if res.Status != "failed" || res.RepositoryConfigured {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, repositorySource)); err == nil {
		t.Fatal("no source may be written for a bad key")
	}
	if keyringName([]byte{0x99, 0x01}) != keyringBinary || keyringName([]byte{0xc6, 0x01}) != keyringBinary {
		t.Fatal("binary public keys must be accepted")
	}
}
