package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/guest"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

func TestPhase19GuestStatusHonestAndVMIOStill422(t *testing.T) {
	_, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "ga-web")
	id := created["id"].(string)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+id+"/guest", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("guest %d %s", res.StatusCode, b)
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	qemuGA, _ := body["qemu_ga"].(map[string]any)
	nodal, _ := body["nodal_ga"].(map[string]any)
	if qemuGA["state"] != guest.StateUnavailable {
		t.Fatalf("stopped qemu-ga must be unavailable: %#v", qemuGA)
	}
	if nodal["state"] != guest.StateNotInstalled {
		t.Fatalf("missing nodal guest must be not_installed: %#v", nodal)
	}
	obs, err := mem.GetGuestObservation(context.Background(), clusterID, id)
	if err != nil || obs == nil || obs.NodalGAState != guest.StateNotInstalled {
		t.Fatalf("observation %+v %v", obs, err)
	}

	term, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/terminal/sessions", strings.NewReader(`{"cwd":"/"}`))
	term.Header.Set("Content-Type", "application/json")
	term.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	tres, err := ts.Client().Do(term)
	if err != nil {
		t.Fatal(err)
	}
	defer tres.Body.Close()
	if tres.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(tres.Body)
		t.Fatalf("product VM terminal stays 422 until nodal_ga is ok: %d %s", tres.StatusCode, b)
	}
	files, _ := http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+id+"/files?path=.", nil)
	files.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	fres, err := ts.Client().Do(files)
	if err != nil {
		t.Fatal(err)
	}
	defer fres.Body.Close()
	if fres.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(fres.Body)
		t.Fatalf("product VM files stay 422 until nodal_ga is ok: %d %s", fres.StatusCode, b)
	}

	cons, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/console/sessions", strings.NewReader(`{"mode":"serial"}`))
	cons.Header.Set("Content-Type", "application/json")
	cons.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	cres, err := ts.Client().Do(cons)
	if err != nil {
		t.Fatal(err)
	}
	defer cres.Body.Close()
	if cres.StatusCode != http.StatusCreated && cres.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(cres.Body)
		t.Fatalf("console must remain usable without guest agent: %d %s", cres.StatusCode, b)
	}
}

func TestPhase19GuestCTRejected(t *testing.T) {
	_, mem, ts, cookie, clusterID, _, _ := phase18Ready(t)
	node, _ := mem.GetNode(context.Background(), clusterID)
	id := uuid.NewString()
	if err := mem.CreateWorkload(context.Background(), appdb.Workload{
		ID: id, ClusterID: clusterID, NodeID: node.ID, Name: "ct-guest",
		Kind: lxc.KindSystemContainer, Status: lxc.StatusStopped,
	}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+id+"/guest", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("ct guest %d %s", res.StatusCode, b)
	}
}
