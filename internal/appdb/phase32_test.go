package appdb

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryListMigrateJobsNewestFirst(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	older := MigrateJob{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: "wl", State: "succeeded",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	newer := MigrateJob{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: "wl", State: "failed",
		CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := m.CreateMigrateJob(t.Context(), older); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateMigrateJob(t.Context(), newer); err != nil {
		t.Fatal(err)
	}
	jobs, err := m.ListMigrateJobs(t.Context(), clusterID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].ID != newer.ID || jobs[1].ID != older.ID {
		t.Fatalf("GET must list newest migrate job first: %+v", jobs)
	}
	limited, err := m.ListMigrateJobs(t.Context(), clusterID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].ID != newer.ID {
		t.Fatalf("limit must keep the newest migrate job: %+v", limited)
	}
}
