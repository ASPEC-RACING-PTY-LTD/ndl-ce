package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestAuditRetentionPrunesWeekly(t *testing.T) {
	s, st, clusterID := auditRetentionServer(t)
	now := time.Now()
	s.Now = func() time.Time { return now }
	add := func(age time.Duration) {
		_ = st.InsertAudit(context.Background(), appdb.AuditEvent{ClusterID: clusterID, Action: "x", Result: "ok", CreatedAt: now.Add(-age)})
	}
	add(33 * 24 * time.Hour)
	add(time.Hour)
	s.pruneAudit(context.Background())
	if n := len(st.Audits()); n != 2 {
		t.Fatalf("pruned before a week past the month: %d left", n)
	}
	add(38 * 24 * time.Hour)
	s.pruneAudit(context.Background())
	if n := len(st.Audits()); n != 1 {
		t.Fatalf("want only the recent event kept, got %d", n)
	}
}

func auditRetentionServer(t *testing.T) (*Server, *appdb.Memory, string) {
	s, mem, _ := testServer(t)
	c, err := mem.GetCluster(context.Background())
	if err != nil || c == nil {
		t.Fatal(err)
	}
	return s, mem, c.ID
}
