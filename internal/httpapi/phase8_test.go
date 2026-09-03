package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/guest"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

type fakeVM struct {
	prep     qemu.Result
	obs      qemu.Observed
	err      error
	actions  []string
	launch   vmspec.Launch
	userData string
	guest    guest.Status
}

func (f *fakeVM) PrepareVM(_ context.Context, req agentrpc.VMPrepareRequest) (qemu.Result, error) {
	f.launch = req.Launch
	f.userData = req.UserData
	res := f.prep
	if res.WorkloadID == "" {
		res.WorkloadID = req.Launch.WorkloadID
	}
	if res.Status == "" {
		res.Status = qemu.StatusStopped
	}
	return res, f.err
}

func (f *fakeVM) LifecycleVM(_ context.Context, id, action string, _ bool) (qemu.Observed, error) {
	f.actions = append(f.actions, action)
	obs := f.obs
	if obs.WorkloadID == "" {
		obs.WorkloadID = id
	}
	switch action {
	case "start", "restart":
		obs.Status = qemu.StatusRunning
		obs.UnitActive = true
	case "stop", "force-stop", "delete-runtime":
		obs.Status = qemu.StatusStopped
		obs.UnitActive = false
	}
	return obs, f.err
}

func (f *fakeVM) QueryPCIVM(_ context.Context, id string) (qemu.Observed, error) {
	obs := f.obs
	obs.WorkloadID = id
	obs.PCI = f.launch.PCI
	match := true
	obs.PCILiveMatch = &match
	return obs, f.err
}

func (f *fakeVM) SnapshotVM(_ context.Context, req qemu.OverlayRequest) (qemu.OverlayResult, error) {
	if f.err != nil {
		return qemu.OverlayResult{}, f.err
	}
	if req.OverlayPath != "" && req.OverlayPath == req.BackingPath {
		return qemu.OverlayResult{}, errors.New("overlay path must not equal backing path")
	}
	return qemu.OverlayResult{WorkloadID: req.WorkloadID, OverlayPath: req.OverlayPath, BackingPath: req.BackingPath, Mechanism: "qcow2-overlay"}, nil
}

func (f *fakeVM) ApplyUSB(_ context.Context, _ string, usbs []vmspec.LaunchUSB) error {
	f.launch.USBs = usbs
	return f.err
}

func (f *fakeVM) HotplugUSB(_ context.Context, _ string, _ bool, usb vmspec.LaunchUSB) error {
	if f.err != nil {
		return f.err
	}
	f.launch.USBs = append(f.launch.USBs, usb)
	return nil
}

func (f *fakeVM) ApplyVFIO(_ context.Context, _ string, hosts []string) error {
	if f.err != nil {
		return f.err
	}
	gpus := make([]vmspec.LaunchGPU, 0, len(hosts))
	for i, h := range hosts {
		gpus = append(gpus, vmspec.LaunchGPU{Host: h, PCIAddr: fmt.Sprintf("0x%x", 0x1a+i)})
	}
	f.launch.GPUs = gpus
	return nil
}

func (f *fakeVM) GuestStatus(_ context.Context, id string) (guest.Status, error) {
	if f.err != nil {
		return guest.Status{}, f.err
	}
	st := f.guest
	st.WorkloadID = id
	if st.QEMUGA.State == "" {
		st.QEMUGA.State = guest.StateUnavailable
		st.QEMUGA.Reason = "vm is stopped"
	}
	if st.NodalGA.State == "" {
		st.NodalGA.State = guest.StateNotInstalled
		st.NodalGA.Reason = "nodal guest is not connected"
	}
	if st.ObservedAt.IsZero() {
		st.ObservedAt = time.Now().UTC()
	}
	return st, nil
}

