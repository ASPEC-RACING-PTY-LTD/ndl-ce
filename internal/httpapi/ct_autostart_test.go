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
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// lifeLog records every lifecycle request, not only the last.
type lifeLog struct {
	*fakeWorkloads
	reqs []lxc.LifecycleRequest
}

func (l *lifeLog) LifecycleCT(ctx context.Context, req lxc.LifecycleRequest) (lxc.Result, error) {
	l.reqs = append(l.reqs, req)
	return l.fakeWorkloads.LifecycleCT(ctx, req)
}

func (l *lifeLog) autostartCalls() []bool {
	var out []bool
	for _, r := range l.reqs {
		if r.Action == lxc.ActionApplySpec && r.Autostart != nil {
			out = append(out, *r.Autostart)
		}
	}
	return out
}

func createCT(t *testing.T, extra string) (*lifeLog, map[string]any, *appdb.Memory) {
	t.Helper()
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	fw := &lifeLog{fakeWorkloads: &fakeWorkloads{}}
	s.Workloads = fw
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/container-root/x",
		Kind: storage.KindFilesystem, Class: storage.ClassContainerRoot, Format: storage.FormatDirectory,
	}}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"ct","kind":"system-container","image_pin":"alpine/3.21/amd64/default","pool_id":"` + poolID + `","network_id":"` + netID + `","desired_power":"stopped"` + extra + `}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	return fw, created, mem
}

func TestCTCreateWithoutAutostartTurnsBootStartOff(t *testing.T) {
	fw, created, _ := createCT(t, `,"autostart":false`)
	if got := fw.autostartCalls(); len(got) != 1 || got[0] {
		t.Fatalf("expected one autostart=false apply, got %v", got)
	}
	if created["autostart"] != false {
		t.Fatalf("row must record autostart=false: %v", created["autostart"])
	}
}

func TestCTCreateWithAutostartRecordsIt(t *testing.T) {
	fw, created, _ := createCT(t, `,"autostart":true`)
	if got := fw.autostartCalls(); len(got) != 0 {
		t.Fatalf("create already enables boot start; no apply expected, got %v", got)
	}
	if created["autostart"] != true {
		t.Fatalf("row must record autostart=true: %v", created["autostart"])
	}
}

// API clients that never sent autostart got boot start; they still do.
func TestCTCreateOmittingAutostartKeepsBootStart(t *testing.T) {
	fw, created, _ := createCT(t, ``)
	if got := fw.autostartCalls(); len(got) != 0 {
		t.Fatalf("no apply expected, got %v", got)
	}
	if created["autostart"] != true {
		t.Fatalf("row must record autostart=true: %v", created["autostart"])
	}
}

func TestBootStartWarning(t *testing.T) {
	cases := []struct {
		power string
		auto  bool
		want  string
	}{
		{"running", false, "stays stopped"},
		{"stopped", true, "starts it again"},
		{"running", true, ""},
		{"stopped", false, ""},
	}
	for _, c := range cases {
		got := bootStartWarning(c.power, c.auto)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Fatalf("%s/%v: %q", c.power, c.auto, got)
		}
	}
}
