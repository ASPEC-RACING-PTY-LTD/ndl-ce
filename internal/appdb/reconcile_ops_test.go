package appdb

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReconcileOperationClosesFinishedMigrationJob(t *testing.T) {
	opID := uuid.NewString()
	jobID := uuid.NewString()
	now := time.Now().UTC()
	op := Operation{ID: opID, Kind: "migration.import", State: OpStateRunning, Stage: "preflight", UpdatedAt: now.Add(-2 * time.Hour)}
	job := MigrationJob{ID: jobID, OperationID: opID, State: OpStateFailed, Stage: "backup", StatusJSON: json.RawMessage(`{"message":"vzdump not readable"}`)}
	dec := ReconcileOperation(op, OpFacts{Jobs: []MigrationJob{job}, Now: now})
	if !dec.Changed || dec.Operation.State != OpStateFailed {
		t.Fatalf("%+v", dec)
	}
	if dec.Operation.Message != "vzdump not readable" {
		t.Fatalf("message=%q", dec.Operation.Message)
	}
}

func TestReconcileOperationSucceedsWhenPoolExists(t *testing.T) {
	now := time.Now().UTC()
	op := Operation{ID: uuid.NewString(), Kind: "pool.create", State: OpStateRunning, Stage: "validating", UpdatedAt: now.Add(-3 * time.Hour)}
	dec := ReconcileOperation(op, OpFacts{Pools: []StoragePool{{ID: uuid.NewString()}}, Now: now})
	if !dec.Changed || dec.Operation.State != OpStateSucceeded {
		t.Fatalf("%+v", dec)
	}
}

func TestReconcileOperationSucceedsWhenNetworkApplyExists(t *testing.T) {
	now := time.Now().UTC()
	op := Operation{ID: uuid.NewString(), Kind: "network.apply", State: OpStateRunning, Stage: "applying", UpdatedAt: now.Add(-30 * time.Second)}
	dec := ReconcileOperation(op, OpFacts{Networks: []Network{{ID: uuid.NewString()}}, Now: now})
	if !dec.Changed || dec.Operation.State != OpStateSucceeded || dec.Operation.Message != "network applied" {
		t.Fatalf("%+v", dec)
	}
	if dec.Operation.Progress == nil || *dec.Operation.Progress != 100 {
		t.Fatalf("progress %+v", dec.Operation.Progress)
	}
}

func TestReconcileOperationAbandonsPreStartRunning(t *testing.T) {
	now := time.Now().UTC()
	started := now.Add(-time.Minute)
	op := Operation{ID: uuid.NewString(), Kind: "pool.create", State: OpStateRunning, Stage: "validating", UpdatedAt: now.Add(-2 * time.Minute)}
	dec := ReconcileOperation(op, OpFacts{Now: now, StartedAt: started})
	if !dec.Changed || dec.Operation.State != OpStateFailed {
		t.Fatalf("crash leftover must close: %+v", dec)
	}
}

func TestReconcileOperationSucceedsWhenNetworkExists(t *testing.T) {
	now := time.Now().UTC()
	op := Operation{ID: uuid.NewString(), Kind: "network.create", State: OpStateRunning, Stage: "applying", UpdatedAt: now.Add(-3 * time.Hour)}
	dec := ReconcileOperation(op, OpFacts{Networks: []Network{{ID: uuid.NewString()}}, Now: now})
	if !dec.Changed || dec.Operation.State != OpStateSucceeded {
		t.Fatalf("%+v", dec)
	}
}

func TestReconcileOperationSucceedsDeleteWhenCatalogEmpty(t *testing.T) {
	now := time.Now().UTC()
	op := Operation{ID: uuid.NewString(), Kind: "workload.delete", State: OpStateRunning, Stage: "delete", UpdatedAt: now.Add(-3 * time.Hour)}
	dec := ReconcileOperation(op, OpFacts{Now: now})
	if !dec.Changed || dec.Operation.State != OpStateSucceeded {
		t.Fatalf("%+v", dec)
	}
}

func TestReconcileOperationLeavesFreshRunningMigration(t *testing.T) {
	opID := uuid.NewString()
	now := time.Now().UTC()
	op := Operation{ID: opID, Kind: "migration.import", State: OpStateRunning, Stage: "transfer", UpdatedAt: now.Add(-30 * time.Second)}
	job := MigrationJob{ID: uuid.NewString(), OperationID: opID, State: OpStateRunning, Stage: "transfer", UpdatedAt: now.Add(-10 * time.Second)}
	dec := ReconcileOperation(op, OpFacts{Jobs: []MigrationJob{job}, Now: now})
	if dec.Changed && dec.Operation.State != OpStateRunning {
		t.Fatalf("must not fail a live job: %+v", dec)
	}
}

func TestReconcileOperationAbandonsStaleUnknownKind(t *testing.T) {
	now := time.Now().UTC()
	op := Operation{ID: uuid.NewString(), Kind: "lab.other", State: OpStateRunning, UpdatedAt: now.Add(-3 * time.Hour)}
	dec := ReconcileOperation(op, OpFacts{Now: now})
	if !dec.Changed || dec.Operation.State != OpStateFailed {
		t.Fatalf("%+v", dec)
	}
}

func TestReconcileOperationIsIdempotentOnTerminal(t *testing.T) {
	op := Operation{ID: uuid.NewString(), Kind: "pool.create", State: OpStateSucceeded, Stage: "done"}
	dec := ReconcileOperation(op, OpFacts{Pools: []StoragePool{{ID: "x"}}, Now: time.Now().UTC()})
	if dec.Changed {
		t.Fatal("terminal ops must not change")
	}
}

func TestStaleRunningOpsThreshold(t *testing.T) {
	now := time.Now().UTC()
	ops := []Operation{
		{ID: "a", State: OpStateRunning, UpdatedAt: now.Add(-15 * time.Minute)},
		{ID: "b", State: OpStateRunning, UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "c", State: OpStateSucceeded, UpdatedAt: now.Add(-time.Hour)},
	}
	got := StaleRunningOps(ops, now, 10*time.Minute)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("%+v", got)
	}
}
