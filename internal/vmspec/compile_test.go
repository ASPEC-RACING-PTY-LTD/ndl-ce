package vmspec

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompileDeterministicPCIAndMAC(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	nicID := "22222222-2222-4222-8222-222222222222"
	spec := Normalize(Spec{
		Name: "web", CPUs: 2, MemoryBytes: 512 << 20, Firmware: FirmwareBIOS,
		NICs:    []NIC{{ID: nicID, NetworkID: "33333333-3333-4333-8333-333333333333"}},
		NoCloud: NoCloud{Enable: true, Hostname: "web", Username: "debian"},
	})
	resolved := Resolved{
		Accel: "tcg",
		Disks: []ResolvedDisk{{
			VolumeID: "44444444-4444-4444-8444-444444444444",
			Role:     DiskRoleBoot, Path: "/var/lib/ndl/storage/local/volumes/vm-disk/44444444-4444-4444-8444-444444444444.qcow2",
			Format: "qcow2",
		}},
		NICs: []ResolvedNIC{{
			ID: nicID, NetworkID: spec.NICs[0].NetworkID, BridgeName: "ndlabcdef01",
			MAC: MACFromID(nicID),
		}},
	}
	a, err := Compile(id, spec, resolved)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Compile(id, spec, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if a.NICs[0].MAC != b.NICs[0].MAC || a.NICs[0].MAC != MACFromID(nicID) {
		t.Fatalf("mac persistence failed %s %s", a.NICs[0].MAC, b.NICs[0].MAC)
	}
	if a.Disks[0].PCIAddr != b.Disks[0].PCIAddr || a.NICs[0].PCIAddr != b.NICs[0].PCIAddr {
		t.Fatal("pci must be deterministic")
	}
	if a.Disks[0].PCIAddr == a.NICs[0].PCIAddr {
		t.Fatal("disk and nic must not share a pci slot")
	}
	if a.NICs[0].TAPName != TAPName(id, 0) {
		t.Fatalf("tap %s", a.NICs[0].TAPName)
	}
	if !a.QGA || a.Machine != DefaultMachine {
		t.Fatal("defaults")
	}
	if a.NoCloud == nil || a.NoCloud.ImagePath == "" {
		t.Fatal("cidata")
	}
}

func TestCompileRejectsMachineAliasAndPathInjection(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	netID := "33333333-3333-4333-8333-333333333333"
	base := Spec{Name: "x", NICs: []NIC{{NetworkID: netID}}}
	resolved := Resolved{
		Accel: "tcg",
		Disks: []ResolvedDisk{{Role: DiskRoleBoot, Path: "/var/lib/ndl/storage/p/volumes/vm-disk/d.qcow2", Format: "qcow2"}},
		NICs:  []ResolvedNIC{{NetworkID: netID, BridgeName: "ndl0", MAC: "02:00:00:00:00:01"}},
	}
	bad := base
	bad.Machine = "q35"
	if _, err := Compile(id, bad, resolved); err == nil {
		t.Fatal("q35 alias")
	}
	resolved.Disks[0].Path = "/etc/passwd"
	if _, err := Compile(id, base, resolved); err == nil {
		t.Fatal("etc passwd")
	}
	resolved.Disks[0].Path = "/var/lib/ndl/storage/p/d.qcow2,driver=raw"
	if _, err := Compile(id, base, resolved); err == nil {
		t.Fatal("comma injection")
	}
}

func TestCompileRejectsUnavailableStorage(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	spec := Spec{Name: "x", NICs: []NIC{{NetworkID: "33333333-3333-4333-8333-333333333333"}}}
	if _, err := Compile(id, spec, Resolved{Accel: "tcg"}); err == nil {
		t.Fatal("missing disks")
	}
}

func TestRedactHidesPassword(t *testing.T) {
	spec := Normalize(Spec{Name: "x", NoCloud: NoCloud{Enable: true, Password: "secret", Username: "debian"}})
	out := Redact(spec)
	if out.NoCloud.Password != "" {
		t.Fatal("password leaked")
	}
	if !out.NoCloud.HasPassword {
		t.Fatal("has_password")
	}
	raw := Normalize(Spec{Name: "x", NoCloud: NoCloud{Enable: true, UserData: "#cloud-config\nchpasswd:\n  list: |\n    debian:hunter2\n"}})
	stripped := Redact(raw)
	if strings.Contains(stripped.NoCloud.UserData, "hunter2") {
		t.Fatal("chpasswd user-data leaked")
	}
	if !stripped.NoCloud.HasPassword {
		t.Fatal("raw secret must set has_password")
	}
}

func TestFATContainsNoCloudFiles(t *testing.T) {
	img, err := BuildCIDATA(map[string][]byte{
		"user-data": []byte("#cloud-config\n"),
		"meta-data": []byte("instance-id: x\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(img) != fatTotalSectors*fatBytesPerSector {
		t.Fatal(len(img))
	}
	if string(img[43:54]) != "cidata     " {
		t.Fatalf("label %q", img[43:54])
	}
	if !bytes.Contains(img, []byte("#cloud-config")) {
		t.Fatal("user-data body missing")
	}
	if !bytes.Contains(img, []byte("instance-id: x")) {
		t.Fatal("meta-data body missing")
	}
}

func TestClassifyCPURequiresRestartDiskRequiresStop(t *testing.T) {
	prev := Normalize(Spec{Name: "a", CPUs: 1, NICs: []NIC{{NetworkID: "33333333-3333-4333-8333-333333333333"}}})
	next := prev
	next.CPUs = 4
	cls := ClassifyEdit(prev, next)
	if !RequiresRestart(cls) || RequiresStop(cls) {
		t.Fatalf("%v", cls)
	}
	next = prev
	next.ISOLibraryID = "55555555-5555-4555-8555-555555555555"
	cls = ClassifyEdit(prev, next)
	if !RequiresStop(cls) {
		t.Fatal("iso change must require stop")
	}
	next = prev
	next.Machine = "pc-q35-9.2"
	cls = ClassifyEdit(prev, next)
	if !HasUnsupported(cls) {
		t.Fatal("machine ABI change is unsupported")
	}
}

func TestMACStableAcrossCompile(t *testing.T) {
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	a := MACFromID(id)
	b := MACFromID(id)
	if a != b {
		t.Fatal(a, b)
	}
	if err := ValidateMAC(a); err != nil {
		t.Fatal(err)
	}
	if len(a) != 17 {
		t.Fatal(a)
	}
}

func TestCompileAcceptsZVolRawDisk(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	netID := "33333333-3333-4333-8333-333333333333"
	resolved := Resolved{
		Accel: "tcg",
		Disks: []ResolvedDisk{{
			Role: DiskRoleBoot, Path: "/dev/zvol/tank/" + id, Format: "raw",
		}},
		NICs: []ResolvedNIC{{NetworkID: netID, BridgeName: "ndl0", MAC: "02:00:00:00:00:01"}},
	}
	launch, err := Compile(id, Spec{Name: "z", NICs: []NIC{{NetworkID: netID}}}, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if launch.Disks[0].Format != "raw" || launch.Disks[0].Path != "/dev/zvol/tank/"+id {
		t.Fatalf("%+v", launch.Disks[0])
	}
	resolved.Disks[0].Path = "/dev/sda"
	if _, err := Compile(id, Spec{Name: "z", NICs: []NIC{{NetworkID: netID}}}, resolved); err == nil {
		t.Fatal("generic /dev")
	}
	rbd := "/dev/rbd/rbd/" + id
	resolved.Disks[0].Path = rbd
	resolved.Disks[0].Format = "raw"
	launch, err = Compile(id, Spec{Name: "z", NICs: []NIC{{NetworkID: netID}}}, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if launch.Disks[0].Path != rbd || launch.Disks[0].Format != "raw" {
		t.Fatalf("rbd %+v", launch.Disks[0])
	}
	lv := "/dev/ndlvg/" + id
	resolved.Disks[0].Path = lv
	launch, err = Compile(id, Spec{Name: "z", NICs: []NIC{{NetworkID: netID}}}, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if launch.Disks[0].Path != lv || launch.Disks[0].Format != "raw" {
		t.Fatalf("lvm %+v", launch.Disks[0])
	}
}