func TestVMCreateLifecycleDeletePreservesVolume(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
			Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
		}},
	}
	vm := &fakeVM{}
	s.VM = vm
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `","cpus":2,"memory_bytes":536870912,"nocloud":{"enable":true,"hostname":"web","username":"debian"}}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	id := created["id"].(string)
	if created["kind"] != "vm" {
		t.Fatalf("kind %v", created["kind"])
	}
	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, id)
	if len(disks) != 1 {
		t.Fatalf("disks %d", len(disks))
	}
	nics, _ := mem.ListWorkloadNICs(context.Background(), cluster.ID, id)
	if len(nics) != 1 || nics[0].NetworkID != netID {
		t.Fatalf("nics %+v", nics)
	}
	if vm.launch.NICs[0].MAC == "" || vm.launch.PCI["vga"] == "" {
		t.Fatal("mac and pci must be compiled")
	}
	mac := vm.launch.NICs[0].MAC
	pci := vm.launch.Disks[0].PCIAddr
	if strings.Contains(string(vmspec.MustJSON(specFromCreated(created))), "password") && strings.Contains(string(vmspec.MustJSON(specFromCreated(created))), "secret") {
		t.Fatal("secret leaked")
	}
	for _, action := range []string{"start", "stop", "restart", "force-stop"} {
		r, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/"+action, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		out, err := ts.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		if out.StatusCode != 200 {
			b, _ := io.ReadAll(out.Body)
			t.Fatalf("%s %d %s", action, out.StatusCode, b)
		}
		_ = out.Body.Close()
	}
	del, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/delete", strings.NewReader("{}"))
	del.Header.Set("Content-Type", "application/json")
	del.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	bad, _ := ts.Client().Do(del)
	if bad.StatusCode != http.StatusConflict {
		t.Fatalf("delete without confirm %d", bad.StatusCode)
	}
	_ = bad.Body.Close()
	del2, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/delete", strings.NewReader("{}"))
	del2.Header.Set("Content-Type", "application/json")
	del2.Header.Set("X-Nodal-Confirm", "delete")
	del2.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	ok, err := ts.Client().Do(del2)
	if err != nil {
		t.Fatal(err)
	}
	if ok.StatusCode != 200 {
		b, _ := io.ReadAll(ok.Body)
		t.Fatalf("delete %d %s", ok.StatusCode, b)
	}
	_ = ok.Body.Close()
	vols, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	if len(vols) == 0 {
		t.Fatal("delete must preserve volumes")
	}
	if mac == "" || pci == "" {
		t.Fatal("compiled identity")
	}
}

func specFromCreated(m map[string]any) vmspec.Spec {
	raw, _ := json.Marshal(m["spec"])
	spec, _ := vmspec.Parse(raw)
	return spec
}

func TestVMRejectsRawArgsCloneAndViewerMutations(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	raw := `{"name":"evil","kind":"vm","network_id":"` + netID + `","pool_id":"` + poolID + `","qemu_args":["-incoming","tcp:0:4444"]}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("raw args %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()

	good := `{"name":"web","kind":"vm","network_id":"` + netID + `","pool_id":"` + poolID + `"}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(good))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()

	hash, _ := auth.HashPassword("password1")
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	var viewCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = login.Body.Close()
	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+created.ID+"/start", strings.NewReader(`{}`))
	start.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	denied, _ := ts.Client().Do(start)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer start %d", denied.StatusCode)
	}
	_ = denied.Body.Close()
	cons, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+created.ID+"/console/sessions", strings.NewReader(`{"mode":"serial"}`))
	cons.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	cd, _ := ts.Client().Do(cons)
	if cd.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer console %d", cd.StatusCode)
	}
	_ = cd.Body.Close()
}

func TestVMConsoleTicketAndStorageUnavailable(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"web","kind":"vm","network_id":"` + netID + `","pool_id":"` + poolID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()

	cons, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+created.ID+"/console/sessions", strings.NewReader(`{"mode":"vnc"}`))
	cons.Header.Set("Content-Type", "application/json")
	cons.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	out, _ := ts.Client().Do(cons)
	if out.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(out.Body)
		t.Fatalf("console %d %s", out.StatusCode, b)
	}
	var sess struct {
		ID     string `json:"id"`
		Ticket string `json:"ticket"`
		Kind   string `json:"kind"`
	}
	_ = json.NewDecoder(out.Body).Decode(&sess)
	_ = out.Body.Close()
	if sess.Ticket == "" || sess.Kind != appdb.IOKindConsole {
		t.Fatal("ticket")
	}
	expired, _ := mem.GetIOSession(context.Background(), cluster.ID, sess.ID)
	expired.ExpiresAt = time.Unix(0, 0).UTC()
	_ = mem.UpdateIOSession(context.Background(), *expired)
	ws, _ := http.NewRequest("GET", ts.URL+"/api/v1/io/sessions/"+sess.ID+"/ws", nil)
	ws.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	ws.Header.Set("X-Nodal-Ticket", sess.Ticket)
	gone, _ := ts.Client().Do(ws)
	if gone.StatusCode != http.StatusGone {
		b, _ := io.ReadAll(gone.Body)
		t.Fatalf("expired console ticket %d %s", gone.StatusCode, b)
	}
	_ = gone.Body.Close()

	disks, _ := mem.ListWorkloadDisks(context.Background(), cluster.ID, created.ID)
	if len(disks) > 0 {
		vol, _ := mem.GetVolume(context.Background(), cluster.ID, disks[0].VolumeID)
		if vol != nil {
			vol.Status = storage.StatusUnavailable
			_ = mem.UpdateVolumeObserved(context.Background(), *vol)
		}
	}
	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+created.ID+"/start", strings.NewReader(`{}`))
	start.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	blocked, _ := ts.Client().Do(start)
	if blocked.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(blocked.Body)
		t.Fatalf("unavailable start %d %s", blocked.StatusCode, b)
	}
	_ = blocked.Body.Close()
}

