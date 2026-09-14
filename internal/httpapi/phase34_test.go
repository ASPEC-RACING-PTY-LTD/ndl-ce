package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/migrate"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func TestPhase34StandbyTakesLeaseAfterFence(t *testing.T) {
	s, mem, token := testServer(t)
	s.LeaseHolder = "writer-a"
	cluster, _ := mem.GetCluster(context.Background())
	control := seedNode(t, mem, cluster.ID, debianInv(), false)
	if err := mem.AcquireLease(context.Background(), cluster.ID, "writer-a", time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{obs: qemu.Observed{Status: qemu.StatusRunning, UnitActive: true}}
	s.VM = vm
	wlID := uuid.NewString()
	if err := mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: wlID, ClusterID: cluster.ID, NodeID: control.ID, OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "keep", Kind: "vm", Status: qemu.StatusRunning,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/ha/promote", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "promote")
	s.LeaseHolder = "writer-b"
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("live writer %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/ha/fence", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("fence confirm %d", res.StatusCode)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/ha/fence", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "fence")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("fence %d %s", res.StatusCode, raw)
	}
	ha, err := mem.GetHAState(context.Background(), cluster.ID)
	if err != nil || ha == nil || ha.FencedHolder != "writer-a" {
		t.Fatalf("fence identity %+v %v", ha, err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/ha/promote", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "promote")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("promote %d %s", res.StatusCode, raw)
	}
	lease, _ := mem.GetClusterLease(context.Background(), cluster.ID)
	if lease == nil || lease.HolderID != "writer-b" {
		t.Fatalf("standby lease %+v", lease)
	}
	ha, err = mem.GetHAState(context.Background(), cluster.ID)
	if err != nil || ha == nil || ha.PromotedHolder != "writer-b" {
		t.Fatalf("promote identity %+v %v", ha, err)
	}
	got, _ := mem.GetWorkload(context.Background(), cluster.ID, wlID)
	if got == nil || got.Status != qemu.StatusRunning {
		t.Fatalf("guest must keep running %+v", got)
	}
	for _, a := range vm.actions {
		if a == "stop" || a == "force-stop" {
			t.Fatalf("CP move must not stop guests: %v", vm.actions)
		}
	}
}

func TestPhase34RollingUpdateDoesNotStopGuests(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	cluster, _ := mem.GetCluster(context.Background())
	control := seedNode(t, mem, cluster.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{obs: qemu.Observed{Status: qemu.StatusRunning, UnitActive: true}}
	s.VM = vm
	wlID := uuid.NewString()
	if err := mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: wlID, ClusterID: cluster.ID, NodeID: control.ID, OwnerNodeID: control.ID, DesiredNodeID: control.ID,
		Name: "keep", Kind: "vm", Status: qemu.StatusRunning,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/update", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("confirm %d", res.StatusCode)
	}
	_ = res.Body.Close()

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/update", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "cluster-update")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("rolling %d %s", res.StatusCode, raw)
	}
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if plan["status"] != "unavailable" {
		t.Fatalf("worker update must stay unavailable %+v", plan)
	}
	if !strings.Contains(string(raw), "worker update agent is not connected") {
		t.Fatalf("honesty %s", raw)
	}
	fu := s.Update.(*fakeUpdate)
	for _, c := range fu.calls {
		if c.Action == "apply" {
			goto applied
		}
	}
	t.Fatal("control node must apply Phase 12 update")
applied:
	got, _ := mem.GetWorkload(context.Background(), cluster.ID, wlID)
	if got == nil || got.Status != qemu.StatusRunning {
		t.Fatalf("rolling must not stop guests %+v", got)
	}
	for _, a := range vm.actions {
		if a == "stop" || a == "force-stop" {
			t.Fatalf("rolling must not stop guests: %v", vm.actions)
		}
	}
	maint, _ := mem.GetNodeMaintenance(context.Background(), cluster.ID, worker.ID)
	if maint == nil {
		t.Fatal("worker must be drained")
	}
	gotPlan, err := mem.LatestRollingPlan(context.Background(), cluster.ID)
	if err != nil || gotPlan == nil {
		t.Fatalf("rolling plan %+v %v", gotPlan, err)
	}
	steps, err := mem.ListRollingSteps(context.Background(), cluster.ID, gotPlan.ID)
	if err != nil || len(steps) == 0 {
		t.Fatalf("rolling steps %+v %v", steps, err)
	}
}

