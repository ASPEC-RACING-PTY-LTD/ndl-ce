package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/oci"
)

func TestPhase36OfficialSampleInstallsFromManifest(t *testing.T) {
	s, mem, ts, cookie, clusterID, _, _ := phase22Ready(t)
	_ = s
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/store/apps", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list %d %s", res.StatusCode, raw)
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) < 1 {
		t.Fatalf("official sample missing %s", raw)
	}
	id, _ := listed.Items[0]["id"].(string)
	if listed.Items[0]["name"] != "sample-web" || listed.Items[0]["class"] != "official" {
		t.Fatalf("%s", raw)
	}

	body, _ := json.Marshal(map[string]any{"name": "sample-web"})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+id+"/install", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("install %d %s", res.StatusCode, raw)
	}
	var inst map[string]any
	if err := json.Unmarshal(raw, &inst); err != nil {
		t.Fatal(err)
	}
	if inst["status"] != appdb.StoreInstallOK || inst["workload_id"] == "" || inst["stack_id"] == "" {
		t.Fatalf("%s", raw)
	}
	got, err := mem.GetStoreInstallation(context.Background(), clusterID, inst["id"].(string))
	if err != nil || got == nil || got.Status != appdb.StoreInstallOK || got.WorkloadID == "" {
		t.Fatalf("install row %+v %v", got, err)
	}
	wls, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(wls) != 1 || wls[0].Kind != oci.KindOCI {
		t.Fatalf("workloads %+v", wls)
	}
}

func TestPhase36RejectRunBashAndRollbackFailedInstall(t *testing.T) {
	s, mem, ts, cookie, clusterID, _, fo := phase22Ready(t)
	evil := `
apiVersion: nodal.store/v1
name: evil
version: "1"
class: community
deployment:
  kind: oci
  image: docker.io/library/caddy:2.8.4
run: bash
`
	body, _ := json.Marshal(map[string]any{"manifest": evil})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/store/apps/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "run") {
		t.Fatalf("bash %d %s", res.StatusCode, raw)
	}

	ok := `
apiVersion: nodal.store/v1
name: flaky
version: "1"
class: community
title: Flaky
deployment:
  kind: oci
  image: docker.io/library/caddy:2.8.4
`
	body, _ = json.Marshal(map[string]any{"manifest": ok})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("import %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "Unsigned Community") {
		t.Fatalf("missing unsigned warning %s", raw)
	}
	var pkg map[string]any
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatal(err)
	}
	id, _ := pkg["id"].(string)
	fo.err = errors.New("pull failed")
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+id+"/install", strings.NewReader(`{"name":"flaky"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusCreated {
		t.Fatalf("failed install succeeded %s", raw)
	}
	wls, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(wls) != 0 {
		t.Fatalf("rollback left workloads %+v", wls)
	}
	stacks, _ := mem.ListStacks(context.Background(), clusterID)
	if len(stacks) != 0 {
		t.Fatalf("rollback left stacks %+v", stacks)
	}
	_ = s
}

func TestPhase36InstallHonorsNodeIDAndFailsRemote(t *testing.T) {
	_, mem, ts, cookie, clusterID, _, _ := phase22Ready(t)
	control, err := mem.GetNode(context.Background(), clusterID)
	if err != nil || control == nil {
		t.Fatal("control node missing")
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/store/apps", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list %d %s", res.StatusCode, raw)
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil || len(listed.Items) < 1 {
		t.Fatalf("official sample missing %s", raw)
	}
	id, _ := listed.Items[0]["id"].(string)
	body, _ := json.Marshal(map[string]any{"name": "on-control", "node_id": control.ID})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+id+"/install", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("install %d %s", res.StatusCode, raw)
	}
	var inst map[string]any
	if err := json.Unmarshal(raw, &inst); err != nil {
		t.Fatal(err)
	}
	if inst["node_id"] != control.ID {
		t.Fatalf("install node_id %s", raw)
	}
	wls, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(wls) != 1 || wls[0].NodeID != control.ID {
		t.Fatalf("workload node %+v", wls)
	}

	worker := appdb.Node{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", ClusterID: clusterID, Name: "box-b", Role: "worker"}
	if err := mem.UpsertNode(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(map[string]any{"name": "on-worker", "node_id": worker.ID})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+id+"/install", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusFailedDependency || !strings.Contains(string(raw), destAgentNotConnected) {
		t.Fatalf("remote %d %s", res.StatusCode, raw)
	}
	wls, _ = mem.ListWorkloads(context.Background(), clusterID)
	if len(wls) != 1 || wls[0].NodeID != control.ID {
		t.Fatalf("remote install must not create or move %+v", wls)
	}
}

func TestPhase36RollbackDeletesVolumes(t *testing.T) {
	_, mem, ts, cookie, clusterID, poolID, fo := phase22Ready(t)
	ok := `
apiVersion: nodal.store/v1
name: vol-flaky
version: "1"
class: community
title: Flaky volumes
deployment:
  kind: oci
  image: docker.io/library/caddy:2.8.4
storage:
  - name: data
    persistent: true
`
	body, _ := json.Marshal(map[string]any{"manifest": ok})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/store/apps/import", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("import %d %s", res.StatusCode, raw)
	}
	var pkg map[string]any
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatal(err)
	}
	id, _ := pkg["id"].(string)
	fo.err = errors.New("pull failed")
	instBody, _ := json.Marshal(map[string]any{"name": "vol-flaky", "pool_id": poolID})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+id+"/install", strings.NewReader(string(instBody)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusCreated {
		t.Fatalf("failed install succeeded %s", raw)
	}
	wls, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(wls) != 0 {
		t.Fatalf("rollback left workloads %+v", wls)
	}
	stacks, _ := mem.ListStacks(context.Background(), clusterID)
	if len(stacks) != 0 {
		t.Fatalf("rollback left stacks %+v", stacks)
	}
	vols, _ := mem.ListVolumes(context.Background(), clusterID, "")
	if len(vols) != 0 {
		t.Fatalf("rollback left volumes %+v", vols)
	}
}

type failUpdateStoreInstallationStore struct {
	appdb.Store
}

func (f failUpdateStoreInstallationStore) UpdateStoreInstallation(context.Context, appdb.StoreInstallation) error {
	return errors.New("persist failed")
}

func TestPhase36InstallFailsClosedWhenRowPersistFails(t *testing.T) {
	s, mem, ts, cookie, _, _, _ := phase22Ready(t)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/store/apps", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list %d %s", res.StatusCode, raw)
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) < 1 {
		t.Fatalf("official sample missing %s", raw)
	}
	id, _ := listed.Items[0]["id"].(string)
	s.Store = failUpdateStoreInstallationStore{Store: mem}

	body, _ := json.Marshal(map[string]any{"name": "sample-web"})
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/store/apps/"+id+"/install", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("install persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record store installation") {
		t.Fatalf("install persist body %s", raw)
	}
}
