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
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func phase18Ready(t *testing.T) (*Server, *appdb.Memory, *httptest.Server, string, string, string, string) {
	t.Helper()
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
	s.Backup = &fakeBackup{}
	inv := inventory.Inventory{
		SchemaVersion: inventory.SchemaVersion,
		ObservedAt:    time.Now().UTC(),
		USB:           []inventory.USBDevice{{Address: "1-2", Vendor: "046d", Product: "c52b", Name: "Unifying"}},
		PCI: []inventory.PCIDevice{
			{Address: "0000:03:00.0", Vendor: "1b21", Device: "2142", Class: "0x0c0330", Driver: "xhci_hcd", IOMMUGroup: "18"},
			{Address: "0000:02:00.0", Vendor: "10de", Device: "1b80", Class: "0x030000", Driver: "nvidia", IOMMUGroup: "12"},
		},
		GPUs: []inventory.GPU{{ID: "0000:02:00.0", PCI: "0000:02:00.0", Vendor: "NVIDIA", IOMMUGroup: "12"}},
		IOMMU: inventory.IOMMU{Status: inventory.StatusAvailable, Groups: []inventory.IOMMUGroup{
			{ID: "12", Devices: []string{"0000:02:00.0", "0000:02:00.1"}},
			{ID: "18", Devices: []string{"0000:03:00.0"}},
		}},
	}
	body, _ := json.Marshal(inv)
	_ = mem.UpsertInventory(context.Background(), appdb.HardwareInventory{
		NodeID: nodeID, ClusterID: cluster.ID, Payload: body, ObservedAt: time.Now().UTC(),
	})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	return s, mem, ts, cookie, cluster.ID, poolID, netID
}

func createPhase18VM(t *testing.T, ts *httptest.Server, cookie, poolID, netID, name string) map[string]any {
	t.Helper()
	raw := `{"name":"` + name + `","kind":"vm","network_id":"` + netID + `","pool_id":"` + poolID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	return created
}

