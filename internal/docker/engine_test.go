package docker

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotGroupsComposeAndActions(t *testing.T) {
	sock := startFakeEngine(t, fakeState{
		version: "27.0.0",
		containers: []inspectJSON{
			fakeInspect("aaaaaaaaaaaa", "shop-web-1", "running", "nginx:alpine", map[string]string{
				LabelProject: "shop", LabelService: "web", LabelWorkingDir: "/srv/shop",
			}, false, 0),
			fakeInspect("bbbbbbbbbbbb", "solo", "exited", "busybox:latest", nil, false, 0),
		},
	})
	eng := &Engine{
		HostSockets:  []string{sock},
		SkipHostCmds: true,
		Now:          func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
	inv, err := eng.Snapshot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Machines) != 1 || !inv.Machines[0].DaemonOK {
		t.Fatalf("machines %+v", inv.Machines)
	}
	if inv.Summary.Containers != 2 || inv.Summary.Projects != 2 {
		t.Fatalf("summary %+v", inv.Summary)
	}
	var web *Container
	for i := range inv.Containers {
		if inv.Containers[i].Service == "web" {
			web = &inv.Containers[i]
		}
	}
	if web == nil || web.Project != "shop" || web.WorkingDir != "/srv/shop" {
		t.Fatalf("web %+v", web)
	}
	res, err := eng.Action(context.Background(), ActionRequest{Action: "restart", MachineID: HostMachineID, ContainerID: "aaaaaaaaaaaa"}, nil)
	if err != nil || !res.OK {
		t.Fatalf("restart %v %+v", err, res)
	}
	logs, err := eng.Action(context.Background(), ActionRequest{Action: "logs", MachineID: HostMachineID, ContainerID: "aaaaaaaaaaaa", Tail: 10}, nil)
	if err != nil || !strings.Contains(logs.Logs, "hello from shop") {
		t.Fatalf("logs %v %q", err, logs.Logs)
	}
	rec, err := eng.Action(context.Background(), ActionRequest{Action: "recreate", MachineID: HostMachineID, ContainerID: "aaaaaaaaaaaa"}, nil)
	if err != nil || !res.OK || !rec.OK {
		t.Fatalf("recreate %v %+v", err, rec)
	}
}

func TestSnapshotSkipsHostWhenNoSocket(t *testing.T) {
	eng := &Engine{HostSockets: []string{filepath.Join(t.TempDir(), "missing.sock")}, SkipHostCmds: true}
	inv, err := eng.Snapshot(context.Background(), []MachineHint{{ID: "wl-1", Name: "ct", Kind: KindSystemContainer}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Machines) != 0 || inv.Summary.Containers != 0 {
		t.Fatalf("disabled/empty inventory still discovered: %+v", inv)
	}
}

func TestIdleClearsUpdateNotes(t *testing.T) {
	eng := &Engine{}
	eng.rememberUpdate("host/abc", true, "pull failed")
	eng.Idle()
	c := Container{Ref: "host/abc"}
	eng.applyUpdateNote(&c)
	if c.UpdateFailed {
		t.Fatal("idle must drop update cache")
	}
}

type fakeState struct {
	version    string
	containers []inspectJSON
}

func fakeInspect(id, name, state, image string, labels map[string]string, oom bool, exit int) inspectJSON {
	ins := inspectJSON{ID: id, Name: "/" + name, Created: time.Unix(1_700_000_000, 0).UTC().Format(time.RFC3339)}
	ins.Config.Image = image
	ins.Config.Labels = labels
	ins.State.Status = state
	ins.State.Running = state == "running"
	ins.State.OOMKilled = oom
	ins.State.ExitCode = exit
	ins.State.StartedAt = time.Unix(1_700_000_000, 0).UTC().Format(time.RFC3339Nano)
	if state == "running" {
		ins.State.Health = &struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
			Log           []struct {
				Output   string `json:"Output"`
				ExitCode int    `json:"ExitCode"`
			} `json:"Log"`
		}{Status: "healthy"}
	}
	return ins
}

func startFakeEngine(t *testing.T, st fakeState) string {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.41/version", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"Version": st.version})
	})
	mux.HandleFunc("/v1.41/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		var items []listItem
		for _, ins := range st.containers {
			items = append(items, listItem{
				ID: ins.ID, Names: []string{ins.Name}, Image: ins.Config.Image, State: ins.State.Status,
				Status: ins.State.Status, Labels: ins.Config.Labels,
			})
		}
		_ = json.NewEncoder(w).Encode(items)
	})
	mux.HandleFunc("/v1.41/containers/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1.41/containers/")
		parts := strings.Split(path, "/")
		id := parts[0]
		var ins *inspectJSON
		for i := range st.containers {
			if strings.HasPrefix(st.containers[i].ID, id) || st.containers[i].ID == id {
				ins = &st.containers[i]
				break
			}
		}
		if ins == nil {
			if r.Method == http.MethodDelete || (len(parts) > 1 && (parts[1] == "start" || parts[1] == "stop" || parts[1] == "rename")) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if len(parts) == 1 || parts[1] == "json" {
			_ = json.NewEncoder(w).Encode(ins)
			return
		}
		switch parts[1] {
		case "stats":
			_ = json.NewEncoder(w).Encode(statsJSON{})
		case "logs":
			w.Write([]byte("hello from shop\n"))
		case "start", "stop", "restart", "rename":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/v1.41/events", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "")
	})
	mux.HandleFunc("/v1.41/images/create", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"Pull complete"}`+"\n")
	})
	mux.HandleFunc("/v1.41/containers/create", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "cccccccccccccccc"})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})
	return sock
}
