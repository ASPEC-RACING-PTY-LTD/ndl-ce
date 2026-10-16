package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/rbac"
)

func TestUIPrefsMergePerUser(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	view := loginRole(t, ts, mem, "view", rbac.Viewer)
	other := loginRole(t, ts, mem, "other", rbac.Viewer)

	patch := func(cookie string, body string) (int, map[string]any) {
		t.Helper()
		res := doCookie(t, ts, cookie, "PATCH", "/api/v1/me", body)
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var me map[string]any
		_ = json.Unmarshal(raw, &me)
		return res.StatusCode, me
	}
	if code, _ := patch(view, `{"ui_prefs":{"theme":{"mode":"dark","accent":"#ff0088"}}}`); code != 200 {
		t.Fatalf("set theme %d", code)
	}
	code, me := patch(view, `{"ui_prefs":{"nav.order":{"Compute":["files","workloads"]}}}`)
	if code != 200 {
		t.Fatalf("set nav %d", code)
	}
	ui, _ := me["ui_prefs"].(map[string]any)
	if ui["theme"] == nil || ui["nav.order"] == nil {
		t.Fatalf("keys were not merged: %+v", ui)
	}
	// A null removes one key and leaves the rest.
	_, me = patch(view, `{"ui_prefs":{"theme":null}}`)
	ui, _ = me["ui_prefs"].(map[string]any)
	if ui["theme"] != nil || ui["nav.order"] == nil {
		t.Fatalf("remove: %+v", ui)
	}
	// Another user's preferences are separate.
	_, me = patch(other, `{"ui_prefs":{"theme":{"mode":"light"}}}`)
	ui, _ = me["ui_prefs"].(map[string]any)
	if ui["nav.order"] != nil {
		t.Fatalf("leaked between users: %+v", ui)
	}
	for _, bad := range []string{
		`{"ui_prefs":{"Bad Key":1}}`,
		`{"ui_prefs":{"big":"` + strings.Repeat("x", 17<<10) + `"}}`,
	} {
		if code, _ := patch(view, bad); code != http.StatusBadRequest {
			t.Fatalf("%.40s accepted: %d", bad, code)
		}
	}
	// Preference changes are not written to the audit log.
	evs, _ := mem.ListAuditEvents(t.Context(), "", 1000)
	for _, e := range evs {
		if e.Action == "prefs.update" {
			t.Fatalf("ui prefs were audited")
		}
	}
}
