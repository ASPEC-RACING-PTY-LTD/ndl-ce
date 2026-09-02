package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/automation"
	"github.com/no-dal/ndl-ce/internal/migrate"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestPhase40StoragePressureCreatesVisibleOperation(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/policies", strings.NewReader(`{"name":"pressure","yaml":"kind: storage_pressure\nrun: bash\n"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "run") {
		t.Fatalf("host.exec shaped policy %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/policies", strings.NewReader(`{"name":"pressure","kind":"storage_pressure","action":"enqueue_migrate_low_priority","threshold_percent":85}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	policyID, _ := created["id"].(string)

	usable, alloc := int64(100<<30), int64(90<<30)
	pool := appdb.StoragePool{
		ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ClusterID: cluster.ID, NodeID: node.ID,
		Name: "full", BackendType: storage.BackendDirectory, Status: storage.StatusAvailable,
		UsableBytes: &usable, AllocatedBytes: &alloc,
	}
	if err := mem.CreateStoragePool(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	vol := appdb.Volume{
		ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", ClusterID: cluster.ID, NodeID: node.ID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Status: storage.StatusAvailable,
	}
	_ = mem.CreateVolume(context.Background(), vol)
	wl := appdb.Workload{
		ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", ClusterID: cluster.ID, NodeID: node.ID,
		Name: "batch", Kind: "vm", Status: "running",
	}
	_ = mem.CreateWorkload(context.Background(), wl)
	_ = mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{
		ID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", ClusterID: cluster.ID, WorkloadID: wl.ID, VolumeID: vol.ID,
	})
	_ = mem.UpsertWorkloadPlacement(context.Background(), appdb.WorkloadPlacement{
		WorkloadID: wl.ID, ClusterID: cluster.ID, Priority: 10,
	})

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/policies/"+policyID+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("apply %d %s", res.StatusCode, raw)
	}
	var run map[string]any
	if err := json.Unmarshal(raw, &run); err != nil {
		t.Fatal(err)
	}
	if run["status"] != appdb.PolicySucceeded || run["service_identity"] != automation.ActorName {
		t.Fatalf("%s", raw)
	}
	ids, _ := run["operation_ids"].([]any)
	if len(ids) == 0 {
		t.Fatalf("expected queued migrate operation %s", raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/tasks", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"kind":"workload.migrate"`) {
		t.Fatalf("tasks %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "dest agent is not connected") {
		t.Fatalf("queued migrate must stay honest %s", raw)
	}

	actor, _ := mem.GetUserByName(context.Background(), cluster.ID, "svc-"+automation.ActorName)
	if actor == nil || actor.Kind != appdb.UserKindService {
		t.Fatal("policies must run as service identity")
	}
	roles, _ := mem.UserRoles(context.Background(), actor.ID)
	if len(roles) != 1 || roles[0] != rbac.Automation {
		t.Fatalf("automation role %v", roles)
	}
	grants := rbac.New().PermissionsForRole(roles[0])
	if rbac.Authorize(grants, rbac.ComputeDelete) || rbac.Authorize(grants, rbac.IdentityTokenCreate) || rbac.Authorize(grants, rbac.FeatureManage) {
		t.Fatal("automation must not inherit Operator")
	}
}

func TestPhase40AutomationStripsOperatorBind(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	_ = seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	u := appdb.User{
		ID: uuid.NewString(), ClusterID: cluster.ID, Username: "svc-" + automation.ActorName,
		PasswordHash: "!", Kind: appdb.UserKindService,
	}
	if err := mem.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if err := mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Operator); err != nil {
		t.Fatal(err)
	}
	if err := mem.UnbindRole(context.Background(), cluster.ID, "missing-user", rbac.Operator); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/policies", strings.NewReader(`{"name":"pressure","kind":"storage_pressure","action":"enqueue_migrate_low_priority","threshold_percent":85}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	policyID, _ := created["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/policies/"+policyID+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("apply %d %s", res.StatusCode, raw)
	}

	roles, _ := mem.UserRoles(context.Background(), u.ID)
	if len(roles) != 1 || roles[0] != rbac.Automation {
		t.Fatalf("leftover operator bind %v", roles)
	}
}

