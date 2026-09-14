package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHelpListsPhase1Commands(t *testing.T) {
	if err := run([]string{"help"}); err != nil {
		t.Fatal(err)
	}
}

func TestVersion(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
}

func TestPostJSONHeadersUsesNodalTLS(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workloads/x/delete" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("X-Nodal-Confirm") != "delete" {
			t.Errorf("confirm %q", r.Header.Get("X-Nodal-Confirm"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()
	t.Setenv("NODAL_URL", ts.URL)
	if err := postJSONHeaders("/api/v1/workloads/x/delete", map[string]any{}, false, map[string]string{"X-Nodal-Confirm": "delete"}); err != nil {
		t.Fatal(err)
	}
}

func TestImageUploadUsesNodalTLS(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/storage/images" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"img"}`))
	}))
	defer ts.Close()
	t.Setenv("NODAL_URL", ts.URL)
	file := filepath.Join(t.TempDir(), "disk.qcow2")
	if err := os.WriteFile(file, []byte("qcow"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmdUploadImage("pool", "disk-image", file); err != nil {
		t.Fatal(err)
	}
}
