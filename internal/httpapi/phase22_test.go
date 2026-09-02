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
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func phase22Ready(t *testing.T) (*Server, *appdb.Memory, *httptest.Server, string, string, string, *fakeOCI) {
	t.Helper()
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, _ := seedCompute(t, mem, cluster.ID, nodeID)
	fo := &fakeOCI{runtime: &oci.FakeRuntime{}}
	s.OCI = fo
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/stack-vol",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	return s, mem, ts, cookie, cluster.ID, poolID, fo
}

const composeFixture = `
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    environment:
      APP_ENV: prod
    volumes:
      - webdata:/usr/share/nginx/html
  api:
    image: busybox:1.36
    environment:
      - LOG_LEVEL=info
    volumes:
      - apidata:/data
volumes:
  webdata:
  apidata:
`

func TestPhase22ComposeImportCreatesMembers(t *testing.T) {
	_, _, ts, cookie, _, poolID, _ := phase22Ready(t)
	body, _ := json.Marshal(map[string]any{
		"name": "demo", "compose": composeFixture, "pool_id": poolID,
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/stacks/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("import %d %s", res.StatusCode, raw)
	}
	var stack map[string]any
	_ = json.Unmarshal(raw, &stack)
	if stack["status"] != appdb.StackStatusDraft {
		t.Fatalf("status %+v", stack["status"])
	}
	members, _ := stack["members"].([]any)
	if len(members) != 2 {
		t.Fatalf("members %s", raw)
	}
	for _, m := range members {
		mm := m.(map[string]any)
		if mm["workload_id"] != nil {
			t.Fatalf("import must not invent workloads: %s", raw)
		}
		desired, _ := mm["desired"].(map[string]any)
		if desired["image_pin"] == nil {
			t.Fatalf("desired missing: %s", raw)
		}
	}

	list, err := ts.Client().Do(func() *http.Request {
		r, _ := http.NewRequest("GET", ts.URL+"/api/v1/stacks", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		return r
	}())
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	lb, _ := io.ReadAll(list.Body)
	if list.StatusCode != http.StatusOK || !strings.Contains(string(lb), `"demo"`) {
		t.Fatalf("list %d %s", list.StatusCode, lb)
	}
}

func TestPhase22RejectPrivilegedUnlessAdmin(t *testing.T) {
	_, _, ts, cookie, _, poolID, _ := phase22Ready(t)
	priv := `
services:
  rootful:
    image: busybox:1
    privileged: true
`
	body, _ := json.Marshal(map[string]any{"name": "priv-admin", "compose": priv, "pool_id": poolID})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/stacks/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("admin privileged import %d %s", res.StatusCode, b)
	}

	hash, _ := auth.HashPassword("password1")
	s, mem, _ := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID2, _ := seedCompute(t, mem, cluster.ID, nodeID)
	opID := uuid.NewString()
	_ = mem.CreateUser(context.Background(), appdb.User{ID: opID, ClusterID: cluster.ID, Username: "op", PasswordHash: hash})
	_ = mem.BindRole(context.Background(), cluster.ID, opID, rbac.Operator)
	s.OCI = &fakeOCI{runtime: &oci.FakeRuntime{}}
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts2 := httptest.NewServer(s.Handler())
	t.Cleanup(ts2.Close)
	login, err := ts2.Client().Post(ts2.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"op","password":"password1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer login.Body.Close()
	var opCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			opCookie = c.Value
		}
	}
	body, _ = json.Marshal(map[string]any{"name": "priv-op", "compose": priv, "pool_id": poolID2})
	req, _ = http.NewRequest("POST", ts2.URL+"/api/v1/stacks/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: opCookie})
	res, err = ts2.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("operator privileged %d %s", res.StatusCode, b)
	}
}