func TestVMCreateRunningRefusesUnavailableStorage(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	vm := &poisonPrepVM{mem: mem, clusterID: cluster.ID}
	s.VM = vm
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"web","kind":"vm","network_id":"` + netID + `","pool_id":"` + poolID + `","desired_power":"running"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("create-and-start unavailable %d %s", res.StatusCode, raw)
	}
	if len(vm.actions) != 0 {
		t.Fatalf("must not start with unavailable storage: %v", vm.actions)
	}
	wls, _ := mem.ListWorkloads(context.Background(), cluster.ID)
	if len(wls) != 1 || wls[0].Status != qemu.StatusUnavailable {
		t.Fatalf("workload must remain unavailable, got %+v", wls)
	}
}

type poisonPrepVM struct {
	fakeVM
	mem       *appdb.Memory
	clusterID string
}

func (p *poisonPrepVM) PrepareVM(ctx context.Context, req agentrpc.VMPrepareRequest) (qemu.Result, error) {
	vols, _ := p.mem.ListVolumes(ctx, p.clusterID, "")
	for _, v := range vols {
		v.Status = storage.StatusUnavailable
		_ = p.mem.UpdateVolumeObserved(ctx, v)
	}
	return p.fakeVM.PrepareVM(ctx, req)
}

func TestVMPatchRequiresStopAndMACPersist(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	vm := &fakeVM{}
	s.VM = vm
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"web","kind":"vm","network_id":"` + netID + `","pool_id":"` + poolID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	id := created["id"].(string)
	nics := created["nics"].([]any)
	firstMAC := nics[0].(map[string]any)["mac"].(string)
	patch, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"cpus":4}`))
	patch.Header.Set("Content-Type", "application/json")
	patch.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	out, _ := ts.Client().Do(patch)
	if out.StatusCode != 200 {
		b, _ := io.ReadAll(out.Body)
		t.Fatalf("patch %d %s", out.StatusCode, b)
	}
	var updated map[string]any
	_ = json.NewDecoder(out.Body).Decode(&updated)
	_ = out.Body.Close()
	nics = updated["nics"].([]any)
	if nics[0].(map[string]any)["mac"].(string) != firstMAC {
		t.Fatal("mac changed")
	}
	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/start", strings.NewReader(`{}`))
	start.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	started, _ := ts.Client().Do(start)
	if started.StatusCode != 200 {
		b, _ := io.ReadAll(started.Body)
		t.Fatalf("start after cpu patch %d %s", started.StatusCode, b)
	}
	_ = started.Body.Close()
	if vm.launch.CPUs != 4 {
		t.Fatalf("start must recompile frozen launch cpus=%d", vm.launch.CPUs)
	}
	bad, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+id, strings.NewReader(`{"firmware":"uefi"}`))
	bad.Header.Set("Content-Type", "application/json")
	bad.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	denied, _ := ts.Client().Do(bad)
	if denied.StatusCode != http.StatusConflict && denied.StatusCode != http.StatusUnprocessableEntity {
		b, _ := io.ReadAll(denied.Body)
		t.Fatalf("firmware while running %d %s", denied.StatusCode, b)
	}
	_ = denied.Body.Close()
}

func TestLiveQemuImgRefused(t *testing.T) {
	e := &qemu.Engine{DataDir: t.TempDir(), SkipHostCmds: true, LiveUnits: map[string]bool{}}
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	disk := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2"
	if _, err := e.Prepare(qemu.Spec{WorkloadID: id, VolumeID: "v", DiskPath: disk, Accel: "tcg"}); err != nil {
		t.Fatal(err)
	}
	e.LiveUnits[id] = true
	if err := e.AssertDiskOffline(context.Background(), disk); err == nil {
		t.Fatal("live qemu-img must be refused")
	}
}

func TestMissingVolumeIDRefused(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	missing := uuid.NewString()
	body := `{"name":"ghost","kind":"vm","network_id":"` + netID + `","pool_id":"` + poolID + `","volume_id":"` + missing + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("missing volume %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	vols, _ := mem.ListVolumes(context.Background(), cluster.ID, "")
	for _, v := range vols {
		if v.ID == missing {
			t.Fatal("missing volume_id must not allocate a blank disk")
		}
	}
}

