package vmspec

import "testing"

func TestNormalizePhysicalBootDoesNotAddVolume(t *testing.T) {
	spec := Normalize(Spec{
		Name: "win",
		Disks: []Disk{{
			Role: DiskRoleBoot, Source: DiskSourcePhysical, DeviceID: "ata-CT1000MX500SSD1_0001",
		}},
		NICs: []NIC{{NetworkID: "11111111-1111-4111-8111-111111111111"}},
	})
	if len(spec.Disks) != 1 {
		t.Fatalf("disks %+v", spec.Disks)
	}
	if spec.Disks[0].Source != DiskSourcePhysical || spec.Disks[0].Format != "raw" || spec.Disks[0].Bus != DiskBusAHCI {
		t.Fatalf("%+v", spec.Disks[0])
	}
	if spec.Disks[0].VolumeID != "" {
		t.Fatal("physical disk must not become a volume")
	}
}

func TestValidatePhysicalDiskRules(t *testing.T) {
	net := "11111111-1111-4111-8111-111111111111"
	ok := Normalize(Spec{
		Name: "win",
		Disks: []Disk{{
			Role: DiskRoleBoot, Source: DiskSourcePhysical, DeviceID: "ata-CT1000MX500SSD1_0001",
		}},
		NICs: []NIC{{NetworkID: net}},
	})
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	dup := ok
	dup.Disks = append(dup.Disks, Disk{Role: DiskRoleData, Source: DiskSourcePhysical, DeviceID: "ata-CT1000MX500SSD1_0001"})
	if err := Validate(dup); err == nil {
		t.Fatal("duplicate device")
	}
}
