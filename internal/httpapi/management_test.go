package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func claimedServer(t *testing.T) (*httptest.Server, *appdb.Memory, string) {
	t.Helper()
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	res, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"owner","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("claim=%d", res.StatusCode)
	}
	var cookie string
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("no owner cookie")
	}
	return ts, mem, cookie
}

func authedJSON(t *testing.T, ts *httptest.Server, method, path, cookie, body string, headers map[string]string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readUserID(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ID == "" {
		t.Fatal("missing id")
	}
	return body.ID
}

func TestManagementRBACAndLastOwner(t *testing.T) {
	ts, mem, ownerCookie := claimedServer(t)

	res := authedJSON(t, ts, "GET", "/api/v1/users", ownerCookie, "", nil)
	if res.StatusCode != 200 {
		t.Fatalf("owner list=%d", res.StatusCode)
	}
	_ = res.Body.Close()

	create := authedJSON(t, ts, "POST", "/api/v1/users", ownerCookie, `{"username":"ndl-e2e-viewer","password":"viewer-pass","role":"viewer","display_name":"Disposable viewer"}`, nil)
	if create.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(create.Body)
		t.Fatalf("create viewer=%d %s", create.StatusCode, raw)
	}
	viewerID := readUserID(t, create)

	createOp := authedJSON(t, ts, "POST", "/api/v1/users", ownerCookie, `{"username":"ndl-e2e-admin","password":"operator1","role":"operator"}`, nil)
	if createOp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(createOp.Body)
		t.Fatalf("create operator=%d %s", createOp.StatusCode, raw)
	}
	opID := readUserID(t, createOp)

	viewerCookie := loginAs(t, ts, "ndl-e2e-viewer", "viewer-pass")
	opCookie := loginAs(t, ts, "ndl-e2e-admin", "operator1")

	denied := authedJSON(t, ts, "GET", "/api/v1/users", viewerCookie, "", nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer users=%d", denied.StatusCode)
	}
	_ = denied.Body.Close()

	denied = authedJSON(t, ts, "GET", "/api/v1/roles", viewerCookie, "", nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer roles=%d", denied.StatusCode)
	}
	_ = denied.Body.Close()

	denied = authedJSON(t, ts, "GET", "/api/v1/settings/security", viewerCookie, "", nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer security=%d", denied.StatusCode)
	}
	_ = denied.Body.Close()

	denied = authedJSON(t, ts, "GET", "/api/v1/users", opCookie, "", nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("operator users=%d", denied.StatusCode)
	}
	_ = denied.Body.Close()

	denied = authedJSON(t, ts, "PATCH", "/api/v1/settings/security", opCookie, `{"mfa_required":true}`, nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("operator security=%d", denied.StatusCode)
	}
	_ = denied.Body.Close()

	tok := authedJSON(t, ts, "GET", "/api/v1/tokens", opCookie, "", nil)
	if tok.StatusCode != 200 {
		t.Fatalf("operator tokens=%d", tok.StatusCode)
	}
	_ = tok.Body.Close()

	denied = authedJSON(t, ts, "GET", "/api/v1/tokens", viewerCookie, "", nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer tokens=%d", denied.StatusCode)
	}
	_ = denied.Body.Close()

	denied = authedJSON(t, ts, "GET", "/api/v1/updates", viewerCookie, "", nil)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer updates=%d", denied.StatusCode)
	}
	_ = denied.Body.Close()

	owners, err := mem.CountEnabledAdmins(context.Background(), mustClusterID(t, mem))
	if err != nil || owners != 1 {
		t.Fatalf("enabled admins=%d %v", owners, err)
	}

	cluster, _ := mem.GetCluster(context.Background())
	owner, err := mem.GetUserByName(context.Background(), cluster.ID, "owner")
	if err != nil || owner == nil {
		t.Fatal(err)
	}
	del := authedJSON(t, ts, "DELETE", "/api/v1/users/"+owner.ID, ownerCookie, "", map[string]string{"X-Nodal-Confirm": "delete-user"})
	if del.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(del.Body)
		t.Fatalf("delete last owner=%d %s", del.StatusCode, raw)
	}
	_ = del.Body.Close()

	demote := authedJSON(t, ts, "PATCH", "/api/v1/users/"+owner.ID, ownerCookie, `{"role":"viewer"}`, nil)
	if demote.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(demote.Body)
		t.Fatalf("demote last owner=%d %s", demote.StatusCode, raw)
	}
	_ = demote.Body.Close()

	disable := authedJSON(t, ts, "PATCH", "/api/v1/users/"+owner.ID, ownerCookie, `{"disabled":true}`, map[string]string{"X-Nodal-Confirm": "disable-user"})
	if disable.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(disable.Body)
		t.Fatalf("disable last owner=%d %s", disable.StatusCode, raw)
	}
	_ = disable.Body.Close()

	second := authedJSON(t, ts, "POST", "/api/v1/users", ownerCookie, `{"username":"ndl-e2e-owner2","password":"owner-pass","role":"admin"}`, nil)
	if second.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(second.Body)
		t.Fatalf("second owner=%d %s", second.StatusCode, raw)
	}
	secondID := readUserID(t, second)

	disableViewer := authedJSON(t, ts, "PATCH", "/api/v1/users/"+viewerID, ownerCookie, `{"disabled":true}`, map[string]string{"X-Nodal-Confirm": "disable-user"})
	if disableViewer.StatusCode != 200 {
		raw, _ := io.ReadAll(disableViewer.Body)
		t.Fatalf("disable viewer=%d %s", disableViewer.StatusCode, raw)
	}
	_ = disableViewer.Body.Close()

	loginDenied, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(
		`{"username":"ndl-e2e-viewer","password":"viewer-pass"}`))
	if err != nil {
		t.Fatal(err)
	}
	if loginDenied.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disabled login=%d", loginDenied.StatusCode)
	}
	_ = loginDenied.Body.Close()

	delSecond := authedJSON(t, ts, "DELETE", "/api/v1/users/"+secondID, ownerCookie, "", map[string]string{"X-Nodal-Confirm": "delete-user"})
	if delSecond.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(delSecond.Body)
		t.Fatalf("delete extra owner=%d %s", delSecond.StatusCode, raw)
	}

	delOp := authedJSON(t, ts, "DELETE", "/api/v1/users/"+opID, ownerCookie, "", map[string]string{"X-Nodal-Confirm": "delete-user"})
	if delOp.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(delOp.Body)
		t.Fatalf("delete operator=%d %s", delOp.StatusCode, raw)
	}

	roles := authedJSON(t, ts, "GET", "/api/v1/roles", ownerCookie, "", nil)
	if roles.StatusCode != 200 {
		t.Fatalf("roles=%d", roles.StatusCode)
	}
	var roleBody struct {
		Custom bool `json:"custom_roles"`
		Items  []struct {
			Name      string `json:"name"`
			Immutable bool   `json:"immutable"`
		} `json:"items"`
	}
	if err := json.NewDecoder(roles.Body).Decode(&roleBody); err != nil {
		t.Fatal(err)
	}
	_ = roles.Body.Close()
	if roleBody.Custom {
		t.Fatal("custom roles must stay off")
	}
	if len(roleBody.Items) == 0 || !roleBody.Items[0].Immutable {
		t.Fatal("built-in roles must be immutable")
	}

	sec := authedJSON(t, ts, "PATCH", "/api/v1/settings/security", ownerCookie, `{"mfa_required":true}`, nil)
	if sec.StatusCode != 200 {
		raw, _ := io.ReadAll(sec.Body)
		t.Fatalf("security=%d %s", sec.StatusCode, raw)
	}
	_ = sec.Body.Close()
	sec = authedJSON(t, ts, "PATCH", "/api/v1/settings/security", ownerCookie, `{"mfa_required":false}`, nil)
	if sec.StatusCode != 200 {
		t.Fatalf("security reset=%d", sec.StatusCode)
	}
	_ = sec.Body.Close()

	reset := authedJSON(t, ts, "POST", "/api/v1/users/"+viewerID+"/password", ownerCookie, `{"password":"viewer-pass"}`, map[string]string{"X-Nodal-Confirm": "reset-password"})
	if reset.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(reset.Body)
		t.Fatalf("reset=%d %s", reset.StatusCode, raw)
	}

	enable := authedJSON(t, ts, "PATCH", "/api/v1/users/"+viewerID, ownerCookie, `{"disabled":false}`, nil)
	if enable.StatusCode != 200 {
		raw, _ := io.ReadAll(enable.Body)
		t.Fatalf("enable=%d %s", enable.StatusCode, raw)
	}
	_ = enable.Body.Close()

	mfa := authedJSON(t, ts, "PATCH", "/api/v1/users/"+viewerID, ownerCookie, `{"mfa_required":true}`, nil)
	if mfa.StatusCode != 200 {
		raw, _ := io.ReadAll(mfa.Body)
		t.Fatalf("require mfa=%d %s", mfa.StatusCode, raw)
	}
	_ = mfa.Body.Close()

	delViewer := authedJSON(t, ts, "DELETE", "/api/v1/users/"+viewerID, ownerCookie, "", map[string]string{"X-Nodal-Confirm": "delete-user"})
	if delViewer.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(delViewer.Body)
		t.Fatalf("delete viewer=%d %s", delViewer.StatusCode, raw)
	}

	left, _ := mem.ListUsers(context.Background(), cluster.ID)
	for _, u := range left {
		if strings.HasPrefix(u.Username, "ndl-e2e-") {
			t.Fatalf("leftover disposable user %s", u.Username)
		}
	}
}

