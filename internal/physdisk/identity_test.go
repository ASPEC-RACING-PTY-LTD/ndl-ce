package physdisk

import "testing"

func TestParseDeviceIDRejectsKernelAndPartitions(t *testing.T) {
	if _, err := ParseDeviceID("/dev/sda"); err == nil {
		t.Fatal("kernel path")
	}
	if _, err := ParseDeviceID("ata-disk-part1"); err == nil {
		t.Fatal("partition")
	}
	if _, err := ParseDeviceID(""); err == nil {
		t.Fatal("empty")
	}
	id, err := ParseDeviceID("/dev/disk/by-id/ata-CT1000MX500SSD1_0001")
	if err != nil || id != "ata-CT1000MX500SSD1_0001" {
		t.Fatalf("got %q %v", id, err)
	}
	if id.Path() != "/dev/disk/by-id/ata-CT1000MX500SSD1_0001" {
		t.Fatal(id.Path())
	}
}

func TestPreferIDPrefersWWNOverATA(t *testing.T) {
	got := PreferID([]string{"ata-disk", "wwn-0x5000", "ata-disk-part1"})
	if got != "wwn-0x5000" {
		t.Fatalf("got %q", got)
	}
}
