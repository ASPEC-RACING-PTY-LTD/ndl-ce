package qemu

import (
	"fmt"
	"github.com/no-dal/ndl-ce/internal/storage"
	"path"
	"strings"
)

const storageRoot = "/var/lib/ndl/storage"

var allowedLaunchFlags = map[string]struct{}{
	"-name": {}, "-machine": {}, "-accel": {}, "-cpu": {},
	"-smp": {}, "-m": {}, "-nodefaults": {}, "-no-user-config": {},
	"-display": {}, "-blockdev": {}, "-device": {}, "-chardev": {},
	"-mon": {}, "-serial": {}, "-vnc": {}, "-netdev": {}, "-boot": {}, "-drive": {},
}

// ValidateDiskPath requires an absolute VolumeHandle locator under the
// Directory storage root. Commas and equals are rejected so they cannot
// inject extra -blockdev options.
func ValidateDiskPath(diskPath string) error {
	if diskPath == "" || !strings.HasPrefix(diskPath, "/") {
		return fmt.Errorf("disk_path must be an absolute VolumeHandle locator")
	}
	if strings.Contains(diskPath, "..") {
		return fmt.Errorf("disk_path is not a clean locator")
	}
	if strings.ContainsAny(diskPath, ",=\n\r\x00") {
		return fmt.Errorf("disk_path contains a banned character")
	}
	cleaned := path.Clean(diskPath)
	if cleaned != diskPath {
		return fmt.Errorf("disk_path is not a clean locator")
	}
	if cleaned != storageRoot && !strings.HasPrefix(cleaned, storageRoot+"/") {
		if strings.HasPrefix(cleaned, storage.ZVolDevPrefix) {
			return storage.ValidateZVolPath(cleaned)
		}
		if err := storage.ValidateLVMDevice(cleaned); err == nil {
			return nil
		}
		if strings.HasPrefix(cleaned, storage.ISCSIByPath) {
			if strings.Contains(cleaned, "..") {
				return fmt.Errorf("disk_path is not a clean locator")
			}
			return nil
		}
		if strings.HasPrefix(cleaned, storage.LVMMountRoot+"/") {
			return nil
		}
		return fmt.Errorf("disk_path must be under the storage root")
	}
	return nil
}

func validateMachine(machine string) error {
	if machine == "q35" || strings.EqualFold(machine, "pc") {
		return fmt.Errorf("machine type must be a pinned pc-q35-X.Y, not an alias")
	}
	if !strings.HasPrefix(machine, "pc-q35-") {
		return fmt.Errorf("machine type must be pc-q35-X.Y")
	}
	rest := strings.TrimPrefix(machine, "pc-q35-")
	parts := strings.Split(rest, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("machine type must be pc-q35-X.Y")
	}
	for _, part := range parts {
		for _, c := range part {
			if c < '0' || c > '9' {
				return fmt.Errorf("machine type must be pc-q35-X.Y")
			}
		}
	}
	if strings.ContainsAny(machine, ",=\n\r\x00") {
		return fmt.Errorf("machine type contains a banned character")
	}
	return nil
}

func validateDiskFormat(format string) error {
	switch format {
	case "qcow2", "raw":
		return nil
	default:
		return fmt.Errorf("disk_format must be qcow2 or raw")
	}
}

func validatePCIAddr(addr string) error {
	if !strings.HasPrefix(addr, "0x") || len(addr) < 3 {
		return fmt.Errorf("pci address must be 0xNN")
	}
	for _, c := range addr[2:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return fmt.Errorf("pci address must be 0xNN")
		}
	}
	return nil
}

func validateInterpolated(name, value string) error {
	if strings.ContainsAny(value, ",=\n\r\x00") {
		return fmt.Errorf("%s contains a banned character", name)
	}
	return nil
}

// ValidateFrozenArgv re-checks the launcher artifact. argv[0] must be the
// pinned QEMU binary. Flags are allowlisted. A shell or human monitor is
// rejected even if the JSON was rewritten on disk.
func ValidateFrozenArgv(id string, argv []string) error {
	if err := ValidateWorkloadID(id); err != nil {
		return err
	}
	if len(argv) < 2 || argv[0] != BinQEMU {
		return fmt.Errorf("frozen argv is not a typed qemu command")
	}
	joined := strings.Join(argv, " ")
	hostExec := "Host" + ".Exec"
	if strings.Contains(joined, "/bin/sh") || strings.Contains(joined, "/bin/bash") || strings.Contains(joined, hostExec) {
		return fmt.Errorf("frozen argv must not invoke a shell")
	}
	if !strings.Contains(joined, "mode=control") {
		return fmt.Errorf("QMP control monitor is required")
	}
	if strings.Contains(joined, "mode=human") || strings.Contains(joined, "monitor stdio") {
		return fmt.Errorf("human monitor is forbidden")
	}
	for _, a := range argv {
		if strings.ContainsAny(a, "\n\r\x00;") {
			return fmt.Errorf("argv contains a banned character")
		}
		if strings.HasPrefix(a, "-") {
			if _, ok := allowedLaunchFlags[a]; !ok {
				return fmt.Errorf("frozen argv flag %s is not allowed", a)
			}
		}
	}
	return nil
}