func TestPhase22ApplyCreatesOCIAndResumesPartial(t *testing.T) {
	_, mem, ts, cookie, clusterID, poolID, fo := phase22Ready(t)
	body, _ := json.Marshal(map[string]any{
		"name": "app", "compose": composeFixture, "pool_id": poolID,
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/stacks/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("import %d %s", res.StatusCode, raw)
	}
	var stack map[string]any
	_ = json.Unmarshal(raw, &stack)
	stackID, _ := stack["id"].(string)

	// Fail the first CreateOCI call to leave a partial apply.
	fo.err = errUnavailable("oci agent is unavailable")
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/stacks/"+stackID+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ = io.ReadAll(res.Body)
	var partial map[string]any
	_ = json.Unmarshal(raw, &partial)
	if partial["status"] != appdb.StackStatusFailed && partial["status"] != appdb.StackStatusPartial {
		t.Fatalf("expected honest partial/failed, got %s", raw)
	}
	members, _ := partial["members"].([]any)
	for _, m := range members {
		mm := m.(map[string]any)
		if mm["status"] == "ready" || mm["status"] == "healthy" {
			t.Fatalf("must not fake healthy members: %s", raw)
		}
	}

	// Resume: clear error and re-apply. Existing failed members without workload_id are retried.
	fo.err = nil
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/stacks/"+stackID+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ = io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("resume %d %s", res.StatusCode, raw)
	}
	var applied map[string]any
	_ = json.Unmarshal(raw, &applied)
	if applied["status"] != appdb.StackStatusApplied {
		t.Fatalf("resume status %s", raw)
	}
	members, _ = applied["members"].([]any)
	if len(members) != 2 {
		t.Fatalf("members %s", raw)
	}
	for _, m := range members {
		mm := m.(map[string]any)
		wlID, _ := mm["workload_id"].(string)
		if wlID == "" {
			t.Fatalf("member missing workload: %s", raw)
		}
		wl, _ := mem.GetWorkload(context.Background(), clusterID, wlID)
		if wl == nil || wl.Kind != oci.KindOCI {
			t.Fatalf("member is not OCI workload: %+v", wl)
		}
		health, _ := mm["workload"].(map[string]any)
		if health == nil {
			t.Fatalf("workload link missing: %s", raw)
		}
		// Honest: not inventing all-healthy; collecting/not_configured/running ok.
		st, _ := mm["status"].(string)
		if st == "healthy" {
			t.Fatal("must never emit fake healthy")
		}
	}

	// Idempotent second apply keeps the same member workloads.
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/stacks/"+stackID+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw2, _ := io.ReadAll(res.Body)
	var again map[string]any
	_ = json.Unmarshal(raw2, &again)
	m1, _ := applied["members"].([]any)
	m2, _ := again["members"].([]any)
	if len(m1) != len(m2) {
		t.Fatal("member count changed")
	}
	for i := range m1 {
		a := m1[i].(map[string]any)["workload_id"]
		b := m2[i].(map[string]any)["workload_id"]
		if a != b {
			t.Fatalf("resume changed workload ids %v -> %v", a, b)
		}
	}
}

func TestPhase22RejectHostBindRootOnImport(t *testing.T) {
	_, _, ts, cookie, _, poolID, _ := phase22Ready(t)
	bad := `
services:
  bad:
    image: busybox:1
    volumes:
      - /:/host
`
	body, _ := json.Marshal(map[string]any{"name": "bad", "compose": bad, "pool_id": poolID})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/stacks/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusCreated {
		t.Fatal("must reject host bind /")
	}
}

func TestPhase22ImportRequiresStorageVolumeCreate(t *testing.T) {
	_, mem, ts, _, clusterID, poolID, _ := phase22Ready(t)
	admin, err := mem.GetUserByName(context.Background(), clusterID, "admin")
	if err != nil || admin == nil {
		t.Fatal("admin user")
	}
	plain := "ndl_compute_only_token"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: clusterID, UserID: admin.ID, Name: "compute-only",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_co",
		Permissions: []string{rbac.ComputeCreate, rbac.ComputeRead},
	})
	body, _ := json.Marshal(map[string]any{"name": "needs-vol", "compose": composeFixture, "pool_id": poolID})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/stacks/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("compute-only volume create %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "storage.volume.create") {
		t.Fatalf("expected storage.volume.create denial: %s", raw)
	}

	noVol := `
services:
  web:
    image: nginx:alpine
`
	body, _ = json.Marshal(map[string]any{"name": "no-vol", "compose": noVol})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/stacks/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ = io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("compute-only without volumes %d %s", res.StatusCode, raw)
	}
}

