package agentrpc

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// startGameAgent serves a Handler on a unix socket, as ndl-agent does, with
// a game host whose Docker calls are recorded instead of run.
func startGameAgent(t *testing.T) (GameHost, *gameserver.LocalHost, func() [][]string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ndlgs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root := filepath.Join(dir, "data")
	host := gameserver.NewLocalHost(root)
	var mu sync.Mutex
	var calls [][]string
	host.RT.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		mu.Lock()
		calls = append(calls, append([]string{name}, args...))
		mu.Unlock()
		if len(args) > 0 && args[0] == "run" {
			// Installs take long enough for progress to be polled.
			time.Sleep(150 * time.Millisecond)
			return []byte("cid-123\n"), nil
		}
		if len(args) > 0 && args[0] == "inspect" {
			return []byte("true\n"), nil
		}
		return nil, nil
	}
	h := &Handler{Games: host}
	path, handler := agentv1connect.NewAgentServiceHandler(h)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h2c.NewHandler(mux, &http2.Server{})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return GameHost{Client: Client{Socket: sock}, ProgressEvery: 20 * time.Millisecond}, host, func() [][]string {
		mu.Lock()
		defer mu.Unlock()
		return append([][]string{}, calls...)
	}
}

func TestGameHostRunsEverythingInTheAgent(t *testing.T) {
	g, local, calls := startGameAgent(t)
	ctx := context.Background()
	id := "5b0f4c1e-9d2b-4b7a-9f1e-3f2a1b0c9d8e"

	dir, err := g.EnsureData(ctx, id)
	if err != nil || dir != local.RT.DataDir(id) {
		t.Fatalf("%q %v", dir, err)
	}
	if _, err := g.EnsureData(ctx, "../../etc"); err == nil {
		t.Fatal("invalid ids must be refused by the agent")
	}

	var phases []string
	var pmu sync.Mutex
	tmpl := gameserver.Template{ID: "t", Name: "Test", InstallScript: "echo hi", InstallImage: "debian:bookworm-slim", DefaultImage: "debian:bookworm-slim"}
	err = g.Install(ctx, gameserver.Server{ID: id, Env: map[string]string{}}, tmpl, func(phase, _ string, _ string) {
		pmu.Lock()
		phases = append(phases, phase)
		pmu.Unlock()
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	pmu.Lock()
	if len(phases) == 0 || phases[0] != "downloading" {
		t.Fatalf("install progress must reach the control plane: %v", phases)
	}
	pmu.Unlock()
	if !strings.Contains(g.LogTail(ctx, id, 50), "install finished") {
		t.Fatalf("log tail: %q", g.LogTail(ctx, id, 50))
	}

	cid, err := g.Start(ctx, gameserver.RunSpec{ID: id, Image: "debian:bookworm-slim", Startup: "sleep 1", NetworkMode: "host"})
	if err != nil || cid != "cid-123" {
		t.Fatalf("%q %v", cid, err)
	}
	var run []string
	for _, c := range calls() {
		if len(c) > 1 && c[1] == "run" && strings.Contains(strings.Join(c, " "), "ndl-gs-"+id) {
			run = c
		}
	}
	if !strings.Contains(strings.Join(run, " "), "--network host") {
		t.Fatalf("network mode from the control plane must be used: %v", run)
	}
	if !g.Running(ctx, id) {
		t.Fatal("running")
	}
	if err := g.Send(ctx, id, "say hi"); err != nil {
		t.Fatal(err)
	}
	if err := g.StopGraceful(ctx, id, "stop", time.Second); err != nil {
		t.Fatal(err)
	}

	if err := g.WriteFile(ctx, id, "server.properties", []byte("motd=hi\n")); err != nil {
		t.Fatal(err)
	}
	if err := g.WriteFile(ctx, id, "../../outside", []byte("x")); err == nil {
		t.Fatal("writes must stay inside the server folder")
	}
	body, err := g.ReadFile(ctx, id, "server.properties", 1<<20)
	if err != nil || string(body) != "motd=hi\n" {
		t.Fatalf("%q %v", body, err)
	}
	if _, err := g.ReadFile(ctx, id, "missing.txt", 1<<20); err == nil {
		t.Fatal("missing file")
	}
	if _, err := g.ListDir(ctx, id, "nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing dir must map to not found: %v", err)
	}
	listing, err := g.ListDir(ctx, id, ".")
	if err != nil || len(listing.Items) == 0 {
		t.Fatalf("%+v %v", listing, err)
	}
	if err := g.Mkdir(ctx, id, "mods"); err != nil {
		t.Fatal(err)
	}
	if err := g.Rename(ctx, id, "server.properties", "mods/server.properties"); err != nil {
		t.Fatal(err)
	}
	b, err := g.Backup(ctx, id, "bk1")
	if err != nil || b.Bytes == 0 {
		t.Fatalf("%+v %v", b, err)
	}
	if err := g.Remove(ctx, id, "mods"); err != nil {
		t.Fatal(err)
	}
	if err := g.RestoreBackup(ctx, id, "bk1"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ReadFile(ctx, id, "mods/server.properties", 1<<20); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := g.DeleteBackup(ctx, id, "bk1"); err != nil {
		t.Fatal(err)
	}
	if err := g.Kill(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveData(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("server folder must be gone: %v", err)
	}
}
