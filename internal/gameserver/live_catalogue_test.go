//go:build livegs

package gameserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLiveCatalogue installs and starts real templates in a disposable
// Docker daemon. It never touches production: it needs NDL_LIVE_IDS (comma
// separated template IDs) and writes a JSON result per template to
// NDL_LIVE_OUT so verification.go can be updated by hand from real runs.
//
//	NDL_LIVE_IDS=ndl-mindustry NDL_LIVE_OUT=/tmp/live go test -tags livegs -run TestLiveCatalogue ./internal/gameserver/
func TestLiveCatalogue(t *testing.T) {
	ids := strings.Split(strings.TrimSpace(os.Getenv("NDL_LIVE_IDS")), ",")
	if len(ids) == 0 || ids[0] == "" {
		t.Skip("NDL_LIVE_IDS is not set")
	}
	out := FirstNonEmpty(os.Getenv("NDL_LIVE_OUT"), t.TempDir())
	_ = os.MkdirAll(out, 0o750)
	root := FirstNonEmpty(os.Getenv("NDL_LIVE_ROOT"), t.TempDir())
	rt := NewRuntime(root)
	if d := os.Getenv("NDL_GS_DOCKER"); d != "" {
		rt.Docker = d
	} else {
		rt.Docker = "docker"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	if err := rt.Available(ctx); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	for _, id := range ids {
		id := strings.TrimSpace(id)
		t.Run(id, func(t *testing.T) {
			res := map[string]any{"id": id, "date": time.Now().UTC().Format("2006-01-02")}
			defer func() {
				raw, _ := json.MarshalIndent(res, "", "  ")
				_ = os.WriteFile(filepath.Join(out, id+".json"), raw, 0o640)
			}()
			tmpl, ok := templateByID(id)
			if !ok {
				t.Fatal("unknown template")
			}
			env := mergeEnv(tmpl, map[string]string{"EULA": "true"})
			for k, v := range liveOverrides(os.Getenv("NDL_LIVE_ENV_" + strings.ToUpper(strings.ReplaceAll(strings.TrimPrefix(id, "ndl-"), "-", "_")))) {
				env[k] = v
			}
			srv := Server{ID: "live-" + strings.TrimPrefix(id, "ndl-"), Env: env, Image: tmpl.DefaultImage}
			start := time.Now()
			if err := rt.Install(ctx, srv, tmpl); err != nil {
				res["level"] = "failed-install"
				res["error"] = clip(err.Error(), 2000)
				res["log"] = rt.LogTail(srv.ID, 40)
				t.Fatalf("install: %v", err)
			}
			res["install_seconds"] = int(time.Since(start).Seconds())
			res["level"] = VerifyInstalled
			entries, _ := os.ReadDir(rt.DataDir(srv.ID))
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			res["files"] = names
			if os.Getenv("NDL_LIVE_START") == "0" {
				return
			}
			if err := CheckStartRequirements(tmpl, env); err != nil {
				res["start_skipped"] = err.Error()
				return
			}
			ports := make([]Port, 0, len(tmpl.DefaultPorts))
			for _, p := range tmpl.DefaultPorts {
				p.HostPort = p.ContainerPort + 20000
				if p.HostPort > 65535 {
					p.HostPort = p.ContainerPort
				}
				ports = append(ports, p)
			}
			mem := int64(tmpl.DefaultMemoryMB) << 20
			startup := ExpandStartup(tmpl.Startup, env, MemoryMB(mem), PrimaryPort(tmpl.DefaultPorts))
			if _, err := rt.Start(ctx, RunSpec{ID: srv.ID, Image: tmpl.DefaultImage, Startup: startup, Env: env, Ports: ports, CPUs: tmpl.DefaultCPUs, MemoryBytes: mem, WorkDir: tmpl.WorkingDir, Dependencies: tmpl.Dependencies}); err != nil {
				res["level"] = "failed-start"
				res["error"] = clip(err.Error(), 2000)
				t.Fatalf("start: %v", err)
			}
			wait := 45 * time.Second
			if w := os.Getenv("NDL_LIVE_WAIT"); w != "" {
				if d, err := time.ParseDuration(w); err == nil {
					wait = d
				}
			}
			deadline := time.Now().Add(wait)
			ready := false
			for time.Now().Before(deadline) {
				if !rt.Running(ctx, srv.ID) {
					break
				}
				if tmpl.Done != "" {
					logs, _ := rt.Logs(ctx, srv.ID, 400)
					if strings.Contains(logs, tmpl.Done) {
						ready = true
						break
					}
				}
				time.Sleep(2 * time.Second)
			}
			running := rt.Running(ctx, srv.ID)
			logs, _ := rt.Logs(ctx, srv.ID, 60)
			res["console_tail"] = clip(logs, 4000)
			res["ready_marker_seen"] = ready
			if !running {
				res["level"] = "failed-start"
				t.Fatalf("server exited:\n%s", logs)
			}
			res["level"] = VerifyStarted
			stopStart := time.Now()
			_ = rt.StopGraceful(ctx, srv.ID, tmpl.Stop, 30*time.Second)
			res["stop_seconds"] = int(time.Since(stopStart).Seconds())
			res["stop_command"] = tmpl.Stop
		})
	}
}

func liveOverrides(raw string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(raw, ";") {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			out[strings.TrimSpace(k)] = v
		}
	}
	return out
}