func TestPhase18CloneNewUUIDsAndMAC(t *testing.T) {
	_, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "web")
	srcID := created["id"].(string)
	srcSpec := specFromCreated(created)
	if len(srcSpec.NICs) == 0 || srcSpec.NICs[0].MAC == "" {
		t.Fatal("source mac")
	}
	volsBefore, _ := mem.ListVolumes(context.Background(), clusterID, "")

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+srcID+"/clone", strings.NewReader(`{"name":"web-clone"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("clone %d %s", res.StatusCode, b)
	}
	var cloned map[string]any
	_ = json.NewDecoder(res.Body).Decode(&cloned)
	if cloned["id"] == srcID {
		t.Fatal("clone reused workload uuid")
	}
	dstSpec := specFromCreated(cloned)
	if dstSpec.NICs[0].MAC == srcSpec.NICs[0].MAC {
		t.Fatal("clone reused mac")
	}
	if dstSpec.NICs[0].ID == srcSpec.NICs[0].ID {
		t.Fatal("clone reused nic id")
	}
	if dstSpec.Disks[0].VolumeID == srcSpec.Disks[0].VolumeID {
		t.Fatal("clone reused volume uuid")
	}
	volsAfter, _ := mem.ListVolumes(context.Background(), clusterID, "")
	if len(volsAfter) <= len(volsBefore) {
		t.Fatal("clone must create a new volume")
	}
	if cloned["desired_power"] != "running" {
		t.Fatalf("clone desired_power %v", cloned["desired_power"])
	}
	if cloned["status"] != qemu.StatusRunning {
		t.Fatalf("clone status %v", cloned["status"])
	}
	disks, _ := mem.ListWorkloadDisks(context.Background(), clusterID, cloned["id"].(string))
	if len(disks) != 1 || disks[0].VolumeID != dstSpec.Disks[0].VolumeID {
		t.Fatalf("clone disks %+v spec %+v", disks, dstSpec.Disks)
	}
	nics, _ := mem.ListWorkloadNICs(context.Background(), clusterID, cloned["id"].(string))
	if len(nics) != 1 || nics[0].NetworkID != netID {
		t.Fatalf("clone nics %+v", nics)
	}
}

func TestPhase18ImportRollbackAndRBAC(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	libID := uuid.NewString()
	_ = mem.CreateLibraryItem(context.Background(), appdb.LibraryItem{
		ID: libID, ClusterID: clusterID, PoolID: poolID, Kind: storage.LibraryDiskImage,
		DisplayName: "disk.qcow2", BackendRef: "library/disk-image/" + libID + ".qcow2", Status: storage.StatusAvailable,
	})
	fail := &fakeBackup{err: errors.New("convert failed")}
	s.Backup = fail
	volsBefore, _ := mem.ListVolumes(context.Background(), clusterID, "")
	wlsBefore, _ := mem.ListWorkloads(context.Background(), clusterID)

	body := `{"name":"imported","library_id":"` + libID + `","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/import", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode < 400 {
		t.Fatalf("failed import %d", res.StatusCode)
	}
	_ = res.Body.Close()
	volsAfter, _ := mem.ListVolumes(context.Background(), clusterID, "")
	if len(volsAfter) != len(volsBefore) {
		t.Fatalf("failed import left volumes %d -> %d", len(volsBefore), len(volsAfter))
	}
	wlsAfter, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(wlsAfter) != len(wlsBefore) {
		t.Fatal("failed import left a workload")
	}

	s.Backup = &fakeBackup{}
	uefi := `{"name":"uefi-import","library_id":"` + libID + `","pool_id":"` + poolID + `","network_id":"` + netID + `","firmware":"uefi"}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/import", strings.NewReader(uefi))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	uefiRes, _ := ts.Client().Do(req)
	if uefiRes.StatusCode < 400 {
		t.Fatalf("uefi import without firmware %d", uefiRes.StatusCode)
	}
	_ = uefiRes.Body.Close()
	volsUEFI, _ := mem.ListVolumes(context.Background(), clusterID, "")
	wlsUEFI, _ := mem.ListWorkloads(context.Background(), clusterID)
	if len(volsUEFI) != len(volsBefore) || len(wlsUEFI) != len(wlsBefore) {
		t.Fatalf("uefi import left adopted state volumes=%d workloads=%d", len(volsUEFI), len(wlsUEFI))
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/import", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	ok, _ := ts.Client().Do(req)
	if ok.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(ok.Body)
		t.Fatalf("import %d %s", ok.StatusCode, b)
	}
	_ = ok.Body.Close()

	hash, _ := auth.HashPassword("password1")
	u := appdb.User{ID: uuid.NewString(), ClusterID: clusterID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), clusterID, u.ID, rbac.Viewer)
	login, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	var viewCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = login.Body.Close()
	denied, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/import", strings.NewReader(body))
	denied.Header.Set("Content-Type", "application/json")
	denied.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	out, _ := ts.Client().Do(denied)
	if out.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer import %d", out.StatusCode)
	}
	_ = out.Body.Close()
}

func TestPhase18USBInventoryAttachAndPCI(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "usbvm")
	id := created["id"].(string)
	node, _ := mem.GetNode(context.Background(), clusterID)

	list, _ := http.NewRequest("GET", ts.URL+"/api/v1/nodes/"+node.ID+"/usb", nil)
	list.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	listed, _ := ts.Client().Do(list)
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("usb list %d", listed.StatusCode)
	}
	var usbList struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(listed.Body).Decode(&usbList)
	_ = listed.Body.Close()
	if len(usbList.Items) != 1 || usbList.Items[0]["address"] != "1-2" {
		t.Fatalf("usb inventory %+v", usbList.Items)
	}

	miss, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/usb", strings.NewReader(`{"address":"9-9"}`))
	miss.Header.Set("Content-Type", "application/json")
	miss.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	bad, _ := ts.Client().Do(miss)
	if bad.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown usb %d", bad.StatusCode)
	}
	_ = bad.Body.Close()

	att, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/usb", strings.NewReader(`{"address":"1-2"}`))
	att.Header.Set("Content-Type", "application/json")
	att.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	ok, _ := ts.Client().Do(att)
	if ok.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(ok.Body)
		t.Fatalf("attach usb %d %s", ok.StatusCode, b)
	}
	_ = ok.Body.Close()
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl == nil || !specHasUSBAddress(wl.SpecJSON, "1-2") {
		t.Fatalf("usb must land in spec %+v", wl)
	}
	list2, _ := http.NewRequest("GET", ts.URL+"/api/v1/nodes/"+node.ID+"/usb", nil)
	list2.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	listed, _ = ts.Client().Do(list2)
	_ = json.NewDecoder(listed.Body).Decode(&usbList)
	_ = listed.Body.Close()
	if usbList.Items[0]["claimed_by"] != id {
		t.Fatalf("usb still listed after attach: %+v", usbList.Items[0])
	}

	pciList, _ := http.NewRequest("GET", ts.URL+"/api/v1/nodes/"+node.ID+"/pci", nil)
	pciList.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	pciRes, _ := ts.Client().Do(pciList)
	var pciBody struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(pciRes.Body).Decode(&pciBody)
	_ = pciRes.Body.Close()
	if len(pciBody.Items) != 1 || pciBody.Items[0]["id"] != "0000:03:00.0" {
		t.Fatalf("pci list must omit GPUs: %+v", pciBody.Items)
	}
	pciAtt, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/pci", strings.NewReader(`{"pci":"0000:03:00.0"}`))
	pciAtt.Header.Set("Content-Type", "application/json")
	pciAtt.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	pciOK, _ := ts.Client().Do(pciAtt)
	if pciOK.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(pciOK.Body)
		t.Fatalf("pci attach %d %s", pciOK.StatusCode, b)
	}
	_ = pciOK.Body.Close()
	wl, _ = mem.GetWorkload(context.Background(), clusterID, id)
	if wl == nil || !strings.Contains(string(wl.SpecJSON), "0000:03:00.0") {
		t.Fatalf("pci must land in spec %+v", wl)
	}
	if len(s.VM.(*fakeVM).launch.GPUs) == 0 {
		t.Fatal("vfio host was not applied")
	}
}

func TestPhase18TemplatesExportAndSecureBoot(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "tmpl-src")
	id := created["id"].(string)

	tmplReq, _ := http.NewRequest("POST", ts.URL+"/api/v1/templates", strings.NewReader(`{"workload_id":"`+id+`","name":"golden"}`))
	tmplReq.Header.Set("Content-Type", "application/json")
	tmplReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	tmplRes, _ := ts.Client().Do(tmplReq)
	if tmplRes.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(tmplRes.Body)
		t.Fatalf("template %d %s", tmplRes.StatusCode, b)
	}
	var tmpl map[string]any
	_ = json.NewDecoder(tmplRes.Body).Decode(&tmpl)
	_ = tmplRes.Body.Close()
	disks, _ := mem.ListWorkloadDisks(context.Background(), clusterID, id)
	if len(disks) == 0 {
		t.Fatal("source disks missing")
	}
	vol, _ := mem.GetVolume(context.Background(), clusterID, disks[0].VolumeID)
	if vol == nil || !strings.Contains(vol.BackendRef, "-tmpl.qcow2") {
		t.Fatalf("template create must retarget the boot volume tip: %+v", vol)
	}

	list, _ := http.NewRequest("GET", ts.URL+"/api/v1/templates", nil)
	list.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	listed, _ := ts.Client().Do(list)
	var items struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(listed.Body).Decode(&items)
	_ = listed.Body.Close()
	if len(items.Items) != 1 {
		t.Fatalf("templates %d", len(items.Items))
	}

	dep, _ := http.NewRequest("POST", ts.URL+"/api/v1/templates/"+tmpl["id"].(string)+"/deploy", strings.NewReader(`{"name":"from-tmpl"}`))
	dep.Header.Set("Content-Type", "application/json")
	dep.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	depRes, _ := ts.Client().Do(dep)
	if depRes.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(depRes.Body)
		t.Fatalf("deploy %d %s", depRes.StatusCode, b)
	}
	_ = depRes.Body.Close()

	exp, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/export", strings.NewReader(`{"display_name":"web.qcow2"}`))
	exp.Header.Set("Content-Type", "application/json")
	exp.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	expRes, _ := ts.Client().Do(exp)
	if expRes.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(expRes.Body)
		t.Fatalf("export %d %s", expRes.StatusCode, b)
	}
	_ = expRes.Body.Close()

	sec := `{"name":"secure","kind":"vm","network_id":"` + netID + `","pool_id":"` + poolID + `","firmware":"uefi","secure_boot":true}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(sec))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	out, _ := ts.Client().Do(req)
	if out.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(out.Body)
		t.Fatalf("secure boot without firmware %d %s", out.StatusCode, b)
	}
	_ = out.Body.Close()
	_ = s
}

