package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

func testServer(t *testing.T) (*Server, *appdb.Memory, string) {
	t.Helper()
	mem := appdb.NewMemory()
	clusterID := uuid.NewString()
	token := "setup-token-value-32bytes-minimum"
	if err := mem.CreateCluster(context.Background(), appdb.Cluster{ID: clusterID, Name: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := mem.PutSetup(context.Background(), clusterID, secutil.HashSHA256(token)); err != nil {
		t.Fatal(err)
	}
	if err := mem.EnsureRoles(context.Background(), clusterID, rbac.SeedRoles()); err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: mem, Lockout: auth.NewLockout(), SetupHash: secutil.HashSHA256(token)}
	return s, mem, token
}

func TestSetupClaimAndReplay(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := `{"token":"` + token + `","username":"admin","password":"correct-horse"}`
	res, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("claim=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	res, err = ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("replay=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	var denied bool
	for _, a := range mem.Audits() {
		if a.Action == "setup.claim" && a.Result == "denied" {
			denied = true
		}
	}
	if !denied {
		t.Fatal("expected audit of replay")
	}
}

func TestLoginWhoamiTokenRBAC(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	claim, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	if claim.StatusCode != 200 {
		t.Fatalf("claim=%d", claim.StatusCode)
	}
	var cookie string
	for _, c := range claim.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	_ = claim.Body.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	me, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if me.StatusCode != 200 {
		t.Fatalf("me=%d", me.StatusCode)
	}
	_ = me.Body.Close()

	tokReq, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens", strings.NewReader(`{"name":"cli"}`))
	tokReq.Header.Set("Content-Type", "application/json")
	tokReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	tokRes, err := ts.Client().Do(tokReq)
	if err != nil {
		t.Fatal(err)
	}
	if tokRes.StatusCode != http.StatusCreated {
		t.Fatalf("token=%d", tokRes.StatusCode)
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(tokRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	_ = tokRes.Body.Close()
	if !strings.HasPrefix(created.Token, "ndl_") {
		t.Fatal(created.Token)
	}

	who, err := ts.Client().Do(func() *http.Request {
		r, _ := http.NewRequest("GET", ts.URL+"/api/v1/me", nil)
		r.Header.Set("Authorization", "Bearer "+created.Token)
		return r
	}())
	if err != nil {
		t.Fatal(err)
	}
	if who.StatusCode != 200 {
		t.Fatalf("bearer me=%d", who.StatusCode)
	}
	_ = who.Body.Close()

	rev, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens/revoke", bytes.NewReader([]byte(`{"id":"`+created.ID+`"}`)))
	rev.Header.Set("Content-Type", "application/json")
	rev.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	revRes, err := ts.Client().Do(rev)
	if err != nil {
		t.Fatal(err)
	}
	if revRes.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke=%d", revRes.StatusCode)
	}
	_ = revRes.Body.Close()

	dead, _ := http.NewRequest("GET", ts.URL+"/api/v1/me", nil)
	dead.Header.Set("Authorization", "Bearer "+created.Token)
	deadRes, err := ts.Client().Do(dead)
	if err != nil {
		t.Fatal(err)
	}
	if deadRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token still works: %d", deadRes.StatusCode)
	}
	_ = deadRes.Body.Close()
}

func TestViewerCannotCreateToken(t *testing.T) {
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	if err := mem.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if err := mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	login, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(
		`{"username":"view","password":"password1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var cookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	_ = login.Body.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer token create=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestBadLoginLockout(t *testing.T) {
	s, _, _ := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	for i := 0; i < 8; i++ {
		res, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(
			`{"username":"admin","password":"nope-nope"}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
	}
	res, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(
		`{"username":"admin","password":"nope-nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("lockout=%d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestAdminLoginLogoutAndSetupStatus(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	st, err := ts.Client().Get(ts.URL + "/api/v1/setup/status")
	if err != nil {
		t.Fatal(err)
	}
	if st.StatusCode != 200 {
		t.Fatal(st.StatusCode)
	}
	_ = st.Body.Close()

	bad, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"wrong-token-value","username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token=%d", bad.StatusCode)
	}
	_ = bad.Body.Close()

	claim, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	if claim.StatusCode != 200 {
		t.Fatalf("claim=%d", claim.StatusCode)
	}
	_ = claim.Body.Close()

	closed, err := ts.Client().Get(ts.URL + "/api/v1/setup/status")
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Open bool `json:"open"`
	}
	if err := json.NewDecoder(closed.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	_ = closed.Body.Close()
	if status.Open {
		t.Fatal("setup should be closed")
	}

	login, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(
		`{"username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	if login.StatusCode != 200 {
		t.Fatalf("login=%d", login.StatusCode)
	}
	var cookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	_ = login.Body.Close()
	if cookie == "" {
		t.Fatal("session cookie")
	}

	out, _ := http.NewRequest("POST", ts.URL+"/api/v1/auth/logout", nil)
	out.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	logout, err := ts.Client().Do(out)
	if err != nil {
		t.Fatal(err)
	}
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout=%d", logout.StatusCode)
	}
	_ = logout.Body.Close()
}

func TestHealth(t *testing.T) {
	s, _, _ := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := ts.Client().Get(ts.URL + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	_ = res.Body.Close()
}
