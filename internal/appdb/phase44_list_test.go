package appdb

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryListMigrationJobsNewestFirst(t *testing.T) {
	m := NewMemory()
	clusterID := uuid.NewString()
	older := MigrationJob{
		ID: uuid.NewString(), ClusterID: clusterID, Adapter: "disk", Direction: "import",
		State: "succeeded", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	newer := MigrationJob{
		ID: uuid.NewString(), ClusterID: clusterID, Adapter: "disk", Direction: "import",
		State: "failed", CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := m.CreateMigrationJob(t.Context(), older); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateMigrationJob(t.Context(), newer); err != nil {
		t.Fatal(err)
	}
	jobs, err := m.ListMigrationJobs(t.Context(), clusterID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].ID != newer.ID || jobs[1].ID != older.ID {
		t.Fatalf("GET must list newest migration job first: %+v", jobs)
	}
	limited, err := m.ListMigrationJobs(t.Context(), clusterID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].ID != newer.ID {
		t.Fatalf("limit must keep the newest migration job: %+v", limited)
	}
}
