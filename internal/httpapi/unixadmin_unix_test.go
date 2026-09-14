//go:build unix

package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/no-dal/ndl-ce/internal/peercred"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func TestPrincipalUnixRootGrantsAdmin(t *testing.T) {
	s, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/api/v1/workloads", nil)
	r = r.WithContext(context.WithValue(r.Context(), peerCredKey{}, peercred.Creds{UID: 0}))
	p, err := s.principal(r)
	if err != nil {
		t.Fatal(err)
	}
	if p.User.Username != "root" || p.User.ID != LocalRootUserID {
		t.Fatalf("%+v", p.User)
	}
	if !rbac.Authorize(p.Grants, rbac.ComputeCreate) || !rbac.Authorize(p.Grants, rbac.ComputeStart) {
		t.Fatal("local root must have admin grants")
	}
}

func TestPrincipalUnixNonRootFallsThrough(t *testing.T) {
	s, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/api/v1/workloads", nil)
	r = r.WithContext(context.WithValue(r.Context(), peerCredKey{}, peercred.Creds{UID: 1000}))
	if _, err := s.principal(r); err == nil {
		t.Fatal("non-root unix peer without a session must not authenticate")
	}
}

func TestRemoteHTTPRejectsUnauthenticatedWorkloads(t *testing.T) {
	s, _, _ := testServer(t)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	res, err := ts.Client().Get(ts.URL + "/api/v1/workloads")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestUnixRootPeercredServesAPI(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("SO_PEERCRED UID 0 is required for local root admin")
	}
	s, _, _ := testServer(t)
	sock := filepath.Join(t.TempDir(), "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := os.Chmod(sock, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: WithUnixPeerCreds(s.Handler()), ConnContext: UnixConnContext}
	go srv.Serve(ln)
	defer srv.Close()

	client := unixHTTPClient(sock)
	res, err := client.Get("http://localhost/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d %s", res.StatusCode, b)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["username"] != "root" {
		t.Fatalf("%v", body)
	}

	list, err := client.Get("http://localhost/api/v1/workloads")
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(list.Body)
		t.Fatalf("workloads status %d %s", list.StatusCode, b)
	}
}

func TestUnixSocketModeRejectsNonRootConnect(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root is required to own a 0600 socket and drop uid")
	}
	if _, err := exec.LookPath("setpriv"); err != nil {
		t.Skip("setpriv is required")
	}
	sock := filepath.Join(t.TempDir(), "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := os.Chmod(sock, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("setpriv", "--reuid=65534", "--regid=65534", "--clear-groups", "python3", "-c",
		"import socket; s=socket.socket(socket.AF_UNIX); s.connect('"+sock+"')")
	if err := cmd.Run(); err == nil {
		t.Fatal("non-root must not connect to mode 0600 control socket")
	}
}

func unixHTTPClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
}
