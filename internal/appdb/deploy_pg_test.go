package appdb

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Run with NODAL_TEST_PG pointing at a migrated test database.
func TestDeploymentsOnPostgres(t *testing.T) {
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
		if _, err := db.ExecContext(ctx, `INSERT INTO clusters (id, name) VALUES ($1, $2)`, cluster, "deploy-test"); err != nil {
			t.Fatal(err)
		}
	}
	wl := uuid.NewString()
	proj := DeployProject{ID: uuid.NewString(), ClusterID: cluster, WorkloadID: wl, Name: "shop", WorkingDir: "/srv/shop",
		Config: DeployProjectConfig{Commands: []DeployCommand{{Run: DefaultDeployCommand}}, Detect: true, Provider: "auto"}}
	if err := p.PutDeployProject(ctx, proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.DeleteDeployProject(ctx, cluster, proj.ID) })
	dup := proj
	dup.ID = uuid.NewString()
	if err := p.PutDeployProject(ctx, dup); err != ErrDeployProjectExists {
		t.Fatalf("duplicate folder: %v", err)
	}
	if err := p.UpdateDeployProjectState(ctx, cluster, proj.ID, DeployProjectState{Due: true, Behind: 2, RemoteCommit: "abc"}); err != nil {
		t.Fatal(err)
	}
	got, err := p.GetDeployProject(ctx, cluster, proj.ID)
	if err != nil || got == nil || !got.State.Due || got.State.Behind != 2 || got.Config.Commands[0].Run != DefaultDeployCommand {
		t.Fatalf("project %+v %v", got, err)
	}
	if err := p.PutDeployProjectSecrets(ctx, cluster, proj.ID, map[string]string{"TOKEN": "x"}, "hook"); err != nil {
		t.Fatal(err)
	}
	env, hook, err := p.DeployProjectSecrets(ctx, cluster, proj.ID)
	if err != nil || env["TOKEN"] != "x" || hook != "hook" {
		t.Fatalf("secrets %v %q %v", env, hook, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	code := 3
	d := Deployment{ID: uuid.NewString(), ClusterID: cluster, ProjectID: proj.ID, WorkloadID: wl, Status: DeployQueued, Trigger: "manual", Actor: "admin", QueuedAt: now,
		Detail: DeploymentDetail{ProjectName: "shop", Commands: proj.Config.Commands}}
	if err := p.PutDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	active, err := p.ListDeployments(ctx, cluster, DeploymentFilter{Active: true})
	if err != nil || len(active) != 1 {
		t.Fatalf("active %d %v", len(active), err)
	}
	d.Status, d.StartedAt, d.FinishedAt, d.Detail.ExitCode = DeployFailed, &now, &now, &code
	if err := p.PutDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	back, err := p.GetDeployment(ctx, cluster, d.ID)
	if err != nil || back.Status != DeployFailed || back.Detail.ExitCode == nil || *back.Detail.ExitCode != 3 || back.FinishedAt == nil {
		t.Fatalf("deployment %+v %v", back, err)
	}
	if active, _ = p.ListDeployments(ctx, cluster, DeploymentFilter{Active: true}); len(active) != 0 {
		t.Fatal("a finished deployment is still active")
	}
	if hist, _ := p.ListDeployments(ctx, cluster, DeploymentFilter{ProjectID: proj.ID, Limit: 10}); len(hist) != 1 {
		t.Fatalf("history %d", len(hist))
	}
	s := DefaultDeploySettings(cluster)
	s.MaxConcurrent = 5
	if err := p.PutDeploySettings(ctx, *s); err != nil {
		t.Fatal(err)
	}
	if got, err := p.GetDeploySettings(ctx, cluster); err != nil || got.MaxConcurrent != 5 || got.PerContainer != 1 {
		t.Fatalf("settings %+v %v", got, err)
	}
	_, _ = db.ExecContext(ctx, `DELETE FROM deploy_settings WHERE cluster_id=$1`, cluster)
	if err := p.DeleteDeployProject(ctx, cluster, proj.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := p.ListDeployments(ctx, cluster, DeploymentFilter{ProjectID: proj.ID}); len(left) != 0 {
		t.Fatal("deleting the project left its deployments")
	}
	if env, _, _ := p.DeployProjectSecrets(ctx, cluster, proj.ID); len(env) != 0 {
		t.Fatal("deleting the project left its secrets")
	}
}
