package migration

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

type fakeSource struct {
	deleted int
}

func (fakeSource) ID() string { return AdapterDisk }
func (fakeSource) Info() AdapterInfo {
	return AdapterInfo{ID: AdapterDisk, Import: true, Modes: []string{ModeDisk}}
}
func (fakeSource) Discover(SourceConn) (Discovery, error) { return Discovery{}, nil }
func (fakeSource) Manifest(SourceConn, string) (Manifest, error) {
	return Manifest{SchemaVersion: ManifestSchema, Kind: KindVM}, nil
}
func (fakeSource) Capabilities(SourceConn, string) (Caps, error) {
	return Caps{Disk: true, Offline: true}, nil
}
func (fakeSource) OpenArtifact(SourceConn, string, string) (Readable, error) { return nil, nil }

func TestSourceAdapterHasNoDeleteMethod(t *testing.T) {
	var s SourceAdapter = fakeSource{}
	rt := reflect.TypeOf(&s).Elem()
	for i := 0; i < rt.NumMethod(); i++ {
		name := strings.ToLower(rt.Method(i).Name)
		if strings.Contains(name, "delete") || strings.Contains(name, "destroy") || strings.Contains(name, "cleanup") {
			t.Fatalf("source adapter must not expose %s", rt.Method(i).Name)
		}
	}
}

func TestForbiddenSourceActions(t *testing.T) {
	for _, a := range ForbiddenSourceActions {
		if a == "" {
			t.Fatal("empty")
		}
	}
	if err := AssertNoSourceDestruction("NONE"); err != nil {
		t.Fatal(err)
	}
	if err := AssertNoSourceDestruction("delete source workload"); err == nil {
		t.Fatal("destructive language must fail")
	}
}

func TestModeMustBeExplicit(t *testing.T) {
	_, err := BuildPlan("j1", AdapterDisk, []DiscoveredWorkload{{SourceID: "a", Name: "n", Kind: KindVM}}, []string{"a"}, map[string]string{}, map[string]Manifest{}, Mapping{}, nil, nil, false, nil)
	if err == nil || !strings.Contains(err.Error(), "mode must be selected") {
		t.Fatalf("got %v", err)
	}
}

func TestOfflineRefusesRunning(t *testing.T) {
	item := ItemPlan{Mode: ModeOffline, Compatibility: CompatReady}
	err := Preflight(item, Caps{Offline: true}, PreflightEnv{SourceExists: true, SourceRunning: true, CredentialsOK: true, DestPoolExists: true, DestCapacityOK: true, DestNetExists: true, NameAvailable: true, ToolsOK: true, StagingOK: true})
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("got %v", err)
	}
}

func TestLiveRequiresAckAndCapability(t *testing.T) {
	env := PreflightEnv{SourceExists: true, CredentialsOK: true, DestPoolExists: true, DestCapacityOK: true, DestNetExists: true, NameAvailable: true, ToolsOK: true, StagingOK: true}
	item := ItemPlan{Mode: ModeLive, Compatibility: CompatReady}
	if err := Preflight(item, Caps{Live: true}, env); err == nil {
		t.Fatal("ack required")
	}
	item.LiveAck = true
	if err := Preflight(item, Caps{}, env); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("capability required: %v", err)
	}
	if err := Preflight(item, Caps{Live: true}, env); err != nil {
		t.Fatal(err)
	}
}

func TestNoSilentModeFallback(t *testing.T) {
	if err := NoSilentFallback(ModeLive, ModeOffline); err == nil {
		t.Fatal("must not fall back")
	}
}

func TestSnapshotRequiresCapability(t *testing.T) {
	item := ItemPlan{Mode: ModeSnapshot, Compatibility: CompatReady}
	err := Preflight(item, Caps{Snapshot: false, SnapshotNote: "no snap"}, PreflightEnv{SourceExists: true, CredentialsOK: true, DestPoolExists: true, DestCapacityOK: true, DestNetExists: true, NameAvailable: true, ToolsOK: true, StagingOK: true})
	if err == nil || !strings.Contains(err.Error(), "no snap") {
		t.Fatalf("got %v", err)
	}
}