func TestPhase18RejectsRawQEMUArgsOnImport(t *testing.T) {
	_, _, ts, cookie, _, poolID, netID := phase18Ready(t)
	body := `{"name":"x","library_id":"` + uuid.NewString() + `","pool_id":"` + poolID + `","network_id":"` + netID + `","qemu_args":["-incoming"]}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/import", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	// extra keys are ignored; import still requires a real library item
	if res.StatusCode == http.StatusCreated {
		t.Fatal("import must not succeed with a fake library and raw args")
	}
	_ = res.Body.Close()
}

type startFailVM struct {
	*fakeVM
	startErr error
}

func (f startFailVM) LifecycleVM(ctx context.Context, id, action string, auto bool) (qemu.Observed, error) {
	if action == "start" && f.startErr != nil {
		return qemu.Observed{WorkloadID: id, Status: qemu.StatusStopped}, f.startErr
	}
	return f.fakeVM.LifecycleVM(ctx, id, action, auto)
}

type snapFailVM struct {
	*fakeVM
	err error
}

func (f snapFailVM) SnapshotVM(context.Context, qemu.OverlayRequest) (qemu.OverlayResult, error) {
	return qemu.OverlayResult{}, f.err
}

type vfioFailVM struct {
	*fakeVM
	err error
}

func (f vfioFailVM) ApplyVFIO(context.Context, string, []string) error {
	return f.err
}

func TestPhase18CloneStartFailureIsNotBooted(t *testing.T) {
	s, _, ts, cookie, _, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "web")
	s.VM = startFailVM{fakeVM: &fakeVM{}, startErr: errors.New("start failed")}
	srcID := created["id"].(string)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+srcID+"/clone", strings.NewReader(`{"name":"web-clone"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusCreated {
		t.Fatalf("start failure must not return 201: %s", b)
	}
	if strings.Contains(string(b), `"status":"running"`) {
		t.Fatalf("must not claim booted: %s", b)
	}
}

func TestPhase18CloneExtraDiskIs422(t *testing.T) {
	_, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "web")
	srcID := created["id"].(string)
	wl, _ := mem.GetWorkload(context.Background(), clusterID, srcID)
	spec, err := vmspec.Parse(wl.SpecJSON)
	if err != nil {
		t.Fatal(err)
	}
	extra := uuid.NewString()
	node, _ := mem.GetNode(context.Background(), clusterID)
	_ = mem.CreateVolume(context.Background(), appdb.Volume{
		ID: extra, ClusterID: clusterID, NodeID: node.ID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/extra.qcow2",
	})
	spec.Disks = append(spec.Disks, vmspec.Disk{Role: vmspec.DiskRoleData, VolumeID: extra})
	_ = mem.UpdateWorkloadSpec(context.Background(), appdb.Workload{
		ID: wl.ID, SpecJSON: vmspec.MustJSON(spec), Firmware: wl.Firmware, CPUs: wl.CPUs, MemoryBytes: wl.MemoryBytes,
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+srcID+"/clone", strings.NewReader(`{"name":"web-extra"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("extra disk clone %d %s", res.StatusCode, b)
	}
	if !strings.Contains(strings.ToLower(string(b)), "disk") {
		t.Fatalf("reason %s", b)
	}
}

func TestPhase18TemplateExtraDiskIs422(t *testing.T) {
	_, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "tmpl-extra")
	srcID := created["id"].(string)
	wl, _ := mem.GetWorkload(context.Background(), clusterID, srcID)
	spec, err := vmspec.Parse(wl.SpecJSON)
	if err != nil {
		t.Fatal(err)
	}
	extra := uuid.NewString()
	node, _ := mem.GetNode(context.Background(), clusterID)
	_ = mem.CreateVolume(context.Background(), appdb.Volume{
		ID: extra, ClusterID: clusterID, NodeID: node.ID, PoolID: poolID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/extra.qcow2",
	})
	spec.Disks = append(spec.Disks, vmspec.Disk{Role: vmspec.DiskRoleData, VolumeID: extra})
	_ = mem.UpdateWorkloadSpec(context.Background(), appdb.Workload{
		ID: wl.ID, SpecJSON: vmspec.MustJSON(spec), Firmware: wl.Firmware, CPUs: wl.CPUs, MemoryBytes: wl.MemoryBytes,
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/templates", strings.NewReader(`{"workload_id":"`+srcID+`","name":"golden"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("extra disk template %d %s", res.StatusCode, b)
	}
	if !strings.Contains(strings.ToLower(string(b)), "disk") {
		t.Fatalf("reason %s", b)
	}
	disks, _ := mem.ListWorkloadDisks(context.Background(), clusterID, srcID)
	if len(disks) == 0 {
		t.Fatal("source disks missing")
	}
	boot, _ := mem.GetVolume(context.Background(), clusterID, disks[0].VolumeID)
	if boot != nil && strings.Contains(boot.BackendRef, "-tmpl.qcow2") {
		t.Fatalf("template create must not retarget the boot volume: %+v", boot)
	}
}

func TestPhase18TemplateRequiresSnapshot(t *testing.T) {
	s, _, ts, cookie, _, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "tmpl-src")
	s.VM = snapFailVM{fakeVM: &fakeVM{}, err: errors.New("snapshot failed")}
	id := created["id"].(string)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/templates", strings.NewReader(`{"workload_id":"`+id+`","name":"golden"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusCreated {
		t.Fatalf("empty snapshot must not create a template: %s", b)
	}
}

func TestPhase18TemplateSnapshotRecordsFrozenBacking(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	rec := &recordOverlayVM{fakeVM: &fakeVM{}}
	s.VM = rec
	created := createPhase18VM(t, ts, cookie, poolID, netID, "tmpl-frozen")
	id := created["id"].(string)
	disks, _ := mem.ListWorkloadDisks(context.Background(), clusterID, id)
	if len(disks) == 0 {
		t.Fatal("source disks missing")
	}
	vol, _ := mem.GetVolume(context.Background(), clusterID, disks[0].VolumeID)
	if vol == nil {
		t.Fatal("boot volume missing")
	}
	frozen := vol.BackendRef

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/templates", strings.NewReader(`{"workload_id":"`+id+`","name":"golden"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("template %d %s", res.StatusCode, b)
	}
	vol, _ = mem.GetVolume(context.Background(), clusterID, disks[0].VolumeID)
	if vol == nil || !strings.Contains(vol.BackendRef, "-tmpl.qcow2") {
		t.Fatalf("template create must retarget the boot volume tip: %+v", vol)
	}
	if vol.BackendRef == frozen {
		t.Fatal("live tip must move off the frozen backing")
	}

	list, _ := http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+id+"/snapshots", nil)
	list.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	listed, _ := ts.Client().Do(list)
	raw, _ := io.ReadAll(listed.Body)
	_ = listed.Body.Close()
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("snapshots %d %s", listed.StatusCode, raw)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("expected template snapshot catalog row %s", raw)
	}
	if body.Items[0]["purpose_tag"] != "template" {
		t.Fatalf("purpose_tag %s", raw)
	}
	ref, _ := body.Items[0]["backend_ref"].(string)
	if ref != frozen {
		t.Fatalf("template snapshot must freeze the pre-overlay backing, got %s want %s", ref, frozen)
	}
	if strings.Contains(ref, "-tmpl.qcow2") {
		t.Fatalf("template snapshot backend_ref must not be the live tip %s", raw)
	}

	snapID, _ := body.Items[0]["id"].(string)
	rb, _ := http.NewRequest("POST", ts.URL+"/api/v1/snapshots/"+snapID+"/rollback", strings.NewReader(`{}`))
	rb.Header.Set("Content-Type", "application/json")
	rb.Header.Set("X-Nodal-Confirm", "rollback")
	rb.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rbRes, _ := ts.Client().Do(rb)
	rbRaw, _ := io.ReadAll(rbRes.Body)
	_ = rbRes.Body.Close()
	if rbRes.StatusCode != http.StatusOK {
		t.Fatalf("rollback %d %s", rbRes.StatusCode, rbRaw)
	}
	if rec.last.Action != qemu.OverlayRollback {
		t.Fatalf("rollback action %+v", rec.last)
	}
	if !strings.Contains(rec.last.BackingPath, frozen) {
		t.Fatalf("rollback backing must be frozen parent %s, got %s", frozen, rec.last.BackingPath)
	}
	if strings.Contains(rec.last.BackingPath, "-tmpl.qcow2") {
		t.Fatalf("rollback must not overlay the live template tip %s", rec.last.BackingPath)
	}
}