func TestPhase34RollingDrainMigratesLocalDestWithoutStopping(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	s.Migrate = migrate.NewFake()
	cluster, _ := mem.GetCluster(context.Background())
	control := seedNode(t, mem, cluster.ID, debianInv(), false)
	worker := appdb.Node{ID: uuid.NewString(), ClusterID: cluster.ID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{obs: qemu.Observed{Status: qemu.StatusRunning, UnitActive: true}}
	s.VM = vm
	wlID := uuid.NewString()
	s.Migrate.(*migrate.Fake).SetSourceRunning(wlID, true)
	if err := mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: wlID, ClusterID: cluster.ID, NodeID: worker.ID, OwnerNodeID: worker.ID, DesiredNodeID: worker.ID,
		Name: "move-local", Kind: "vm", Status: qemu.StatusRunning,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/update", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "cluster-update")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("rolling %d %s", res.StatusCode, raw)
	}
	got, _ := mem.GetWorkload(context.Background(), cluster.ID, wlID)
	if got == nil || got.NodeID != control.ID {
		t.Fatalf("rolling drain must migrate to local dest %+v", got)
	}
	if got.Status != qemu.StatusRunning {
		t.Fatalf("rolling must not stop guests %+v", got)
	}
	for _, a := range vm.actions {
		if a == "stop" || a == "force-stop" {
			t.Fatalf("rolling must not stop guests: %v", vm.actions)
		}
	}
}

