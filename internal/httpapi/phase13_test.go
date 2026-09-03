package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/mfa"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

func TestMFAEnrollChallengeAndViewerAuditDenied(t *testing.T) {
	s, mem, token := testServer(t)
	now := time.Date(2026, 9, 1, 15, 0, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/mfa/enroll", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("enroll %d %s", res.StatusCode, b)
	}
	var enrolled struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(b, &enrolled); err != nil || enrolled.Secret == "" {
		t.Fatal(string(b))
	}
	code := mfa.Code(enrolled.Secret, now)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/mfa/confirm", strings.NewReader(`{"code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusOK {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("confirm %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"admin","password":"correct-horse"}`))
	lb, _ := io.ReadAll(login.Body)
	_ = login.Body.Close()
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login %d %s", login.StatusCode, lb)
	}
	var challenge struct {
		Required bool   `json:"mfa_required"`
		ID       string `json:"mfa_challenge_id"`
		Token    string `json:"mfa_token"`
	}
	if err := json.Unmarshal(lb, &challenge); err != nil || !challenge.Required {
		t.Fatalf("%s", lb)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/auth/mfa/verify", strings.NewReader(`{"mfa_challenge_id":"`+challenge.ID+`","mfa_token":"`+challenge.Token+`","code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res, _ = ts.Client().Do(req)
	mb, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("verify %d %s", res.StatusCode, mb)
	}
	var me map[string]any
	if err := json.Unmarshal(mb, &me); err != nil {
		t.Fatal(err)
	}
	if me["aal"] != float64(2) || me["mfa_enabled"] != true {
		t.Fatalf("%s", mb)
	}

	cluster, _ := mem.GetCluster(context.Background())
	hash, _ := auth.HashPassword("password1")
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	vlogin, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	var viewCookie string
	for _, c := range vlogin.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = vlogin.Body.Close()
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/audit", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer audit %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestMFAChallengeCannotBeReplayed(t *testing.T) {
	s, _, token := testServer(t)
	now := time.Date(2026, 9, 1, 15, 0, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/mfa/enroll", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("enroll %d %s", res.StatusCode, b)
	}
	var enrolled struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(b, &enrolled); err != nil {
		t.Fatal(string(b))
	}
	code := mfa.Code(enrolled.Secret, now)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/mfa/confirm", strings.NewReader(`{"code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusOK {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("confirm %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"admin","password":"correct-horse"}`))
	lb, _ := io.ReadAll(login.Body)
	_ = login.Body.Close()
	var challenge struct {
		ID    string `json:"mfa_challenge_id"`
		Token string `json:"mfa_token"`
	}
	if err := json.Unmarshal(lb, &challenge); err != nil || challenge.ID == "" {
		t.Fatalf("%s", lb)
	}
	body := `{"mfa_challenge_id":"` + challenge.ID + `","mfa_token":"` + challenge.Token + `","code":"` + code + `"}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/auth/mfa/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusOK {
		mb, _ := io.ReadAll(res.Body)
		t.Fatalf("first verify %d %s", res.StatusCode, mb)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/auth/mfa/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, _ = ts.Client().Do(req)
	mb, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replay %d %s", res.StatusCode, mb)
	}
}

func TestTokenPermissionsCannotExceedCreator(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	op := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "op"}
	_ = mem.CreateUser(context.Background(), op)
	_ = mem.BindRole(context.Background(), cluster.ID, op.ID, rbac.Operator)
	plain := "ndl_op_phase13"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: op.ID, Name: "op",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_op",
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens", strings.NewReader(`{"name":"x","permissions":["*"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("operator * token %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestServicePrincipalCannotPasswordLogin(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/service-principals", strings.NewReader(`{"name":"backup"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	cluster, _ := mem.GetCluster(context.Background())
	sps, err := mem.ListServicePrincipals(context.Background(), cluster.ID)
	if err != nil || len(sps) != 1 || sps[0].Name != "backup" {
		t.Fatalf("principal row %+v %v", sps, err)
	}
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"svc-backup","password":"x"}`))
	if login.StatusCode != http.StatusUnauthorized {
		t.Fatalf("service login %d", login.StatusCode)
	}
	_ = login.Body.Close()
}

func TestClusterDestroyRequiresStepUp(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/destroy", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "destroy-cluster")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("aal1 destroy %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestGroupCreateAndLostMFARecover(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/groups", strings.NewReader(`{"name":"ops"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("group %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	cluster, _ := mem.GetCluster(context.Background())
	admin, _ := mem.GetUserByName(context.Background(), cluster.ID, "admin")
	now := time.Date(2026, 9, 1, 15, 0, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/mfa/enroll", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	var enrolled struct {
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal(b, &enrolled)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/mfa/confirm", strings.NewReader(`{"code":"`+mfa.Code(enrolled.Secret, now)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	_ = mem.DeleteUserMFA(context.Background(), admin.ID)
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"admin","password":"correct-horse"}`))
	lb, _ := io.ReadAll(login.Body)
	_ = login.Body.Close()
	if strings.Contains(string(lb), `"mfa_required":true`) {
		t.Fatalf("recover must clear MFA: %s", lb)
	}
}

func TestOperatorCanEnrollMFAAndCannotBindAdmin(t *testing.T) {
	s, mem, token := testServer(t)
	now := time.Date(2026, 9, 1, 15, 0, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	hash, _ := auth.HashPassword("password1")
	op := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "op13", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), op)
	_ = mem.BindRole(context.Background(), cluster.ID, op.ID, rbac.Operator)
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"op13","password":"password1"}`))
	var opCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			opCookie = c.Value
		}
	}
	_ = login.Body.Close()
	if opCookie == "" {
		t.Fatal("operator cookie")
	}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/mfa/enroll", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: opCookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("operator enroll %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/groups", strings.NewReader(`{"name":"ops"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: opCookie})
	res, _ = ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("operator group %d %s", res.StatusCode, b)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &created); err != nil || created.ID == "" {
		t.Fatal(string(b))
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/groups/"+created.ID+"/roles", strings.NewReader(`{"role":"admin"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: opCookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("admin group bind %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/groups/"+created.ID+"/roles", strings.NewReader(`{"role":"operator"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: opCookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusOK {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("operator bind %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestEnabledMFACannotBeReplacedWithoutRecover(t *testing.T) {
	s, mem, token := testServer(t)
	now := time.Date(2026, 9, 1, 15, 0, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/mfa/enroll", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	var enrolled struct {
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal(b, &enrolled)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/mfa/confirm", strings.NewReader(`{"code":"`+mfa.Code(enrolled.Secret, now)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("confirm %d", res.StatusCode)
	}
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/mfa/enroll", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusConflict {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("re-enroll %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	cluster, _ := mem.GetCluster(context.Background())
	admin, _ := mem.GetUserByName(context.Background(), cluster.ID, "admin")
	plain := "ndl_admin_mfa"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: admin.ID, Name: "t",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_adm",
	})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/mfa/enroll", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("token enroll %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func enrollAdminAAL2(t *testing.T, s *Server, ts *httptest.Server, cookie string) string {
	t.Helper()
	now := time.Date(2026, 9, 1, 15, 0, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/mfa/enroll", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("enroll %d %s", res.StatusCode, b)
	}
	var enrolled struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(b, &enrolled); err != nil || enrolled.Secret == "" {
		t.Fatal(string(b))
	}
	code := mfa.Code(enrolled.Secret, now)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/mfa/confirm", strings.NewReader(`{"code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusOK {
		b, _ = io.ReadAll(res.Body)
		t.Fatalf("confirm %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	login, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	lb, _ := io.ReadAll(login.Body)
	_ = login.Body.Close()
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login %d %s", login.StatusCode, lb)
	}
	var challenge struct {
		Required bool   `json:"mfa_required"`
		ID       string `json:"mfa_challenge_id"`
		Token    string `json:"mfa_token"`
	}
	if err := json.Unmarshal(lb, &challenge); err != nil || !challenge.Required {
		t.Fatalf("%s", lb)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/auth/mfa/verify", strings.NewReader(`{"mfa_challenge_id":"`+challenge.ID+`","mfa_token":"`+challenge.Token+`","code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res, _ = ts.Client().Do(req)
	mb, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("verify %d %s", res.StatusCode, mb)
	}
	var aal2 string
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			aal2 = c.Value
		}
	}
	if aal2 == "" {
		t.Fatal("aal2 cookie")
	}
	return aal2
}

func TestIdentityCompletionStaysHonestAfterAAL2(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	aal2 := enrollAdminAAL2(t, s, ts, cookie)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/destroy", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "destroy-cluster")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: aal2})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(b), `"status":"not_implemented"`) {
		t.Fatalf("destroy %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/secrets/reveal", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: aal2})
	res, _ = ts.Client().Do(req)
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(b), `"status":"not_configured"`) {
		t.Fatalf("reveal %d %s", res.StatusCode, b)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes/"+uuid.NewString()+"/unlock", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: aal2})
	res, _ = ts.Client().Do(req)
	b, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(b), `"status":"not_configured"`) {
		t.Fatalf("unlock %d %s", res.StatusCode, b)
	}
}

func TestSecretRevealAndVolumeUnlockRequireStepUp(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/secrets/reveal", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("aal1 reveal %d", res.StatusCode)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/storage/volumes/"+uuid.NewString()+"/unlock", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("aal1 unlock %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestAPITokenCannotPassIdentityCompletionAAL(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	admin, _ := mem.GetUserByName(context.Background(), cluster.ID, "admin")
	plain := "ndl_aal_token"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: cluster.ID, UserID: admin.ID, Name: "t",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_aal",
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/destroy", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "destroy-cluster")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("token destroy %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

type failEnableMFAMethodStore struct {
	appdb.Store
}

func (f failEnableMFAMethodStore) EnableMFAMethod(context.Context, string) error {
	return errors.New("persist failed")
}

func TestMFAConfirmFailsClosedWhenEnablePersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	now := time.Date(2026, 9, 1, 15, 0, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/mfa/enroll", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("enroll %d %s", res.StatusCode, b)
	}
	var enrolled struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(b, &enrolled); err != nil || enrolled.Secret == "" {
		t.Fatal(string(b))
	}

	s.Store = failEnableMFAMethodStore{Store: mem}
	code := mfa.Code(enrolled.Secret, now)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/mfa/confirm", strings.NewReader(`{"code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("confirm persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not enable mfa") {
		t.Fatalf("confirm persist body %s", raw)
	}

	s.Store = mem
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/mfa", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	got, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get mfa %d %s", res.StatusCode, got)
	}
	var status map[string]any
	if err := json.Unmarshal(got, &status); err != nil {
		t.Fatal(err)
	}
	if status["enabled"] != false {
		t.Fatalf("GET /mfa must not show enabled after failed confirm %s", got)
	}
}