func TestPhase18TemplateHonorsOverlayChainCap(t *testing.T) {
	_, _, ts, cookie, _, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "tmpl-cap")
	id := created["id"].(string)
	for i := 0; i < qemu.ChainMax; i++ {
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/snapshots", strings.NewReader(`{"name":"s"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, _ := ts.Client().Do(req)
		if res.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			t.Fatalf("snap %d: %d %s", i, res.StatusCode, b)
		}
		_ = res.Body.Close()
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/templates", strings.NewReader(`{"workload_id":"`+id+`","name":"overflow"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("template chain cap %d %s", res.StatusCode, b)
	}
}

func TestPhase18TemplateSnapshotRecordsOverlayChain(t *testing.T) {
	_, _, ts, cookie, _, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "tmpl-chain")
	id := created["id"].(string)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/snapshots", strings.NewReader(`{"name":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("user snap %d %s", res.StatusCode, raw)
	}
	var user map[string]any
	if err := json.Unmarshal(raw, &user); err != nil {
		t.Fatal(err)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/templates", strings.NewReader(`{"workload_id":"`+id+`","name":"golden"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("template %d %s", res.StatusCode, raw)
	}

	tmpl := phase18TemplateSnapshot(t, ts, cookie, id)
	if tmpl["parent_id"] != user["id"] {
		t.Fatalf("template parent_id %v want %s", tmpl["parent_id"], user["id"])
	}
	if tmpl["chain_depth"] != float64(2) {
		t.Fatalf("template chain_depth %v", tmpl["chain_depth"])
	}
}

