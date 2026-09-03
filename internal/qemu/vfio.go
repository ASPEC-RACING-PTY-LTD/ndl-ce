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

// MergeHostAddrs unions PCI BDFs without duplicates. Empty add returns current.
func MergeHostAddrs(current, add []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(current)+len(add))
	addOne := func(id string) {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range current {
		addOne(id)
	}
	for _, id := range add {
		addOne(id)
	}
	return out
}

// DropHostAddrs removes PCI BDFs from current. Empty drop returns current.
func DropHostAddrs(current, drop []string) []string {
	remove := map[string]struct{}{}
	for _, id := range drop {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			remove[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(current))
	seen := map[string]struct{}{}
	for _, id := range current {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		if _, ok := remove[id]; ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// HostAddrsFromLaunch returns typed vfio-pci host BDFs from frozen launch.
func HostAddrsFromLaunch(launch vmspec.Launch) []string {
	out := make([]string, 0, len(launch.GPUs))
	for _, g := range launch.GPUs {
		if h := strings.TrimSpace(g.Host); h != "" {
			out = append(out, h)
		}
	}
	return out
}
