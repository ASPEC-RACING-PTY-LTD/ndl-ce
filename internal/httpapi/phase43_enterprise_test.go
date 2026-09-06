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

func TestPhase43ImportSignedEntitlement(t *testing.T) {
	signer, trust, err := license.NewEphemeralSigner("ee-test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	doc, err := signer.Sign(license.Document{
		Version: license.DocumentVersion, Edition: license.EditionEE, Organization: "Example Pty Ltd",
		Capabilities: []string{license.CapIdentityOIDC, license.AuditExport},
		IssuedAt:     now.Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
		GraceUntil: now.Add(24 * time.Hour).Format(time.RFC3339), Accepted: true, Entitled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _, token := testServer(t)
	s.EETrust = trust
	s.EEPresent = func() bool { return true }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	body, _ := json.Marshal(map[string]any{"entitlement": doc})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/settings/license/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("confirm %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/settings/license/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, license.ImportConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("import %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"edition":"ee"`) || !strings.Contains(string(raw), `"status":"active"`) {
		t.Fatalf("want ee active %s", raw)
	}
	if strings.Contains(string(raw), `"workloads_stopped":true`) {
		t.Fatalf("%s", raw)
	}
	if !strings.Contains(string(raw), license.CapIdentityOIDC) {
		t.Fatalf("capabilities %s", raw)
	}

	s.EEPresent = func() bool { return false }
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/settings/license", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(raw), `"edition":"ce"`) || strings.Contains(string(raw), `"workloads_stopped":true`) {
		t.Fatalf("sidecar absent stays CE %s", raw)
	}
}

func TestPhase43EnterpriseStatusWithoutSidecar(t *testing.T) {
	s, _, token := testServer(t)
	s.EEPresent = func() bool { return false }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/enterprise/status", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"edition":"ce"`) {
		t.Fatalf("status %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/enterprise/capabilities", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound || strings.Contains(string(raw), `"workloads_stopped":true`) {
		t.Fatalf("proxy %d %s", res.StatusCode, raw)
	}
}

func TestPhase43BogusSignatureDoesNotEnableEE(t *testing.T) {
	s, _, token := testServer(t)
	s.EEPresent = func() bool { return true }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := []byte(`{"entitlement":{"version":"ndl-ee-entitlement-v1","key_id":"ee-dev-2026","edition":"ee","accepted":true,"entitled":true,"signature":"AAAA","workloads_stopped":false}}`)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/settings/license/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, license.ImportConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bogus %d %s", res.StatusCode, raw)
	}
}
