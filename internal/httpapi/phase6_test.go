package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/iojail"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type fakeIO struct {
	root     string
	lastKind string
	lastJail string
	lastPath string
	lastAct  string
}

func (f *fakeIO) record(kind, jail, path, action string) {
	f.lastKind = kind
	f.lastJail = jail
	f.lastPath = path
	f.lastAct = action
}

func (f *fakeIO) join(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if strings.Contains(rel, "\x00") {
		return "", fmt.Errorf("path contains a NUL")
	}
	for _, r := range rel {
		if r < 32 || r == 127 {
			return "", fmt.Errorf("path contains a control character")
		}
	}
	if rel == "" || rel == "." || rel == "/" {
		return f.root, nil
	}
	rel = strings.TrimPrefix(rel, "/")
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes the jail")
	}
	out := filepath.Join(f.root, filepath.FromSlash(clean))
	relOut, err := filepath.Rel(f.root, out)
	if err != nil || strings.HasPrefix(relOut, "..") {
		return "", fmt.Errorf("path escapes the jail")
	}
	return out, nil
}

func (f *fakeIO) FilesOp(_ context.Context, call agentrpc.FilesCall) (json.RawMessage, error) {
	f.record(call.TargetKind, call.JailRoot, call.Path, call.Action)
	p, err := f.join(call.Path)
	if err != nil {
		return nil, err
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
		return json.Marshal(map[string]any{
			"name": info.Name(), "type": kind, "size": info.Size(), "path": call.Path,
			"mode": uint32(info.Mode().Perm()), "mtime": info.ModTime().UTC().Format(time.RFC3339),
		})
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
		dest, jerr := f.join(call.DestPath)
		if jerr != nil {
			return nil, jerr
		}
		if err := os.Rename(p, dest); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"ok": true, "path": call.DestPath})
	case "copy":
		dest, jerr := f.join(call.DestPath)
		if jerr != nil {
			return nil, jerr
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dest, b, 0o644); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"ok": true, "path": call.DestPath})
	case "chmod":
		if err := os.Chmod(p, os.FileMode(call.Mode).Perm()); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"ok": true, "path": call.Path, "mode": call.Mode})
	case "chown":
		return json.Marshal(map[string]any{"ok": true, "path": call.Path})
	default:
		return nil, errUnavailable("unknown action")
	}
}

func (f *fakeIO) FilesPut(_ context.Context, call agentrpc.FilesPutCall, r io.Reader, expectedSHA string) (json.RawMessage, error) {
	f.record(call.TargetKind, call.JailRoot, call.Path, "upload")
	dest, err := f.join(call.Path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	hexSum := hex.EncodeToString(sum[:])
	if expectedSHA != "" && !strings.EqualFold(expectedSHA, hexSum) {
		return nil, fmt.Errorf("sha256 mismatch")
	}
	if err := os.WriteFile(dest, b, 0o644); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true, "path": call.Path, "size": len(b), "sha256": hexSum})
}

func (f *fakeIO) FilesGet(_ context.Context, call agentrpc.FilesGetCall) (io.ReadCloser, error) {
	f.record(call.TargetKind, call.JailRoot, call.Path, "download")
	dest, err := f.join(call.Path)
	if err != nil {
		return nil, err
	}
	return os.Open(dest)
}

