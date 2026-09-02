package appdb

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIOSessionMemoryRoundTrip(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	userID := uuid.NewString()
	_ = m.CreateCluster(context.Background(), Cluster{ID: clusterID, Name: "c"})
	now := time.Now().UTC()
	row := IOSession{
		ID: uuid.NewString(), ClusterID: clusterID, UserID: userID,
		TargetKind: IOTargetSystemContainer, TargetID: uuid.NewString(),
		Kind: IOKindTerminal, CWD: "/root", TicketHash: "abc123",
		State: IOStatePending, ExpiresAt: now.Add(2 * time.Minute),
	}
	if err := m.CreateIOSession(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetIOSession(context.Background(), clusterID, row.ID)
	if err != nil || got == nil || got.TicketHash != "abc123" {
		t.Fatalf("%+v %v", got, err)
	}
	byHash, err := m.GetIOSessionByTicketHash(context.Background(), "abc123")
	if err != nil || byHash == nil || byHash.ID != row.ID {
		t.Fatalf("by hash %+v %v", byHash, err)
	}
	connected := now
	got.State = IOStateConnected
	got.ConnectedAt = &connected
	if err := m.UpdateIOSession(context.Background(), *got); err != nil {
		t.Fatal(err)
	}
	again, _ := m.GetIOSession(context.Background(), clusterID, row.ID)
	if again.State != IOStateConnected || again.ConnectedAt == nil {
		t.Fatalf("%+v", again)
	}
}
