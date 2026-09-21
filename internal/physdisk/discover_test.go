package physdisk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/inventory"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
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
	return root
}

func fixture(t *testing.T) inventory.FS {
	t.Helper()
	root := writeTree(t, map[string]string{
		"proc/mounts":                  "/dev/nvme0n1p2 / ext4 rw 0 0\n/dev/nvme0n1p1 /boot/efi vfat rw 0 0\n/dev/sdb1 /mnt/data ext4 rw 0 0\n",
		"proc/swaps":                   "Filename\tType\tSize\tUsed\tPriority\n/dev/nvme0n1p3 partition 1 0 -2\n",
		"sys/class/block/nvme0n1/size": "1953525168\n",
		"sys/class/block/nvme0n1/queue/rotational":      "0\n",
		"sys/class/block/nvme0n1/removable":             "0\n",
		"sys/class/block/nvme0n1/dev":                   "259:0\n",
		"sys/class/block/nvme0n1p1/partition":           "1\n",
		"sys/class/block/nvme0n1p1/size":                "204800\n",
		"sys/class/block/nvme0n1p1/dev":                 "259:1\n",
		"sys/class/block/nvme0n1p2/partition":           "2\n",
		"sys/class/block/nvme0n1p2/size":                "1000000\n",
		"sys/class/block/nvme0n1p3/partition":           "3\n",
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
		"run/udev/data/b8@0":                            "E:ID_FS_TYPE=\n",
	})
	return inventory.FS{Root: root}
}

func mustDev(t *testing.T, disks []Device, kernel string) Device {
	t.Helper()
	for _, d := range disks {
		if d.KernelName == kernel {
			return d
		}
	}
	t.Fatalf("missing %s", kernel)
	return Device{}
}

func TestDiscoverRejectsHostRootMountedSwapAndLVM(t *testing.T) {
	disks := Discover(fixture(t), HostHints{})
	root := mustDev(t, disks, "nvme0n1")
	if root.Eligible {
		t.Fatalf("root must be ineligible: %+v", root)
	}
	if !containsReason(root, "host root") && !containsReason(root, "swap") && !containsReason(root, "/boot") {
		t.Fatalf("root reasons %+v", root.Reasons)
	}

	mounted := mustDev(t, disks, "sdb")
	if mounted.Eligible {
		t.Fatal("mounted data disk")
	}
	if !containsReason(mounted, "Mounted") {
		t.Fatalf("mounted reasons %+v", mounted.Reasons)
	}

	lvm := mustDev(t, disks, "sdd")
	if lvm.Eligible {
		t.Fatal("lvm holder")
	}
}

func TestDiscoverEligibleUnusedSSD(t *testing.T) {
	disks := Discover(fixture(t), HostHints{})
	ssd := mustDev(t, disks, "sda")
	if !ssd.Eligible {
		t.Fatalf("unused ssd should be eligible: %+v", ssd.Reasons)
	}
	if ssd.ID != "ata-CT1000MX500SSD1_0001" {
		t.Fatalf("id=%q", ssd.ID)
	}
	if !ssd.ExistingData {
		t.Fatal("partitions should mark existing data")
	}
	foundNTFS, foundEFI := false, false
	for _, s := range ssd.FSSignatures {
		if s == "ntfs" {
			foundNTFS = true
		}
		if s == "efi" || s == "vfat" {
			foundEFI = true
		}
	}
	if !foundNTFS || !foundEFI {
		t.Fatalf("signatures %+v", ssd.FSSignatures)
	}
}

func TestDiscoverRejectsZFSPoolAssignmentAndMissingID(t *testing.T) {
	fs := fixture(t)
	disks := Discover(fs, HostHints{
		ZFSMembers: []string{"sda"},
		Assignments: []Assignment{{
			DeviceID: "ata-UNUSED", WorkloadID: "wl-1", WorkloadName: "windows-test",
		}},
		PoolDisks: []PoolDisk{{Kind: "ZFS", Name: "storage", Disk: "/dev/disk/by-id/ata-UNUSED"}},
	})
	zfs := mustDev(t, disks, "sda")
	if zfs.Eligible || !containsReason(zfs, "ZFS") {
		t.Fatalf("zfs %+v", zfs.Reasons)
	}
	used := mustDev(t, disks, "sdc")
	if used.Eligible {
		t.Fatalf("assigned/pool disk %+v", used.Reasons)
	}
	if !containsReason(used, "windows-test") && !containsReason(used, "storage") {
		t.Fatalf("assignment/pool reasons %+v", used.Reasons)
	}
}

func TestEvaluateMissingDevice(t *testing.T) {
	_, err := ResolvePath(fixture(t), "ata-NOT-THERE")
	if err == nil || !strings.Contains(err.Error(), "could not be resolved") {
		t.Fatalf("missing: %v", err)
	}
}

func containsReason(d Device, sub string) bool {
	for _, r := range d.Reasons {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}
