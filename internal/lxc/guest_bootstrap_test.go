package lxc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAlpineReachabilityArgvAvoidsDebianTools(t *testing.T) {
	for _, argv := range alpineReachabilityArgv() {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, "python") || strings.Contains(joined, "getent") || strings.Contains(joined, "apt") {
			t.Fatalf("alpine probe must not use debian tools: %s", joined)
		}
	}
	if len(alpineReachabilityArgv()) == 0 {
		t.Fatal("alpine reachability argv is empty")
	}
}

func TestBootstrapAlpineSkipsDebianProbes(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	id := uuid.NewString()
	root := filepath.Join(e.DataDir, "rootfs")
	if err := os.MkdirAll(filepath.Join(root, guestNDLDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, guestFirstBootstrapRel), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.writeApplied(Spec{
		WorkloadID: id, Name: "alp", ImagePin: "alpine/3.21/amd64/default", RootfsPath: root,
	}, true, "abc"); err != nil {
		t.Fatal(err)
	}
	var cmds []string
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		joined := name + " " + strings.Join(args, " ")
		cmds = append(cmds, joined)
		if strings.Contains(joined, "busybox") && strings.Contains(joined, "wget") {
			return []byte("ok"), nil
		}
		return nil, fmt.Errorf("not alpine reachable")
	}
	if err := e.bootstrapGuest(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range cmds {
		if strings.Contains(c, "apt-get") || strings.Contains(c, "getent") || strings.Contains(c, "python3") || strings.Contains(c, "systemctl") {
			t.Fatalf("alpine bootstrap used debian tool: %s", c)
		}
	}
	if _, err := os.Stat(filepath.Join(root, guestBaselineRel)); err != nil {
		t.Fatal("alpine first bootstrap must write the baseline marker")
	}
}

func TestBootstrapAlpineWritesResolvFallbackWhenUnreachable(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	id := uuid.NewString()
	root := filepath.Join(e.DataDir, "rootfs")
	if err := os.MkdirAll(filepath.Join(root, guestNDLDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, guestFirstBootstrapRel), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.writeApplied(Spec{
		WorkloadID: id, Name: "alp", ImagePin: "alpine/3.21/amd64/default", RootfsPath: root,
	}, true, "abc"); err != nil {
		t.Fatal(err)
	}
	e.Run = func(context.Context, string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("unreachable")
	}
	err := e.bootstrapGuest(context.Background(), id, false)
	if err == nil || !strings.Contains(err.Error(), "guest DNS and repository reachability failed") {
		t.Fatalf("first alpine bootstrap must fail closed when unreachable: %v", err)
	}
	body, rerr := os.ReadFile(filepath.Join(root, "etc", "resolv.conf"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(body), "1.1.1.1") {
		t.Fatalf("resolv fallback: %s", body)
	}
}

func TestBootstrapReconcileSkipsPackageProvisioning(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	id := uuid.NewString()
	root := filepath.Join(e.DataDir, "rootfs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.writeApplied(Spec{
		WorkloadID: id, Name: "reconcile", ImagePin: "imported", SkipImage: true, RootfsPath: root,
	}, true, "abc"); err != nil {
		t.Fatal(err)
	}
	var commands []string
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		command := name + " " + strings.Join(args, " ")
		commands = append(commands, command)
		if strings.Contains(command, "getent hosts") {
			return []byte("192.0.2.1 example\n"), nil
		}
		return nil, fmt.Errorf("unexpected command: %s", command)
	}
	if err := e.bootstrapGuest(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if strings.Contains(command, "apt-get") {
			t.Fatalf("reconcile must not provision packages: %s", command)
		}
	}
}
