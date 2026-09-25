package appdb

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUpdateOperationCandidatesAreCopied(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	candidate := UpdateCandidate{Name: "nodal", CurrentVersion: "1.0.1", CandidateVersion: "1.0.2"}
	op := UpdateOperation{
		ID: "check-1", ClusterID: "cluster-1", Action: "check", Status: UpdateSucceeded,
		Version: "1.0.1", Candidates: []UpdateCandidate{candidate}, StartedAt: time.Now().UTC(),
	}
	if err := m.CreateUpdateOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	op.Candidates[0].CandidateVersion = "mutated"
	got, err := m.GetLatestCheckUpdateOperation(ctx, "cluster-1")
	if err != nil || got == nil || got.Candidates[0].CandidateVersion != "1.0.2" {
		t.Fatalf("stored candidate changed through caller slice: %+v %v", got, err)
	}
	got.Candidates[0].CandidateVersion = "mutated result"
	again, err := m.GetLatestCheckUpdateOperation(ctx, "cluster-1")
	if err != nil || again == nil || again.Candidates[0].CandidateVersion != "1.0.2" {
		t.Fatalf("stored candidate changed through result slice: %+v %v", again, err)
	}
}

func TestMemoryGetLatestCheckUpdateOperationIgnoresNewerOtherActions(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	want := UpdateOperation{
		ID: uuid.NewString(), ClusterID: clusterID, Action: "check", Status: UpdateSucceeded,
		Version: "0.1.10", StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := m.CreateUpdateOperation(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	emptyCheck := UpdateOperation{
		ID: uuid.NewString(), ClusterID: clusterID, Action: "check", Status: UpdateSucceeded,
		StartedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := m.CreateUpdateOperation(t.Context(), emptyCheck); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 21; i++ {
		other := UpdateOperation{
			ID: uuid.NewString(), ClusterID: clusterID, Action: "apply", Status: UpdateSucceeded,
			StartedAt: time.Date(2026, 6, 1, 0, 0, i, 0, time.UTC),
		}
		if err := m.CreateUpdateOperation(t.Context(), other); err != nil {
			t.Fatal(err)
		}
	}
	window, err := m.ListUpdateOperations(t.Context(), clusterID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range window {
		if op.ID == want.ID {
			t.Fatalf("20-row cluster window still contains the check: %+v", window)
		}
	}
	got, err := m.GetLatestCheckUpdateOperation(t.Context(), clusterID)
	if err != nil || got == nil || got.ID != want.ID || got.Version != want.Version {
		t.Fatalf("rollback must still find the recorded check version: %+v %v", got, err)
	}
}

func TestUpdateUpdateOperationFailsWhenMissing(t *testing.T) {
	m := NewMemory()
	err := m.UpdateUpdateOperation(t.Context(), UpdateOperation{
		ID: uuid.NewString(), ClusterID: uuid.NewString(), Status: UpdateSucceeded,
	})
	if err == nil || err.Error() != "update operation not found" {
		t.Fatalf("missing update operation: %v", err)
	}
}
