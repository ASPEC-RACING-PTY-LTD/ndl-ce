package qemu

import (
	"fmt"
	"strings"

	"github.com/no-dal/ndl-ce/internal/gpu"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

const vfioFirstSlot = 0x1a

// ApplyVFIOHost rewrites frozen launch argv with typed vfio-pci host devices.
// An empty list removes VFIO devices. This is not a user-supplied QEMU argv string.
func (e *Engine) ApplyVFIOHost(id string, hosts []string) error {
	launch, err := e.ReadLaunch(id)
	if err != nil {
		return err
	}
	gpus := make([]vmspec.LaunchGPU, 0, len(hosts))
	slot := vfioFirstSlot
	for _, host := range hosts {
		addr, err := gpu.ParseGPUID(host)
		if err != nil {
			return err
		}
		if strings.ContainsAny(addr, ",=") {
			return fmt.Errorf("vfio host address contains a banned character")
		}
		pci := fmt.Sprintf("0x%x", slot)
		if err := vmspec.ValidatePCIAddr(pci); err != nil {
			return err
		}
		gpus = append(gpus, vmspec.LaunchGPU{Host: addr, PCIAddr: pci})
		slot++
	}
	launch.GPUs = gpus
	argv, err := e.CompileLaunch(launch)
	if err != nil {
		return err
	}
	return e.writeLaunch(launch, argv)
}
