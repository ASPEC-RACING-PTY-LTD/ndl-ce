package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeIO struct {
	root string
}

func (f fakeIO) FilesOp(_ context.Context, call agentrpc.FilesCall) (json.RawMessage, error) {
	p := filepath.Join(f.root, filepath.FromSlash(strings.TrimPrefix(call.Path, "/")))
	if call.Path == "" || call.Path == "." {
		p = f.root
	}
	switch call.Action {
	case "list":
		ents, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(ents))
		for _, e := range ents {
			info, _ := e.Info()
			kind := "file"
			if e.IsDir() {
				kind = "dir"
			}
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			out = append(out, map[string]any{"name": e.Name(), "type": kind, "size": size, "path": e.Name()})
		}
		return json.Marshal(map[string]any{"path": call.Path, "entries": out})
	case "stat":
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		kind := "file"
		if info.IsDir() {
			kind = "dir"
		}
		return json.Marshal(map[string]any{"name": info.Name(), "type": kind, "size": info.Size(), "path": call.Path})
	case "mkdir":
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"ok": true, "path": call.Path})
	case "delete":
		if err := os.RemoveAll(p); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"ok": true, "path": call.Path})
	case "rename":
		dest := filepath.Join(f.root, filepath.FromSlash(strings.TrimPrefix(call.DestPath, "/")))
		if err := os.Rename(p, dest); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"ok": true, "path": call.DestPath})
	default:
		return nil, errUnavailable("unknown action")
	}
}

func (f fakeIO) FilesPut(_ context.Context, call agentrpc.FilesPutCall, r io.Reader, _ string) (json.RawMessage, error) {
	dest := filepath.Join(f.root, filepath.FromSlash(strings.TrimPrefix(call.Path, "/")))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(dest, b, 0o644); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true, "path": call.Path, "size": len(b)})
}

func (f fakeIO) FilesGet(_ context.Context, call agentrpc.FilesGetCall) (io.ReadCloser, error) {
	dest := filepath.Join(f.root, filepath.FromSlash(strings.TrimPrefix(call.Path, "/")))
	return os.Open(dest)
}

func (f fakeIO) OpenTerminal(context.Context, agentrpc.TermOpen) (agentrpc.TermConn, error) {
	return &loopTerm{ch: make(chan []byte, 8)}, nil
}

type loopTerm struct{ ch chan []byte }

func (l *loopTerm) Send(frame []byte) error {
	l.ch <- frame
	return nil
}
func (l *loopTerm) Recv() ([]byte, error) {
	b, ok := <-l.ch
	if !ok {
		return nil, io.EOF
	}
	return b, nil
}
func (l *loopTerm) Close() error {
	close(l.ch)
	return nil
}

func seedPhase6(t *testing.T) (*Server, *appdb.Memory, *httptest.Server, string, string, string) {
	t.Helper()
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	volID := uuid.NewString()
	_ = mem.CreateVolume(context.Background(), appdb.Volume{
		ID: volID, ClusterID: cluster.ID, NodeID: nodeID, PoolID: poolID,
		Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/" + volID,
	})
	wlID := uuid.NewString()
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: wlID, ClusterID: cluster.ID, NodeID: nodeID, Name: "accept-ct",
		Kind: lxc.KindSystemContainer, Status: lxc.StatusRunning, ImagePin: "alpine/3.21/amd64/default",
	})
	_ = mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: cluster.ID, WorkloadID: wlID, VolumeID: volID, Role: "root",
	})
	_ = netID
	root := t.TempDir()
	s.IO = fakeIO{root: root}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	adminCookie := claimAdmin(t, ts, token)
	return s, mem, ts, adminCookie, nodeID, wlID
}