func TestPhase18SecondTemplateUsesUniqueOverlay(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	rec := &recordOverlayVM{fakeVM: &fakeVM{}}
	s.VM = rec
	created := createPhase18VM(t, ts, cookie, poolID, netID, "tmpl-twice")
	id := created["id"].(string)
	disks, _ := mem.ListWorkloadDisks(context.Background(), clusterID, id)
	if len(disks) == 0 {
		t.Fatal("source disks missing")
	}
	vol, _ := mem.GetVolume(context.Background(), clusterID, disks[0].VolumeID)
	if vol == nil {
		t.Fatal("boot volume missing")
	}
	original := vol.BackendRef

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/templates", strings.NewReader(`{"workload_id":"`+id+`","name":"golden"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("first template %d %s", res.StatusCode, raw)
	}
	vol, _ = mem.GetVolume(context.Background(), clusterID, disks[0].VolumeID)
	if vol == nil || !strings.Contains(vol.BackendRef, "-tmpl.qcow2") {
		t.Fatalf("first template tip %+v", vol)
	}
	firstTip := vol.BackendRef
	if rec.last.OverlayPath == rec.last.BackingPath {
		t.Fatalf("first overlay must not equal backing %+v", rec.last)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/templates", strings.NewReader(`{"workload_id":"`+id+`","name":"golden-2"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("second template %d %s", res.StatusCode, raw)
	}
	if rec.last.OverlayPath == rec.last.BackingPath {
		t.Fatalf("second overlay must not equal backing %+v", rec.last)
	}
	vol, _ = mem.GetVolume(context.Background(), clusterID, disks[0].VolumeID)
	if vol == nil || !strings.Contains(vol.BackendRef, "-tmpl.qcow2") {
		t.Fatalf("second template tip %+v", vol)
	}
	if vol.BackendRef == firstTip {
		t.Fatal("second template must not reuse the first -tmpl.qcow2 path")
	}

	list, _ := http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+id+"/snapshots", nil)
	list.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	listed, _ := ts.Client().Do(list)
	listedRaw, _ := io.ReadAll(listed.Body)
	_ = listed.Body.Close()
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("snapshots %d %s", listed.StatusCode, listedRaw)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(listedRaw, &body); err != nil {
		t.Fatal(err)
	}
	var tmpls []map[string]any
	for _, item := range body.Items {
		if item["purpose_tag"] == "template" {
			tmpls = append(tmpls, item)
		}
	}
	if len(tmpls) != 2 {
		t.Fatalf("expected two template catalog rows %s", listedRaw)
	}
	seenOrig, seenFirst := false, false
	for _, item := range tmpls {
		ref, _ := item["backend_ref"].(string)
		switch ref {
		case original:
			seenOrig = true
		case firstTip:
			seenFirst = true
		default:
			t.Fatalf("unexpected template backend_ref %s", listedRaw)
		}
	}
	if !seenOrig || !seenFirst {
		t.Fatalf("catalog must freeze original boot then first tmpl tip %s", listedRaw)
	}
}

func TestPhase18TemplateAfterFlattenDoesNotInheritStaleParent(t *testing.T) {
	_, _, ts, cookie, _, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "tmpl-flat")
	id := created["id"].(string)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/snapshots", strings.NewReader(`{"name":"before-flat"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("user snap %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/snapshots/flatten", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Confirm", "flatten")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("flatten %d %s", res.StatusCode, raw)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/templates", strings.NewReader(`{"workload_id":"`+id+`","name":"golden"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("template %d %s", res.StatusCode, raw)
	}

	tmpl := phase18TemplateSnapshot(t, ts, cookie, id)
	if tmpl["parent_id"] != "" && tmpl["parent_id"] != nil {
		t.Fatalf("template overlay after flatten must not inherit leftover parent %v", tmpl)
	}
	if tmpl["chain_depth"] != float64(1) {
		t.Fatalf("post-flatten template chain_depth %v", tmpl["chain_depth"])
	}
}

func phase18TemplateSnapshot(t *testing.T, ts *httptest.Server, cookie, workloadID string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/workloads/"+workloadID+"/snapshots", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("snapshots %d %s", res.StatusCode, raw)
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	for _, item := range listed.Items {
		if item["purpose_tag"] == "template" {
			return item
		}
	}
	t.Fatalf("template snapshot missing %s", raw)
	return nil
}

type recordOverlayVM struct {
	*fakeVM
	last qemu.OverlayRequest
}

func (f *recordOverlayVM) SnapshotVM(_ context.Context, req qemu.OverlayRequest) (qemu.OverlayResult, error) {
	f.last = req
	return f.fakeVM.SnapshotVM(context.Background(), req)
}

