package qemu

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func (e *Engine) compile(spec Spec) ([]string, error) {
	if err := ValidateWorkloadID(spec.WorkloadID); err != nil {
		return nil, err
	}
	if err := ValidateDiskPath(spec.DiskPath); err != nil {
		return nil, err
	}
	machine := spec.Machine
	if machine == "" {
		machine = DefaultMachine
	}
	if err := validateMachine(machine); err != nil {
		return nil, err
	}
	accel := spec.Accel
	if accel == "" {
		accel = DetectAccel()
	}
	if accel != "kvm" && accel != "tcg" {
		return nil, fmt.Errorf("accel must be kvm or tcg")
	}
	memMB := spec.MemoryBytes / (1 << 20)
	if memMB < 64 {
		memMB = 128
	}
	cpus := spec.CPUs
	if cpus < 1 {
		cpus = 1
	}
	diskAddr := spec.PCIDiskAddr
	if diskAddr == "" {
		diskAddr = "0x5"
	}
	serialAddr := spec.PCISerialAddr
	if serialAddr == "" {
		serialAddr = "0x6"
	}
	if err := validatePCIAddr(diskAddr); err != nil {
		return nil, fmt.Errorf("pci disk addr: %w", err)
	}
	if err := validatePCIAddr(serialAddr); err != nil {
		return nil, fmt.Errorf("pci serial addr: %w", err)
	}
	format := spec.DiskFormat
	if format == "" {
		format = "qcow2"
	}
	if err := validateDiskFormat(format); err != nil {
		return nil, err
	}
	if err := validateInterpolated("accel", accel); err != nil {
		return nil, err
	}
	qmp := e.qmpPath(spec.WorkloadID)
	serial := e.serialPath(spec.WorkloadID)
	vnc := e.vncPath(spec.WorkloadID)
	qga := e.qgaPath(spec.WorkloadID)
	nga := e.guestPath(spec.WorkloadID)
	for _, sock := range []string{qmp, serial, vnc, qga, nga} {
		if err := validateInterpolated("socket", sock); err != nil {
			return nil, err
		}
	}
	argv := []string{
		BinQEMU,
		"-name", spec.WorkloadID,
		"-machine", machine + ",usb=off",
		"-accel", accel,
		"-cpu", cpuForAccel(accel),
		"-smp", strconv.Itoa(cpus),
		"-m", strconv.FormatInt(memMB, 10),
		"-nodefaults",
		"-no-user-config",
		"-display", "none",
		"-blockdev", fmt.Sprintf("driver=file,node-name=disk0-file,filename=%s,discard=unmap", spec.DiskPath),
		"-blockdev", fmt.Sprintf("driver=%s,node-name=disk0,file=disk0-file", format),
		"-device", "virtio-blk-pci,drive=disk0,addr=" + diskAddr,
		"-chardev", "socket,id=qmp0,path=" + qmp + ",server=on,wait=off",
		"-mon", "chardev=qmp0,mode=control",
		"-chardev", "socket,id=serial0,path=" + serial + ",server=on,wait=off",
		"-serial", "chardev:serial0",
		"-vnc", "unix:" + vnc,
		"-chardev", "socket,id=qga0,path=" + qga + ",server=on,wait=off",
		"-device", "virtio-serial-pci,addr=" + serialAddr,
		"-device", "virtserialport,chardev=qga0,name=" + GuestAgentName,
		"-chardev", "socket,id=nga0,path=" + nga + ",server=on,wait=off",
		"-device", "virtserialport,chardev=nga0,name=" + NodalGuestName,
	}
	if spec.IncomingDefer {
		argv = append(argv, "-incoming", IncomingDefer)
	}
	for _, a := range argv {
		if strings.ContainsAny(a, "\n\r\x00") {
			return nil, fmt.Errorf("argv contains a banned character")
		}
	}
	if err := ValidateFrozenArgv(spec.WorkloadID, argv); err != nil {
		return nil, err
	}
	return argv, nil
}

func cpuForAccel(accel string) string {
	if accel == "kvm" {
		return "host"
	}
	return "qemu64"
}

// DetectAccel returns kvm when /dev/kvm exists, otherwise honest tcg.
func DetectAccel() string {
	if st, err := os.Stat("/dev/kvm"); err == nil && st.Mode()&os.ModeDevice != 0 {
		return "kvm"
	}
	if _, err := os.Stat("/dev/kvm"); err == nil {
		return "kvm"
	}
	return "tcg"
}

// DetectFirmware returns the first allowlisted OVMF code file on this host.
func DetectFirmware() string {
	for _, p := range []string{
		"/usr/share/OVMF/OVMF_CODE_4M.fd",
		"/usr/share/OVMF/OVMF_CODE.fd",
		"/usr/share/qemu/OVMF_CODE_4M.fd",
		"/usr/share/qemu/OVMF_CODE.fd",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ConsoleSocket is the unix locator for serial or VNC. It is not a ticket.
func (e *Engine) ConsoleSocket(id, kind string) (string, error) {
	if err := ValidateWorkloadID(id); err != nil {
		return "", err
	}
	switch strings.TrimSpace(kind) {
	case "serial":
		return e.serialPath(id), nil
	case "vnc":
		return e.vncPath(id), nil
	default:
		return "", fmt.Errorf("console mode must be serial or vnc")
	}
}
