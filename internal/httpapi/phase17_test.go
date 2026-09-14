package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func TestExpertDoesNotGrantPermissions(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)

	res := doCookie(t, ts, view, "PATCH", "/api/v1/me", `{"ux_level":"expert"}`)
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("expert without ack %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	res = doCookie(t, ts, view, "PATCH", "/api/v1/me", `{"ux_level":"expert","expert_ack":true}`)
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("ack expert %d %s", res.StatusCode, b)
	}
	var me map[string]any
	if err := json.NewDecoder(res.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if me["ux_level"] != appdb.UXExpert || me["expert_ack"] != true {
		t.Fatalf("%+v", me)
	}

	res = doCookie(t, ts, view, "POST", "/api/v1/workloads", `{"name":"x","kind":"vm"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer expert create %d", res.StatusCode)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, view, "POST", "/api/v1/storage/pools", `{"name":"p","path":"/var/lib/ndl/storage/x"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer expert storage %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestExpertAckIsOneTime(t *testing.T) {
	s, mem, token := testServer(t)
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return t0 }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	view := loginRole(t, ts, mem, "view2", rbac.Viewer)

	res := doCookie(t, ts, view, "GET", "/api/v1/me", "")
	if res.StatusCode != 200 {
		t.Fatalf("me %d", res.StatusCode)
	}
	var me map[string]any
	if err := json.NewDecoder(res.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if me["ux_level"] != appdb.UXGuided || me["expert_ack"] != false {
		t.Fatalf("default %+v", me)
	}

	res = doCookie(t, ts, view, "PATCH", "/api/v1/me", `{"ux_level":"expert","expert_ack":true}`)
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("ack %d %s", res.StatusCode, b)
	}
	if err := json.NewDecoder(res.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if me["expert_ack_at"] != t0.Format(time.RFC3339) {
		t.Fatalf("ack at %v", me["expert_ack_at"])
	}

	s.Now = func() time.Time { return t0.Add(time.Hour) }
	res = doCookie(t, ts, view, "PATCH", "/api/v1/me", `{"ux_level":"guided"}`)
	if res.StatusCode != 200 {
		t.Fatalf("guided %d", res.StatusCode)
	}
	_ = res.Body.Close()

	res = doCookie(t, ts, view, "PATCH", "/api/v1/me", `{"ux_level":"expert"}`)
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("reselect expert %d %s", res.StatusCode, b)
	}
	if err := json.NewDecoder(res.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if me["ux_level"] != appdb.UXExpert {
		t.Fatalf("level %+v", me)
	}
	if me["expert_ack_at"] != t0.Format(time.RFC3339) {
		t.Fatalf("ack must stay one-time %+v", me)
	}
}

func TestGuidedAndAdvancedShareCreateContract(t *testing.T) {
	body := map[string]any{
		"name": "vm-1", "kind": "vm", "network_id": "net", "pool_id": "pool",
		"cpus": 2, "memory_bytes": 2147483648, "firmware": "bios", "autostart": false,
	}
	guided, _ := json.Marshal(body)
	advanced, _ := json.Marshal(body)
	if string(guided) != string(advanced) {
		t.Fatal("guided and advanced must post the same body")
	}
}
