package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/mfa"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// acctCall sends a request, optionally as if a trusted cloudflared had
// forwarded it from cfIP.
func acctCall(t *testing.T, ts *httptest.Server, cookie, method, path, body, cfIP string) (int, map[string]any, *http.Response) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, ts.URL+"/api/v1"+path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	if cfIP != "" {
		req.Header.Set("CF-Connecting-IP", cfIP)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	if len(out) == 0 && len(raw) > 0 {
		out["raw"] = string(raw)
	}
	return res.StatusCode, out, res
}

func sessionFrom(res *http.Response) string {
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	return ""
}

func enableCloudflare(t *testing.T, s *Server, mem *appdb.Memory) {
	t.Helper()
	cluster, _ := mem.GetCluster(context.Background())
	if err := mem.UpsertFeature(context.Background(), appdb.Feature{ClusterID: cluster.ID, ID: features.IDCloudflare, Enabled: true, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.trusted = trustedProxyCache{}
}

func enrollTestMFA(t *testing.T, ts *httptest.Server, cookie string, now time.Time) string {
	t.Helper()
	code, body, _ := acctCall(t, ts, cookie, "POST", "/mfa/enroll", "{}", "")
	if code != http.StatusCreated {
		t.Fatalf("enroll %d %v", code, body)
	}
	secret, _ := body["secret"].(string)
	code, body, _ = acctCall(t, ts, cookie, "POST", "/mfa/confirm", `{"code":"`+mfa.Code(secret, now)+`"}`, "")
	if code != http.StatusOK {
		t.Fatalf("confirm %d %v", code, body)
	}
	return secret
}

func TestClientIPTrustsOnlyTheActiveProxy(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	// Nothing in front of ndl-ce: the header is ignored.
	_, body, _ := acctCall(t, ts, cookie, "GET", "/me/ip", "", "203.0.113.9")
	if body["ip"] != "127.0.0.1" || body["via_proxy"] != false {
		t.Fatalf("untrusted header was believed: %v", body)
	}
	// Cloudflare on: loopback cloudflared is trusted without setup.
	enableCloudflare(t, s, mem)
	_, body, _ = acctCall(t, ts, cookie, "GET", "/me/ip", "", "203.0.113.9")
	if body["ip"] != "203.0.113.9" || body["via_proxy"] != true {
		t.Fatalf("detected cloudflared not trusted: %v", body)
	}
	// An admin can turn the detected proxy off.
	code, body, _ := acctCall(t, ts, cookie, "PUT", "/settings/trusted-proxies",
		`{"manual":[],"detected_off":["auto:cloudflared-host:v4","auto:cloudflared-host:v6"]}`, "")
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	_, body, _ = acctCall(t, ts, cookie, "GET", "/me/ip", "", "203.0.113.9")
	if body["ip"] != "127.0.0.1" {
		t.Fatalf("disabled proxy still trusted: %v", body)
	}
	// Every address cannot be a proxy.
	code, _, _ = acctCall(t, ts, cookie, "PUT", "/settings/trusted-proxies", `{"manual":[{"cidr":"0.0.0.0/0","kind":"generic"}],"detected_off":[]}`, "")
	if code != http.StatusBadRequest {
		t.Fatalf("0.0.0.0/0 accepted: %d", code)
	}
}

func TestIPAllowlistNeedsMFAAndBlocksOtherAddresses(t *testing.T) {
	s, mem, token := testServer(t)
	now := time.Date(2026, 10, 20, 9, 0, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	enableCloudflare(t, s, mem)

	on := `{"enabled":true,"entries":[{"cidr":"198.51.100.7"}]}`
	if code, body, _ := acctCall(t, ts, cookie, "PUT", "/me/ip-allowlist", on, "198.51.100.7"); code != http.StatusConflict {
		t.Fatalf("allowlist without MFA: %d %v", code, body)
	}
	secret := enrollTestMFA(t, ts, cookie, now)
	if code, body, _ := acctCall(t, ts, cookie, "PUT", "/me/ip-allowlist", on, "192.0.2.1"); code != http.StatusConflict {
		t.Fatalf("allowlist without the current address: %d %v", code, body)
	}
	code, body, _ := acctCall(t, ts, cookie, "PUT", "/me/ip-allowlist", on, "198.51.100.7")
	if code != http.StatusOK || body["enabled"] != true {
		t.Fatalf("%d %v", code, body)
	}
	// MFA cannot be removed while the allowlist is on.
	code, _, _ = acctCall(t, ts, cookie, "POST", "/me/mfa/disable", `{"password":"correct-horse","code":"`+mfa.Code(secret, now)+`"}`, "198.51.100.7")
	if code != http.StatusConflict {
		t.Fatalf("mfa removed under an allowlist: %d", code)
	}
	// The same session from another address is refused.
	code, body, _ = acctCall(t, ts, cookie, "GET", "/me", "", "192.0.2.1")
	if code != http.StatusUnauthorized || body["code"] != "ip_not_allowed" {
		t.Fatalf("other address got in: %d %v", code, body)
	}
	if code, _, _ = acctCall(t, ts, cookie, "GET", "/me", "", "198.51.100.7"); code != http.StatusOK {
		t.Fatalf("allowed address refused: %d", code)
	}
}

func TestIPRecoveryNeedsPasswordAndMFAAndAddsTheAddress(t *testing.T) {
	s, mem, token := testServer(t)
	now := time.Date(2026, 10, 20, 9, 0, 5, 0, time.UTC)
	s.Now = func() time.Time { return now }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	enableCloudflare(t, s, mem)
	secret := enrollTestMFA(t, ts, cookie, now)
	if code, body, _ := acctCall(t, ts, cookie, "PUT", "/me/ip-allowlist", `{"enabled":true,"entries":[{"cidr":"198.51.100.0/24"}]}`, "198.51.100.7"); code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}

	// Wrong password: no recovery is offered.
	code, _, _ := acctCall(t, ts, "", "POST", "/auth/login", `{"username":"admin","password":"wrong"}`, "192.0.2.50")
	if code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", code)
	}
	code, body, _ := acctCall(t, ts, "", "POST", "/auth/login", `{"username":"admin","password":"correct-horse"}`, "192.0.2.50")
	if code != http.StatusOK || body["ip_recovery"] != true {
		t.Fatalf("recovery not offered: %d %v", code, body)
	}
	id, tok := body["mfa_challenge_id"].(string), body["mfa_token"].(string)
	// A wrong code does not recover.
	code, _, _ = acctCall(t, ts, "", "POST", "/auth/mfa/verify", `{"mfa_challenge_id":"`+id+`","mfa_token":"`+tok+`","code":"000000"}`, "192.0.2.50")
	if code != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", code)
	}
	code, body, res := acctCall(t, ts, "", "POST", "/auth/mfa/verify", `{"mfa_challenge_id":"`+id+`","mfa_token":"`+tok+`","code":"`+mfa.Code(secret, now)+`"}`, "192.0.2.50")
	if code != http.StatusOK {
		t.Fatalf("recovery verify: %d %v", code, body)
	}
	fresh := sessionFrom(res)
	code, body, _ = acctCall(t, ts, fresh, "GET", "/me/ip-allowlist", "", "192.0.2.50")
	if code != http.StatusOK || !strings.Contains(toJSON(body), "192.0.2.50/32") {
		t.Fatalf("recovered address not added: %d %v", code, body)
	}
	// Rate limit: the short window allows three recoveries per address.
	for i := 0; i < 3; i++ {
		code, _, _ = acctCall(t, ts, "", "POST", "/auth/login", `{"username":"admin","password":"correct-horse"}`, "192.0.2.77")
	}
	if code != http.StatusTooManyRequests {
		t.Fatalf("recovery not rate limited: %d", code)
	}
}