func TestPhase34ReplicaDSNNeverReturned(t *testing.T) {
	s, mem, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"endpoint":"postgres-replica:5432","dsn":"postgresql://ndl:secret-pass@postgres-replica/nodal"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/ha/replica", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("replica %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "secret-pass") || strings.Contains(string(raw), `"dsn"`) || strings.Contains(string(raw), "postgresql://") {
		t.Fatalf("dsn leaked: %s", raw)
	}
	var ha map[string]any
	_ = json.Unmarshal(raw, &ha)
	if ha["replica_status"] != "unavailable" || ha["multi_master"] != false {
		t.Fatalf("%+v", ha)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/cluster/ha", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	listed, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(listed), "secret-pass") {
		t.Fatal("list leaked dsn")
	}

	cluster, _ := mem.GetCluster(context.Background())
	hash, _ := auth.HashPassword("password1")
	op := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "op34", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), op)
	_ = mem.BindRole(context.Background(), cluster.ID, op.ID, rbac.Operator)
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"op34","password":"password1"}`))
	var opCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			opCookie = c.Value
		}
	}
	_ = login.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/ha/promote", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "promote")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: opCookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("operator promote %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestPhase34ReplicaEndpointRefusesCredentials(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"endpoint":"postgresql://ndl:secret-pass@postgres-replica/nodal"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/ha/replica", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "secret-pass") {
		t.Fatalf("secret echoed %s", raw)
	}
}

func TestPhase34ReplicaEndpointRefusesDSN(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"endpoint":"postgresql://postgres-replica/nodal"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/ha/replica", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "replica endpoint must be host:port without credentials") {
		t.Fatalf("dsn endpoint %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/cluster/ha", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	listed, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(listed), "postgresql://") || strings.Contains(string(listed), "postgres-replica/nodal") {
		t.Fatalf("GET /cluster/ha must not list a DSN replica_endpoint: %s", listed)
	}
}

type failUpsertHAStateStore struct {
	appdb.Store
}

func (f failUpsertHAStateStore) UpsertHAState(context.Context, appdb.HAState) error {
	return errors.New("persist failed")
}

func TestPhase34FenceFailsClosedWhenHAStatePersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.LeaseHolder = "writer-a"
	cluster, _ := mem.GetCluster(context.Background())
	if err := mem.AcquireLease(context.Background(), cluster.ID, "writer-a", time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	s.Store = failUpsertHAStateStore{Store: mem}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/ha/fence", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "fence")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("fence persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record HA fence") {
		t.Fatalf("fence persist body %s", raw)
	}
}

func TestPhase34PromoteFailsClosedWhenHAStatePersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.LeaseHolder = "writer-b"
	s.Store = failUpsertHAStateStore{Store: mem}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/ha/promote", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "promote")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("promote persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record HA promote") {
		t.Fatalf("promote persist body %s", raw)
	}
}

type failSetNodeMaintenanceStore struct {
	appdb.Store
}

func (f failSetNodeMaintenanceStore) SetNodeMaintenance(context.Context, appdb.NodeMaintenance) error {
	return errors.New("persist failed")
}

func TestPhase34RollingDrainFailsClosedWhenMaintenancePersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	s.Store = failSetNodeMaintenanceStore{Store: mem}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/update", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "cluster-update")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("rolling drain persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record node maintenance") {
		t.Fatalf("rolling drain persist body %s", raw)
	}
	maint, err := mem.GetNodeMaintenance(context.Background(), cluster.ID, node.ID)
	if err != nil || maint != nil {
		t.Fatalf("maintenance GET %+v %v", maint, err)
	}
	gotPlan, err := mem.LatestRollingPlan(context.Background(), cluster.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotPlan == nil {
		return
	}
	steps, err := mem.ListRollingSteps(context.Background(), cluster.ID, gotPlan.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		if st.Action == appdb.RollingActionDrain && st.Status == appdb.RollingSucceeded && st.Reason == rollingDrainReason {
			t.Fatalf("drain step persisted after maintenance miss %+v", st)
		}
	}
}

func TestPhase34RollingDrainFailsClosedWhenMaintenancePersistMisses(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	cluster, _ := mem.GetCluster(context.Background())
	node := seedNode(t, mem, cluster.ID, debianInv(), false)
	s.Store = missSetNodeMaintenanceStore{Store: mem}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/update", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "cluster-update")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("rolling drain persist miss %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record node maintenance") {
		t.Fatalf("rolling drain persist miss body %s", raw)
	}
	s.Store = mem
	maint, err := mem.GetNodeMaintenance(context.Background(), cluster.ID, node.ID)
	if err != nil || maint != nil {
		t.Fatalf("maintenance GET %+v %v", maint, err)
	}
	gotPlan, err := mem.LatestRollingPlan(context.Background(), cluster.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotPlan == nil {
		return
	}
	steps, err := mem.ListRollingSteps(context.Background(), cluster.ID, gotPlan.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		if st.Action == appdb.RollingActionDrain && st.Status == appdb.RollingSucceeded && st.Reason == rollingDrainReason {
			t.Fatalf("drain step persisted after maintenance persist miss %+v", st)
		}
	}
}

type failCreateRollingStepStore struct {
	appdb.Store
}

func (f failCreateRollingStepStore) CreateRollingStep(context.Context, appdb.RollingStep) error {
	return errors.New("persist failed")
}

func TestPhase34RollingUpdateFailsClosedWhenStepPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	s.Store = failCreateRollingStepStore{Store: mem}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/update", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "cluster-update")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("rolling persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record rolling step") {
		t.Fatalf("rolling persist body %s", raw)
	}
}

type failUpdateRollingPlanStore struct {
	appdb.Store
}

func (f failUpdateRollingPlanStore) UpdateRollingPlan(context.Context, appdb.RollingPlan) error {
	return errors.New("persist failed")
}

func TestPhase34RollingUpdateFailsClosedWhenPlanPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	s.Update = &fakeUpdate{supported: true}
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	s.Store = failUpdateRollingPlanStore{Store: mem}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/update", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "cluster-update")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("rolling plan persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record rolling plan") {
		t.Fatalf("rolling plan persist body %s", raw)
	}
	got, _ := mem.LatestRollingPlan(context.Background(), cluster.ID)
	if got != nil && got.Status == appdb.RollingSucceeded {
		t.Fatal("failed plan persist must not invent succeeded")
	}
}
