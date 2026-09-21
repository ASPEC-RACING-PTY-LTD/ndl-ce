package qemu

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/no-dal/ndl-ce/internal/gpu"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

// CompileLaunch turns a frozen Launch into typed QEMU argv. No shell strings.
func (e *Engine) CompileLaunch(launch vmspec.Launch) ([]string, error) {
	if err := vmspec.ValidateWorkloadID(launch.WorkloadID); err != nil {
		return nil, err
	}
	if err := vmspec.ValidateMachine(launch.Machine); err != nil {
		return nil, err
	}
	if launch.Accel != "kvm" && launch.Accel != "tcg" {
		return nil, fmt.Errorf("accel must be kvm or tcg")
	}
	if launch.CPUs < 1 {
		launch.CPUs = 1
	}
	if launch.MemoryMiB < 64 {
		launch.MemoryMiB = 128
	}
	if !launch.QGA {
		return nil, fmt.Errorf("qemu-ga channel is required")
	}
	qmp := e.qmpPath(launch.WorkloadID)
	serial := e.serialPath(launch.WorkloadID)
	vnc := e.vncPath(launch.WorkloadID)
	qga := e.qgaPath(launch.WorkloadID)
	nga := e.guestPath(launch.WorkloadID)
	for _, sock := range []string{qmp, serial, vnc, qga, nga} {
		if err := validateInterpolated("socket", sock); err != nil {
			return nil, err
		}
	}
	vga := launch.PCI["vga"]
	if vga == "" {
		vga = vmspec.PCIVGA
	}
	serialPCI := launch.PCI["serial"]
	if serialPCI == "" {
		serialPCI = vmspec.PCISerial
	}
	argv := []string{
		BinQEMU,
		"-name", launch.WorkloadID,
		"-machine", launch.Machine + ",usb=off",
		"-accel", launch.Accel,
		"-cpu", cpuForAccel(launch.Accel),
		"-smp", strconv.Itoa(launch.CPUs),
		"-m", strconv.FormatInt(launch.MemoryMiB, 10),
		"-nodefaults",
		"-no-user-config",
		"-display", "none",
		"-boot", "order=" + safeBoot(launch.BootOrder),
		"-device", "VGA,id=vga0,addr=" + vga,
		"-chardev", "socket,id=qmp0,path=" + qmp + ",server=on,wait=off",
		"-mon", "chardev=qmp0,mode=control",
	}
	if launch.Firmware.Mode == vmspec.FirmwareUEFI {
		if err := vmspec.ValidateCleanPath(launch.Firmware.CodePath, "firmware code"); err != nil {
			return nil, err
		}
		if err := vmspec.ValidateCleanPath(launch.Firmware.VarsPath, "firmware vars"); err != nil {
			return nil, err
		}
		argv = append(argv,
			"-drive", "if=pflash,format=raw,readonly=on,file="+launch.Firmware.CodePath,
			"-drive", "if=pflash,format=raw,file="+launch.Firmware.VarsPath,
		)
	}
	if launch.Console.Serial {
		argv = append(argv,
			"-chardev", "socket,id=serial0,path="+serial+",server=on,wait=off",
			"-serial", "chardev:serial0",
		)
	}
	if launch.Console.VNC {
		argv = append(argv, "-vnc", "unix:"+vnc)
	}
	argv = append(argv,
		"-chardev", "socket,id=qga0,path="+qga+",server=on,wait=off",
		"-device", "virtio-serial-pci,id=virtio-serial0,addr="+serialPCI,
		"-device", "virtserialport,chardev=qga0,name="+GuestAgentName,
		"-chardev", "socket,id=nga0,path="+nga+",server=on,wait=off",
		"-device", "virtserialport,chardev=nga0,name="+NodalGuestName,
	)
	if launch.Balloon {
		addr := launch.PCI["balloon"]
		if addr == "" {
			addr = vmspec.PCIBalloon
		}
		argv = append(argv, "-device", "virtio-balloon-pci,id=balloon0,addr="+addr)
	}
	ahciPort := 0
	for _, d := range launch.Disks {
		if err := validateLaunchDisk(launch.WorkloadID, d); err != nil {
			return nil, err
		}
		ro := ""
		if d.ReadOnly {
			ro = ",read-only=on"
		}
		fileNode := d.NodeName + "-file"
		fileDriver := "file"
		cache := ""
		discard := "unmap"
		if isPhysicalLaunchDisk(d) {
			fileDriver = "host_device"
			cache = ",cache.direct=on,aio=native"
			if !d.Discard {
				discard = "ignore"
			}
		}
		argv = append(argv,
			"-blockdev", fmt.Sprintf("driver=%s,node-name=%s,filename=%s,discard=%s%s%s", fileDriver, fileNode, d.Path, discard, cache, ro),
			"-blockdev", fmt.Sprintf("driver=%s,node-name=%s,file=%s%s", d.Format, d.NodeName, fileNode, ro),
		)
		switch {
		case d.Role == vmspec.DiskRoleCDROM:
			// q35 provides a native ICH9 AHCI controller. Attach installation
			// media to it so guests such as Windows PE can access the ISO
			// without requiring a VirtIO SCSI driver.
			dev := fmt.Sprintf("ide-cd,drive=%s,bus=ide.%d,id=%s", d.NodeName, ahciPort, d.NodeName)
			ahciPort++
			if idx := bootIndex(launch.BootOrder, 'd'); idx > 0 {
				dev += ",bootindex=" + strconv.Itoa(idx)
			}
			argv = append(argv, "-device", dev)
		case usesAHCI(d):
			dev := fmt.Sprintf("ide-hd,drive=%s,bus=ide.%d,id=%s", d.NodeName, ahciPort, d.NodeName)
			ahciPort++
			if serial := qemuSerial(d.Serial, d.DeviceID); serial != "" {
				dev += ",serial=" + serial
			}
			if d.Role == vmspec.DiskRoleBoot {
				if idx := bootIndex(launch.BootOrder, 'c'); idx > 0 {
					dev += ",bootindex=" + strconv.Itoa(idx)
				}
			}
			argv = append(argv, "-device", dev)
		default:
			if d.PCIAddr == "" {
				return nil, fmt.Errorf("disk %s is missing a pci address", d.NodeName)
			}
			if err := vmspec.ValidatePCIAddr(d.PCIAddr); err != nil {
				return nil, err
			}
			dev := "virtio-blk-pci,drive=" + d.NodeName + ",addr=" + d.PCIAddr
			if serial := qemuSerial(d.Serial, d.DeviceID); serial != "" {
				dev += ",serial=" + serial
			}
			if d.Role == vmspec.DiskRoleBoot {
				if idx := bootIndex(launch.BootOrder, 'c'); idx > 0 {
					dev += ",bootindex=" + strconv.Itoa(idx)
				}
			}
			argv = append(argv, "-device", dev)
		}
	}
	for i, g := range launch.GPUs {
		if err := vmspec.ValidatePCIAddr(g.PCIAddr); err != nil {
			return nil, err
		}
		if _, err := gpu.ParseGPUID(g.Host); err != nil {
			return nil, err
		}
		if strings.ContainsAny(g.Host, ",=") {
			return nil, fmt.Errorf("vfio host contains a banned character")
		}
		argv = append(argv, "-device", fmt.Sprintf("vfio-pci,host=%s,addr=%s,id=vfio%d", g.Host, g.PCIAddr, i))
	}
	argv = append(argv, "-device", "qemu-xhci,id=usb")
	for _, u := range launch.USBs {
		dev, err := usbHostDevice(u)
		if err != nil {
			return nil, err
		}
		argv = append(argv, "-device", dev)
	}
	for i, n := range launch.NICs {
		if err := vmspec.ValidateMAC(n.MAC); err != nil {
			return nil, err
		}
		if err := vmspec.ValidatePCIAddr(n.PCIAddr); err != nil {
			return nil, err
		}
		if n.TAPName == "" || strings.ContainsAny(n.TAPName, " \n\r,=") {
			return nil, fmt.Errorf("tap name is invalid")
		}
		netID := fmt.Sprintf("net%d", i)
		argv = append(argv,
			"-netdev", "tap,id="+netID+",ifname="+n.TAPName+",script=no,downscript=no",
			"-device", "virtio-net-pci,netdev="+netID+",mac="+n.MAC+",addr="+n.PCIAddr,
		)
	}
	for _, a := range argv {
		if strings.ContainsAny(a, "\n\r\x00") {
			return nil, fmt.Errorf("argv contains a banned character")
		}
	}
	if err := ValidateFrozenArgv(launch.WorkloadID, argv); err != nil {
		return nil, err
	}
	return argv, nil
}

