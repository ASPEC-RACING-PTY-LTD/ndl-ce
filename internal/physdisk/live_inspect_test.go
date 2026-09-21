package physdisk

import (
	"os"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/inventory"
)

func TestLiveMX500ReadOnly(t *testing.T) {
	if os.Getenv("NDL_LIVE_PHYSDISK") == "" {
		t.Skip("set NDL_LIVE_PHYSDISK=1 to inspect the live host")
	}
	disks := Discover(inventory.Live(), HostHints{})
	var mx Device
	for _, d := range disks {
		if strings.Contains(d.Model, "MX500") || strings.Contains(string(d.ID), "MX500") || d.KernelName == "sdb" {
			mx = d
			break
		}
	}
	if mx.ID == "" {
		t.Fatal("MX500 was not discovered")
	}
	t.Logf("id=%s path=%s model=%s serial=%s size=%d eligible=%v reasons=%v signatures=%v mounted=%v",
		mx.ID, mx.ByIDPath, mx.Model, mx.Serial, mx.SizeBytes, mx.Eligible, mx.Reasons, mx.FSSignatures, mx.Mounted)
	if mx.HostDisk {
		t.Fatal("MX500 must not be classified as the host disk")
	}
	if len(mx.Mounted) > 0 {
		t.Fatalf("MX500 is mounted: %v", mx.Mounted)
	}
	if !mx.Eligible {
		t.Fatalf("MX500 should be eligible: %v", mx.Reasons)
	}
}
