package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/ndltls"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func TestCertGenerateSetsSecureCookieAndRejectsCleartextWS(t *testing.T) {
	s, _, token := testServer(t)
	s.CertDir = ndltls.Dir{Root: t.TempDir()}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/certs/generate", strings.NewReader(`{"common_name":"nodal.test","sans":["localhost"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("missing confirm %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/certs/generate", strings.NewReader(`{"common_name":"nodal.test","sans":["localhost"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "enable-tls")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("generate %d %s", res.StatusCode, b)
	}
	var status map[string]any
	_ = json.NewDecoder(res.Body).Decode(&status)
	_ = res.Body.Close()
	if status["enabled"] != true || status["fingerprint"] == "" || status["mode"] != "self_signed" {
		t.Fatalf("status %+v", status)
	}
	if status["restart_required"] != true {
		t.Fatalf("generate must report restart_required until HTTPS is serving: %+v", status)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "BEGIN ") || strings.Contains(strings.ToUpper(string(raw)), "PRIVATE KEY") {
		t.Fatal("private key leaked")
	}

	login, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	foundSecure := false
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie && c.Secure {
			foundSecure = true
		}
	}
	_ = login.Body.Close()
	if !foundSecure {
		t.Fatal("session cookie must be Secure after TLS is enabled")
	}

	ws, _ := http.NewRequest("GET", ts.URL+"/api/v1/events/stream", nil)
	ws.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	denied, _ := ts.Client().Do(ws)
	if denied.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(denied.Body)
		t.Fatalf("cleartext stream %d %s", denied.StatusCode, b)
	}
	_ = denied.Body.Close()
}

func TestCertViewerCannotManage(t *testing.T) {
	s, mem, token := testServer(t)
	s.CertDir = ndltls.Dir{Root: t.TempDir()}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	var viewCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = login.Body.Close()
	get, _ := http.NewRequest("GET", ts.URL+"/api/v1/certs", nil)
	get.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	out, _ := ts.Client().Do(get)
	if out.StatusCode != 200 {
		b, _ := io.ReadAll(out.Body)
		t.Fatalf("viewer get %d %s", out.StatusCode, b)
	}
	_ = out.Body.Close()
	gen, _ := http.NewRequest("POST", ts.URL+"/api/v1/certs/generate", strings.NewReader(`{"common_name":"x","sans":[]}`))
	gen.Header.Set("Content-Type", "application/json")
	gen.Header.Set("X-Nodal-Confirm", "enable-tls")
	gen.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	denied, _ := ts.Client().Do(gen)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer generate %d", denied.StatusCode)
	}
	_ = denied.Body.Close()
}

func TestACMEFailedDirectoryIsHonest(t *testing.T) {
	s, _, token := testServer(t)
	s.CertDir = ndltls.Dir{Root: t.TempDir()}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/certs/acme", strings.NewReader(`{"directory":"http://127.0.0.1/dir","email":"ops@example.com","domain":"nodal.test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "enable-tls")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("acme %d %s", res.StatusCode, b)
	}
	var status map[string]any
	_ = json.NewDecoder(res.Body).Decode(&status)
	_ = res.Body.Close()
	if status["acme_status"] != "failed" {
		t.Fatalf("expected failed, got %+v", status)
	}
}

func TestHTTPSListenerSetsSecureCookie(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	res, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("claim %d %s", res.StatusCode, b)
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie && c.Secure {
			return
		}
	}
	t.Fatal("HTTPS session cookie must be Secure")
}
