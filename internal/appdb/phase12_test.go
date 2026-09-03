package appdb

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

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
