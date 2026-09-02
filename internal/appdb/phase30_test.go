package appdb

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryJoinTokenReuseFails(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	if err := m.CreateCluster(t.Context(), Cluster{ID: clusterID, Name: "local"}); err != nil {
		t.Fatal(err)
	}
	tok := JoinToken{
		ID: uuid.NewString(), ClusterID: clusterID, TokenHash: "abc", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := m.CreateJoinToken(t.Context(), tok); err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.NewString()
	if _, err := m.ConsumeJoinToken(t.Context(), "abc", nodeID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConsumeJoinToken(t.Context(), "abc", uuid.NewString(), time.Now().UTC()); err != ErrJoinTokenUsed {
		t.Fatalf("reuse: %v", err)
	}
}

func TestMemorySecondLeaseFails(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	exp := time.Now().UTC().Add(time.Minute)
	if err := m.AcquireLease(t.Context(), clusterID, "writer-a", exp); err != nil {
		t.Fatal(err)
	}
	if err := m.AcquireLease(t.Context(), clusterID, "writer-b", exp); err != ErrLeaseHeld {
		t.Fatalf("second writer: %v", err)
	}
	if err := m.AcquireLease(t.Context(), clusterID, "writer-a", exp); err != nil {
		t.Fatal(err)
	}
	if err := m.FenceLease(t.Context(), clusterID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := m.AcquireLease(t.Context(), clusterID, "writer-a", exp); err != ErrLeaseHeld {
		t.Fatalf("fenced holder must not reclaim: %v", err)
	}
	if err := m.AcquireLease(t.Context(), clusterID, "writer-b", exp); err != nil {
		t.Fatalf("standby after fence: %v", err)
	}
	got, _ := m.GetClusterLease(t.Context(), clusterID)
	if got == nil || got.HolderID != "writer-b" || got.Fenced {
		t.Fatalf("lease %+v", got)
	}
}

func TestMemoryReleaseLeaseIsOwnershipSafe(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	exp := time.Now().UTC().Add(time.Minute)
	if err := m.AcquireLease(t.Context(), clusterID, "writer-a", exp); err != nil {
		t.Fatal(err)
	}
	if err := m.ReleaseLease(t.Context(), clusterID, "writer-b"); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetClusterLease(t.Context(), clusterID)
	if err != nil || got == nil || got.HolderID != "writer-a" {
		t.Fatalf("foreign release must not drop the live writer: %+v %v", got, err)
	}
	if err := m.AcquireLease(t.Context(), clusterID, "writer-b", exp); err != ErrLeaseHeld {
		t.Fatalf("second writer after foreign release: %v", err)
	}
	if err := m.ReleaseLease(t.Context(), clusterID, "writer-a"); err != nil {
		t.Fatal(err)
	}
	got, err = m.GetClusterLease(t.Context(), clusterID)
	if err != nil || got != nil {
		t.Fatalf("owner release must clear the lease: %+v %v", got, err)
	}
	if err := m.AcquireLease(t.Context(), clusterID, "writer-b", exp); err != nil {
		t.Fatalf("immediate acquire after owner release: %v", err)
	}
}

func TestMemoryCrashedWriterLeaseSurvivesUntilExpiry(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	exp := time.Now().UTC().Add(40 * time.Millisecond)
	if err := m.AcquireLease(t.Context(), clusterID, "writer-crash", exp); err != nil {
		t.Fatal(err)
	}
	if err := m.AcquireLease(t.Context(), clusterID, "writer-b", time.Now().UTC().Add(time.Minute)); err != ErrLeaseHeld {
		t.Fatalf("live crashed writer must still reject takeover: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := m.AcquireLease(t.Context(), clusterID, "writer-b", time.Now().UTC().Add(time.Minute)); err == nil {
			got, _ := m.GetClusterLease(t.Context(), clusterID)
			if got == nil || got.HolderID != "writer-b" {
				t.Fatalf("takeover after expiry: %+v", got)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expired crashed-writer lease must become acquirable")
}

func TestMemoryGetNodeStaysControlWithWorkers(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	control := Node{ID: uuid.NewString(), ClusterID: clusterID, Name: "local", Role: "control"}
	worker := Node{ID: uuid.NewString(), ClusterID: clusterID, Name: "box-b", Role: "worker", Hostname: "box-b"}
	_ = m.UpsertNode(t.Context(), control)
	_ = m.UpsertNode(t.Context(), worker)
	got, err := m.GetNode(t.Context(), clusterID)
	if err != nil || got == nil || got.ID != control.ID {
		t.Fatalf("control node: %+v %v", got, err)
	}
	list, _ := m.ListClusterNodes(t.Context(), clusterID)
	if len(list) != 2 {
		t.Fatalf("inventory %d", len(list))
	}
}