func TestPhase40RequireApprovalNeedsConfirm(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/policies", strings.NewReader(`{"name":"gated","kind":"storage_pressure","require_approval":true,"threshold_percent":85}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	policyID, _ := created["id"].(string)

	usable, alloc := int64(100<<30), int64(90<<30)
	pool := appdb.StoragePool{
		ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab", ClusterID: cluster.ID, NodeID: node.ID,
		Name: "full", BackendType: storage.BackendDirectory, Status: storage.StatusAvailable,
		UsableBytes: &usable, AllocatedBytes: &alloc,
	}
	if err := mem.CreateStoragePool(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	vol := appdb.Volume{
		ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbc", ClusterID: cluster.ID, NodeID: node.ID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Status: storage.StatusAvailable,
	}
	_ = mem.CreateVolume(context.Background(), vol)
	wl := appdb.Workload{
		ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccd", ClusterID: cluster.ID, NodeID: node.ID,
		Name: "batch", Kind: "vm", Status: "running",
	}
	_ = mem.CreateWorkload(context.Background(), wl)
	_ = mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{
		ID: "dddddddd-dddd-4ddd-8ddd-ddddddddddde", ClusterID: cluster.ID, WorkloadID: wl.ID, VolumeID: vol.ID,
	})

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/policies/"+policyID+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted || !strings.Contains(string(raw), `"status":"pending"`) {
		t.Fatalf("pending %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/tasks", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(raw), `"kind":"workload.migrate"`) {
		t.Fatalf("migrate must not enqueue before confirm %s", raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/policies/"+policyID+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(confirmHeader, automation.ApplyConfirm)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"status":"succeeded"`) {
		t.Fatalf("confirm %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/tasks", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"kind":"workload.migrate"`) {
		t.Fatalf("tasks after confirm %d %s", res.StatusCode, raw)
	}
}

func TestPhase40ViewerCannotApply(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	admin := claimAdmin(t, ts, token)

	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	login, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var viewCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = login.Body.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/policies", strings.NewReader(`{"name":"p"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ := ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer create %d", res.StatusCode)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/policies", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("admin list %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/policies", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("viewer list %d", res.StatusCode)
	}
}

func TestPhase40AttemptedMigrateFailureIsNotSucceeded(t *testing.T) {
	s, mem, token := testServer(t)
	fake := migrate.NewFake()
	fake.FailLive = true
	s.Migrate = fake
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	worker := seedNode(t, mem, cluster.ID, debianInv(), false)
	destUsable, destAlloc := int64(100<<30), int64(10<<30)
	if err := mem.CreateStoragePool(context.Background(), appdb.StoragePool{
		ID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeea", ClusterID: cluster.ID, NodeID: worker.ID,
		Name: "dest", BackendType: storage.BackendDirectory, Status: storage.StatusAvailable,
		UsableBytes: &destUsable, AllocatedBytes: &destAlloc, RootPath: "/var/lib/ndl/storage/dest",
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/policies", strings.NewReader(`{"name":"pressure","kind":"storage_pressure","action":"enqueue_migrate_low_priority","threshold_percent":85}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	policyID, _ := created["id"].(string)

	usable, alloc := int64(100<<30), int64(90<<30)
	pool := appdb.StoragePool{
		ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaac", ClusterID: cluster.ID, NodeID: node.ID,
		Name: "full", BackendType: storage.BackendDirectory, Status: storage.StatusAvailable,
		UsableBytes: &usable, AllocatedBytes: &alloc, RootPath: "/var/lib/ndl/storage/local",
	}
	if err := mem.CreateStoragePool(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	vol := appdb.Volume{
		ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbd", ClusterID: cluster.ID, NodeID: node.ID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Status: storage.StatusAvailable,
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
	}
	_ = mem.CreateVolume(context.Background(), vol)
	wl := appdb.Workload{
		ID: "cccccccc-cccc-4ccc-8ccc-ccccccccccce", ClusterID: cluster.ID, NodeID: node.ID,
		OwnerNodeID: node.ID, DesiredNodeID: node.ID, Name: "batch", Kind: "vm", Status: "running",
	}
	_ = mem.CreateWorkload(context.Background(), wl)
	fake.SetSourceRunning(wl.ID, true)
	_ = mem.CreateWorkloadDisk(context.Background(), appdb.WorkloadDisk{
		ID: "dddddddd-dddd-4ddd-8ddd-dddddddddddf", ClusterID: cluster.ID, WorkloadID: wl.ID, VolumeID: vol.ID,
	})
	_ = mem.UpsertWorkloadPlacement(context.Background(), appdb.WorkloadPlacement{
		WorkloadID: wl.ID, ClusterID: cluster.ID, Priority: 10,
	})

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/policies/"+policyID+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("apply %d %s", res.StatusCode, raw)
	}
	var run map[string]any
	if err := json.Unmarshal(raw, &run); err != nil {
		t.Fatal(err)
	}
	if run["status"] != appdb.PolicyFailed {
		t.Fatalf("attempted migrate failure must not succeed %s", raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/tasks", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"state":"failed"`) {
		t.Fatalf("failed migrate must be visible %s", raw)
	}
}
