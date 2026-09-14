package main

import (
	"net"
	"net/http"
	"path/filepath"
	"testing"
)

func TestDoUsesUnixControlSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"username":"root"}`))
	})}
	go srv.Serve(ln)
	defer srv.Close()

	t.Setenv("NODAL_CONTROL_SOCKET", sock)
	t.Setenv("NODAL_URL", "")
	body, err := do("GET", "/api/v1/me", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"username":"root"}` {
		t.Fatalf("%s", body)
	}
}

func TestNODALURLDoesNotUseDefaultSocket(t *testing.T) {
	t.Setenv("NODAL_CONTROL_SOCKET", "")
	t.Setenv("NODAL_URL", "http://127.0.0.1:9")
	if localControlSocket() != "" {
		t.Fatal("NODAL_URL must keep nodalctl on HTTP")
	}
}
