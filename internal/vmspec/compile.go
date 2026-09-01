package vmspec

import (
	"fmt"
	"path"
	"strings"

	"github.com/no-dal/ndl-ce/internal/storage"
)

// Compile turns desired spec plus resolved locators into a frozen Launch.
// The same inputs always produce the same Launch (and therefore the same argv).
func Compile(workloadID string, spec Spec, resolved Resolved) (Launch, error) {
	if err := ValidateWorkloadID(workloadID); err != nil {
		return Launch{}, err
	}
	spec = Normalize(spec)
	spec = PersistNICs(workloadID, spec)
	if err := Validate(spec); err != nil {
		return Launch{}, err
	}
	spec, pci, err := AllocatePCI(spec)
	if err != nil {
		return Launch{}, err
	}
	accel := resolved.Accel
	if accel == "" {
		accel = "tcg"
	}
	if accel != "kvm" && accel != "tcg" {
		return Launch{}, fmt.Errorf("accel must be kvm or tcg")
	}
	launch := Launch{
		SchemaVersion: SchemaLaunch,
		WorkloadID:    workloadID,
		Machine:       spec.Machine,
		Accel:         accel,
		CPUs:          spec.CPUs,
		MemoryMiB:     spec.MemoryBytes / (1 << 20),
		Autostart:     spec.Autostart,
		Balloon:       spec.Balloon,
		QGA:           true,
		PCI:           pci,
		Console:       LaunchConsole{Serial: spec.Console.Serial, VNC: spec.Console.VNC},
		Firmware:      Firmware{Mode: spec.Firmware},
		BootOrder:     bootChars(spec.BootOrder),
	}
	if launch.MemoryMiB < 64 {
		launch.MemoryMiB = 128
	}
	if spec.Firmware == FirmwareUEFI {
		if err := ValidateCleanPath(resolved.FirmwareCode, "firmware code"); err != nil {
			return Launch{}, fmt.Errorf("uefi firmware is unavailable")
		}
		if !allowedFirmware(resolved.FirmwareCode) {
			return Launch{}, fmt.Errorf("firmware code path is not a host firmware file")
		}
		vars := resolved.FirmwareVarsIn
		if vars == "" {
			vars = path.Join("/var/lib/ndl/runtime/qemu", workloadID, "vars.fd")
		}
		if err := ValidateCleanPath(vars, "firmware vars"); err != nil {
			return Launch{}, err
		}
		if !strings.HasPrefix(vars, "/var/lib/ndl/runtime/qemu/"+workloadID+"/") {
			return Launch{}, fmt.Errorf("firmware vars must be per-VM runtime state")
		}
		launch.Firmware.CodePath = resolved.FirmwareCode
		launch.Firmware.VarsPath = vars
	}
	if len(resolved.Disks) == 0 {
		return Launch{}, fmt.Errorf("resolved disks are required")
	}
	for i, d := range resolved.Disks {
		if err := ValidateCleanPath(d.Path, "disk path"); err != nil {
			return Launch{}, err
		}
		zvol := strings.HasPrefix(d.Path, storage.ZVolDevPrefix)
		if !strings.HasPrefix(d.Path, "/var/lib/ndl/storage/") && !strings.HasPrefix(d.Path, "/var/lib/ndl/runtime/qemu/"+workloadID+"/") {
			if err := storage.ValidateZVolPath(d.Path); err != nil {
				return Launch{}, fmt.Errorf("disk path must be a VolumeHandle locator")
			}
			zvol = true
		}
		format := d.Format
		if format == "" {
			if zvol {
				format = "raw"
			} else {
				format = "qcow2"
			}
		}
		if zvol && format != "raw" {
			return Launch{}, fmt.Errorf("zvol disk format must be raw")
		}
		if format != "qcow2" && format != "raw" {
			return Launch{}, fmt.Errorf("disk format must be qcow2 or raw")
		}
		addr := d.PCIAddr
		if addr == "" && i < len(spec.Disks) {
			addr = spec.Disks[i].PCIAddr
		}
		launch.Disks = append(launch.Disks, LaunchDisk{
			VolumeID: d.VolumeID,
			Role:     d.Role,
			Slot:     d.Slot,
			Path:     d.Path,
			Format:   format,
			ReadOnly: d.ReadOnly || d.Role == DiskRoleCDROM || d.Role == DiskRoleCIDATA,
			PCIAddr:  addr,
			NodeName: fmt.Sprintf("disk%d", i),
		})
	}
	if len(resolved.NICs) == 0 {
		return Launch{}, fmt.Errorf("resolved nics are required")
	}
	for i, n := range resolved.NICs {
		if err := ValidateMAC(n.MAC); err != nil {
			return Launch{}, err
		}
		tap := n.TAPName
		if tap == "" {
			tap = TAPName(workloadID, i)
		}
		if len(tap) > 15 || strings.ContainsAny(tap, " \n\r,=") {
			return Launch{}, fmt.Errorf("tap name is invalid")
		}
		if n.BridgeName == "" || strings.ContainsAny(n.BridgeName, " \n\r,=") {
			return Launch{}, fmt.Errorf("bridge name is invalid")
		}
		addr := n.PCIAddr
		if addr == "" && i < len(spec.NICs) {
			addr = spec.NICs[i].PCIAddr
		}
		launch.NICs = append(launch.NICs, LaunchNIC{
			ID: n.ID, NetworkID: n.NetworkID, BridgeName: n.BridgeName,
			TAPName: tap, MAC: strings.ToLower(n.MAC), Model: NICModelVirtio, PCIAddr: addr,
		})
	}
	if spec.ISOLibraryID != "" {
		if err := ValidateCleanPath(resolved.ISOPath, "iso path"); err != nil {
			return Launch{}, fmt.Errorf("installation media is unavailable")
		}
		if !strings.HasPrefix(resolved.ISOPath, "/var/lib/ndl/storage/") {
			return Launch{}, fmt.Errorf("iso path must be a library locator")
		}
		launch.Disks = append(launch.Disks, LaunchDisk{
			Role: DiskRoleCDROM, Path: resolved.ISOPath, Format: "raw", ReadOnly: true,
			NodeName: "iso0",
		})
	}
	if spec.NoCloud.Enable {
		img := path.Join("/var/lib/ndl/runtime/qemu", workloadID, "cidata.fat")
		userData, err := RenderUserData(spec.NoCloud)
		if err != nil {
			return Launch{}, err
		}
		launch.NoCloud = &LaunchNoCloud{
			Enable:        true,
			Hostname:      spec.NoCloud.Hostname,
			Username:      spec.NoCloud.Username,
			ImagePath:     img,
			HasPassword:   spec.NoCloud.HasPassword || spec.NoCloud.Password != "",
			UserDataSHA:   SHA256String(userData),
			NetworkConfig: spec.NoCloud.NetworkConfig,
		}
		launch.Disks = append(launch.Disks, LaunchDisk{
			Role: DiskRoleCIDATA, Path: img, Format: "raw", ReadOnly: true,
			PCIAddr:  pci[fmt.Sprintf("disk:%s:%d", DiskRoleCIDATA, 0)],
			NodeName: "cidata",
		})
	}
	return launch, nil
}

func bootChars(order []string) string {
	var b strings.Builder
	seen := map[rune]struct{}{}
	for _, item := range order {
		var c rune
		switch item {
		case "cdrom":
			c = 'd'
		default:
			c = 'c'
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		b.WriteRune(c)
	}
	if b.Len() == 0 {
		return "c"
	}
	return b.String()
}

func allowedFirmware(p string) bool {
	switch p {
	case "/usr/share/OVMF/OVMF_CODE_4M.fd",
		"/usr/share/OVMF/OVMF_CODE.fd",
		"/usr/share/OVMF/OVMF_CODE_4M.secboot.fd",
		"/usr/share/qemu/OVMF_CODE_4M.fd",
		"/usr/share/qemu/OVMF_CODE.fd":
		return true
	default:
		return false
	}
}