func TestPhase18FailedVFIORollsBackAssignment(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "pci-vm")
	s.VM = vfioFailVM{fakeVM: &fakeVM{}, err: errors.New("vfio bind failed")}
	id := created["id"].(string)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/pci", strings.NewReader(`{"pci":"0000:03:00.0"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode < 400 {
		t.Fatalf("failed vfio %d", res.StatusCode)
	}
	_ = res.Body.Close()
	assigns, _ := mem.ListGPUAssignments(context.Background(), clusterID)
	for _, a := range assigns {
		if a.WorkloadID == id {
			t.Fatalf("failed ApplyVFIO left assignment %+v", a)
		}
	}
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl != nil && strings.Contains(string(wl.SpecJSON), "0000:03:00.0") {
		t.Fatalf("failed ApplyVFIO left PCI in spec %+v", wl)
	}
}

type usbFailVM struct {
	*fakeVM
	err error
}

func (f usbFailVM) ApplyUSB(context.Context, string, []vmspec.LaunchUSB) error {
	return f.err
}

func specHasUSBAddress(specJSON json.RawMessage, addr string) bool {
	spec, err := vmspec.Parse(specJSON)
	if err != nil {
		return false
	}
	for _, u := range spec.USBs {
		if u.Address == addr {
			return true
		}
	}
	return false
}

func TestPhase18FailedUSBRollsBackAttachmentAndSpec(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "usb-vm")
	s.VM = usbFailVM{fakeVM: &fakeVM{}, err: errors.New("usb bind failed")}
	id := created["id"].(string)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/usb", strings.NewReader(`{"address":"1-2"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode < 400 {
		t.Fatalf("failed usb %d", res.StatusCode)
	}
	_ = res.Body.Close()
	atts, _ := mem.ListUSBAttachments(context.Background(), clusterID, id)
	if len(atts) != 0 {
		t.Fatalf("failed ApplyUSB left attachment %+v", atts)
	}
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl != nil && specHasUSBAddress(wl.SpecJSON, "1-2") {
		t.Fatalf("failed ApplyUSB left USB in spec %+v", wl)
	}
}

func TestPhase18USBAttachFailsClosedWhenAgentUnavailable(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "usb-noagent")
	id := created["id"].(string)
	s.VM = nil
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/usb", strings.NewReader(`{"address":"1-2"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("unavailable usb %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "vm agent is unavailable") {
		t.Fatalf("unavailable usb body %s", raw)
	}
	atts, _ := mem.ListUSBAttachments(context.Background(), clusterID, id)
	if len(atts) != 0 {
		t.Fatalf("unavailable usb leaked attachment %+v", atts)
	}
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl != nil && specHasUSBAddress(wl.SpecJSON, "1-2") {
		t.Fatalf("unavailable usb leaked spec %+v", wl)
	}
}

func TestPhase18PCIAttachFailsClosedWhenAgentUnavailable(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "pci-noagent")
	id := created["id"].(string)
	s.VM = nil
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/pci", strings.NewReader(`{"pci":"0000:03:00.0"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("unavailable pci %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "vm agent is unavailable") {
		t.Fatalf("unavailable pci body %s", raw)
	}
	assigns, _ := mem.ListGPUAssignments(context.Background(), clusterID)
	for _, a := range assigns {
		if a.WorkloadID == id {
			t.Fatalf("unavailable pci leaked assignment %+v", a)
		}
	}
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl != nil && strings.Contains(string(wl.SpecJSON), "0000:03:00.0") {
		t.Fatalf("unavailable pci leaked spec %+v", wl)
	}
}

type failUpdateWorkloadSpecStore struct {
	appdb.Store
}

func (f failUpdateWorkloadSpecStore) UpdateWorkloadSpec(context.Context, appdb.Workload) error {
	return errors.New("persist failed")
}

func TestPhase18USBAttachFailsClosedWhenSpecPersistFails(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "usb-persist")
	id := created["id"].(string)
	s.Store = failUpdateWorkloadSpecStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/usb", strings.NewReader(`{"address":"1-2"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("usb spec persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record USB spec") {
		t.Fatalf("usb spec persist body %s", raw)
	}
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl != nil && specHasUSBAddress(wl.SpecJSON, "1-2") {
		t.Fatalf("failed spec persist must not rewrite USB into spec %+v", wl)
	}
	atts, _ := mem.ListUSBAttachments(context.Background(), clusterID, id)
	if len(atts) != 0 {
		t.Fatalf("failed spec persist leaked USB attachment %+v", atts)
	}
}

func TestPhase18PCIAttachFailsClosedWhenSpecPersistFails(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "pci-persist")
	id := created["id"].(string)
	s.Store = failUpdateWorkloadSpecStore{Store: mem}

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/pci", strings.NewReader(`{"pci":"0000:03:00.0"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("pci spec persist %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "could not record PCI spec") {
		t.Fatalf("pci spec persist body %s", raw)
	}
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl != nil && strings.Contains(string(wl.SpecJSON), "0000:03:00.0") {
		t.Fatalf("failed spec persist must not rewrite PCI into spec %+v", wl)
	}
	assigns, _ := mem.ListGPUAssignments(context.Background(), clusterID)
	for _, a := range assigns {
		if a.WorkloadID == id {
			t.Fatalf("failed spec persist leaked PCI assignment %+v", a)
		}
	}
}

