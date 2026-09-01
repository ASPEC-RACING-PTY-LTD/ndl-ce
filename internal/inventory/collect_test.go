package inventory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(encodeFixturePath(p)))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func missingTools(string) (string, error) {
	return "", errors.New("not on PATH")
}

func foundTools(name string) (string, error) {
	return `/usr/sbin/` + name, nil
}

func collectFixture(t *testing.T, root string, look func(string) (string, error)) Inventory {
	t.Helper()
	if look == nil {
		look = missingTools
	}
	return Collect(Options{
		FS:       FS{Root: root},
		Now:      time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		Arch:     "x86_64",
		LookPath: look,
	})
}

func mustCap(t *testing.T, caps []Capability, id string) Capability {
	t.Helper()
	for _, c := range caps {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("missing capability %q", id)
	return Capability{}
}

func mustBlock(t *testing.T, devs []BlockDevice, name string) BlockDevice {
	t.Helper()
	for _, d := range devs {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("missing block device %q", name)
	return BlockDevice{}
}

func mustNIC(t *testing.T, nics []NIC, name string) NIC {
	t.Helper()
	for _, n := range nics {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("missing nic %q", name)
	return NIC{}
}

func mustPCI(t *testing.T, devs []PCIDevice, addr string) PCIDevice {
	t.Helper()
	for _, d := range devs {
		if d.Address == addr {
			return d
		}
	}
	t.Fatalf("missing pci %q", addr)
	return PCIDevice{}
}

func richTree(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"etc/os-release": `PRETTY_NAME="Debian GNU/Linux 13 (trixie)"
NAME="Debian GNU/Linux"
VERSION_ID="13"
ID=debian
`,
		"proc/cpuinfo": `processor	: 0
vendor_id	: GenuineIntel
model name	: Intel(R) Xeon(R) Gold 6230 CPU @ 2.10GHz
flags		: fpu vme de pse tsc vmx

processor	: 1
vendor_id	: GenuineIntel
model name	: Intel(R) Xeon(R) Gold 6230 CPU @ 2.10GHz
flags		: fpu vmx

processor	: 2
vendor_id	: GenuineIntel
model name	: Intel(R) Xeon(R) Gold 6230 CPU @ 2.10GHz
flags		: fpu vmx

processor	: 3
vendor_id	: GenuineIntel
model name	: Intel(R) Xeon(R) Gold 6230 CPU @ 2.10GHz
flags		: fpu vmx
`,
		"sys/devices/system/cpu/online":                            "0-3\n",
		"sys/devices/system/cpu/cpu0/topology/physical_package_id": "0\n",
		"sys/devices/system/cpu/cpu0/topology/core_id":             "0\n",
		"sys/devices/system/cpu/cpu1/topology/physical_package_id": "0\n",
		"sys/devices/system/cpu/cpu1/topology/core_id":             "0\n",
		"sys/devices/system/cpu/cpu2/topology/physical_package_id": "1\n",
		"sys/devices/system/cpu/cpu2/topology/core_id":             "0\n",
		"sys/devices/system/cpu/cpu3/topology/physical_package_id": "1\n",
		"sys/devices/system/cpu/cpu3/topology/core_id":             "0\n",
		"proc/meminfo": `MemTotal:        8192000 kB
MemAvailable:    2048000 kB
`,
		"proc/mounts":                                       "/dev/nvme0n1 / ext4 rw,relatime 0 0\n",
		"sys/class/block/sda/size":                          "3907029168\n",
		"sys/class/block/sda/removable":                     "0\n",
		"sys/class/block/sda/queue/rotational":              "1\n",
		"sys/class/block/sda/queue/logical_block_size":      "512\n",
		"sys/class/block/sda/queue/physical_block_size":     "4096\n",
		"sys/class/block/sda/device/model":                  "ST2000\n",
		"sys/class/block/sda/device/vendor":                 "ATA\n",
		"sys/class/block/sda/device/serial":                 "Z9A123\n",
		"sys/class/block/sda/device/uevent":                 "DRIVER=ahci\n",
		"sys/class/block/sda1/partition":                    "1\n",
		"sys/class/block/sda1/size":                         "2048\n",
		"sys/class/block/nvme0n1/size":                      "1953525168\n",
		"sys/class/block/nvme0n1/removable":                 "0\n",
		"sys/class/block/nvme0n1/queue/rotational":          "0\n",
		"sys/class/block/nvme0n1/queue/logical_block_size":  "512\n",
		"sys/class/block/nvme0n1/queue/physical_block_size": "512\n",
		"sys/class/block/nvme0n1/device/model":              "SAMSUNG MZVL\n",
		"sys/class/block/nvme0n1/device/serial":             "S456\n",
		"sys/class/block/nvme0n1/device/uevent":             "DRIVER=nvme\n",
		"sys/class/block/vda/size":                          "20971520\n",
		"sys/class/block/vda/device/uevent":                 "DRIVER=virtio_blk\n",
		"sys/class/block/loop0/size":                        "0\n",
		"sys/class/block/ram0/size":                         "131072\n",
		"sys/class/block/fd0/size":                          "2880\n",
		"sys/class/net/lo/ifindex":                          "1\n",
		"sys/class/net/lo/address":                          "00:00:00:00:00:00\n",
		"sys/class/net/lo/mtu":                              "65536\n",
		"sys/class/net/lo/operstate":                        "unknown\n",
		"sys/class/net/lo/type":                             "772\n",
		"sys/class/net/eth0/ifindex":                        "2\n",
		"sys/class/net/eth0/address":                        "52:54:00:12:34:56\n",
		"sys/class/net/eth0/mtu":                            "1500\n",
		"sys/class/net/eth0/operstate":                      "up\n",
		"sys/class/net/eth0/speed":                          "1000\n",
		"sys/class/net/eth0/type":                           "1\n",
		"sys/class/net/eth0/device/uevent":                  "DRIVER=e1000e\nPCI_SLOT_NAME=0000:00:1f.6\n",
		"sys/class/net/virbr0/ifindex":                      "3\n",
		"sys/class/net/virbr0/address":                      "52:54:00:aa:bb:cc\n",
		"sys/class/net/virbr0/mtu":                          "1500\n",
		"sys/class/net/virbr0/operstate":                    "down\n",
		"sys/class/net/virbr0/type":                         "1\n",
		"sys/class/net/eth1/ifindex":                        "4\n",
		"sys/class/net/eth1/address":                        "52:54:00:00:00:01\n",
		"sys/class/net/eth1/mtu":                            "1500\n",
		"sys/class/net/eth1/operstate":                      "down\n",
		"sys/class/net/eth1/speed":                          "-1\n",
		"sys/class/net/eth1/type":                           "1\n",
		"sys/class/net/eth1/device/uevent":                  "DRIVER=e1000e\nPCI_SLOT_NAME=0000:00:1f.7\n",
		"sys/bus/pci/devices/0000:00:02.0/vendor":           "0x8086\n",
		"sys/bus/pci/devices/0000:00:02.0/device":           "0x3e92\n",
		"sys/bus/pci/devices/0000:00:02.0/class":            "0x030000\n",
		"sys/bus/pci/devices/0000:00:02.0/uevent":           "DRIVER=i915\nPCI_SLOT_NAME=0000:00:02.0\n",
		"sys/bus/pci/devices/0000:01:00.0/vendor":           "0x10de\n",
		"sys/bus/pci/devices/0000:01:00.0/device":           "0x1b80\n",
		"sys/bus/pci/devices/0000:01:00.0/class":            "0x030000\n",
		"sys/bus/pci/devices/0000:01:00.0/uevent":           "DRIVER=nvidia\nPCI_SLOT_NAME=0000:01:00.0\n",
		"sys/bus/pci/devices/0000:02:00.0/vendor":           "0x1002\n",
		"sys/bus/pci/devices/0000:02:00.0/device":           "0x73bf\n",
		"sys/bus/pci/devices/0000:02:00.0/class":            "0x030200\n",
		"sys/bus/pci/devices/0000:02:00.0/uevent":           "DRIVER=amdgpu\nPCI_SLOT_NAME=0000:02:00.0\n",
		"sys/bus/pci/devices/0000:00:1f.2/vendor":           "0x8086\n",
		"sys/bus/pci/devices/0000:00:1f.2/device":           "0x8c02\n",
		"sys/bus/pci/devices/0000:00:1f.2/class":            "0x010601\n",
		"sys/bus/pci/devices/0000:00:1f.2/uevent":           "DRIVER=ahci\nPCI_SLOT_NAME=0000:00:1f.2\n",
		"sys/kernel/iommu_groups/0/devices/0000:00:02.0":    "",
		"sys/kernel/iommu_groups/1/devices/0000:01:00.0":    "",
		"sys/kernel/iommu_groups/12/devices/0000:02:00.0":   "",
		"sys/kernel/iommu_groups/2/devices/0000:00:1f.2":    "",
		"sys/bus/usb/devices/1-2/idVendor":                  "046d\n",
		"sys/bus/usb/devices/1-2/idProduct":                 "c52b\n",
		"sys/bus/usb/devices/1-2/product":                   "USB Receiver\n",
		"sys/bus/usb/devices/1-2/manufacturer":              "Logitech\n",
		"sys/bus/usb/devices/1-2:1.0/bInterfaceClass":       "03\n",
		"sys/class/hwmon/hwmon0/name":                       "coretemp\n",
		"sys/class/hwmon/hwmon0/temp1_input":                "45000\n",
		"sys/class/hwmon/hwmon0/temp1_label":                "Package id 0\n",
		"sys/class/hwmon/hwmon0/temp2_input":                "43000\n",
		"sys/class/dmi/id/sys_vendor":                       "Dell Inc.\n",
		"sys/class/dmi/id/product_name":                     "PowerEdge R740\n",
		"sys/class/dmi/id/board_vendor":                     "Dell Inc.\n",
		"sys/class/dmi/id/board_name":                       "0XYZZY\n",
		"sys/class/dmi/id/bios_vendor":                      "Dell Inc.\n",
		"sys/class/dmi/id/bios_version":                     "2.15.0\n",
		"sys/class/dmi/id/bios_date":                        "01/01/2024\n",
		"sys/class/dmi/id/product_serial":                   "ABC123\n",
		"sys/class/dmi/id/product_uuid":                     "should-not-be-read\n",
		"dev/kvm":                                           "",
	})
}

func TestCollectDebian13Host(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if inv.Host.Status != StatusAvailable {
		t.Fatalf("host status=%q", inv.Host.Status)
	}
	if inv.Host.ID != "debian" {
		t.Fatalf("id=%q", inv.Host.ID)
	}
	if inv.Host.VersionID != "13" {
		t.Fatalf("version_id=%q", inv.Host.VersionID)
	}
	if inv.Host.Family != "debian" {
		t.Fatalf("family=%q", inv.Host.Family)
	}
	if inv.Host.Architecture != "amd64" {
		t.Fatalf("arch=%q", inv.Host.Architecture)
	}
	if inv.Host.SupportTier != "tier1" {
		t.Fatalf("tier=%q", inv.Host.SupportTier)
	}
	if !strings.Contains(inv.Host.PrettyName, "Debian") {
		t.Fatalf("pretty=%q", inv.Host.PrettyName)
	}
}

func TestCollectCPUTopology(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	c := inv.CPU
	if c.Status != StatusAvailable {
		t.Fatalf("status=%q", c.Status)
	}
	if c.Vendor != "GenuineIntel" {
		t.Fatalf("vendor=%q", c.Vendor)
	}
	if !strings.Contains(c.Model, "Xeon") {
		t.Fatalf("model=%q", c.Model)
	}
	if c.Architecture != "amd64" {
		t.Fatalf("arch=%q", c.Architecture)
	}
	if c.Sockets != 2 {
		t.Fatalf("sockets=%d", c.Sockets)
	}
	if c.Cores != 2 {
		t.Fatalf("cores=%d", c.Cores)
	}
	if c.Threads != 4 {
		t.Fatalf("threads=%d", c.Threads)
	}
	if c.Online != 4 {
		t.Fatalf("online=%d", c.Online)
	}
	if c.VirtCapability != "vmx" {
		t.Fatalf("virt=%q", c.VirtCapability)
	}
	if c.MaxMHz != nil {
		t.Fatalf("invented max mhz %v", *c.MaxMHz)
	}
}

func TestCollectNoFakeFrequency(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if inv.CPU.MaxMHz != nil {
		t.Fatal("max mhz must be omitted when cpufreq is missing")
	}
}

func TestCollectMaxMHzWhenPresent(t *testing.T) {
	root := writeTree(t, map[string]string{
		"proc/cpuinfo":                  "processor : 0\nvendor_id : GenuineIntel\nmodel name : Test CPU\nflags : vmx\n",
		"sys/devices/system/cpu/online": "0\n",
		"sys/devices/system/cpu/cpu0/topology/physical_package_id": "0\n",
		"sys/devices/system/cpu/cpu0/topology/core_id":             "0\n",
		"sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq":     "4000000\n",
	})
	inv := collectFixture(t, root, missingTools)
	if inv.CPU.MaxMHz == nil {
		t.Fatal("expected max mhz")
	}
	if *inv.CPU.MaxMHz != 4000 {
		t.Fatalf("max mhz=%v", *inv.CPU.MaxMHz)
	}
}

func TestCollectMemory(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	m := inv.Memory
	if m.Status != StatusAvailable {
		t.Fatalf("status=%q", m.Status)
	}
	if m.TotalBytes != 8192000*1024 {
		t.Fatalf("total=%d", m.TotalBytes)
	}
	if m.AvailableBytes == nil || *m.AvailableBytes != 2048000*1024 {
		t.Fatalf("available=%v", m.AvailableBytes)
	}
	wantUsed := uint64((8192000 - 2048000) * 1024)
	if m.UsedBytes == nil || *m.UsedBytes != wantUsed {
		t.Fatalf("used=%v", m.UsedBytes)
	}
	if m.DIMMStatus != StatusNotReported {
		t.Fatalf("dimm_status=%q", m.DIMMStatus)
	}
	if len(m.DIMMs) != 0 {
		t.Fatalf("dimms=%v", m.DIMMs)
	}
}

func TestCollectBlockNVMe(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if len(inv.BlockDevices) != 3 {
		t.Fatalf("devices=%d %+v", len(inv.BlockDevices), namesOf(inv.BlockDevices))
	}
	sda := mustBlock(t, inv.BlockDevices, "sda")
	if sda.Kind != "disk" || sda.Transport != "sata" {
		t.Fatalf("sda kind=%q transport=%q", sda.Kind, sda.Transport)
	}
	if sda.SizeBytes != 3907029168*512 {
		t.Fatalf("sda size=%d", sda.SizeBytes)
	}
	if sda.Rotational == nil || !*sda.Rotational {
		t.Fatalf("sda rotational=%v", sda.Rotational)
	}
	if sda.Removable == nil || *sda.Removable {
		t.Fatalf("sda removable=%v", sda.Removable)
	}
	if sda.LogicalBlock != 512 || sda.PhysicalBlock != 4096 {
		t.Fatalf("sda blocks %d/%d", sda.LogicalBlock, sda.PhysicalBlock)
	}
	if sda.Model != "ST2000" || sda.Vendor != "ATA" || sda.Serial != "Z9A123" {
		t.Fatalf("sda identity %+v", sda)
	}

	nvme := mustBlock(t, inv.BlockDevices, "nvme0n1")
	if nvme.Kind != "nvme" || nvme.Transport != "nvme" {
		t.Fatalf("nvme kind=%q transport=%q", nvme.Kind, nvme.Transport)
	}
	if nvme.MountHint != "/" {
		t.Fatalf("mount_hint=%q", nvme.MountHint)
	}
	if nvme.Rotational == nil || *nvme.Rotational {
		t.Fatalf("nvme rotational=%v", nvme.Rotational)
	}

	vda := mustBlock(t, inv.BlockDevices, "vda")
	if vda.Kind != "disk" || vda.Transport != "virtio" {
		t.Fatalf("vda kind=%q transport=%q", vda.Kind, vda.Transport)
	}
}

func namesOf(devs []BlockDevice) []string {
	out := make([]string, len(devs))
	for i, d := range devs {
		out[i] = d.Name
	}
	return out
}

func TestCollectSMARTNotHealthy(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if len(inv.BlockDevices) == 0 {
		t.Fatal("expected block devices")
	}
	for _, d := range inv.BlockDevices {
		if d.SMARTStatus != StatusNotReported {
			t.Fatalf("%s smart_status=%q", d.Name, d.SMARTStatus)
		}
		if d.SMARTStatus == StatusAvailable {
			t.Fatalf("%s SMART must not be available without a real result", d.Name)
		}
	}
}

func TestCollectNICs(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if len(inv.NICs) != 4 {
		t.Fatalf("nics=%d", len(inv.NICs))
	}
	lo := mustNIC(t, inv.NICs, "lo")
	if lo.Kind != "loopback" || lo.IfIndex != 1 {
		t.Fatalf("lo %+v", lo)
	}
	eth0 := mustNIC(t, inv.NICs, "eth0")
	if eth0.Kind != "physical" {
		t.Fatalf("eth0 kind=%q", eth0.Kind)
	}
	if eth0.Driver != "e1000e" || eth0.PCI != "0000:00:1f.6" {
		t.Fatalf("eth0 driver/pci %+v", eth0)
	}
	if eth0.MAC != "52:54:00:12:34:56" || eth0.MTU != 1500 || eth0.State != "up" {
		t.Fatalf("eth0 fields %+v", eth0)
	}
	if eth0.SpeedMbps == nil || *eth0.SpeedMbps != 1000 {
		t.Fatalf("eth0 speed=%v", eth0.SpeedMbps)
	}
	if len(eth0.Addresses) != 0 {
		t.Fatalf("addresses should stay empty: %v", eth0.Addresses)
	}
	vir := mustNIC(t, inv.NICs, "virbr0")
	if vir.Kind != "virtual" {
		t.Fatalf("virbr0 kind=%q", vir.Kind)
	}
	eth1 := mustNIC(t, inv.NICs, "eth1")
	if eth1.SpeedMbps != nil {
		t.Fatalf("invalid speed must be omitted, got %v", *eth1.SpeedMbps)
	}
}

func TestCollectPCI(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if len(inv.PCI) != 4 {
		t.Fatalf("pci=%d", len(inv.PCI))
	}
	igpu := mustPCI(t, inv.PCI, "0000:00:02.0")
	if igpu.Vendor != "0x8086" || igpu.Device != "0x3e92" || igpu.Class != "0x030000" {
		t.Fatalf("igpu %+v", igpu)
	}
	if igpu.Driver != "i915" || igpu.IOMMUGroup != "0" {
		t.Fatalf("igpu driver/iommu %+v", igpu)
	}
	sata := mustPCI(t, inv.PCI, "0000:00:1f.2")
	if sata.Driver != "ahci" || sata.IOMMUGroup != "2" {
		t.Fatalf("sata %+v", sata)
	}
}

func TestCollectUSB(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if len(inv.USB) != 1 {
		t.Fatalf("usb=%d %+v", len(inv.USB), inv.USB)
	}
	u := inv.USB[0]
	if u.Address != "1-2" || u.Vendor != "046d" || u.Product != "c52b" {
		t.Fatalf("usb %+v", u)
	}
	if u.Name != "USB Receiver" {
		t.Fatalf("name=%q", u.Name)
	}
}

func TestCollectGPUFromPCI(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if len(inv.GPUs) != 3 {
		t.Fatalf("gpus=%d %+v", len(inv.GPUs), inv.GPUs)
	}
	byID := map[string]GPU{}
	for _, g := range inv.GPUs {
		byID[g.ID] = g
	}
	intel := byID["0000:00:02.0"]
	if intel.Vendor != "Intel" || intel.Driver != "i915" || intel.IOMMUGroup != "0" {
		t.Fatalf("intel %+v", intel)
	}
	if intel.Model != "PCI 8086:3e92" || intel.PCI != "0000:00:02.0" {
		t.Fatalf("intel model/pci %+v", intel)
	}
	nvidia := byID["0000:01:00.0"]
	if nvidia.Vendor != "NVIDIA" || nvidia.Model != "PCI 10de:1b80" {
		t.Fatalf("nvidia %+v", nvidia)
	}
	amd := byID["0000:02:00.0"]
	if amd.Vendor != "AMD" || amd.Model != "PCI 1002:73bf" {
		t.Fatalf("amd %+v", amd)
	}
}

func TestCollectGPUEmptyValid(t *testing.T) {
	root := writeTree(t, map[string]string{
		"sys/bus/pci/devices/0000:00:1f.2/vendor": "0x8086\n",
		"sys/bus/pci/devices/0000:00:1f.2/device": "0x8c02\n",
		"sys/bus/pci/devices/0000:00:1f.2/class":  "0x010601\n",
		"sys/bus/pci/devices/0000:00:1f.2/uevent": "DRIVER=ahci\n",
	})
	inv := collectFixture(t, root, missingTools)
	if inv.GPUs == nil {
		inv.GPUs = []GPU{}
	}
	if len(inv.GPUs) != 0 {
		t.Fatalf("gpu absence must be valid, got %+v", inv.GPUs)
	}
	if mustCap(t, inv.Capabilities, "gpu").Status != StatusUnavailable {
		t.Fatalf("gpu cap=%q", mustCap(t, inv.Capabilities, "gpu").Status)
	}
}

func TestCollectIOMMU(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if inv.IOMMU.Status != StatusAvailable {
		t.Fatalf("status=%q", inv.IOMMU.Status)
	}
	if len(inv.IOMMU.Groups) != 4 {
		t.Fatalf("groups=%d", len(inv.IOMMU.Groups))
	}
	found := map[string][]string{}
	for _, g := range inv.IOMMU.Groups {
		found[g.ID] = g.Devices
	}
	if got := strings.Join(found["1"], ","); got != "0000:01:00.0" {
		t.Fatalf("group 1=%q", got)
	}
	if got := strings.Join(found["12"], ","); got != "0000:02:00.0" {
		t.Fatalf("group 12=%q", got)
	}
}

func TestCollectHwmon(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if len(inv.Temperatures) != 2 {
		t.Fatalf("temps=%d %+v", len(inv.Temperatures), inv.Temperatures)
	}
	var pkg Sensor
	for _, s := range inv.Temperatures {
		if s.Label == "Package id 0" {
			pkg = s
		}
		if s.Status != StatusAvailable || s.MilliC == nil {
			t.Fatalf("sensor %+v", s)
		}
	}
	if pkg.ID != "hwmon0/temp1" || pkg.Name != "coretemp" || *pkg.MilliC != 45000 {
		t.Fatalf("package sensor %+v", pkg)
	}
}

func TestCollectDMI(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	fw := inv.Firmware
	if fw.Status != StatusAvailable {
		t.Fatalf("status=%q", fw.Status)
	}
	if fw.SysVendor != "Dell Inc." || fw.Product != "PowerEdge R740" {
		t.Fatalf("product %+v", fw)
	}
	if fw.BoardVendor != "Dell Inc." || fw.Board != "0XYZZY" {
		t.Fatalf("board %+v", fw)
	}
	if fw.BIOSVendor != "Dell Inc." || fw.BIOSVersion != "2.15.0" || fw.BIOSDate != "01/01/2024" {
		t.Fatalf("bios %+v", fw)
	}
	if fw.ProductSerial != "ABC123" {
		t.Fatalf("serial=%q", fw.ProductSerial)
	}
	if strings.Contains(fw.ProductSerial, "should-not-be-read") || strings.Contains(fw.Note, "should-not-be-read") {
		t.Fatal("must not read product_uuid")
	}
}

func TestCollectMissingTools(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	smart := mustCap(t, inv.Capabilities, "smart_tool")
	if smart.Status != StatusNotReported {
		t.Fatalf("smart=%q", smart.Status)
	}
	nvme := mustCap(t, inv.Capabilities, "nvme_cli")
	if nvme.Status != StatusNotReported {
		t.Fatalf("nvme_cli=%q", nvme.Status)
	}
}

func TestCollectToolsPresent(t *testing.T) {
	inv := collectFixture(t, richTree(t), foundTools)
	if mustCap(t, inv.Capabilities, "smart_tool").Status != StatusAvailable {
		t.Fatalf("smart=%q", mustCap(t, inv.Capabilities, "smart_tool").Status)
	}
	if mustCap(t, inv.Capabilities, "nvme_cli").Status != StatusAvailable {
		t.Fatalf("nvme_cli=%q", mustCap(t, inv.Capabilities, "nvme_cli").Status)
	}
	for _, d := range inv.BlockDevices {
		if d.SMARTStatus != StatusNotReported {
			t.Fatalf("tool presence must not mark %s SMART healthy: %q", d.Name, d.SMARTStatus)
		}
	}
}

func TestCollectEmptyRootHonest(t *testing.T) {
	inv := collectFixture(t, t.TempDir(), missingTools)
	if inv.SchemaVersion != SchemaVersion {
		t.Fatalf("schema=%q", inv.SchemaVersion)
	}
	if inv.Host.Status != StatusUnavailable {
		t.Fatalf("host=%q", inv.Host.Status)
	}
	if inv.CPU.Status != StatusUnavailable {
		t.Fatalf("cpu=%q", inv.CPU.Status)
	}
	if inv.CPU.Sockets != 0 || inv.CPU.Cores != 0 || inv.CPU.Threads != 0 {
		t.Fatalf("cpu invented topology %+v", inv.CPU)
	}
	if inv.CPU.MaxMHz != nil {
		t.Fatal("empty root invented frequency")
	}
	if inv.Memory.Status != StatusUnavailable {
		t.Fatalf("memory=%q", inv.Memory.Status)
	}
	if inv.Memory.TotalBytes != 0 {
		t.Fatalf("memory invented total=%d", inv.Memory.TotalBytes)
	}
	if inv.Memory.DIMMStatus != StatusNotReported {
		t.Fatalf("dimm=%q", inv.Memory.DIMMStatus)
	}
	if len(inv.BlockDevices) != 0 || len(inv.NICs) != 0 || len(inv.PCI) != 0 || len(inv.USB) != 0 || len(inv.GPUs) != 0 {
		t.Fatalf("empty lists expected")
	}
	if inv.IOMMU.Status != StatusUnavailable {
		t.Fatalf("iommu=%q", inv.IOMMU.Status)
	}
	if len(inv.Temperatures) != 0 {
		t.Fatalf("temps=%v", inv.Temperatures)
	}
	if inv.Firmware.Status != StatusUnavailable {
		t.Fatalf("firmware=%q", inv.Firmware.Status)
	}
	if mustCap(t, inv.Capabilities, "kvm").Status != StatusUnavailable {
		t.Fatalf("kvm=%q", mustCap(t, inv.Capabilities, "kvm").Status)
	}
	if mustCap(t, inv.Capabilities, "iommu").Status != StatusUnavailable {
		t.Fatalf("iommu cap=%q", mustCap(t, inv.Capabilities, "iommu").Status)
	}
	if mustCap(t, inv.Capabilities, "gpu").Status != StatusUnavailable {
		t.Fatalf("gpu=%q", mustCap(t, inv.Capabilities, "gpu").Status)
	}
	if mustCap(t, inv.Capabilities, "hwmon").Status != StatusUnavailable {
		t.Fatalf("hwmon=%q", mustCap(t, inv.Capabilities, "hwmon").Status)
	}
	if mustCap(t, inv.Capabilities, "smart_tool").Status != StatusNotReported {
		t.Fatalf("smart=%q", mustCap(t, inv.Capabilities, "smart_tool").Status)
	}
	if mustCap(t, inv.Capabilities, "nvme_cli").Status != StatusNotReported {
		t.Fatalf("nvme=%q", mustCap(t, inv.Capabilities, "nvme_cli").Status)
	}
	if mustCap(t, inv.Capabilities, "virt_extensions").Status != StatusNotReported {
		t.Fatalf("virt=%q", mustCap(t, inv.Capabilities, "virt_extensions").Status)
	}
}

func TestDeriveCapabilities(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if mustCap(t, inv.Capabilities, "kvm").Status != StatusAvailable {
		t.Fatal("kvm")
	}
	if mustCap(t, inv.Capabilities, "iommu").Status != StatusAvailable {
		t.Fatal("iommu")
	}
	if mustCap(t, inv.Capabilities, "gpu").Status != StatusAvailable {
		t.Fatal("gpu")
	}
	if mustCap(t, inv.Capabilities, "hwmon").Status != StatusAvailable {
		t.Fatal("hwmon")
	}
	virt := mustCap(t, inv.Capabilities, "virt_extensions")
	if virt.Status != StatusAvailable || virt.Detail != "vmx" {
		t.Fatalf("virt %+v", virt)
	}
	for _, c := range inv.Capabilities {
		if c.ID == "vm" || c.ID == "lxc" {
			t.Fatalf("must not invent subsystem capability %q", c.ID)
		}
	}
}

func TestCollectUnsupportedHostStillAvailable(t *testing.T) {
	root := writeTree(t, map[string]string{
		"etc/os-release": "ID=fedora\nVERSION_ID=42\nPRETTY_NAME=\"Fedora Linux 42\"\n",
	})
	inv := collectFixture(t, root, missingTools)
	if inv.Host.Status != StatusAvailable {
		t.Fatalf("parsed host must be available, got %q", inv.Host.Status)
	}
	if inv.Host.ID != "fedora" || inv.Host.VersionID != "42" {
		t.Fatalf("host %+v", inv.Host)
	}
	if inv.Host.SupportTier != "unsupported" {
		t.Fatalf("tier=%q", inv.Host.SupportTier)
	}
}

func TestCollectAMDSVM(t *testing.T) {
	root := writeTree(t, map[string]string{
		"proc/cpuinfo": `processor : 0
vendor_id : AuthenticAMD
model name : AMD EPYC
flags : fpu svm
`,
		"sys/devices/system/cpu/online":                            "0\n",
		"sys/devices/system/cpu/cpu0/topology/physical_package_id": "0\n",
		"sys/devices/system/cpu/cpu0/topology/core_id":             "0\n",
	})
	inv := collectFixture(t, root, missingTools)
	if inv.CPU.VirtCapability != "svm" {
		t.Fatalf("virt=%q", inv.CPU.VirtCapability)
	}
	if mustCap(t, inv.Capabilities, "virt_extensions").Detail != "svm" {
		t.Fatalf("cap=%+v", mustCap(t, inv.Capabilities, "virt_extensions"))
	}
}

func TestCollectPartialCPUNoPanic(t *testing.T) {
	root := writeTree(t, map[string]string{
		"proc/cpuinfo": "processor : 0\nvendor_id : GenuineIntel\nmodel name : Partial\nflags : pse\n",
	})
	inv := collectFixture(t, root, missingTools)
	if inv.CPU.Status != StatusAvailable {
		t.Fatalf("status=%q", inv.CPU.Status)
	}
	if inv.CPU.Threads != 1 {
		t.Fatalf("threads=%d", inv.CPU.Threads)
	}
	if inv.CPU.Sockets != 0 || inv.CPU.Cores != 0 {
		t.Fatalf("must not invent topology %+v", inv.CPU)
	}
	if inv.CPU.VirtCapability != "" {
		t.Fatalf("virt=%q", inv.CPU.VirtCapability)
	}
}

func TestCollectIOMMUMissing(t *testing.T) {
	root := writeTree(t, map[string]string{
		"proc/cpuinfo": "processor : 0\nvendor_id : GenuineIntel\n",
	})
	inv := collectFixture(t, root, missingTools)
	if inv.IOMMU.Status != StatusUnavailable {
		t.Fatalf("iommu=%q", inv.IOMMU.Status)
	}
	if len(inv.IOMMU.Groups) != 0 {
		t.Fatalf("groups=%v", inv.IOMMU.Groups)
	}
}

func TestCollectHwmonDirWithoutTemps(t *testing.T) {
	root := writeTree(t, map[string]string{
		"sys/class/hwmon/hwmon0/name": "coretemp\n",
	})
	inv := collectFixture(t, root, missingTools)
	if len(inv.Temperatures) != 0 {
		t.Fatalf("must not invent temps: %+v", inv.Temperatures)
	}
	if mustCap(t, inv.Capabilities, "hwmon").Status != StatusAvailable {
		t.Fatal("hwmon dir exists")
	}
}

func TestCollectObservedAtAndSchema(t *testing.T) {
	inv := collectFixture(t, richTree(t), missingTools)
	if inv.SchemaVersion != SchemaVersion {
		t.Fatalf("schema=%q", inv.SchemaVersion)
	}
	want := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if !inv.ObservedAt.Equal(want) {
		t.Fatalf("observed=%v", inv.ObservedAt)
	}
}
