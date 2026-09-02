package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func spaTestServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	s, _, _ := testServer(t)
	s.UI = os.DirFS(dir)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func writeUITree(t *testing.T, dir, index, assetName, assetBody string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if assetName != "" {
		if err := os.WriteFile(filepath.Join(dir, "assets", assetName), []byte(assetBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSPAReadsIndexFromDiskAfterReplace(t *testing.T) {
	dir := t.TempDir()
	writeUITree(t, dir, "<!doctype html>v1", "app-aaa.js", "js-v1")
	ts := spaTestServer(t, dir)

	res, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index Cache-Control=%q", got)
	}
	if !strings.Contains(string(body), "v1") {
		t.Fatalf("body=%s", body)
	}

	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), "v2") {
		t.Fatalf("stale in-memory index: %s", body)
	}
	if strings.Contains(string(body), "v1") {
		t.Fatalf("old index still served: %s", body)
	}
}

func TestSPAHashedAssetsAreImmutable(t *testing.T) {
	dir := t.TempDir()
	writeUITree(t, dir, "<!doctype html><script type=\"module\" src=\"/assets/app-aaa.js\"></script>", "app-aaa.js", "export default 1")
	ts := spaTestServer(t, dir)

	res, err := ts.Client().Get(ts.URL + "/assets/app-aaa.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	got := res.Header.Get("Cache-Control")
	if !strings.Contains(got, "immutable") || !strings.Contains(got, "max-age=31536000") {
		t.Fatalf("asset Cache-Control=%q", got)
	}
	if string(body) != "export default 1" {
		t.Fatalf("body=%s", body)
	}
}

func TestSPAMissingAssetIsNotHTML(t *testing.T) {
	dir := t.TempDir()
	writeUITree(t, dir, "<!doctype html><script type=\"module\" src=\"/assets/app-aaa.js\"></script>", "app-aaa.js", "export default 1")
	ts := spaTestServer(t, dir)

	res, err := ts.Client().Get(ts.URL + "/assets/app-oldhash.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d", res.StatusCode)
	}
	ct := res.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		t.Fatalf("missing hashed asset returned HTML %q: %s", ct, body)
	}
	if strings.Contains(strings.ToLower(string(body)), "<!doctype") || strings.Contains(string(body), "<script") {
		t.Fatalf("SPA fallback for missing JS module: %s", body)
	}
}

func TestSPAClientRouteServesFreshIndex(t *testing.T) {
	dir := t.TempDir()
	writeUITree(t, dir, "<!doctype html>shell", "app-aaa.js", "js")
	ts := spaTestServer(t, dir)

	res, err := ts.Client().Get(ts.URL + "/workloads")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if res.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("route Cache-Control=%q", res.Header.Get("Cache-Control"))
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("Content-Type=%q", res.Header.Get("Content-Type"))
	}
	if string(body) != "<!doctype html>shell" {
		t.Fatalf("body=%s", body)
	}
}
