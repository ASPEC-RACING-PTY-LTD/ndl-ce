package lxc

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestDockerStartArgvCoversDebianAndAlpine(t *testing.T) {
	deb := dockerStartArgv("debian")
	if len(deb) == 0 || deb[0][0] != "/bin/systemctl" {
		t.Fatalf("debian docker start argv: %+v", deb)
	}
	alp := dockerStartArgv("alpine")
	if len(alp) == 0 || alp[0][0] != "/sbin/rc-update" {
		t.Fatalf("alpine docker start argv: %+v", alp)
	}
	if dockerStartArgv("unknown") != nil {
		t.Fatal("unknown family must not invent start commands")
	}
}

func TestStartGuestDockerFailsWhenInfoNeverSucceeds(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	e.Run = func(context.Context, string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("docker info failed")
	}
	err := e.startGuestDocker(context.Background(), "id", "debian")
	if err == nil || !strings.Contains(err.Error(), "engine is not running") {
		t.Fatalf("got %v", err)
	}
}

func TestFirstAvailableComposePackagePrefersExisting(t *testing.T) {
	got := firstAvailableComposePackage([]string{"docker-compose-v2", "docker-compose"}, func(name string) bool {
		return name == "docker-compose"
	})
	if got != "docker-compose" {
		t.Fatalf("trixie must select docker-compose, got %q", got)
	}
	got = firstAvailableComposePackage([]string{"docker-compose-v2", "docker-compose"}, func(name string) bool {
		return name == "docker-compose-v2"
	})
	if got != "docker-compose-v2" {
		t.Fatalf("backports must select docker-compose-v2, got %q", got)
	}
	if firstAvailableComposePackage([]string{"docker-compose-v2"}, func(string) bool { return false }) != "" {
		t.Fatal("missing packages must not invent a candidate")
	}
}

func TestInstallGuestComposeIsIdempotentWhenPluginWorks(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	calls := 0
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		joined := name + " " + strings.Join(args, " ")
		if strings.Contains(joined, "docker") && strings.Contains(joined, "compose") && strings.Contains(joined, "version") {
			return []byte("Docker Compose version v2.26.1"), nil
		}
		t.Fatalf("unexpected command %s", joined)
		return nil, fmt.Errorf("unexpected")
	}
	if err := e.installGuestCompose(context.Background(), "id", "debian", []string{"docker-compose-v2", "docker-compose"}, extraInstall); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("already-installed compose must not apt-get, calls=%d", calls)
	}
}

func TestInstallGuestComposeReportsPluginFailure(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		joined := name + " " + strings.Join(args, " ")
		if strings.Contains(joined, "compose") && strings.Contains(joined, "version") {
			return nil, fmt.Errorf("unknown command")
		}
		if strings.Contains(joined, "apt-cache") && strings.Contains(joined, "show") {
			if strings.Contains(joined, "docker-compose-v2") {
				return nil, fmt.Errorf("E: No packages found")
			}
			return []byte("Package: docker-compose\nVersion: 2.26.1-4\n"), nil
		}
		if strings.Contains(joined, "apt-get") {
			return []byte("ok"), nil
		}
		return nil, fmt.Errorf("not compose")
	}
	err := e.installGuestCompose(context.Background(), "id", "debian", []string{"docker-compose-v2", "docker-compose"}, extraInstall)
	if err == nil || !strings.Contains(err.Error(), "Docker Compose plugin installation failed") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "docker compose version") {
		t.Fatalf("error must mention docker compose version: %v", err)
	}
}

func TestStartGuestDockerSucceedsOnInfo(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		joined := name + " " + strings.Join(args, " ")
		if strings.Contains(joined, "docker") && strings.Contains(joined, "info") {
			return []byte("ok"), nil
		}
		return nil, fmt.Errorf("not info")
	}
	if err := e.startGuestDocker(context.Background(), "id", "debian"); err != nil {
		t.Fatal(err)
	}
}

func TestAptRetryWaitsOutLockAndKeepsAptsMessage(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	calls := 0
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		return []byte("Reading package lists...\nE: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 412 (apt-get)\n"), fmt.Errorf("exit status 100")
	}
	err := e.aptRetry(context.Background(), "id", "install", "-y", "docker.io")
	if calls != 2 || err == nil || !strings.Contains(err.Error(), "Could not get lock") {
		t.Fatalf("lock must be retried and reported: calls=%d err=%v", calls, err)
	}
	calls = 0
	e.Run = func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return []byte("E: Unable to locate package nope\n"), fmt.Errorf("exit status 100")
	}
	if err := e.aptRetry(context.Background(), "id", "install", "-y", "nope"); calls != 1 || err == nil || err.Error() != "E: Unable to locate package nope" {
		t.Fatalf("other failures are not retried: calls=%d err=%v", calls, err)
	}
}

func TestExtraModesUseUpgradeAndReinstall(t *testing.T) {
	if got := strings.Join(aptModeArgs(extraUpdate, []string{"docker.io"}), " "); got != "install -y --only-upgrade docker.io" {
		t.Fatal(got)
	}
	if got := strings.Join(aptModeArgs(extraReinstall, []string{"git"}), " "); got != "install -y --reinstall git" {
		t.Fatal(got)
	}
	if got := strings.Join(apkModeArgs("/sbin/apk", extraUpdate, []string{"git"}), " "); got != "/sbin/apk add --no-cache --upgrade git" {
		t.Fatal(got)
	}
	e := &Engine{DataDir: t.TempDir()}
	var ran []string
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		ran = append(ran, joined)
		switch {
		case strings.Contains(joined, "dpkg-query") && strings.Contains(joined, " git"):
			return nil, fmt.Errorf("no packages found matching git")
		case strings.Contains(joined, "dpkg-query"):
			return []byte("install ok installed|26.1.5"), nil
		case strings.Contains(joined, "docker info"):
			return []byte("ok"), nil
		}
		return nil, nil
	}
	if err := e.installGuestExtra(context.Background(), "id", "debian", "git", []string{"git"}, extraUpdate); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("updating an extra that is not installed must say so: %v", err)
	}
	ran = nil
	if err := e.installGuestExtra(context.Background(), "id", "debian", "docker", []string{"docker.io"}, extraReinstall); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(ran, "\n"), "install -y --reinstall docker.io") {
		t.Fatalf("reinstall must use --reinstall: %v", ran)
	}
}