type failCreateServicePrincipalStore struct {
	appdb.Store
}

func (f failCreateServicePrincipalStore) CreateServicePrincipal(context.Context, appdb.ServicePrincipal) error {
	return errors.New("persist failed")
}

func TestServicePrincipalCreateFailsClosedWhenPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	s.Store = failCreateServicePrincipalStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/service-principals", strings.NewReader(`{"name":"backup"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("principal persist %d %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "could not record service principal") {
		t.Fatalf("principal persist body %s", b)
	}
}

type missBindGroupRoleStore struct {
	appdb.Store
}

func (missBindGroupRoleStore) BindGroupRole(context.Context, string, string, string) error {
	return nil
}

func TestPhase13BindGroupRoleFailsClosedWhenPersistMisses(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	hash, _ := auth.HashPassword("password1")
	view := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view-bind", PasswordHash: hash}
	if err := mem.CreateUser(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	if err := mem.BindRole(context.Background(), cluster.ID, view.ID, rbac.Viewer); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/groups", strings.NewReader(`{"name":"bind-miss"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("group %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	gid, _ := created["id"].(string)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/groups/"+gid+"/members", strings.NewReader(`{"user_id":"`+view.ID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("member %d %s", res.StatusCode, raw)
	}
	s.Store = missBindGroupRoleStore{Store: mem}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/groups/"+gid+"/roles", strings.NewReader(`{"role":"operator"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("role persist miss %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record group role") {
		t.Fatalf("role persist miss body %s", raw)
	}

	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view-bind","password":"password1"}`))
	loginBody, _ := io.ReadAll(login.Body)
	_ = login.Body.Close()
	if login.StatusCode != http.StatusOK {
		t.Fatalf("viewer login %d %s", login.StatusCode, loginBody)
	}
	var viewCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	if viewCookie == "" {
		t.Fatal("viewer cookie")
	}
	me, _ := http.NewRequest("GET", ts.URL+"/api/v1/me", nil)
	me.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	got, _ := ts.Client().Do(me)
	body, _ := io.ReadAll(got.Body)
	_ = got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Fatalf("GET /me %d %s", got.StatusCode, body)
	}
	if strings.Contains(string(body), `"operator"`) {
		t.Fatalf("GET /me must not claim operator after persist miss: %s", body)
	}
}

func TestPhase13GroupMemberAddFailsClosedForMissingUserAndIsIdempotent(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	op := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "op-member"}
	_ = mem.CreateUser(context.Background(), op)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/groups", strings.NewReader(`{"name":"ops"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("group %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	gid, _ := created["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/groups/"+gid+"/members", strings.NewReader(`{"user_id":"`+uuid.NewString()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing user %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), `"ok":true`) {
		t.Fatalf("200 must not invent membership of a missing user: %s", raw)
	}

	body := `{"user_id":"` + op.ID + `"}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/groups/"+gid+"/members", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("add member %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/groups/"+gid+"/members", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("re-add member %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/groups", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list groups %d %s", res.StatusCode, raw)
	}
	var listed struct {
		Items []struct {
			ID        string   `json:"id"`
			MemberIDs []string `json:"member_ids"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, g := range listed.Items {
		if g.ID != gid {
			continue
		}
		for _, id := range g.MemberIDs {
			if id == op.ID {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("GET member_ids must list the user once, got %d in %s", count, raw)
	}
}

type missAddGroupMemberStore struct {
	appdb.Store
}

func (missAddGroupMemberStore) AddGroupMember(context.Context, string, string, string) error {
	return nil
}

func TestPhase13AddGroupMemberFailsClosedWhenPersistMisses(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	op := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "op-miss-member"}
	if err := mem.CreateUser(context.Background(), op); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/groups", strings.NewReader(`{"name":"member-miss"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("group %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	gid, _ := created["id"].(string)
	s.Store = missAddGroupMemberStore{Store: mem}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/groups/"+gid+"/members", strings.NewReader(`{"user_id":"`+op.ID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("member persist miss %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record group member") {
		t.Fatalf("member persist miss body %s", raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/groups", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list groups %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), op.ID) {
		t.Fatalf("GET member_ids must not claim the user after persist miss: %s", raw)
	}
}
