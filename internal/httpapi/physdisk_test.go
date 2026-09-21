package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func physFixture(t *testing.T) inventory.FS {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"proc/mounts":                  "/dev/nvme0n1p2 / ext4 rw 0 0\n/dev/nvme0n1p1 /boot/efi vfat rw 0 0\n/dev/sdb1 /mnt/data ext4 rw 0 0\n",
		"proc/swaps":                   "Filename\tType\tSize\tUsed\tPriority\n/dev/nvme0n1p3 partition 1 0 -2\n",
		"sys/class/block/nvme0n1/size": "1953525168\n",
		"sys/class/block/nvme0n1/queue/rotational":      "0\n",
		"sys/class/block/nvme0n1/removable":             "0\n",
		"sys/class/block/nvme0n1/dev":                   "259:0\n",
		"sys/class/block/nvme0n1p1/partition":           "1\n",
		"sys/class/block/nvme0n1p1/size":                "204800\n",
		"sys/class/block/nvme0n1p2/partition":           "2\n",
		"sys/class/block/sda/size":                      "1953525168\n",
		"sys/class/block/sda/queue/rotational":          "0\n",
		"sys/class/block/sda/removable":                 "0\n",
		"sys/class/block/sda/device/model":              "CT1000MX500SSD1\n",
		"sys/class/block/sda/device/vendor":             "ATA\n",
		"sys/class/block/sda/device/serial":             "0001\n",
		"sys/class/block/sda/dev":                       "8:0\n",
		"sys/class/block/sda1/partition":                "1\n",
		"sys/class/block/sda1/size":                     "204800\n",
		"sys/class/block/sda1/dev":                      "8:1\n",
		"sys/class/block/sda2/partition":                "2\n",
		"sys/class/block/sda2/size":                     "1900000\n",
		"sys/class/block/sda2/dev":                      "8:2\n",
		"sys/class/block/sdb/size":                      "3907029168\n",
		"sys/class/block/sdb/queue/rotational":          "1\n",
		"sys/class/block/sdb/removable":                 "0\n",
		"sys/class/block/sdb/device/model":              "ST2000\n",
		"sys/class/block/sdb1/partition":                "1\n",
		"sys/class/block/sdc/size":                      "1953525168\n",
		"sys/class/block/sdc/queue/rotational":          "0\n",
		"sys/class/block/sdc/removable":                 "0\n",
		"sys/class/block/sdc/device/model":              "UNUSED\n",
		"sys/class/block/sdd/size":                      "1953525168\n",
		"sys/class/block/sdd/queue/rotational":          "0\n",
		"sys/class/block/sdd/holders/dm-0":              "",
		"dev/disk/by-id/nvme-eui.1111":                  "->../../nvme0n1",
		"dev/disk/by-id/ata-CT1000MX500SSD1_0001":       "->../../sda",
		"dev/disk/by-id/ata-CT1000MX500SSD1_0001-part1": "->../../sda1",
		"dev/disk/by-id/ata-ST2000":                     "->../../sdb",
		"dev/disk/by-id/ata-UNUSED":                     "->../../sdc",
		"dev/disk/by-id/ata-LVMDISK":                    "->../../sdd",
		"run/udev/data/b8@1":                            "E:ID_FS_TYPE=vfat\nE:ID_PART_ENTRY_TYPE=c12a7328-f81f-11d2-ba4b-00a0c93ec93b\n",
		"run/udev/data/b8@2":                            "E:ID_FS_TYPE=ntfs\n",
	}
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(body, "->") {
			if err := os.Symlink(strings.TrimPrefix(body, "->"), full); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return inventory.FS{Root: root}
}

