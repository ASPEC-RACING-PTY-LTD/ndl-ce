package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestListAuditSystemAndMalformedActors(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = mem.InsertAudit(context.Background(), appdb.AuditEvent{
		ID: uuid.NewString(), ClusterID: cluster.ID, ActorUserID: "",
		Action: "inventory.refresh", Result: "ok", CreatedAt: time.Now().UTC(),
	})
	_ = mem.InsertAudit(context.Background(), appdb.AuditEvent{
		ID: uuid.NewString(), ClusterID: cluster.ID, ActorUserID: "not-a-uuid",
		Action: "legacy.event", Result: "ok", CreatedAt: time.Now().UTC(),
	})
	gone := uuid.NewString()
	_ = mem.InsertAudit(context.Background(), appdb.AuditEvent{
		ID: uuid.NewString(), ClusterID: cluster.ID, ActorUserID: gone,
		Action: "users.delete", Result: "ok", Detail: json.RawMessage(`{"detail":"diagnostic","password":"secret"}`),
		CreatedAt: time.Now().UTC(),
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/audit", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("audit %d %s", res.StatusCode, b)
	}
	if string(b) == `{"error":"ERROR: invalid input syntax for type uuid: \"\" (SQLSTATE 22P02)"}` {
		t.Fatal("empty actor still coerced to uuid")
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	var sawSystem, sawDeleted bool
	for _, item := range body.Items {
		if item["action"] == "inventory.refresh" {
			if item["actor_kind"] != "system" || item["actor_label"] != "System" {
				t.Fatalf("system actor %+v", item)
			}
			sawSystem = true
		}
		if item["action"] == "users.delete" {
			if item["actor_kind"] != "deleted" {
				t.Fatalf("deleted actor %+v", item)
			}
			detail, _ := item["detail"].(map[string]any)
			if _, ok := detail["password"]; ok {
				t.Fatal("password leaked in audit detail")
			}
			sawDeleted = true
		}
		if item["action"] == "legacy.event" {
			if item["actor_user_id"] != nil {
				t.Fatalf("malformed actor id must not be returned: %+v", item)
			}
		}
	}
	if !sawSystem || !sawDeleted {
		t.Fatalf("missing expected audit rows: %s", b)
	}
}

func TestSanitizeAuditDetailDropsSecrets(t *testing.T) {
	got := sanitizeAuditDetail(json.RawMessage(`{"detail":"Nightly","token":"ndl_secret","mfa_secret":"x"}`))
	if _, ok := got["token"]; ok {
		t.Fatal("token survived")
	}
	if got["detail"] != "Nightly" {
		t.Fatalf("kept detail: %v", got)
	}
}
