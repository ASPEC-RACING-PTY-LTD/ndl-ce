//go:build livegs

package gameserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveFiveServerMatrix(t *testing.T) {
	docker := strings.TrimSpace(os.Getenv("NDL_GS_DOCKER"))
	if docker == "" {
		docker = "/tmp/ndl-gs-bin/docker/docker"
	}
	if _, err := os.Stat(docker); err != nil {
		t.Skip("disposable docker CLI is not present")
	}
	root := t.TempDir()
	rt := NewRuntime(root)
	rt.Docker = docker
	rt.DockerHost = FirstNonEmpty(os.Getenv("DOCKER_HOST"), "unix:///tmp/ndl-gs.sock")
	rt.NetworkMode = FirstNonEmpty(os.Getenv("NDL_GS_NETWORK"), "host")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
	defer cancel()
	if err := rt.Available(ctx); err != nil {
		t.Fatalf("docker: %v", err)
	}

	type caseSpec struct {
		id       string
		env      map[string]string
		start    bool
		file     string
		credNote string
	}
	cases := []caseSpec{
		{id: "ndl-minecraft-paper", env: map[string]string{"EULA": "true", "MC_VERSION": "1.21.10", "BUILD_NUMBER": "latest", "SERVER_JARFILE": "server.jar", "SERVER_MEMORY": "768"}, start: true, file: "server.jar"},
		{id: "ndl-mindustry", env: map[string]string{"MINDUSTRY_VERSION": "latest", "SERVER_MEMORY": "512"}, start: true, file: "server-release.jar"},
		{id: "ndl-valheim", env: map[string]string{"SRCDS_APPID": "896660", "SERVER_NAME": "NDL Test", "WORLD": "Dedicated", "SERVER_PASSWORD": "secret", "SERVER_PORT": "2456", "PUBLIC": "0"}, start: true, file: "valheim_server.x86_64"},
		{id: "ndl-gmod", env: map[string]string{"SRCDS_APPID": "4020", "SERVER_NAME": "NDL Test", "MAP": "gm_flatgrass", "GAMEMODE": "sandbox", "MAX_PLAYERS": "4", "SERVER_PORT": "27015"}, start: true, file: "srcds_run", credNote: "public listing needs a GSLT; anonymous SteamCMD install does not"},
		{id: "ndl-fivem", env: map[string]string{"FIVEM_LICENSE": "", "SERVER_NAME": "NDL Test", "MAX_CLIENTS": "8", "FIVEM_ARTIFACT": "latest"}, start: false, file: "run.sh", credNote: "Cfx.re license is required to start; install is exercised without faking start success"},
		{id: "ndl-minecraft-vanilla", env: map[string]string{"EULA": "true", "MC_VERSION": "1.21.8", "SERVER_JARFILE": "server.jar", "SERVER_MEMORY": "768"}, start: true, file: "server.jar"},
		{id: "ndl-minecraft-fabric", env: map[string]string{"EULA": "true", "MC_VERSION": "1.21.8", "FABRIC_LOADER": "latest", "SERVER_JARFILE": "server.jar", "SERVER_MEMORY": "768"}, start: true, file: "server.jar"},
		{id: "ndl-minecraft-purpur", env: map[string]string{"EULA": "true", "MC_VERSION": "1.21.8", "SERVER_JARFILE": "server.jar", "SERVER_MEMORY": "768"}, start: true, file: "server.jar"},
		{id: "ndl-velocity", env: map[string]string{"MC_VERSION": "latest", "BUILD_NUMBER": "latest", "SERVER_JARFILE": "velocity.jar", "SERVER_MEMORY": "512"}, start: true, file: "velocity.jar"},
		{id: "ndl-terraria", env: map[string]string{"TERRARIA_VERSION": "1449", "WORLD": "World", "MAX_PLAYERS": "4", "SERVER_PORT": "7777", "AUTOCREATE": "1"}, start: true, file: "TerrariaServer.bin.x86_64"},
		{id: "ndl-factorio", env: map[string]string{"FACTORIO_VERSION": "stable", "SAVE_NAME": "world", "SERVER_PORT": "34197"}, start: true, file: "bin/x64/factorio"},
		{id: "ndl-dst", env: map[string]string{"SRCDS_APPID": "343050", "CLUSTER_TOKEN": "", "CLUSTER_NAME": "Cluster_1", "SERVER_NAME": "NDL Test", "MAX_PLAYERS": "4"}, start: false, file: "bin64/dontstarve_dedicated_server_nullrenderer_x64", credNote: "Klei cluster token is required to start; install is exercised without faking start success"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.id, func(t *testing.T) {
			tmpl, ok := templateByID(c.id)
			if !ok {
				t.Fatal("missing template")
			}
			srv := Server{ID: strings.ReplaceAll(c.id, "ndl-", "live-"), Env: c.env}
			if err := rt.Install(ctx, srv, tmpl); err != nil {
				t.Fatalf("install: %v", err)
			}
			if c.file != "" {
				st, err := os.Stat(filepath.Join(rt.DataDir(srv.ID), c.file))
				if err != nil {
					t.Fatalf("expected %s after install: %v", c.file, err)
				}
				t.Logf("installed %s (%d bytes) in %s", c.file, st.Size(), rt.DataDir(srv.ID))
			}
			note := filepath.Join(rt.DataDir(srv.ID), "ndl-operator.txt")
			if err := os.WriteFile(note, []byte("live matrix"), 0o640); err != nil {
				t.Fatal(err)
			}
			ports := tmpl.DefaultPorts
			for i := range ports {
				if ports[i].HostPort == 0 {
					ports[i].HostPort = ports[i].ContainerPort + 10000
				}
			}
			if c.credNote != "" {
				t.Log(c.credNote)
			}
			if !c.start {
				return
			}
			_, err := rt.Start(ctx, RunSpec{
				ID: srv.ID, Image: tmpl.DefaultImage, Startup: ExpandStartup(tmpl.Startup, c.env, MemoryMB(int64(tmpl.DefaultMemoryMB)<<20), PrimaryPort(ports)),
				Env: c.env, Ports: ports, CPUs: 1, MemoryBytes: 768 << 20, WorkDir: tmpl.WorkingDir,
			})
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			time.Sleep(3 * time.Second)
			if !rt.Running(ctx, srv.ID) {
				logs, _ := rt.Logs(ctx, srv.ID, 80)
				t.Fatalf("not running after start:\n%s", logs)
			}
			_ = rt.Send(ctx, srv.ID, "help")
			if err := rt.Stop(ctx, srv.ID); err != nil {
				t.Fatalf("stop: %v", err)
			}
			if _, err := rt.Start(ctx, RunSpec{
				ID: srv.ID, Image: tmpl.DefaultImage, Startup: ExpandStartup(tmpl.Startup, c.env, 512, PrimaryPort(ports)),
				Env: c.env, Ports: ports, CPUs: 1, MemoryBytes: 768 << 20, WorkDir: tmpl.WorkingDir,
			}); err != nil {
				t.Fatalf("restart: %v", err)
			}
			_ = rt.Kill(ctx, srv.ID)
		})
	}
}