func physServer(t *testing.T) (*Server, *appdb.Memory, string, string, string, string, *httptest.Server, *fakeVM) {
	t.Helper()
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, mem, cluster.ID, nodeID)
	s.PhysFS = physFixture(t)
	s.Storage = fakeStorage{
		vol: storage.CreateVolumeResult{Handle: storage.VolumeHandle{
			BackendType: storage.BackendDirectory, BackendRef: "volumes/vm-disk/boot.qcow2",
			Kind: storage.KindBlock, Class: storage.ClassVMDisk, Format: storage.FormatQCOW2,
		}},
	}
	vm := &fakeVM{}
	s.VM = vm
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	cookie := claimAdmin(t, ts, token)
	return s, mem, cluster.ID, poolID, netID, cookie, ts, vm
}

func TestPhysicalDiskListEligibility(t *testing.T) {
	_, _, _, _, _, cookie, ts, _ := physServer(t)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/physical-disks", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("list %d %s", res.StatusCode, b)
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	byID := map[string]map[string]any{}
	for _, item := range out.Items {
		byID[item["id"].(string)] = item
	}
	mx := byID["ata-CT1000MX500SSD1_0001"]
	if mx == nil || mx["eligible"] != true {
		t.Fatalf("mx500 %+v", mx)
	}
	if mx["existing_data"] != true {
		t.Fatal("existing data")
	}
	root := byID["nvme-eui.1111"]
	if root == nil || root["eligible"] != false {
		t.Fatalf("root %+v", root)
	}
	mounted := byID["ata-ST2000"]
	if mounted == nil || mounted["eligible"] != false {
		t.Fatalf("mounted %+v", mounted)
	}
	lvm := byID["ata-LVMDISK"]
	if lvm == nil || lvm["eligible"] != false {
		t.Fatalf("lvm %+v", lvm)
	}
}

func TestPhysicalDiskCreateBootAndRejectDuplicate(t *testing.T) {
	_, mem, clusterID, _, netID, cookie, ts, vm := physServer(t)
	body := `{"name":"windows-disk","kind":"vm","network_id":"` + netID + `","spec":{"disks":[{"role":"boot","source":"physical","device_id":"ata-CT1000MX500SSD1_0001"}]}}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created map[string]any
	if err := json.Unmarshal(b, &created); err != nil {
		t.Fatal(err)
	}
	id := created["id"].(string)
	assigns, _ := mem.ListPhysicalDiskAssignments(context.Background(), clusterID, id)
	if len(assigns) != 1 || assigns[0].DeviceID != "ata-CT1000MX500SSD1_0001" {
		t.Fatalf("assigns %+v", assigns)
	}
	vols, _ := mem.ListVolumes(context.Background(), clusterID, "")
	if len(vols) != 0 {
		t.Fatalf("physical boot must not create a volume: %d", len(vols))
	}
	if len(vm.launch.Disks) == 0 || vm.launch.Disks[0].Source != vmspec.DiskSourcePhysical {
		t.Fatalf("launch %+v", vm.launch.Disks)
	}
	if !strings.HasPrefix(vm.launch.Disks[0].Path, "/dev/disk/by-id/") {
		t.Fatalf("path %s", vm.launch.Disks[0].Path)
	}

	dup := `{"name":"other","kind":"vm","network_id":"` + netID + `","spec":{"disks":[{"role":"boot","source":"physical","device_id":"ata-CT1000MX500SSD1_0001"}]}}`
	dreq, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(dup))
	dreq.Header.Set("Content-Type", "application/json")
	dreq.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	dres, err := ts.Client().Do(dreq)
	if err != nil {
		t.Fatal(err)
	}
	db, _ := io.ReadAll(dres.Body)
	_ = dres.Body.Close()
	if dres.StatusCode == http.StatusCreated {
		t.Fatalf("duplicate assignment must fail: %s", db)
	}
	if !strings.Contains(string(db), "already assigned") && !strings.Contains(string(db), "windows-disk") {
		t.Fatalf("duplicate error %s", db)
	}

	del, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/delete", strings.NewReader("{}"))
	del.Header.Set("X-Nodal-Confirm", "delete")
	del.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	ok, err := ts.Client().Do(del)
	if err != nil {
		t.Fatal(err)
	}
	if ok.StatusCode != 200 {
		bb, _ := io.ReadAll(ok.Body)
		t.Fatalf("delete %d %s", ok.StatusCode, bb)
	}
	_ = ok.Body.Close()
	left, _ := mem.ListPhysicalDiskAssignments(context.Background(), clusterID, id)
	if len(left) != 0 {
		t.Fatalf("assignment must be released: %+v", left)
	}
}

func TestPhysicalDiskRejectsHostRootAndMounted(t *testing.T) {
	_, _, _, _, netID, cookie, ts, _ := physServer(t)
	for _, id := range []string{"nvme-eui.1111", "ata-ST2000", "ata-LVMDISK"} {
		body := `{"name":"bad-` + id + `","kind":"vm","network_id":"` + netID + `","spec":{"disks":[{"role":"boot","source":"physical","device_id":"` + id + `"}]}}`
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode == http.StatusCreated {
			t.Fatalf("%s was accepted: %s", id, b)
		}
		if strings.Contains(string(b), "guest.sock") || strings.Contains(string(b), "failed to start VM") {
			t.Fatalf("leaked transport error for %s: %s", id, b)
		}
	}
}

func TestPhysicalDiskViewerCannotAssign(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	nodeID := uuid.NewString()
	_ = mem.UpsertNode(context.Background(), appdb.Node{ID: nodeID, ClusterID: cluster.ID, Name: "local"})
	s.PhysFS = physFixture(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_ = claimAdmin(t, ts, token)
	hash, _ := auth.HashPassword("password1")
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	if err := mem.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	vlogin, _ := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	if vlogin.StatusCode != 200 {
		t.Fatalf("login %d", vlogin.StatusCode)
	}
	var viewCookie *http.Cookie
	for _, c := range vlogin.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c
		}
	}
	_ = vlogin.Body.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/physical-disks/assign", strings.NewReader(`{"workload_id":"`+uuid.NewString()+`","device_id":"ata-UNUSED"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(viewCookie)
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("viewer assign %d %s", res.StatusCode, b)
	}
	_ = res.Body.Close()
}