func TestIPAllowlistWithoutMFAHasNoSelfRecovery(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	enableCloudflare(t, s, mem)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	hash, _ := auth.HashPassword("password-123")
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "ops", PasswordHash: hash, CreatedAt: time.Now()}
	_ = mem.CreateUser(ctx, u)
	_ = mem.BindRole(ctx, cluster.ID, u.ID, rbac.User)
	_ = mem.UpsertAccountProfile(ctx, appdb.AccountProfile{UserID: u.ID, ClusterID: cluster.ID, IPAllowlistOn: true,
		IPAllowlist: []appdb.IPAllowEntry{{CIDR: "198.51.100.7/32"}}})
	code, body, _ := acctCall(t, ts, "", "POST", "/auth/login", `{"username":"ops","password":"password-123"}`, "192.0.2.1")
	if code != http.StatusForbidden || body["code"] != "ip_not_allowed" || body["mfa_token"] != nil {
		t.Fatalf("no-MFA user got recovery: %d %v", code, body)
	}
	// A disabled account never gets in, allowlist or not.
	now := time.Now()
	u.DisabledAt = &now
	_ = mem.UpdateUser(ctx, u)
	code, _, _ = acctCall(t, ts, "", "POST", "/auth/login", `{"username":"ops","password":"password-123"}`, "198.51.100.7")
	if code != http.StatusUnauthorized {
		t.Fatalf("disabled account: %d", code)
	}
}