func TestMappingsAndOverrides(t *testing.T) {
	m := Manifest{Kind: KindVM, VM: &VMSection{
		Disks: []Disk{{Storage: "local-zfs"}},
		NICs:  []NIC{{Bridge: "vmbr0"}, {Bridge: "vmbr2", VLAN: 20}},
	}}
	bulk := Mapping{Storage: map[string]string{"local-zfs": "Fast-ZFS"}, Network: map[string]string{"vmbr0": "LAN", "vmbr2/20": "Lab"}}
	got := ApplyMapping(m, bulk, nil)
	if got.VM.Disks[0].Storage != "Fast-ZFS" || got.VM.NICs[0].Network != "LAN" || got.VM.NICs[1].Network != "Lab" {
		t.Fatalf("%+v", got.VM)
	}
	ov := Mapping{Network: map[string]string{"vmbr0": "Servers"}}
	got = ApplyMapping(m, bulk, &ov)
	if got.VM.NICs[0].Network != "Servers" {
		t.Fatal("override")
	}
	miss := MissingMappings(Manifest{Kind: KindVM, VM: &VMSection{NICs: []NIC{{Bridge: "vmbr9"}}}}, Mapping{})
	if len(miss) == 0 || miss[0].Level != CompatRequiresMapping {
		t.Fatal("missing mapping")
	}
}

func TestCompatibilityStates(t *testing.T) {
	ready, _ := Analyze(Manifest{SchemaVersion: ManifestSchema, Kind: KindVM, VM: &VMSection{CPUs: 2, MemoryBytes: 1 << 30, Disks: []Disk{{Format: "qcow2"}}}}, Mapping{}, nil, nil, map[string]bool{"qcow2": true})
	if ready != CompatReady && ready != CompatWarning {
		t.Fatalf("ready %s", ready)
	}
	warn, f := Analyze(Manifest{SchemaVersion: ManifestSchema, Kind: KindVM, VM: &VMSection{CPUs: 2, MemoryBytes: 1 << 20, CPUType: "EPYC-Rome", Disks: []Disk{{Format: "qcow2"}}}}, Mapping{}, nil, nil, nil)
	if warn != CompatWarning {
		t.Fatalf("warn %s %+v", warn, f)
	}
	need, _ := Analyze(Manifest{SchemaVersion: ManifestSchema, Kind: KindVM, VM: &VMSection{CPUs: 1, MemoryBytes: 1, Disks: []Disk{{Storage: "nas01", Format: "qcow2"}}, NICs: []NIC{{Bridge: "vmbr2"}}}}, Mapping{}, nil, nil, nil)
	if need != CompatRequiresMapping {
		t.Fatalf("map %s", need)
	}
	block, _ := Analyze(Manifest{SchemaVersion: ManifestSchema, Kind: KindVM, VM: &VMSection{CPUs: 1, MemoryBytes: 1, Disks: []Disk{{Format: "bogus"}}}}, Mapping{}, nil, nil, map[string]bool{})
	if block != CompatBlocked {
		t.Fatalf("block %s", block)
	}
	un, _ := Analyze(Manifest{SchemaVersion: ManifestSchema, Kind: "oci"}, Mapping{}, nil, nil, nil)
	if un != CompatUnsupported {
		t.Fatalf("unsup %s", un)
	}
}

func TestArchiveTraversalAndSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "../etc/passwd", Size: 4, Mode: 0644, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("root"))
	_ = tw.Close()
	if err := ExtractTar(bytes.NewReader(buf.Bytes()), dir, 1<<20); err == nil {
		t.Fatal("traversal")
	}
	buf.Reset()
	tw = tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "link", Linkname: "/etc/shadow", Typeflag: tar.TypeSymlink})
	_ = tw.Close()
	if err := ExtractTar(bytes.NewReader(buf.Bytes()), dir, 1<<20); err == nil {
		t.Fatal("symlink")
	}
	buf.Reset()
	tw = tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "dev", Typeflag: tar.TypeBlock})
	_ = tw.Close()
	if err := ExtractTar(bytes.NewReader(buf.Bytes()), dir, 1<<20); err == nil {
		t.Fatal("device")
	}
	sparse, err := os.ReadFile(filepath.Join("testdata", "gnu-nil-sparse-data.tar"))
	if err != nil {
		t.Fatal(err)
	}
	err = ExtractTar(bytes.NewReader(sparse), dir, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "sparse") {
		t.Fatalf("gnu sparse members must be refused, got %v", err)
	}
}