func validateLaunchDisk(id string, d vmspec.LaunchDisk) error {
	if d.NodeName == "" || strings.ContainsAny(d.NodeName, ",=\n") {
		return fmt.Errorf("disk node-name is invalid")
	}
	if d.Format != "qcow2" && d.Format != "raw" {
		return fmt.Errorf("disk format must be qcow2 or raw")
	}
	if isPhysicalLaunchDisk(d) && d.Format != "raw" {
		return fmt.Errorf("physical disk format must be raw")
	}
	prefix := "/var/lib/ndl/runtime/qemu/" + id + "/"
	if strings.HasPrefix(d.Path, prefix) {
		return vmspec.ValidateCleanPath(d.Path, "disk path")
	}
	return ValidateDiskPath(d.Path)
}

func isPhysicalLaunchDisk(d vmspec.LaunchDisk) bool {
	return d.Source == vmspec.DiskSourcePhysical || strings.HasPrefix(d.Path, "/dev/disk/by-id/")
}

func usesAHCI(d vmspec.LaunchDisk) bool {
	if d.Role == vmspec.DiskRoleCDROM {
		return true
	}
	return isPhysicalLaunchDisk(d) && (d.Bus == "" || d.Bus == vmspec.DiskBusAHCI)
}

func qemuSerial(serial, deviceID string) string {
	s := strings.TrimSpace(serial)
	if s == "" {
		s = strings.TrimSpace(deviceID)
	}
	s = strings.ReplaceAll(s, "/", "")
	if strings.ContainsAny(s, ",=\n\r ") {
		return ""
	}
	if len(s) > 20 {
		s = s[:20]
	}
	return s
}

func bootIndex(order string, device byte) int {
	for i := 0; i < len(order); i++ {
		if order[i] == device {
			return i + 1
		}
	}
	return 0
}

func safeBoot(order string) string {
	out := make([]byte, 0, 2)
	for _, c := range order {
		if c == 'c' || c == 'd' {
			out = append(out, byte(c))
		}
	}
	if len(out) == 0 {
		return "c"
	}
	return string(out)
}

func (e *Engine) writeLaunch(launch vmspec.Launch, argv []string) error {
	if err := e.ensureDirs(launch.WorkloadID); err != nil {
		return err
	}
	argFile := ArgvFile{WorkloadID: launch.WorkloadID, Argv: argv, Launch: launch}
	raw, err := json.MarshalIndent(argFile, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(e.argvPath(launch.WorkloadID), append(raw, '\n'), 0o640); err != nil {
		return err
	}
	applied := Applied{
		SchemaVersion: LastAppliedSchema,
		Launch:        launch,
		Argv:          argv,
		AppliedAt:     e.now(),
	}
	b, err := json.MarshalIndent(applied, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(e.appliedPath(launch.WorkloadID), append(b, '\n'), 0o640)
}
