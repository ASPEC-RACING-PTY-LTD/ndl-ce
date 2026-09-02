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
