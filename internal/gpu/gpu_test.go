package gpu

import (
	"testing"

	"github.com/no-dal/ndl-ce/internal/inventory"
)

func TestParseGPUIDRejectsAll(t *testing.T) {
	if _, err := ParseGPUID("all"); err == nil {
		t.Fatal("gpu=all must be refused")
	}
	if _, err := ParseGPUID("ALL"); err == nil {
		t.Fatal("ALL")
	}
	id, err := ParseGPUID("0000:02:00.0")
	if err != nil || id != "0000:02:00.0" {
		t.Fatalf("%q %v", id, err)
	}
}

func TestGroupListsHDMIAudio(t *testing.T) {
	inv := inventory.Inventory{
		GPUs: []inventory.GPU{{ID: "0000:02:00.0", PCI: "0000:02:00.0", IOMMUGroup: "12"}},
		PCI: []inventory.PCIDevice{
			{Address: "0000:02:00.0", Class: "0x030000", IOMMUGroup: "12"},
			{Address: "0000:02:00.1", Class: "0x040300", IOMMUGroup: "12"},
		},
		IOMMU: inventory.IOMMU{
			Status: inventory.StatusAvailable,
			Groups: []inventory.IOMMUGroup{{ID: "12", Devices: []string{"0000:02:00.0", "0000:02:00.1"}}},
		},
	}
	gid, members, err := GroupMembers("0000:02:00.0", inv)
	if err != nil || gid != "12" || len(members) != 2 {
		t.Fatalf("%s %d %v", gid, len(members), err)
	}
	kinds := map[string]string{}
	for _, m := range members {
		kinds[m.Address] = MemberKind(m.Class)
	}
	if kinds["0000:02:00.0"] != "display" || kinds["0000:02:00.1"] != "audio" {
		t.Fatalf("%v", kinds)
	}
}

func TestRefuseACSAndDeviceNodes(t *testing.T) {
	if err := RefuseACSOverride(true); err == nil {
		t.Fatal("acs")
	}
	if !AllowDeviceNode("/dev/dri/renderD128") || AllowDeviceNode("/dev/sda") || AllowDeviceNode("/etc/passwd") {
		t.Fatal("device allowlist")
	}
	if !ExclusiveForMode(ModeVFIO, false) || ExclusiveForMode(ModeRender, false) {
		t.Fatal("exclusive")
	}
	if !Conflicts(true, "0000:02:00.0", "0000:02:00.0", false) {
		t.Fatal("exclusive conflict")
	}
}
