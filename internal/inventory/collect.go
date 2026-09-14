package inventory

import (
	"os/exec"
	"runtime"
	"time"
)

// Options configures a fixture or live collection.
type Options struct {
	FS       FS
	Now      time.Time
	Arch     string
	LookPath func(string) (string, error)
}

func (o Options) now() time.Time {
	if o.Now.IsZero() {
		return time.Now().UTC()
	}
	return o.Now.UTC()
}

func (o Options) arch() string {
	if o.Arch != "" {
		return o.Arch
	}
	return runtime.GOARCH
}

func (o Options) lookPath(name string) (string, error) {
	if o.LookPath != nil {
		return o.LookPath(name)
	}
	return exec.LookPath(name)
}

func (o Options) fs() FS {
	if o.FS.Root == "" {
		return Live()
	}
	return o.FS
}

// Collect reads observed hardware. Partial failure never invents values.
func Collect(opt Options) Inventory {
	fs := opt.fs()
	opt.FS = fs
	inv := Inventory{
		SchemaVersion: SchemaVersion,
		ObservedAt:    opt.now(),
	}
	inv.Host = collectHost(opt)
	inv.CPU = collectCPU(opt)
	inv.Memory = collectMemory(opt)
	inv.BlockDevices = collectBlock(opt)
	inv.NICs = collectNICs(opt)
	inv.PCI = collectPCI(opt)
	inv.USB = collectUSB(opt)
	inv.GPUs = collectGPUs(opt, inv.PCI)
	inv.IOMMU = collectIOMMU(opt)
	inv.Temperatures = collectTemps(opt)
	inv.Firmware = collectFirmware(opt)
	inv.Capabilities = deriveCapabilities(opt, inv)
	return inv
}
