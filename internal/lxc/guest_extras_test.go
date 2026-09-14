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
	if err := e.installGuestCompose(context.Background(), "id", "debian", []string{"docker-compose-v2", "docker-compose"}); err != nil {
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
	err := e.installGuestCompose(context.Background(), "id", "debian", []string{"docker-compose-v2", "docker-compose"})
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