func TestExtractTarIgnoresPrivilegedXattrs(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: "bin", Size: 2, Mode: 0755, Typeflag: tar.TypeReg,
		PAXRecords: map[string]string{
			"SCHILY.xattr.security.capability":        "pwn",
			"LIBARCHIVE.xattr.trusted.overlay.opaque": "y",
			"SCHILY.xattr.user.comment":               "ok",
		},
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := ExtractTar(bytes.NewReader(buf.Bytes()), dest, 1<<20); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dest, "bin")
	if n, err := unix.Getxattr(target, "security.capability", make([]byte, 256)); err == nil && n > 0 {
		t.Fatal("security.capability must not restore")
	}
	if n, err := unix.Getxattr(target, "trusted.overlay.opaque", make([]byte, 256)); err == nil && n > 0 {
		t.Fatal("trusted xattr must not restore")
	}
}

func TestConvertArgvRejectsInjection(t *testing.T) {
	if _, err := ConvertArgv("/tmp/a;rm", "qcow2", "/tmp/b", "qcow2"); err == nil {
		t.Fatal("semi")
	}
	if _, err := ConvertArgv("/etc/passwd", "qcow2", "/tmp/b", "qcow2"); err == nil {
		t.Fatal("etc")
	}
	argv, err := ConvertArgv("/tmp/src.vmdk", "vmdk", "/tmp/dst.qcow2", "qcow2")
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != qemuImgBin || strings.Join(argv, " ") != qemuImgBin+" convert -p -f vmdk -O qcow2 /tmp/src.vmdk /tmp/dst.qcow2" {
		t.Fatalf("%q", argv)
	}
}

func TestChecksumMismatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksum(p, "deadbeef"); err == nil {
		t.Fatal("mismatch")
	}
}

func TestBundleRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "disk.qcow2")
	if err := os.WriteFile(src, []byte("qcow"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := Manifest{SchemaVersion: ManifestSchema, Kind: KindVM, Identity: Identity{Name: "rt"}, VM: &VMSection{CPUs: 2, MemoryBytes: 64, Disks: []Disk{{Artifact: "disks/disk.qcow2", Format: "qcow2"}}}}
	if err := WriteBundle(dir, m, map[string]string{"disks/disk.qcow2": src}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Name != "rt" || got.Checksums["disks/disk.qcow2"] == "" {
		t.Fatalf("%+v", got)
	}
	if err := ValidateManifestJSON([]byte(`{"schema_version":"ndl.migration.manifest.v1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifestJSON([]byte(`{not json`)); err == nil {
		t.Fatal("malformed")
	}
}

func TestOVFAndLibvirtParse(t *testing.T) {
	ovf := `<?xml version="1.0"?><Envelope xmlns="http://schemas.dmtf.org/ovf/envelope/1"><References><File ovf:href="disk.vmdk" ovf:id="file1"/></References><DiskSection><Disk ovf:fileRef="file1" ovf:capacity="1073741824" ovf:format="http://www.vmware.com/interfaces/specifications/vmdk.html#streamOptimized"/></DiskSection><VirtualSystem><Name>Box</Name><VirtualHardwareSection><Item><ResourceType>3</ResourceType><VirtualQuantity>2</VirtualQuantity></Item><Item><ResourceType>4</ResourceType><VirtualQuantity>2048</VirtualQuantity><AllocationUnits>byte * 2^20</AllocationUnits></Item></VirtualHardwareSection></VirtualSystem></Envelope>`
	m, refs, err := ParseOVF(strings.NewReader(ovf))
	if err != nil || m.Identity.Name != "Box" || len(refs) != 1 || m.VM.CPUs != 2 {
		t.Fatalf("%v %+v %v", err, m, refs)
	}
	xml := `<domain><name>lv</name><memory unit="MiB">512</memory><vcpu>1</vcpu><os firmware="efi"><type machine="pc-q35-10.0">hvm</type></os><devices><disk type="file" device="disk"><driver type="qcow2"/><source file="/var/lib/libvirt/images/a.qcow2"/><target dev="vda" bus="virtio"/></disk><interface type="bridge"><mac address="52:54:00:12:34:56"/><source bridge="virbr0"/><model type="virtio"/></interface></devices></domain>`
	lm, err := ParseLibvirtXML(strings.NewReader(xml))
	if err != nil || lm.VM.Firmware != "uefi" || lm.VM.NICs[0].MAC == "" {
		t.Fatalf("%v %+v", err, lm)
	}
}

func TestProxmoxDiscoveryAndVMABlock(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"node": "pve"}}})
	})
	mux.HandleFunc("/api2/json/nodes/pve/qemu", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"vmid": 100, "name": "win", "status": "stopped", "cpus": 2, "maxmem": 4 << 30, "maxdisk": 20 << 30}}})
	})
	mux.HandleFunc("/api2/json/nodes/pve/lxc", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"vmid": 104, "name": "SoundDock", "status": "running", "cpus": 4, "maxmem": 8 << 30, "maxdisk": 120 << 30}}})
	})
	mux.HandleFunc("/api2/json/nodes/pve/storage", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"storage": "local-zfs", "type": "zfspool"}}})
	})
	mux.HandleFunc("/api2/json/nodes/pve/network", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"iface": "vmbr0", "type": "bridge"}}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c := &PVEClient{Base: ts.URL, Token: "user@pam!tok=secret", Insecure: true, Client: ts.Client()}
	d, err := c.DiscoverRemote()
	if err != nil || len(d.Workloads) != 2 {
		t.Fatalf("%v %+v", err, d)
	}
	bad := &PVEClient{Base: ts.URL, Insecure: true, Client: ts.Client()}
	if _, err := bad.DiscoverRemote(); err == nil {
		t.Fatal("permission")
	}
	cfg := map[string]any{"name": "win", "cores": 2.0, "memory": 4096.0, "bios": "ovmf", "scsi0": "local-zfs:vm-100-disk-0,size=32G", "net0": "virtio=BC:24:11:00:00:01,bridge=vmbr0,tag=20"}
	man := PVEManifest(KindVM, "pve", "100", cfg)
	if man.VM.Firmware != "uefi" || man.VM.Disks[0].Storage != "local-zfs" || man.VM.NICs[0].VLAN != 20 {
		t.Fatalf("%+v", man.VM)
	}
	caps := PVECapabilities(false, KindVM, "vma")
	if caps.Backup || caps.Live {
		t.Fatal("vma/live must be unavailable")
	}
	if !IsVMA("vzdump-qemu-100.vma.zst") {
		t.Fatal("vma")
	}
}

func TestReportDoesNotClaimApp(t *testing.T) {
	r := NewReport("SoundDock", ModeOffline, map[string]string{"Configuration": "Imported"}, []string{VerifyTransfer, VerifyConfig})
	if r.SourceSafety != SourceProtected || r.SourceChanges != "NONE" {
		t.Fatalf("%+v", r)
	}
	joined := strings.Join(r.Observed, ",")
	if strings.Contains(joined, VerifyApp) {
		t.Fatal("must not claim application verified")
	}
}

func TestStagingCleanupIsLocal(t *testing.T) {
	root := t.TempDir()
	dir, err := StagingDir(root, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveStaging(root, "job-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("staging remains")
	}
	if err := RemoveStaging(root, "../etc"); err == nil {
		t.Fatal("escape")
	}
}

func TestConvertNotRunWithoutRunner(t *testing.T) {
	e := &Engine{StagingRoot: t.TempDir()}
	if err := e.ConvertDisk(context.Background(), "/tmp/a.qcow2", "qcow2", "/tmp/b.qcow2", "qcow2"); err == nil {
		t.Fatal("must not fake convert")
	}
}

func TestIdentityConflict(t *testing.T) {
	item := ItemPlan{Mode: ModeOffline, Compatibility: CompatReady, StartAfter: true}
	err := Preflight(item, Caps{Offline: true}, PreflightEnv{SourceExists: true, SourceRunning: true, CredentialsOK: true, DestPoolExists: true, DestCapacityOK: true, DestNetExists: true, NameAvailable: true, ToolsOK: true, StagingOK: true})
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("offline+running: %v", err)
	}
	item.Mode = ModeDisk
	err = Preflight(item, Caps{Disk: true}, PreflightEnv{SourceExists: true, SourceRunning: true, CredentialsOK: true, DestPoolExists: true, DestCapacityOK: true, DestNetExists: true, NameAvailable: true, ToolsOK: true, StagingOK: true})
	if err == nil || !strings.Contains(err.Error(), "NETWORK IDENTITY CONFLICT") {
		t.Fatalf("conflict: %v", err)
	}
}

func TestInsufficientStorage(t *testing.T) {
	item := ItemPlan{Mode: ModeDisk, Compatibility: CompatReady}
	err := Preflight(item, Caps{Disk: true}, PreflightEnv{SourceExists: true, CredentialsOK: true, DestPoolExists: true, DestCapacityOK: false, DestNetExists: true, NameAvailable: true, ToolsOK: true, StagingOK: true, EstimatedBytes: 100, DestPoolBytes: 10})
	if err == nil {
		t.Fatal("capacity")
	}
}

func TestModesAreHonestAboutLiveAndSnapshot(t *testing.T) {
	for _, m := range Modes() {
		if m.ID == ModeLive || m.ID == ModeSnapshot {
			if m.Available {
				t.Fatalf("%s must not be listed as available without an implementation", m.ID)
			}
			if m.UnavailableReason == "" {
				t.Fatalf("%s needs an unavailable reason", m.ID)
			}
		}
	}
	info, _ := AdapterByID(AdapterProxmox)
	for _, id := range info.Modes {
		if id == ModeLive || id == ModeSnapshot {
			t.Fatalf("proxmox catalog must not list unimplemented mode %s", id)
		}
	}
}

func TestPVEDownloadFileAndRejectJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/nodes/pve/storage/local/content/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "json-vol") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"volid": "local:json-vol"}})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("QFI\xfbqcow-bytes"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c := &PVEClient{Base: ts.URL, Token: "t", Insecure: true, Client: ts.Client()}
	dir := t.TempDir()
	good := filepath.Join(dir, "disk.qcow2")
	if err := c.DownloadVolume("pve", "local", "local:100/vm-100-disk-0.qcow2", good); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(good)
	if !strings.Contains(string(body), "qcow-bytes") {
		t.Fatalf("got %q", body)
	}
	bad := filepath.Join(dir, "bad.bin")
	if err := c.DownloadVolume("pve", "local", "local:json-vol", bad); err == nil {
		t.Fatal("json metadata must not count as a disk")
	}
	if !StorageTypeDownloadable("dir") || StorageTypeDownloadable("lvmthin") {
		t.Fatal("storage type downloadable")
	}
}

func TestWriteTarRoundTrip(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "hello"), []byte("hi"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("hello", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	_ = unixSetxattr(filepath.Join(src, "hello"))
	tarPath := filepath.Join(t.TempDir(), "root.tar")
	if err := WriteTar(src, tarPath); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := ExtractArchiveFile(tarPath, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "hello"))
	if err != nil || string(got) != "hi" {
		t.Fatalf("file %v %s", err, got)
	}
	link, err := os.Readlink(filepath.Join(dest, "link"))
	if err != nil || link != "hello" {
		t.Fatalf("link %v %s", err, link)
	}
}

func unixSetxattr(path string) error {
	return nil
}
