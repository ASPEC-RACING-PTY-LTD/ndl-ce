package appdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Run with NODAL_TEST_PG pointing at a migrated test database.
func TestIAMSettingsNotificationsApprovalsOnPostgres(t *testing.T) {
	dsn := os.Getenv("NODAL_TEST_PG")
	if dsn == "" {
		t.Skip("NODAL_TEST_PG not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	p := &Postgres{DB: db}
	var cluster string
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM clusters LIMIT 1`).Scan(&cluster); err != nil {
		cluster = uuid.NewString()
		if _, err := db.ExecContext(ctx, `INSERT INTO clusters (id, name) VALUES ($1, 'iam-test')`, cluster); err != nil {
			t.Fatal(err)
		}
	}
	uid := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, cluster_id, username, password_hash) VALUES ($1,$2,$3,'x')`, uid, cluster, "iam-"+uid[:8]); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, uid) //nolint:errcheck

	st, err := p.GetIAMSettings(ctx, cluster)
	if err != nil {
		t.Fatal(err)
	}
	st.ClusterID, st.RootUserID = cluster, uid
	st.Creators = AutomationCreators{Roles: []string{"admin", "ops"}}
	st.CreatorsLocked, st.CreatorsAdminsMayEdit = true, true
	st.ApprovalsAccess = map[string][]string{"admin": {"approvals.view"}}
	st.UpdatedBy = "tester"
	if err := p.PutIAMSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	got, err := p.GetIAMSettings(ctx, cluster)
	if err != nil || got.RootUserID != uid || len(got.Creators.Roles) != 2 || !got.CreatorsLocked || !got.CreatorsAdminsMayEdit || got.ApprovalsAccess["admin"][0] != "approvals.view" {
		t.Fatalf("%+v %v", got, err)
	}

	// Notifications: newest first, unread count, mark one and all read.
	base := time.Now().UTC().Add(-time.Minute)
	var ids []string
	for i := 0; i < 3; i++ {
		id := uuid.NewString()
		ids = append(ids, id)
		if err := p.CreateNotification(ctx, Notification{ID: id, ClusterID: cluster, UserID: uid, Kind: "k", Title: "t", CreatedAt: base.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := p.ListNotifications(ctx, cluster, uid, false, 10)
	if len(list) != 3 || list[0].ID != ids[2] {
		t.Fatalf("list %+v", list)
	}
	if err := p.MarkNotificationsRead(ctx, cluster, uid, []string{ids[0]}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n, _ := p.CountUnreadNotifications(ctx, cluster, uid); n != 2 {
		t.Fatalf("unread %d", n)
	}
	if unread, _ := p.ListNotifications(ctx, cluster, uid, true, 10); len(unread) != 2 {
		t.Fatalf("unread list %d", len(unread))
	}
	if err := p.MarkNotificationsRead(ctx, cluster, uid, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n, _ := p.CountUnreadNotifications(ctx, cluster, uid); n != 0 {
		t.Fatalf("unread after all %d", n)
	}

	// Approvals: a pending version, decided once.
	flow := AutomationFlow{ID: uuid.NewString(), ClusterID: cluster, Name: "approval-" + uid[:8], Draft: json.RawMessage(`{}`), Limits: json.RawMessage(`{}`), CreatedBy: "x", DraftTainted: true}
	if err := p.CreateAutomationFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DELETE FROM automation_flows WHERE id=$1`, flow.ID) //nolint:errcheck
	if err := p.CreateAutomationFlowVersion(ctx, AutomationFlowVersion{
		FlowID: flow.ID, ClusterID: cluster, Version: 1, Definition: json.RawMessage(`{"nodes":[]}`), PublishedBy: "x",
		Status: FlowVersionPending, Hash: "h1", NeedsApproval: true, Protected: json.RawMessage(`[{"node_id":"a"}]`), RequestedByID: uid,
	}); err != nil {
		t.Fatal(err)
	}
	flow.PendingVersion = 1
	if err := p.UpdateAutomationFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	f, _ := p.GetAutomationFlow(ctx, cluster, flow.ID)
	if f.PendingVersion != 1 || !f.DraftTainted {
		t.Fatalf("flow %+v", f)
	}
	pending, _ := p.ListAutomationApprovals(ctx, cluster, FlowVersionPending, 10)
	found := false
	for _, v := range pending {
		found = found || (v.FlowID == flow.ID && v.Hash == "h1" && v.RequestedByID == uid && string(v.Protected) != "[]")
	}
	if !found {
		t.Fatalf("pending %+v", pending)
	}
	d := FlowVersionDecision{Status: FlowVersionApproved, DecidedBy: "boss", DecidedAt: time.Now().UTC(), Note: "ok"}
	if err := p.DecideAutomationFlowVersion(ctx, cluster, flow.ID, 1, d); err != nil {
		t.Fatal(err)
	}
	if err := p.DecideAutomationFlowVersion(ctx, cluster, flow.ID, 1, d); err != ErrNotPending {
		t.Fatalf("second decision %v", err)
	}
	v, _ := p.GetAutomationFlowVersion(ctx, cluster, flow.ID, 1)
	if v.Status != FlowVersionApproved || v.DecidedBy != "boss" || v.DecidedAt == nil || v.DecisionNote != "ok" {
		t.Fatalf("version %+v", v)
	}
}