func TestSetupClaimDoesNotOverwriteExistingOwner(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	claim, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"owner","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	if claim.StatusCode != 200 {
		t.Fatalf("claim=%d", claim.StatusCode)
	}
	_ = claim.Body.Close()

	cluster, _ := mem.GetCluster(context.Background())
	before, err := mem.GetUserByName(context.Background(), cluster.ID, "owner")
	if err != nil || before == nil {
		t.Fatal(err)
	}
	hash := before.PasswordHash

	replay, err := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(
		`{"token":"`+token+`","username":"owner","password":"different-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	if replay.StatusCode != http.StatusConflict {
		t.Fatalf("replay=%d", replay.StatusCode)
	}
	_ = replay.Body.Close()

	if err := mem.EnsureRoles(context.Background(), cluster.ID, rbac.SeedRoles()); err != nil {
		t.Fatal(err)
	}
	after, err := mem.GetUserByName(context.Background(), cluster.ID, "owner")
	if err != nil || after == nil {
		t.Fatal(err)
	}
	if after.PasswordHash != hash {
		t.Fatal("EnsureRoles and setup replay must not change the owner password hash")
	}
	roles, err := mem.UserRoles(context.Background(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(roles, rbac.Admin) {
		t.Fatalf("roles=%v", roles)
	}
}

func TestMeIncludesGrants(t *testing.T) {
	ts, _, cookie := claimedServer(t)
	res := authedJSON(t, ts, "GET", "/api/v1/me", cookie, "", nil)
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("me=%d", res.StatusCode)
	}
	var me struct {
		Roles  []string `json:"roles"`
		Grants []string `json:"grants"`
	}
	if err := json.NewDecoder(res.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if !containsString(me.Roles, rbac.Admin) || !containsString(me.Grants, rbac.All) {
		t.Fatalf("me roles=%v grants=%v", me.Roles, me.Grants)
	}
}

func mustClusterID(t *testing.T, mem *appdb.Memory) string {
	t.Helper()
	c, err := mem.GetCluster(context.Background())
	if err != nil || c == nil {
		t.Fatal(err)
	}
	return c.ID
}

func TestUserJSONNeverIncludesSecrets(t *testing.T) {
	ts, _, cookie := claimedServer(t)
	res := authedJSON(t, ts, "GET", "/api/v1/users", cookie, "", nil)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if bytes.Contains(bytes.ToLower(raw), []byte("password_hash")) || bytes.Contains(raw, []byte("$argon2")) {
		t.Fatalf("user list leaked a secret: %s", raw)
	}
}
