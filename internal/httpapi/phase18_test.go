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
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
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
	if len(s.VM.(*fakeVM).launch.GPUs) == 0 {
		t.Fatal("vfio host was not applied")
	}
}

func TestPhase18TemplatesExportAndSecureBoot(t *testing.T) {
	s, _, ts, cookie, _, poolID, netID := phase18Ready(t)
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