func TestPhase22ImportVolumeMapSkipsCreatePermission(t *testing.T) {
	_, mem, ts, _, clusterID, poolID, _ := phase22Ready(t)
	admin, err := mem.GetUserByName(context.Background(), clusterID, "admin")
	if err != nil || admin == nil {
		t.Fatal("admin user")
	}
	pool, err := mem.GetStoragePool(context.Background(), clusterID, poolID)
	if err != nil || pool == nil {
		t.Fatal("pool")
	}
	webVol := uuid.NewString()
	apiVol := uuid.NewString()
	for _, id := range []string{webVol, apiVol} {
		_ = mem.CreateVolume(context.Background(), appdb.Volume{
			ID: id, ClusterID: clusterID, NodeID: pool.NodeID, PoolID: poolID,
			Class: storage.ClassContainerRoot, Kind: storage.KindFilesystem, Format: storage.FormatDirectory,
			Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/" + id,
		})
	}
	plain := "ndl_compute_map_token"
	_ = mem.CreateToken(context.Background(), appdb.APIToken{
		ID: uuid.NewString(), ClusterID: clusterID, UserID: admin.ID, Name: "compute-map",
		TokenHash: secutil.HashSHA256(plain), Prefix: "ndl_cm",
		Permissions: []string{rbac.ComputeCreate, rbac.ComputeRead, rbac.StorageRead},
	})
	body, _ := json.Marshal(map[string]any{
		"name": "mapped", "compose": composeFixture, "pool_id": poolID,
		"volume_map": map[string]string{"webdata": webVol, "apidata": apiVol},
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/stacks/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plain)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("mapped import %d %s", res.StatusCode, raw)
	}
}

func TestPhase22PatchMemberDesiredBeforeApply(t *testing.T) {
	_, mem, ts, cookie, clusterID, poolID, _ := phase22Ready(t)
	body, _ := json.Marshal(map[string]any{"name": "editme", "compose": composeFixture, "pool_id": poolID})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/stacks/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("import %d %s", res.StatusCode, raw)
	}
	var stack map[string]any
	_ = json.Unmarshal(raw, &stack)
	stackID, _ := stack["id"].(string)
	members, _ := stack["members"].([]any)
	first := members[0].(map[string]any)
	memberID, _ := first["id"].(string)

	patch, _ := json.Marshal(map[string]any{
		"image_pin": "nginx:1.27-alpine",
		"env":       []map[string]string{{"name": "APP_ENV", "value": "staging"}},
	})
	req, _ = http.NewRequest("PATCH", ts.URL+"/api/v1/stacks/"+stackID+"/members/"+memberID, strings.NewReader(string(patch)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ = io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch %d %s", res.StatusCode, raw)
	}
	var updated map[string]any
	_ = json.Unmarshal(raw, &updated)
	found := false
	for _, m := range updated["members"].([]any) {
		mm := m.(map[string]any)
		if mm["id"] != memberID {
			continue
		}
		desired, _ := mm["desired"].(map[string]any)
		if desired["image_pin"] != "nginx:1.27-alpine" {
			t.Fatalf("image not edited: %s", raw)
		}
		found = true
	}
	if !found {
		t.Fatalf("member missing after patch: %s", raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/stacks/"+stackID+"/apply", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ = io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("apply %d %s", res.StatusCode, raw)
	}
	_ = json.Unmarshal(raw, &updated)
	for _, m := range updated["members"].([]any) {
		mm := m.(map[string]any)
		if mm["id"] != memberID {
			continue
		}
		wlID, _ := mm["workload_id"].(string)
		wl, _ := mem.GetWorkload(context.Background(), clusterID, wlID)
		if wl == nil || wl.ImagePin != "nginx:1.27-alpine" {
			t.Fatalf("apply ignored edited member: %+v", wl)
		}
	}
}

func TestPhase22PatchMemberPrivilegedRequiresAdmin(t *testing.T) {
	_, mem, ts, cookie, clusterID, poolID, _ := phase22Ready(t)
	simple := `
services:
  web:
    image: busybox:1
`
	body, _ := json.Marshal(map[string]any{"name": "priv-edit", "compose": simple, "pool_id": poolID})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/stacks/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("import %d %s", res.StatusCode, raw)
	}
	var stack map[string]any
	_ = json.Unmarshal(raw, &stack)
	stackID, _ := stack["id"].(string)
	memberID, _ := stack["members"].([]any)[0].(map[string]any)["id"].(string)

	hash, _ := auth.HashPassword("password1")
	opID := uuid.NewString()
	_ = mem.CreateUser(context.Background(), appdb.User{ID: opID, ClusterID: clusterID, Username: "op-edit", PasswordHash: hash})
	_ = mem.BindRole(context.Background(), clusterID, opID, rbac.Operator)
	login, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"op-edit","password":"password1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer login.Body.Close()
	var opCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			opCookie = c.Value
		}
	}
	patch, _ := json.Marshal(map[string]any{"privileged": true})
	req, _ = http.NewRequest("PATCH", ts.URL+"/api/v1/stacks/"+stackID+"/members/"+memberID, strings.NewReader(string(patch)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: opCookie})
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("operator privileged patch %d %s", res.StatusCode, b)
	}
}