func TestPasswordSeedSurvivesReprepare(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	vm := &fakeVM{}
	s.VM = vm
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"web","kind":"vm","network_id":"` + netID + `","pool_id":"` + poolID + `","nocloud":{"enable":true,"username":"debian","password":"hunter2"}}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	if strings.Contains(string(mustSpecJSON(created)), "hunter2") {
		t.Fatal("password leaked in API spec")
	}
	if !strings.Contains(vm.userData, "hunter2") {
		t.Fatal("create must write password into the private seed")
	}
	id := created["id"].(string)
	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/start", strings.NewReader(`{}`))
	start.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	out, _ := ts.Client().Do(start)
	if out.StatusCode != 200 {
		b, _ := io.ReadAll(out.Body)
		t.Fatalf("start %d %s", out.StatusCode, b)
	}
	_ = out.Body.Close()
	if vm.userData != "" {
		t.Fatalf("reprepare must not overwrite the private seed, got %q", vm.userData)
	}
}

func TestVMPatchFailsClosedWhenSpecPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	body := `{"name":"web","kind":"vm","network_id":"` + netID + `","pool_id":"` + poolID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
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
	s.Store = failUpdateWorkloadSpecStore{Store: mem}

	patch, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/workloads/"+created["id"].(string), strings.NewReader(`{"cpus":4}`))
	patch.Header.Set("Content-Type", "application/json")
	patch.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	out, _ := ts.Client().Do(patch)
	b, _ := io.ReadAll(out.Body)
	_ = out.Body.Close()
	if out.StatusCode != http.StatusInternalServerError {
		t.Fatalf("patch persist %d %s", out.StatusCode, b)
	}
	if !strings.Contains(string(b), "could not record VM spec") {
		t.Fatalf("patch persist body %s", b)
	}
}

type failCreateWorkloadDiskStore struct {
	appdb.Store
}

func (f failCreateWorkloadDiskStore) CreateWorkloadDisk(context.Context, appdb.WorkloadDisk) error {
	return errors.New("persist failed")
}

type failCreateWorkloadNICStore struct {
	appdb.Store
}

func (f failCreateWorkloadNICStore) CreateWorkloadNIC(context.Context, appdb.WorkloadNIC) error {
	return errors.New("persist failed")
}

type failCreateVolumeStore struct {
	appdb.Store
}

func (f failCreateVolumeStore) CreateVolume(context.Context, appdb.Volume) error {
	return errors.New("persist failed")
}

func TestVMCreateFailsClosedWhenDiskPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	s.Store = failCreateWorkloadDiskStore{Store: mem}

	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("disk persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record VM disk") {
		t.Fatalf("disk persist body %s", raw)
	}
}

func TestVMCreateFailsClosedWhenNICPersistFails(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.Storage = fakeStorage{vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
		BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
		Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
	}}}
	s.VM = &fakeVM{}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	s.Store = failCreateWorkloadNICStore{Store: mem}

	body := `{"name":"web","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("nic persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record VM NIC") {
		t.Fatalf("nic persist body %s", raw)
	}
}

func mustSpecJSON(m map[string]any) json.RawMessage {
	raw, _ := json.Marshal(m["spec"])
	return raw
}
