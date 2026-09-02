package ai

import "testing"

func TestCompilePlanInstallDatabase(t *testing.T) {
	steps, err := CompilePlan("install a database on node-02", "node-uuid", "node-02", "")
	if err != nil || len(steps) != 1 {
		t.Fatalf("%+v %v", steps, err)
	}
	if steps[0].Action != ActionCreateWorkload || steps[0].Path != "/api/v1/workloads" || steps[0].Body["node_id"] != "node-uuid" {
		t.Fatalf("%+v", steps[0])
	}
	if steps[0].Permission != "compute.create" {
		t.Fatal("must use existing compute.create")
	}
}

func TestCompilePlanRejectsExec(t *testing.T) {
	if _, err := CompilePlan("run host.exec on the node", "", "", ""); err == nil {
		t.Fatal("host.exec")
	}
	if _, err := CompilePlan("open a shell", "", "", ""); err == nil {
		t.Fatal("shell")
	}
}

func TestCompilePlanAutomateStorage(t *testing.T) {
	steps, err := CompilePlan("If this storage pool exceeds 85%, move eligible low-priority workloads", "", "", "")
	if err != nil || len(steps) != 1 || steps[0].Action != ActionCreatePolicy {
		t.Fatalf("%+v %v", steps, err)
	}
}

func TestCompilePlanInstallDatabaseKeepsWorkloads(t *testing.T) {
	steps, err := CompilePlan("install a database", "node-uuid", "node-02", "store-pkg")
	if err != nil || len(steps) != 1 {
		t.Fatalf("%+v %v", steps, err)
	}
	if steps[0].Action != ActionCreateWorkload || steps[0].Path != "/api/v1/workloads" {
		t.Fatalf("database must stay compute API %+v", steps[0])
	}
	if !PlanMutates(steps) {
		t.Fatal("create workload mutates")
	}
}

func TestCompilePlanStoreAppID(t *testing.T) {
	steps, err := CompilePlan("install the sample-web app", "node-uuid", "node-02", "pkg-1")
	if err != nil || len(steps) != 1 {
		t.Fatalf("%+v %v", steps, err)
	}
	if steps[0].Action != ActionInstallStore || steps[0].Path != "/api/v1/store/apps/pkg-1/install" {
		t.Fatalf("%+v", steps[0])
	}
	if steps[0].Body["store_app_id"] != "pkg-1" {
		t.Fatalf("store_app_id %+v", steps[0].Body)
	}
	if steps[0].Permission != "store.install" {
		t.Fatal("must use existing store.install")
	}
}
