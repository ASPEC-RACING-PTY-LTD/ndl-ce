package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/no-dal/ndl-ce/internal/rbac"
)

func TestWorkloadOrderIsSavedPerUser(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	admin := claimAdmin(t, ts, token)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)

	res := doCookie(t, ts, view, "PATCH", "/api/v1/me", `{"workload_sort":"custom","workload_order":["b"," a ","b",""]}`)
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("save order %d %s", res.StatusCode, b)
	}
	var me map[string]any
	if err := json.NewDecoder(res.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	order, _ := me["workload_order"].([]any)
	if me["workload_sort"] != "custom" || len(order) != 2 || order[0] != "b" || order[1] != "a" {
		t.Fatalf("order not cleaned and saved: %+v", me)
	}

	res = doCookie(t, ts, admin, "GET", "/api/v1/me", "")
	me = map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&me)
	_ = res.Body.Close()
	if other, _ := me["workload_order"].([]any); len(other) != 0 || me["workload_sort"] != "" {
		t.Fatalf("order leaked to another user: %+v", me)
	}

	res = doCookie(t, ts, view, "PATCH", "/api/v1/me", `{"workload_sort":"random"}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad sort %d", res.StatusCode)
	}
	_ = res.Body.Close()
}
