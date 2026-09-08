package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/rbac"
)

func TestTokenListPresetTTLAndExpiry(t *testing.T) {
	s, _, token := testServer(t)
	fixed := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return fixed }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	create, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens", strings.NewReader(`{"name":"diag","preset":"readonly-debug","ttl_hours":2}`))
	create.Header.Set("Content-Type", "application/json")
	create.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(create)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created struct {
		ID          string   `json:"id"`
		Token       string   `json:"token"`
		Permissions []string `json:"permissions"`
		ExpiresAt   string   `json:"expires_at"`
		Preset      string   `json:"preset"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.Token == "" || created.ID == "" {
		t.Fatal(string(raw))
	}
	if created.Preset != rbac.TokenPresetReadonlyDebug || !rbac.Authorize(created.Permissions, rbac.MigrationRead) || rbac.Authorize(created.Permissions, rbac.AuditRead) {
		t.Fatalf("preset grants %+v", created)
	}
	if created.ExpiresAt != fixed.Add(2*time.Hour).UTC().Format(time.RFC3339) {
		t.Fatalf("expires %s", created.ExpiresAt)
	}

	listReq, _ := http.NewRequest("GET", ts.URL+"/api/v1/tokens", nil)
	listReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	listRes, _ := ts.Client().Do(listReq)
	listRaw, _ := io.ReadAll(listRes.Body)
	_ = listRes.Body.Close()
	if listRes.StatusCode != http.StatusOK || !strings.Contains(string(listRaw), created.ID) || !strings.Contains(string(listRaw), `"disabled":false`) {
		t.Fatalf("list %d %s", listRes.StatusCode, listRaw)
	}

	for _, path := range []string{"/events", "/workloads", "/storage/pools", "/networks", "/nodes", "/migration/jobs", "/migration/jobs/missing/diagnostics"} {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1"+path, nil)
		req.Header.Set("Authorization", "Bearer "+created.Token)
		got, _ := ts.Client().Do(req)
		_, _ = io.Copy(io.Discard, got.Body)
		_ = got.Body.Close()
		if got.StatusCode == http.StatusUnauthorized || got.StatusCode == http.StatusForbidden {
			t.Fatalf("debug token %s %d", path, got.StatusCode)
		}
	}
	deny, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens", strings.NewReader(`{"name":"x"}`))
	deny.Header.Set("Content-Type", "application/json")
	deny.Header.Set("Authorization", "Bearer "+created.Token)
	denyRes, _ := ts.Client().Do(deny)
	_ = denyRes.Body.Close()
	if denyRes.StatusCode != http.StatusForbidden {
		t.Fatalf("readonly must not create tokens %d", denyRes.StatusCode)
	}

	s.Now = func() time.Time { return fixed.Add(3 * time.Hour) }
	expired, _ := http.NewRequest("GET", ts.URL+"/api/v1/me", nil)
	expired.Header.Set("Authorization", "Bearer "+created.Token)
	expRes, _ := ts.Client().Do(expired)
	_ = expRes.Body.Close()
	if expRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired token %d", expRes.StatusCode)
	}

	rev, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens/revoke", strings.NewReader(`{"id":"`+created.ID+`"}`))
	rev.Header.Set("Content-Type", "application/json")
	rev.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	revRes, _ := ts.Client().Do(rev)
	_ = revRes.Body.Close()
	if revRes.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke %d", revRes.StatusCode)
	}
}

func TestFullAuditPresetRequiresAuditGrant(t *testing.T) {
	if !rbac.Authorize(rbac.New().PermissionsForRole(rbac.Admin), rbac.AuditRead) {
		t.Fatal("admin audit")
	}
	op := rbac.New().PermissionsForRole(rbac.Operator)
	if rbac.Authorize(op, rbac.AuditRead) {
		t.Fatal("operator must not grant full-audit")
	}
}
