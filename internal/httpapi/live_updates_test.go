package httpapi

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/no-dal/ndl-ce/internal/liveupdate"
)

func TestLiveUpdateUploadVerifiesAndQueues(t *testing.T) {
	s, _, token := testServer(t)
	root, keys := t.TempDir(), t.TempDir()
	s.LiveUpdates = LiveUpdatePaths{Root: root, KeysDir: keys, Enabled: filepath.Join(t.TempDir(), "enabled")}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	src := t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "ui"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "ui", "index.html"), []byte("<html>"), 0o644)
	var bundle bytes.Buffer
	if _, err := liveupdate.Pack(&bundle, liveupdate.Manifest{ID: "u1", Version: "1.5.0+live.1", BaseVersion: "1.5.0"}, src, priv); err != nil {
		t.Fatal(err)
	}
	post := func(body []byte) int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/live-updates/bundles", bytes.NewReader(body))
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	// Not set up: no inbox.
	if code := post(bundle.Bytes()); code != http.StatusConflict {
		t.Fatalf("without an inbox: %d", code)
	}
	_ = os.MkdirAll(filepath.Join(root, "inbox"), 0o770)
	// No trusted key yet.
	if code := post(bundle.Bytes()); code != http.StatusConflict {
		t.Fatalf("without a trusted key: %d", code)
	}
	_ = os.WriteFile(filepath.Join(keys, "dev.pub"), []byte(base64.StdEncoding.EncodeToString(pub)), 0o644)
	// A tampered bundle is refused and leaves nothing behind.
	bad := append([]byte(nil), bundle.Bytes()...)
	bad[len(bad)/2] ^= 0xff
	if code := post(bad); code != http.StatusUnprocessableEntity {
		t.Fatalf("tampered: %d", code)
	}
	if code := post(bundle.Bytes()); code != http.StatusAccepted {
		t.Fatalf("genuine: %d", code)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "inbox"))
	if len(entries) != 1 || entries[0].Name() != "u1"+liveupdate.BundleExt {
		t.Fatalf("inbox %v", entries)
	}
	code, st := jsonCall(t, ts, cookie, "GET", "/api/v1/live-updates", "")
	if code != http.StatusOK || st["enabled"] != false || len(st["queued"].([]any)) != 1 {
		t.Fatalf("status %d %v", code, st)
	}
	code, _ = jsonCall(t, ts, cookie, "POST", "/api/v1/live-updates/rollback", "")
	if _, err := os.Stat(filepath.Join(root, "inbox", liveupdate.RollbackRequest)); code != http.StatusAccepted || err != nil {
		t.Fatalf("rollback request %d %v", code, err)
	}
}

// An idle, freshly enabled host has nothing queued, busy or in history; the
// status must still give lists, or the Updates page cannot render it.
func TestLiveUpdateStatusIdleGivesEmptyLists(t *testing.T) {
	s, _, token := testServer(t)
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "inbox"), 0o770)
	s.LiveUpdates = LiveUpdatePaths{Root: root, KeysDir: t.TempDir(), Enabled: filepath.Join(t.TempDir(), "enabled")}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	code, st := jsonCall(t, ts, cookie, "GET", "/api/v1/live-updates", "")
	if code != http.StatusOK {
		t.Fatalf("status %d %v", code, st)
	}
	for _, k := range []string{"queued", "history", "trusted_keys", "busy", "applied_migrations"} {
		if _, ok := st[k].([]any); !ok {
			t.Errorf("%s = %#v, want a list", k, st[k])
		}
	}
}