func TestPhysicalDiskMissingDevice(t *testing.T) {
	_, _, _, _, netID, cookie, ts, _ := physServer(t)
	body := `{"name":"gone","kind":"vm","network_id":"` + netID + `","spec":{"disks":[{"role":"boot","source":"physical","device_id":"ata-NOT-THERE"}]}}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusCreated {
		t.Fatal("missing device")
	}
	if !strings.Contains(string(b), "could not be resolved") && !strings.Contains(string(b), "device_id") {
		t.Fatalf("missing error %s", b)
	}
}

func TestPhysicalDiskAssignmentPersistenceAndRelease(t *testing.T) {
	_, mem, clusterID, poolID, netID, cookie, ts, _ := physServer(t)
	body := `{"name":"mixed","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
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

	assign, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/physical-disks", strings.NewReader(`{"device_id":"ata-UNUSED","role":"data"}`))
	assign.Header.Set("Content-Type", "application/json")
	assign.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	ares, err := ts.Client().Do(assign)
	if err != nil {
		t.Fatal(err)
	}
	if ares.StatusCode != 200 {
		b, _ := io.ReadAll(ares.Body)
		t.Fatalf("assign %d %s", ares.StatusCode, b)
	}
	_ = ares.Body.Close()
	got, _ := mem.GetPhysicalDiskAssignment(context.Background(), clusterID, "ata-UNUSED")
	if got == nil || got.WorkloadID != id {
		t.Fatalf("persist %+v", got)
	}

	rm, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/physical-disks/ata-UNUSED/remove", nil)
	rm.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rres, err := ts.Client().Do(rm)
	if err != nil {
		t.Fatal(err)
	}
	if rres.StatusCode != 200 {
		b, _ := io.ReadAll(rres.Body)
		t.Fatalf("remove %d %s", rres.StatusCode, b)
	}
	_ = rres.Body.Close()
	gone, _ := mem.GetPhysicalDiskAssignment(context.Background(), clusterID, "ata-UNUSED")
	if gone != nil {
		t.Fatal("assignment must be released without wiping the disk")
	}
}

func TestPhysicalDiskCloneRefused(t *testing.T) {
	s, mem, clusterID, _, netID, cookie, ts, _ := physServer(t)
	s.Backup = &fakeBackup{}
	body := `{"name":"phys","kind":"vm","network_id":"` + netID + `","spec":{"disks":[{"role":"boot","source":"physical","device_id":"ata-UNUSED"}]}}`
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
	clone, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/clone", strings.NewReader(`{"name":"copy"}`))
	clone.Header.Set("Content-Type", "application/json")
	clone.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	cres, err := ts.Client().Do(clone)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(cres.Body)
	_ = cres.Body.Close()
	if cres.StatusCode == http.StatusCreated {
		t.Fatal("clone of physical disk VM")
	}
	if !strings.Contains(string(b), "physical disk") {
		t.Fatalf("clone error %s", b)
	}
	left, _ := mem.ListPhysicalDiskAssignments(context.Background(), clusterID, id)
	if len(left) != 1 {
		t.Fatalf("source assignment %+v", left)
	}
}

func TestPhysicalDiskStaleArgvRegeneratedOnStart(t *testing.T) {
	s, mem, clusterID, poolID, netID, cookie, ts, vm := physServer(t)
	body := `{"name":"regen","kind":"vm","pool_id":"` + poolID + `","network_id":"` + netID + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	_ = res.Body.Close()
	id := created["id"].(string)
	row, _ := mem.GetWorkload(context.Background(), clusterID, id)
	spec, _ := vmspec.Parse(row.SpecJSON)
	spec.Disks = append(spec.Disks, vmspec.Disk{Role: vmspec.DiskRoleData, Source: vmspec.DiskSourcePhysical, DeviceID: "ata-UNUSED", Format: "raw", Bus: vmspec.DiskBusAHCI, Slot: 1})
	_ = mem.UpdateWorkloadSpec(context.Background(), appdb.Workload{
		ID: id, CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes, DesiredPower: "stopped",
		SpecJSON: vmspec.MustJSON(spec), AppliedJSON: row.AppliedJSON, Autostart: false, Firmware: spec.Firmware,
	})
	_ = mem.CreatePhysicalDiskAssignment(context.Background(), appdb.PhysicalDiskAssignment{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: id, DeviceID: "ata-UNUSED",
		ByIDPath: "/dev/disk/by-id/ata-UNUSED", Role: "data", Bus: "ahci", Slot: 1,
	})
	start, _ := http.NewRequest("POST", ts.URL+"/api/v1/workloads/"+id+"/start", strings.NewReader("{}"))
	start.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	sres, err := ts.Client().Do(start)
	if err != nil {
		t.Fatal(err)
	}
	if sres.StatusCode != 200 {
		b, _ := io.ReadAll(sres.Body)
		t.Fatalf("start %d %s", sres.StatusCode, b)
	}
	_ = sres.Body.Close()
	found := false
	for _, d := range vm.launch.Disks {
		if d.DeviceID == "ata-UNUSED" && d.Source == vmspec.DiskSourcePhysical {
			found = true
		}
	}
	if !found {
		t.Fatalf("start must recompile physical disk into launch: %+v", vm.launch.Disks)
	}
	_ = s
}

func TestPhysicalDiskAssignmentMemoryUnique(t *testing.T) {
	mem := appdb.NewMemory()
	clusterID := uuid.NewString()
	wl := uuid.NewString()
	a := appdb.PhysicalDiskAssignment{ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: wl, DeviceID: "ata-UNUSED", ByIDPath: "/dev/disk/by-id/ata-UNUSED"}
	if err := mem.CreatePhysicalDiskAssignment(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	a.ID = uuid.NewString()
	a.WorkloadID = uuid.NewString()
	if err := mem.CreatePhysicalDiskAssignment(context.Background(), a); err == nil {
		t.Fatal("duplicate assignment")
	}
}