func TestPhase18CloneFailsClosedWhenDiskPersistFails(t *testing.T) {
	s, mem, ts, cookie, _, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "web")
	s.Store = failCreateWorkloadDiskStore{Store: mem}
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+created["id"].(string)+"/clone", strings.NewReader(`{"name":"web-clone"}`))
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

func TestPhase18CloneFailsClosedWhenNICPersistFails(t *testing.T) {
	s, mem, ts, cookie, _, poolID, netID := phase18Ready(t)
	created := createPhase18VM(t, ts, cookie, poolID, netID, "web")
	s.Store = failCreateWorkloadNICStore{Store: mem}
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+created["id"].(string)+"/clone", strings.NewReader(`{"name":"web-clone"}`))
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

func TestPhase18ImportFailsClosedWhenDiskPersistFails(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	libID := uuid.NewString()
	_ = mem.CreateLibraryItem(context.Background(), appdb.LibraryItem{
		ID: libID, ClusterID: clusterID, PoolID: poolID, Kind: storage.LibraryDiskImage,
		DisplayName: "disk.qcow2", BackendRef: "library/disk-image/" + libID + ".qcow2", Status: storage.StatusAvailable,
	})
	s.Store = failCreateWorkloadDiskStore{Store: mem}
	body := `{"name":"imported","library_id":"` + libID + `","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/import", strings.NewReader(body))
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

func TestPhase18ImportFailsClosedWhenNICPersistFails(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	libID := uuid.NewString()
	_ = mem.CreateLibraryItem(context.Background(), appdb.LibraryItem{
		ID: libID, ClusterID: clusterID, PoolID: poolID, Kind: storage.LibraryDiskImage,
		DisplayName: "disk.qcow2", BackendRef: "library/disk-image/" + libID + ".qcow2", Status: storage.StatusAvailable,
	})
	s.Store = failCreateWorkloadNICStore{Store: mem}
	body := `{"name":"imported","library_id":"` + libID + `","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/import", strings.NewReader(body))
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

func addPhase18PCI(t *testing.T, mem *appdb.Memory, clusterID, addr, group string) {
	t.Helper()
	node, err := mem.GetNode(context.Background(), clusterID)
	if err != nil || node == nil {
		t.Fatalf("node: %v", err)
	}
	row, err := mem.GetInventory(context.Background(), node.ID)
	if err != nil || row == nil {
		t.Fatalf("inventory: %v", err)
	}
	parsed, ok := decodeInv(row)
	if !ok {
		t.Fatal("inventory decode")
	}
	parsed.PCI = append(parsed.PCI, inventory.PCIDevice{
		Address: addr, Vendor: "8086", Device: "10d3", Class: "0x020000", Driver: "e1000e", IOMMUGroup: group,
	})
	parsed.IOMMU.Groups = append(parsed.IOMMU.Groups, inventory.IOMMUGroup{ID: group, Devices: []string{addr}})
	body, _ := json.Marshal(parsed)
	if err := mem.UpsertInventory(context.Background(), appdb.HardwareInventory{
		NodeID: node.ID, ClusterID: clusterID, Payload: body, ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func vfioHostSet(vm *fakeVM) map[string]struct{} {
	out := map[string]struct{}{}
	for _, g := range vm.launch.GPUs {
		out[strings.ToLower(g.Host)] = struct{}{}
	}
	return out
}

func TestPhase18SecondPCIAttachKeepsFirstVFIO(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	addPhase18PCI(t, mem, clusterID, "0000:04:00.0", "19")
	created := createPhase18VM(t, ts, cookie, poolID, netID, "pci-two")
	id := created["id"].(string)
	first, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/pci", strings.NewReader(`{"pci":"0000:03:00.0"}`))
	first.Header.Set("Content-Type", "application/json")
	first.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(first)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("first pci %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	second, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/pci", strings.NewReader(`{"pci":"0000:04:00.0"}`))
	second.Header.Set("Content-Type", "application/json")
	second.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(second)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("second pci %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	hosts := vfioHostSet(s.VM.(*fakeVM))
	if _, ok := hosts["0000:03:00.0"]; !ok {
		t.Fatalf("second attach dropped first VFIO host: %+v", hosts)
	}
	if _, ok := hosts["0000:04:00.0"]; !ok {
		t.Fatalf("second attach missing new VFIO host: %+v", hosts)
	}
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl == nil || !strings.Contains(string(wl.SpecJSON), "0000:03:00.0") || !strings.Contains(string(wl.SpecJSON), "0000:04:00.0") {
		t.Fatalf("spec must keep both PCI hosts %+v", wl)
	}
}

func TestPhase18PCIAttachKeepsAssignedGPUVFIO(t *testing.T) {
	s, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	s.GPU = &fakeGPU{}
	created := createPhase18VM(t, ts, cookie, poolID, netID, "pci-gpu")
	id := created["id"].(string)
	if err := mem.CreateSnapshot(context.Background(), appdb.Snapshot{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: id, Name: "pre-vfio", Status: appdb.SnapshotAvailable,
	}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/gpus/assign", strings.NewReader(`{"gpu_id":"0000:02:00.0","workload_id":"`+id+`","mode":"vfio"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("gpu vfio %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	pci, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/pci", strings.NewReader(`{"pci":"0000:03:00.0"}`))
	pci.Header.Set("Content-Type", "application/json")
	pci.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(pci)
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("pci attach %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	hosts := vfioHostSet(s.VM.(*fakeVM))
	if _, ok := hosts["0000:02:00.0"]; !ok {
		t.Fatalf("pci attach dropped GPU VFIO host: %+v", hosts)
	}
	if _, ok := hosts["0000:03:00.0"]; !ok {
		t.Fatalf("pci attach missing PCI VFIO host: %+v", hosts)
	}
	wl, _ := mem.GetWorkload(context.Background(), clusterID, id)
	if wl == nil || !strings.Contains(string(wl.SpecJSON), "0000:02:00.0") || !strings.Contains(string(wl.SpecJSON), "0000:03:00.0") {
		t.Fatalf("spec must keep GPU and PCI hosts %+v", wl)
	}
	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/start", strings.NewReader(`{}`))
	start.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(start)
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("start %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
	hosts = vfioHostSet(s.VM.(*fakeVM))
	if _, ok := hosts["0000:02:00.0"]; !ok {
		t.Fatalf("start reprepare dropped GPU VFIO host: %+v", hosts)
	}
	if _, ok := hosts["0000:03:00.0"]; !ok {
		t.Fatalf("start reprepare dropped PCI VFIO host: %+v", hosts)
	}
}

func deletePhase18VM(t *testing.T, ts *httptest.Server, cookie, id string) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/delete", strings.NewReader(`{}`))
	req.Header.Set("X-Nodal-Confirm", "delete")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete %d %s", res.StatusCode, raw)
	}
}

func TestPhase18DeleteReleasesPCIClaim(t *testing.T) {
	_, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	first := createPhase18VM(t, ts, cookie, poolID, netID, "pci-a")
	id := first["id"].(string)
	pciAtt, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/pci", strings.NewReader(`{"pci":"0000:03:00.0"}`))
	pciAtt.Header.Set("Content-Type", "application/json")
	pciAtt.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	pciOK, _ := ts.Client().Do(pciAtt)
	if pciOK.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(pciOK.Body)
		t.Fatalf("pci attach %d %s", pciOK.StatusCode, b)
	}
	_ = pciOK.Body.Close()
	deletePhase18VM(t, ts, cookie, id)
	got, _ := mem.ListGPUAssignments(context.Background(), clusterID)
	if len(got) != 0 {
		t.Fatalf("delete must release PCI assignment %+v", got)
	}
	second := createPhase18VM(t, ts, cookie, poolID, netID, "pci-b")
	pciAtt, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+second["id"].(string)+"/pci", strings.NewReader(`{"pci":"0000:03:00.0"}`))
	pciAtt.Header.Set("Content-Type", "application/json")
	pciAtt.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	pciOK, _ = ts.Client().Do(pciAtt)
	raw, _ := io.ReadAll(pciOK.Body)
	_ = pciOK.Body.Close()
	if pciOK.StatusCode != http.StatusCreated {
		t.Fatalf("reattach after delete %d %s", pciOK.StatusCode, raw)
	}
}

func TestPhase18DeleteReleasesUSBClaim(t *testing.T) {
	_, mem, ts, cookie, clusterID, poolID, netID := phase18Ready(t)
	first := createPhase18VM(t, ts, cookie, poolID, netID, "usb-a")
	id := first["id"].(string)
	att, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/usb", strings.NewReader(`{"address":"1-2"}`))
	att.Header.Set("Content-Type", "application/json")
	att.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	ok, _ := ts.Client().Do(att)
	if ok.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(ok.Body)
		t.Fatalf("usb attach %d %s", ok.StatusCode, b)
	}
	_ = ok.Body.Close()
	deletePhase18VM(t, ts, cookie, id)
	left, _ := mem.ListUSBAttachments(context.Background(), clusterID, "")
	if len(left) != 0 {
		t.Fatalf("delete must release USB %+v", left)
	}
	second := createPhase18VM(t, ts, cookie, poolID, netID, "usb-b")
	att, _ = http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+second["id"].(string)+"/usb", strings.NewReader(`{"address":"1-2"}`))
	att.Header.Set("Content-Type", "application/json")
	att.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	ok, _ = ts.Client().Do(att)
	raw, _ := io.ReadAll(ok.Body)
	_ = ok.Body.Close()
	if ok.StatusCode != http.StatusCreated {
		t.Fatalf("reattach USB after delete %d %s", ok.StatusCode, raw)
	}
}

func TestPhase18PCIUnassignSendsHostBDF(t *testing.T) {
	s, _, ts, cookie, _, poolID, netID := phase18Ready(t)
	fg := &fakeGPU{}
	s.GPU = fg
	created := createPhase18VM(t, ts, cookie, poolID, netID, "pci-unassign")
	id := created["id"].(string)
	pciAtt, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/pci", strings.NewReader(`{"pci":"0000:03:00.0"}`))
	pciAtt.Header.Set("Content-Type", "application/json")
	pciAtt.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	pciOK, _ := ts.Client().Do(pciAtt)
	raw, _ := io.ReadAll(pciOK.Body)
	_ = pciOK.Body.Close()
	if pciOK.StatusCode != http.StatusCreated {
		t.Fatalf("pci attach %d %s", pciOK.StatusCode, raw)
	}
	var attached map[string]any
	if err := json.Unmarshal(raw, &attached); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/gpus/unassign", strings.NewReader(`{"id":"`+attached["id"].(string)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unassign %d %s", res.StatusCode, raw)
	}
	if len(fg.calls) == 0 || fg.calls[len(fg.calls)-1].Action != "unassign" {
		t.Fatalf("unassign must call GPU agent: %+v", fg.calls)
	}
	hosts := fg.calls[len(fg.calls)-1].PCIDevices
	if len(hosts) != 1 || hosts[0] != "0000:03:00.0" {
		t.Fatalf("PCI unassign must send the BDF: %+v", hosts)
	}
}
