package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/appmanifest"
)

func TestPhase37OfficialVerifyAndScanReport(t *testing.T) {
	_, _, ts, cookie, _, _, _ := phase22Ready(t)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/store/apps", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list %d %s", res.StatusCode, raw)
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) < 1 || listed.Items[0]["signed"] != true {
		t.Fatalf("official sample must be signed %s", raw)
	}
	id, _ := listed.Items[0]["id"].(string)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+id+"/verify", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("verify %d %s", res.StatusCode, raw)
	}
	var ver map[string]any
	if err := json.Unmarshal(raw, &ver); err != nil {
		t.Fatal(err)
	}
	if ver["status"] != appdb.StoreVerifyPass || ver["trust_class"] != appmanifest.ClassOfficial {
		t.Fatalf("%s", raw)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/store/apps/"+id+"/scans", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"kind":"vulnerability"`) || !strings.Contains(string(raw), "unavailable") {
		t.Fatalf("scan report %d %s", res.StatusCode, raw)
	}
}

func TestPhase37TamperFailsClosedAndRevokedKeyStopsInstall(t *testing.T) {
	_, mem, ts, cookie, clusterID, _, _ := phase22Ready(t)
	manifest := `
apiVersion: nodal.store/v1
name: signed-web
version: "1"
class: community
title: Signed
deployment:
  kind: oci
  image: docker.io/library/caddy:2.8.4
`
	body, _ := json.Marshal(map[string]any{"manifest": manifest})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/store/apps/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("import %d %s", res.StatusCode, raw)
	}
	var pkg map[string]any
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatal(err)
	}
	pkgID, _ := pkg["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/keys", strings.NewReader(`{"name":"publisher","class":"verified"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("key %d %s", res.StatusCode, raw)
	}
	var key map[string]any
	if err := json.Unmarshal(raw, &key); err != nil {
		t.Fatal(err)
	}
	if key["public_key"] == "" || key["private_key"] != nil {
		t.Fatalf("private key must not be returned %s", raw)
	}
	keyID, _ := key["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+pkgID+"/sign", strings.NewReader(`{"key_id":"`+keyID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("sign %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("PUT", ts.URL+"/api/v1/store/policy", strings.NewReader(`{"install_policy":"verified-only"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("policy %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+pkgID+"/verify", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"status":"pass"`) {
		t.Fatalf("verify pass %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+pkgID+"/install", strings.NewReader(`{"name":"signed-web"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("install signed %d %s", res.StatusCode, raw)
	}
	wls, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(wls) != 1 {
		t.Fatalf("workloads %+v", wls)
	}

	row, err := mem.GetStorePackage(context.Background(), clusterID, pkgID)
	if err != nil || row == nil {
		t.Fatal("package missing")
	}
	row.ManifestYAML = row.ManifestYAML + "\n# tampered\n"
	if err := mem.UpsertStorePackage(context.Background(), *row); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+pkgID+"/verify", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "tamper") {
		t.Fatalf("tamper verify %d %s", res.StatusCode, raw)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+pkgID+"/install", strings.NewReader(`{"name":"tampered"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("tamper install %d %s", res.StatusCode, raw)
	}

	good := `
apiVersion: nodal.store/v1
name: keep-running
version: "1"
class: community
deployment:
  kind: oci
  image: docker.io/library/caddy:2.8.4
`
	body, _ = json.Marshal(map[string]any{"manifest": good})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	var pkg2 map[string]any
	if err := json.Unmarshal(raw, &pkg2); err != nil {
		t.Fatal(err)
	}
	pkg2ID, _ := pkg2["id"].(string)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+pkg2ID+"/sign", strings.NewReader(`{"key_id":"`+keyID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("sign 2 %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/keys/"+keyID+"/revoke", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, revokeStoreKeyConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("revoke %d %s", res.StatusCode, raw)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+pkg2ID+"/install", strings.NewReader(`{"name":"keep-running"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "revoked") {
		t.Fatalf("revoked install %d %s", res.StatusCode, raw)
	}
	wls, _ = mem.ListWorkloads(context.Background(), clusterID)
	if len(wls) != 1 {
		t.Fatalf("revoke must not delete running workloads %+v", wls)
	}
}

func TestPhase37VerifiedOnlyRefusesUnsigned(t *testing.T) {
	_, _, ts, cookie, _, _, _ := phase22Ready(t)
	manifest := `
apiVersion: nodal.store/v1
name: unsigned-web
version: "1"
class: community
deployment:
  kind: oci
  image: docker.io/library/caddy:2.8.4
`
	body, _ := json.Marshal(map[string]any{"manifest": manifest})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/store/apps/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	var pkg map[string]any
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatal(err)
	}
	id, _ := pkg["id"].(string)
	req, _ = http.NewRequest("PUT", ts.URL+"/api/v1/store/policy", strings.NewReader(`{"install_policy":"verified-only"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+id+"/install", strings.NewReader(`{"name":"unsigned-web"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "unsigned") {
		t.Fatalf("unsigned %d %s", res.StatusCode, raw)
	}
}