func loginRole(t *testing.T, ts *httptest.Server, mem *appdb.Memory, username, role string) string {
	t.Helper()
	cluster, _ := mem.GetCluster(context.Background())
	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: username, PasswordHash: hash}
	if err := mem.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if err := mem.BindRole(context.Background(), cluster.ID, u.ID, role); err != nil {
		t.Fatal(err)
	}
	res, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(
		`{"username":"`+username+`","password":"password1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatal("no cookie")
	return ""
}

func doCookie(t *testing.T, ts *httptest.Server, cookie, method, path, body string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestHostTerminalAdminOnly(t *testing.T) {
	_, mem, ts, admin, nodeID, _ := seedPhase6(t)
	op := loginRole(t, ts, mem, "oper", rbac.Operator)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)
	res := doCookie(t, ts, admin, "POST", "/api/v1/nodes/"+nodeID+"/terminal/sessions", `{"cwd":"/"}`)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("admin host term %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, op, "POST", "/api/v1/nodes/"+nodeID+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("operator host term %d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, view, "POST", "/api/v1/nodes/"+nodeID+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer host term %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestCTTerminalOperatorAndViewer(t *testing.T) {
	_, mem, ts, admin, _, wlID := seedPhase6(t)
	op := loginRole(t, ts, mem, "oper", rbac.Operator)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)
	res := doCookie(t, ts, op, "POST", "/api/v1/workloads/"+wlID+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("operator CT term %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	if created["ticket"] == nil || created["ticket"] == "" {
		t.Fatal("ticket must be returned once")
	}
	res = doCookie(t, ts, view, "POST", "/api/v1/workloads/"+wlID+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer CT term %d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("admin CT term %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestViewerFilesReadNoDownload(t *testing.T) {
	s, mem, ts, admin, _, wlID := seedPhase6(t)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)
	_ = os.WriteFile(filepath.Join(s.IO.(fakeIO).root, "hello.txt"), []byte("hi"), 0o644)
	res := doCookie(t, ts, view, "GET", "/api/v1/workloads/"+wlID+"/files?path=.", "")
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusBadGateway {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("viewer list %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, view, "GET", "/api/v1/workloads/"+wlID+"/files/download?path=hello.txt", "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer download %d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "GET", "/api/v1/workloads/"+wlID+"/files/download?path=hello.txt", "")
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("admin download %d %s", res.StatusCode, b)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(b) != "hi" {
		t.Fatalf("download %q", b)
	}
}

func TestOperatorHostFilesDenied(t *testing.T) {
	_, mem, ts, _, nodeID, _ := seedPhase6(t)
	op := loginRole(t, ts, mem, "oper", rbac.Operator)
	res := doCookie(t, ts, op, "GET", "/api/v1/nodes/"+nodeID+"/files?path=.", "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("operator host files %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestVMFilesUnsupported(t *testing.T) {
	_, mem, ts, admin, _, _ := seedPhase6(t)
	cluster, _ := mem.GetCluster(context.Background())
	id := uuid.NewString()
	_ = mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: id, ClusterID: cluster.ID, Name: "vm-a", Kind: "vm", Status: "stopped",
	})
	res := doCookie(t, ts, admin, "GET", "/api/v1/workloads/"+id+"/files?path=.", "")
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("vm files %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+id+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("vm term %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestTicketHeaderOnlyAndOrigin(t *testing.T) {
	_, _, ts, admin, _, wlID := seedPhase6(t)
	res := doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/terminal/sessions", `{}`)
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	id := created["id"].(string)
	ticket := created["ticket"].(string)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/io/sessions/"+id+"/ws?ticket="+ticket, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	req.Header.Set("Origin", "http://evil.example")
	got, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != http.StatusBadRequest && got.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(got.Body)
		t.Fatalf("query ticket / bad origin %d %s", got.StatusCode, b)
	}
	_ = got.Body.Close()
	req2, _ := http.NewRequest("GET", ts.URL+"/api/v1/io/sessions/"+id, nil)
	req2.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	got2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	if got2.StatusCode != http.StatusOK {
		t.Fatalf("get session %d", got2.StatusCode)
	}
	_ = got2.Body.Close()
	_ = ticket
}
