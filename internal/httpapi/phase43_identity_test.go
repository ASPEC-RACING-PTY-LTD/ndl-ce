package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/license"
)

func TestSSOProvidersEmptyWithoutEE(t *testing.T) {
	s, _, token := testServer(t)
	s.EEPresent = func() bool { return false }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	res, err := ts.Client().Get(ts.URL + "/api/v1/auth/sso/providers")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"items"`) {
		t.Fatalf("%d %s", res.StatusCode, raw)
	}
}

func TestLDAPLoginViaEnterpriseSidecar(t *testing.T) {
	signer, trust, err := license.NewEphemeralSigner("ee-test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	doc, err := signer.Sign(license.Document{
		Version: license.DocumentVersion, Edition: license.EditionEE, Organization: "Example Pty Ltd",
		Capabilities: []string{license.CapIdentityLDAP, license.CapPolicyAdvanced, license.CapIdentityOIDC},
		IssuedAt:     now.Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
		GraceUntil: now.Add(24 * time.Hour).Format(time.RFC3339), Accepted: true, Entitled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/enterprise/identity/ldap/bind" && r.Method == http.MethodPost:
			var req struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Username != "alice" || req.Password != "secret" {
				http.Error(w, `{"error":"ldap credentials are invalid"}`, http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"username": "alice", "subject": "uid=alice", "kind": "ldap"})
		case r.URL.Path == "/api/v1/enterprise/policy":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ee.Close()

	s, _, token := testServer(t)
	s.EETrust = trust
	s.EEPresent = func() bool { return true }
	s.EEURL = ee.URL
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	body, _ := json.Marshal(map[string]any{"entitlement": doc})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/settings/license/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, license.ImportConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("import %d %s", res.StatusCode, raw)
	}

	login, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"alice","password":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(login.Body)
	_ = login.Body.Close()
	if login.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"username":"alice"`) {
		t.Fatalf("ldap login %d %s", login.StatusCode, raw)
	}
}

func TestLocalLoginUnchangedWithoutEE(t *testing.T) {
	s, _, token := testServer(t)
	s.EEPresent = func() bool { return false }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	res, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"admin","password":"correct-horse"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("local login %d %s", res.StatusCode, raw)
	}
	_ = res.Body.Close()
}

func TestPolicyDenyLocalPasswordDoesNotStopWorkloads(t *testing.T) {
	signer, trust, err := license.NewEphemeralSigner("ee-test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	doc, err := signer.Sign(license.Document{
		Version: license.DocumentVersion, Edition: license.EditionEE,
		Capabilities: []string{license.CapPolicyAdvanced},
		IssuedAt:     now.Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
		GraceUntil: now.Add(24 * time.Hour).Format(time.RFC3339), Accepted: true, Entitled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/enterprise/policy" {
			_ = json.NewEncoder(w).Encode(map[string]any{"deny_local_password": true})
			return
		}
		http.NotFound(w, r)
	}))
	defer ee.Close()
	s, _, token := testServer(t)
	s.EETrust = trust
	s.EEPresent = func() bool { return true }
	s.EEURL = ee.URL
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body, _ := json.Marshal(map[string]any{"entitlement": doc})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/settings/license/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, license.ImportConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("import %d", res.StatusCode)
	}
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"admin","password":"correct-horse"}`))
	raw, _ := io.ReadAll(login.Body)
	_ = login.Body.Close()
	if login.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected deny %d %s", login.StatusCode, raw)
	}
	if strings.Contains(string(raw), `"workloads_stopped":true`) {
		t.Fatalf("%s", raw)
	}
}

func TestFleetHeartbeatOnLicenseRead(t *testing.T) {
	signer, trust, err := license.NewEphemeralSigner("ee-test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	doc, err := signer.Sign(license.Document{
		Version: license.DocumentVersion, Edition: license.EditionEE,
		Capabilities: []string{license.CapFleetInventory},
		IssuedAt:     now.Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
		GraceUntil: now.Add(24 * time.Hour).Format(time.RFC3339), Accepted: true, Entitled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	saw := make(chan struct{}, 1)
	ee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/enterprise/fleet/heartbeat" {
			select {
			case saw <- struct{}{}:
			default:
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "ok"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ee.Close()
	s, _, token := testServer(t)
	s.EETrust = trust
	s.EEPresent = func() bool { return true }
	s.EEURL = ee.URL
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body, _ := json.Marshal(map[string]any{"entitlement": doc})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/settings/license/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, license.ImportConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("import %d", res.StatusCode)
	}
	select {
	case <-saw:
	case <-time.After(2 * time.Second):
		t.Fatal("expected fleet heartbeat")
	}
}
