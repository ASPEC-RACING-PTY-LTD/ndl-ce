package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/guest"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func phase20Ready(t *testing.T) (*Server, *fakeIO, *fakeVM, *httptest.Server, string, string) {
	t.Helper()
	s, _, ts, cookie, _, poolID, netID := phase18Ready(t)
	rec := &fakeIO{root: t.TempDir()}
	s.IO = rec
	vm, ok := s.VM.(*fakeVM)
	if !ok {
		t.Fatal("expected fakeVM")
	}
	created := createPhase18VM(t, ts, cookie, poolID, netID, "guest-io")
	id := created["id"].(string)
	return s, rec, vm, ts, cookie, id
}

func TestPhase20VMIORequiresGuestOK(t *testing.T) {
	_, _, _, ts, cookie, id := phase20Ready(t)

	res := doCookie(t, ts, cookie, "POST", "/api/v1/workloads/"+id+"/terminal/sessions", `{"cwd":"/home"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("missing guest must 422: %d %s", res.StatusCode, b)
	}

	res = doCookie(t, ts, cookie, "GET", "/api/v1/workloads/"+id+"/files?path=.", "")
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("missing guest files must 422: %d %s", res.StatusCode, b)
	}

	cons := doCookie(t, ts, cookie, "POST", "/api/v1/workloads/"+id+"/console/sessions", `{"mode":"serial"}`)
	defer cons.Body.Close()
	if cons.StatusCode != http.StatusCreated && cons.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(cons.Body)
		t.Fatalf("console must remain usable without guest: %d %s", cons.StatusCode, b)
	}
}

func TestPhase20VMTerminalAndFilesWhenGuestOK(t *testing.T) {
	s, rec, vm, ts, cookie, id := phase20Ready(t)
	vm.guest = guest.Status{
		NodalGA: guest.ChannelState{State: guest.StateOK, Version: "0.1.18"},
		QEMUGA:  guest.ChannelState{State: guest.StateUnavailable, Reason: "fixture guest channel only"},
	}

	res := doCookie(t, ts, cookie, "POST", "/api/v1/workloads/"+id+"/terminal/sessions", `{"cwd":"/home"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("vm terminal %d %s", res.StatusCode, b)
	}
	var sess map[string]any
	_ = json.NewDecoder(res.Body).Decode(&sess)
	if sess["target_kind"] != vmspec.KindVM {
		t.Fatalf("target_kind %v", sess["target_kind"])
	}
	if sess["cwd"] != "/home" {
		t.Fatalf("cwd %v", sess["cwd"])
	}
	if sess["jail_root"] != guest.JailRoot {
		t.Fatalf("jail_root %v", sess["jail_root"])
	}

	res = doCookie(t, ts, cookie, "GET", "/api/v1/workloads/"+id+"/files?path=.", "")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("vm files %d %s", res.StatusCode, b)
	}
	if rec.lastKind != vmspec.KindVM {
		t.Fatalf("files target kind %q", rec.lastKind)
	}
	if rec.lastJail != guest.JailRoot {
		t.Fatalf("files jail %q", rec.lastJail)
	}

	res = doCookie(t, ts, cookie, "POST", "/api/v1/workloads/"+id+"/files/mkdir", `{"path":"home"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("mkdir %d %s", res.StatusCode, b)
	}

	cluster, err := s.Store.GetCluster(context.Background())
	if err != nil || cluster == nil {
		t.Fatal(err)
	}
	audits, err := s.Store.ListAuditEvents(context.Background(), cluster.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	foundVM := false
	for _, a := range audits {
		if a.Action == "terminal.open" && a.Result == "ok" && strings.Contains(string(a.Detail), "vm:/home") {
			foundVM = true
			break
		}
	}
	if !foundVM {
		t.Fatalf("expected vm:/ audit path, got %+v", audits)
	}
}

func TestPhase20VMIOPermissionSplit(t *testing.T) {
	s, mem, ts, cookie, _, poolID, netID := phase18Ready(t)
	s.IO = &fakeIO{root: t.TempDir()}
	vm := s.VM.(*fakeVM)
	vm.guest = guest.Status{NodalGA: guest.ChannelState{State: guest.StateOK}}
	created := createPhase18VM(t, ts, cookie, poolID, netID, "perm-vm")
	id := created["id"].(string)
	op := loginRole(t, ts, mem, "oper20", rbac.Operator)
	view := loginRole(t, ts, mem, "view20", rbac.Viewer)

	res := doCookie(t, ts, op, "POST", "/api/v1/workloads/"+id+"/terminal/sessions", `{"cwd":"/"}`)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("operator vm term %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, view, "POST", "/api/v1/workloads/"+id+"/terminal/sessions", `{}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer vm term %d", res.StatusCode)
	}
	_ = res.Body.Close()

	res = doCookie(t, ts, view, "GET", "/api/v1/workloads/"+id+"/files?path=.", "")
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("viewer list %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	res = doCookie(t, ts, view, "POST", "/api/v1/workloads/"+id+"/files/mkdir", `{"path":"x"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer mkdir %d", res.StatusCode)
	}
	_ = res.Body.Close()
}

func TestPhase20AgentDisconnectDisablesVMIO(t *testing.T) {
	_, _, vm, ts, cookie, id := phase20Ready(t)
	vm.guest = guest.Status{NodalGA: guest.ChannelState{State: guest.StateOK}}

	res := doCookie(t, ts, cookie, "GET", "/api/v1/workloads/"+id+"/files?path=.", "")
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("connected files %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	vm.guest = guest.Status{NodalGA: guest.ChannelState{State: guest.StateUnavailable, Reason: "nodal guest is not connected"}}
	res = doCookie(t, ts, cookie, "GET", "/api/v1/workloads/"+id+"/files?path=.", "")
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("disconnect files %d %s", res.StatusCode, body)
	}
	if !strings.Contains(string(body), "not connected") && !strings.Contains(string(body), "unavailable") {
		t.Fatalf("disconnect must include a reason: %s", body)
	}

	vm.err = errUnavailable("vm agent is unavailable")
	res = doCookie(t, ts, cookie, "POST", "/api/v1/workloads/"+id+"/terminal/sessions", `{}`)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("agent down term %d %s", res.StatusCode, b)
	}

	cons := doCookie(t, ts, cookie, "POST", "/api/v1/workloads/"+id+"/console/sessions", `{"mode":"serial"}`)
	defer cons.Body.Close()
	if cons.StatusCode != http.StatusCreated && cons.StatusCode != http.StatusOK {
		bb, _ := io.ReadAll(cons.Body)
		t.Fatalf("console after disconnect %d %s", cons.StatusCode, bb)
	}
}

func TestAuditFilesPathVM(t *testing.T) {
	if got := auditFilesPath(vmspec.KindVM, "home/user"); got != "vm:/home/user" {
		t.Fatalf("got %q", got)
	}
	if got := auditFilesPath(vmspec.KindVM, "/etc/hosts"); got != "vm:/etc/hosts" {
		t.Fatalf("got %q", got)
	}
}