func TestRootAccountIsManagedOnlyByItself(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	rootCookie := claimAdmin(t, ts, token)
	ctx := context.Background()
	cluster, _ := mem.GetCluster(ctx)
	root, _ := mem.GetUserByName(ctx, cluster.ID, "admin")
	hash, _ := auth.HashPassword("password-123")
	other := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "second", PasswordHash: hash, CreatedAt: root.CreatedAt.Add(time.Hour)}
	_ = mem.CreateUser(ctx, other)
	_ = mem.BindRole(ctx, cluster.ID, other.ID, rbac.SuperAdmin)
	_, _, res := acctCall(t, ts, "", "POST", "/auth/login", `{"username":"second","password":"password-123"}`, "")
	otherCookie := sessionFrom(res)
	if otherCookie == "" {
		t.Fatal("second admin could not sign in")
	}
	if code, _, _ := acctCall(t, ts, otherCookie, "PATCH", "/users/"+root.ID, `{"display_name":"x"}`, ""); code != http.StatusForbidden {
		t.Fatalf("another admin edited root: %d", code)
	}
	if code, _, _ := acctCall(t, ts, otherCookie, "PUT", "/users/"+root.ID+"/ip-allowlist", `{"enabled":false,"entries":[]}`, ""); code != http.StatusForbidden {
		t.Fatalf("another admin edited root's allowlist: %d", code)
	}
	if code, body, _ := acctCall(t, ts, rootCookie, "PATCH", "/users/"+other.ID, `{"display_name":"Second","email":"second@example.com"}`, ""); code != http.StatusOK || body["email"] != "second@example.com" {
		t.Fatalf("root could not edit an admin: %d %v", code, body)
	}
}

func TestProfileDisplayNameIsUIOnlyAndEmailNeedsPassword(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	code, body, _ := acctCall(t, ts, cookie, "PATCH", "/me/profile", `{"display_name":"Boss"}`, "")
	if code != http.StatusOK || body["display_name"] != "Boss" || body["username"] != "admin" {
		t.Fatalf("%d %v", code, body)
	}
	if code, _, _ = acctCall(t, ts, cookie, "PATCH", "/me/profile", `{"email":"me@example.com","current_password":"nope"}`, ""); code != http.StatusForbidden {
		t.Fatalf("email changed without the password: %d", code)
	}
	code, body, _ = acctCall(t, ts, cookie, "PATCH", "/me/profile", `{"email":"me@example.com","email_secondary":"me2@example.com","current_password":"correct-horse"}`, "")
	if code != http.StatusOK || body["email"] != "me@example.com" || body["email_secondary"] != "me2@example.com" {
		t.Fatalf("%d %v", code, body)
	}
	code, _, res := acctCall(t, ts, cookie, "POST", "/me/password", `{"current_password":"correct-horse","new_password":"another-horse-9"}`, "")
	if code != http.StatusNoContent || sessionFrom(res) == "" {
		t.Fatalf("password change: %d", code)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