func (f *fakeIO) OpenTerminal(_ context.Context, open agentrpc.TermOpen) (agentrpc.TermConn, error) {
	f.record(open.TargetKind, open.JailRoot, open.CWD, "terminal")
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
	s.IO = &fakeIO{root: root}
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

func TestTerminalCreateFailsClosedForJailEscapeCWD(t *testing.T) {
	_, mem, ts, admin, nodeID, wlID := seedPhase6(t)
	op := loginRole(t, ts, mem, "oper", rbac.Operator)

	host := doCookie(t, ts, admin, "POST", "/api/v1/nodes/"+nodeID+"/terminal/sessions", `{"cwd":".."}`)
	hostRaw, _ := io.ReadAll(host.Body)
	_ = host.Body.Close()
	if host.StatusCode != http.StatusBadRequest || !strings.Contains(string(hostRaw), "path escapes the jail") {
		t.Fatalf("host cwd %d %s", host.StatusCode, hostRaw)
	}

	ct := doCookie(t, ts, op, "POST", "/api/v1/workloads/"+wlID+"/terminal/sessions", `{"cwd":"/foo/../../etc"}`)
	ctRaw, _ := io.ReadAll(ct.Body)
	_ = ct.Body.Close()
	if ct.StatusCode != http.StatusBadRequest || !strings.Contains(string(ctRaw), "path escapes the jail") {
		t.Fatalf("ct cwd %d %s", ct.StatusCode, ctRaw)
	}

	listed := doCookie(t, ts, admin, "GET", "/api/v1/io/sessions", "")
	listRaw, _ := io.ReadAll(listed.Body)
	_ = listed.Body.Close()
	if strings.Contains(string(listRaw), `"cwd":".."`) || strings.Contains(string(listRaw), `/foo/../../etc`) {
		t.Fatalf("GET /io/sessions must not list an escaped cwd: %s", listRaw)
	}
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

func TestMultipleCTTerminalSessionsAreIndependent(t *testing.T) {
	_, mem, ts, _, _, wlID := seedPhase6(t)
	op := loginRole(t, ts, mem, "oper", rbac.Operator)
	res := doCookie(t, ts, op, "POST", "/api/v1/workloads/"+wlID+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("first %d", res.StatusCode)
	}
	var a map[string]any
	_ = json.NewDecoder(res.Body).Decode(&a)
	_ = res.Body.Close()
	res = doCookie(t, ts, op, "POST", "/api/v1/workloads/"+wlID+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("second %d", res.StatusCode)
	}
	var b map[string]any
	_ = json.NewDecoder(res.Body).Decode(&b)
	_ = res.Body.Close()
	res = doCookie(t, ts, op, "POST", "/api/v1/workloads/"+wlID+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("third %d", res.StatusCode)
	}
	var c map[string]any
	_ = json.NewDecoder(res.Body).Decode(&c)
	_ = res.Body.Close()
	idA, _ := a["id"].(string)
	idB, _ := b["id"].(string)
	idC, _ := c["id"].(string)
	if idA == "" || idA == idB || idB == idC || idA == idC {
		t.Fatalf("sessions must have distinct ids %q %q %q", idA, idB, idC)
	}
	if a["target_id"] != b["target_id"] {
		t.Fatal("same target required")
	}
	res = doCookie(t, ts, op, "GET", "/api/v1/io/sessions", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list %d", res.StatusCode)
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(res.Body).Decode(&listed)
	_ = res.Body.Close()
	if len(listed.Items) < 3 {
		t.Fatalf("list %d", len(listed.Items))
	}
	cluster, _ := mem.GetCluster(context.Background())
	gotA, err := mem.GetIOSession(context.Background(), cluster.ID, idA)
	if err != nil || gotA == nil {
		t.Fatal(err)
	}
	gotB, err := mem.GetIOSession(context.Background(), cluster.ID, idB)
	if err != nil || gotB == nil {
		t.Fatal("sibling missing")
	}
	gotA.State = appdb.IOStateEnded
	ended := time.Now().UTC()
	gotA.EndedAt = &ended
	if err := mem.UpdateIOSession(context.Background(), *gotA); err != nil {
		t.Fatal(err)
	}
	againB, _ := mem.GetIOSession(context.Background(), cluster.ID, idB)
	if againB == nil || againB.State == appdb.IOStateEnded {
		t.Fatal("closing one session must not end a sibling")
	}
}

func TestViewerCannotListIOSessions(t *testing.T) {
	_, mem, ts, _, _, _ := seedPhase6(t)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)
	res := doCookie(t, ts, view, "GET", "/api/v1/io/sessions", "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer list %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestHostAndWorkloadTerminalSessionsTogether(t *testing.T) {
	_, _, ts, admin, nodeID, wlID := seedPhase6(t)
	res := doCookie(t, ts, admin, "POST", "/api/v1/nodes/"+nodeID+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("host %d", res.StatusCode)
	}
	var host map[string]any
	_ = json.NewDecoder(res.Body).Decode(&host)
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("workload %d", res.StatusCode)
	}
	var wl map[string]any
	_ = json.NewDecoder(res.Body).Decode(&wl)
	_ = res.Body.Close()
	if host["id"] == wl["id"] {
		t.Fatal("distinct session ids required")
	}
	if host["node_id"] != nodeID {
		t.Fatalf("host node_id %v", host["node_id"])
	}
	if wl["node_id"] != nodeID {
		t.Fatalf("workload node_id %v", wl["node_id"])
	}
}

func TestViewerFilesReadNoDownload(t *testing.T) {
	s, mem, ts, admin, _, wlID := seedPhase6(t)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)
	_ = os.WriteFile(filepath.Join(s.IO.(*fakeIO).root, "hello.txt"), []byte("hi"), 0o644)
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
	s, mem, ts, admin, _, _ := seedPhase6(t)
	s.VM = &fakeVM{}
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

func TestWorkloadFilesCRUDCopyContentAndConflict(t *testing.T) {
	s, mem, ts, admin, _, wlID := seedPhase6(t)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)
	root := s.IO.(*fakeIO).root
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := doCookie(t, ts, admin, "GET", "/api/v1/workloads/"+wlID+"/files?path=.", "")
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("list %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/files/mkdir", `{"path":"etc"}`)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("mkdir %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/files/copy", `{"path":"hello.txt","dest_path":"etc/hello.txt"}`)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("copy %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/files/move", `{"path":"etc/hello.txt","dest_path":"etc/renamed.txt"}`)
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("move %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/files/copy", `{"path":"etc/renamed.txt","dest_path":"../outside.txt"}`)
	if res.StatusCode == http.StatusCreated || res.StatusCode == http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("copy dest escape %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "GET", "/api/v1/workloads/"+wlID+"/files/content?path=hello.txt", "")
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("content %d %s", res.StatusCode, b)
	}
	var content map[string]any
	_ = json.NewDecoder(res.Body).Decode(&content)
	_ = res.Body.Close()
	if content["content"] != "hi" || content["binary"] == true {
		t.Fatalf("content %+v", content)
	}
	mtime, _ := content["mtime"].(string)
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/files/delete", `{"path":"hello.txt","expected_mtime":"1999-01-01T00:00:00Z"}`)
	if res.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("stale delete %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/files/delete", `{"path":"hello.txt","expected_mtime":"`+mtime+`"}`)
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("delete %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	if err := os.MkdirAll(filepath.Join(root, "tree", "n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tree", "n", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/files/delete", `{"path":"tree"}`)
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("recursive delete %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, view, "POST", "/api/v1/workloads/"+wlID+"/files/delete", `{"path":"etc"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer delete %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestWorkloadFilesBinaryContent(t *testing.T) {
	s, _, ts, admin, _, wlID := seedPhase6(t)
	root := s.IO.(*fakeIO).root
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), []byte("a\x00b"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := doCookie(t, ts, admin, "GET", "/api/v1/workloads/"+wlID+"/files/content?path=blob.bin", "")
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("binary content %d %s", res.StatusCode, b)
	}
	var content map[string]any
	_ = json.NewDecoder(res.Body).Decode(&content)
	_ = res.Body.Close()
	if content["binary"] != true {
		t.Fatalf("binary %+v", content)
	}
	if _, ok := content["content"]; ok {
		t.Fatal("binary payload must not include text content")
	}
}

func postMultipart(t *testing.T, ts *httptest.Server, cookie, urlPath, rel, name, body string, fields ...string) *http.Response {
	t.Helper()
	if len(fields)%2 != 0 {
		t.Fatal("fields must be key/value pairs")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("path", rel); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(fields); i += 2 {
		if err := w.WriteField(fields[i], fields[i+1]); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", ts.URL+urlPath, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestWorkloadFilesUploadLargeTraversalAndChmod(t *testing.T) {
	s, mem, ts, admin, _, wlID := seedPhase6(t)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)
	root := s.IO.(*fakeIO).root

	res := postMultipart(t, ts, admin, "/api/v1/workloads/"+wlID+"/files/upload", "created.txt", "created.txt", "hello-upload")
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("upload %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	got, err := os.ReadFile(filepath.Join(root, "created.txt"))
	if err != nil || string(got) != "hello-upload" {
		t.Fatalf("uploaded %s %v", got, err)
	}

	res = doCookie(t, ts, admin, "GET", "/api/v1/workloads/"+wlID+"/files/content?path=created.txt", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("read %d", res.StatusCode)
	}
	_ = res.Body.Close()

	huge := bytes.Repeat([]byte("a"), iojail.PreviewMaxBytes+1)
	if err := os.WriteFile(filepath.Join(root, "huge.txt"), huge, 0o644); err != nil {
		t.Fatal(err)
	}
	res = doCookie(t, ts, admin, "GET", "/api/v1/workloads/"+wlID+"/files/content?path=huge.txt", "")
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("huge %d %s", res.StatusCode, b)
	}
	var content map[string]any
	_ = json.NewDecoder(res.Body).Decode(&content)
	_ = res.Body.Close()
	if content["too_large"] != true {
		t.Fatalf("too_large %+v", content)
	}
	if _, ok := content["content"]; ok {
		t.Fatal("huge files must not include text content")
	}

	preview := bytes.Repeat([]byte("b"), iojail.EditorMaxBytes+8)
	if err := os.WriteFile(filepath.Join(root, "preview.txt"), preview, 0o644); err != nil {
		t.Fatal(err)
	}
	res = doCookie(t, ts, admin, "GET", "/api/v1/workloads/"+wlID+"/files/content?path=preview.txt", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("preview %d", res.StatusCode)
	}
	content = map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&content)
	_ = res.Body.Close()
	if content["editable"] != false || content["too_large"] == true {
		t.Fatalf("editor fallback %+v", content)
	}
	if _, ok := content["content"]; !ok {
		t.Fatal("preview should include text")
	}

	res = doCookie(t, ts, admin, "GET", "/api/v1/workloads/"+wlID+"/files?path=../etc/passwd", "")
	if res.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("traversal list %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/files/mkdir", `{"path":"../escape"}`)
	if res.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("traversal mkdir %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	res = doCookie(t, ts, view, "POST", "/api/v1/workloads/"+wlID+"/files/chmod", `{"path":"created.txt","mode":384}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer chmod %d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/files/chmod", `{"path":"created.txt","mode":384}`)
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("admin chmod %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/files/chmod", `{"path":"created.txt"}`)
	if res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("chmod missing mode %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, admin, "POST", "/api/v1/workloads/"+wlID+"/files/chmod", `{"path":"created.txt","mode":0}`)
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("chmod 0 %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	info, err := os.Stat(filepath.Join(s.IO.(*fakeIO).root, "created.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0 {
		t.Fatalf("chmod 0 left mode %o", info.Mode().Perm())
	}
}

func TestWorkloadFilesUploadSeparatesContentAndCASDigests(t *testing.T) {
	s, _, ts, admin, _, wlID := seedPhase6(t)
	root := s.IO.(*fakeIO).root
	urlPath := "/api/v1/workloads/" + wlID + "/files/upload"
	sum := func(body string) string {
		h := sha256.Sum256([]byte(body))
		return hex.EncodeToString(h[:])
	}

	newBody := "brand-new-bytes"
	res := postMultipart(t, ts, admin, urlPath, "cas.txt", "cas.txt", newBody, "sha256", sum(newBody))
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("content sha %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	res = postMultipart(t, ts, admin, urlPath, "wrong.txt", "wrong.txt", "payload", "sha256", sum("not-payload"))
	if res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("wrong content sha %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	if _, err := os.Stat(filepath.Join(root, "wrong.txt")); err == nil {
		t.Fatal("mismatch must not write")
	}

	oldBody := "old-on-disk"
	if err := os.WriteFile(filepath.Join(root, "swap.txt"), []byte(oldBody), 0o644); err != nil {
		t.Fatal(err)
	}
	next := "new-on-disk"
	res = postMultipart(t, ts, admin, urlPath, "swap.txt", "swap.txt", next,
		"sha256", sum(next), "expected_sha256", sum(oldBody))
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("cas+content %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	got, err := os.ReadFile(filepath.Join(root, "swap.txt"))
	if err != nil || string(got) != next {
		t.Fatalf("swapped %s %v", got, err)
	}

	res = postMultipart(t, ts, admin, urlPath, "swap.txt", "swap.txt", "third",
		"sha256", sum("third"), "expected_sha256", sum(oldBody))
	if res.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("stale cas %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	got, err = os.ReadFile(filepath.Join(root, "swap.txt"))
	if err != nil || string(got) != next {
		t.Fatalf("stale cas must not write %s %v", got, err)
	}
}
