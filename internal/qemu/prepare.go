package qemu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/vmspec"
)

// PrepareLaunch writes frozen argv, cidata, firmware vars, and TAP devices.
func (e *Engine) PrepareLaunch(ctx context.Context, launch vmspec.Launch, source ConvertRequest) (Result, error) {
	if err := vmspec.ValidateWorkloadID(launch.WorkloadID); err != nil {
		return Result{}, err
	}
	if e.AlreadyRunning(ctx, launch.WorkloadID) {
		return Result{}, ErrAlreadyRunning
	}
	if source.DestPath != "" && source.SourcePath != "" {
		if err := e.ConvertOffline(ctx, source); err != nil {
			return Result{}, err
		}
	}
	if launch.Firmware.Mode == vmspec.FirmwareUEFI {
		if err := e.ensureUEFIVars(launch); err != nil {
			return Result{}, err
		}
	}
	if launch.NoCloud != nil && launch.NoCloud.Enable {
		if strings.TrimSpace(launch.NoCloud.NetworkConfig) != "" {
			if err := e.ensureDirs(launch.WorkloadID); err != nil {
				return Result{}, err
			}
			if err := os.WriteFile(filepath.Join(e.runtimeDir(launch.WorkloadID), "cidata-network-config"), []byte(launch.NoCloud.NetworkConfig), 0o600); err != nil {
				return Result{}, err
			}
		}
		if err := e.writeCIDATA(launch); err != nil {
			return Result{}, err
		}
	}
	argv, err := e.CompileLaunch(launch)
	if err != nil {
		return Result{}, err
	}
	if err := e.writeLaunch(launch, argv); err != nil {
		return Result{}, err
	}
	if !e.SkipHostCmds {
		if err := e.prepareTAPs(launch); err != nil {
			_ = e.cleanupTAPs(launch)
			return Result{}, err
		}
		if err := e.chownRuntime(launch.WorkloadID); err != nil {
			_ = e.cleanupTAPs(launch)
			return Result{}, err
		}
		for _, d := range launch.Disks {
			if d.Role == vmspec.DiskRoleBoot || d.Role == vmspec.DiskRoleData {
				if err := e.chownDisk(d.Path); err != nil {
					_ = e.cleanupTAPs(launch)
					return Result{}, err
				}
			}
		}
	}
	return Result{WorkloadID: launch.WorkloadID, Status: StatusStopped, Machine: launch.Machine, Accel: launch.Accel, Argv: argv}, nil
}

func (e *Engine) writeCIDATA(launch vmspec.Launch) error {
	if launch.NoCloud == nil {
		return nil
	}
	userData := ""
	if applied, err := e.ReadApplied(launch.WorkloadID); err == nil && applied.Launch.NoCloud != nil {
		_ = applied
	}
	meta := vmspec.RenderMetaData(vmspec.NoCloud{Hostname: launch.NoCloud.Hostname}, launch.WorkloadID)
	files := map[string][]byte{"meta-data": []byte(meta)}
	seed := filepath.Join(e.runtimeDir(launch.WorkloadID), "cidata-user-data")
	if b, err := os.ReadFile(seed); err == nil {
		userData = string(b)
	}
	if userData == "" {
		rendered, err := vmspec.RenderUserData(vmspec.NoCloud{
			Enable: true, Hostname: launch.NoCloud.Hostname, Username: launch.NoCloud.Username,
		})
		if err != nil {
			return err
		}
		userData = rendered
	}
	files["user-data"] = []byte(userData)
	netCfg := launch.NoCloud.NetworkConfig
	seedNet := filepath.Join(e.runtimeDir(launch.WorkloadID), "cidata-network-config")
	if b, err := os.ReadFile(seedNet); err == nil && len(b) > 0 {
		netCfg = string(b)
	}
	if strings.TrimSpace(netCfg) != "" {
		files["network-config"] = []byte(netCfg)
	}
	img, err := vmspec.BuildCIDATA(files)
	if err != nil {
		return err
	}
	if err := e.ensureDirs(launch.WorkloadID); err != nil {
		return err
	}
	return os.WriteFile(launch.NoCloud.ImagePath, img, 0o640)
}

// WriteNoCloudSeed stores user-data privately under the VM runtime dir.
func (e *Engine) WriteNoCloudSeed(id string, userData string) error {
	if err := ValidateWorkloadID(id); err != nil {
		return err
	}
	if err := e.ensureDirs(id); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(e.runtimeDir(id), "cidata-user-data"), []byte(userData), 0o600)
}

func (e *Engine) ensureUEFIVars(launch vmspec.Launch) error {
	if launch.Firmware.VarsPath == "" {
		return fmt.Errorf("uefi vars path is missing")
	}
	if _, err := os.Stat(launch.Firmware.VarsPath); err == nil {
		return nil
	}
	src := launch.Firmware.CodePath
	template := strings.Replace(src, "OVMF_CODE", "OVMF_VARS", 1)
	template = strings.Replace(template, ".secboot.fd", ".fd", 1)
	if err := e.ensureDirs(launch.WorkloadID); err != nil {
		return err
	}
	if e.SkipHostCmds {
		return os.WriteFile(launch.Firmware.VarsPath, make([]byte, 540672), 0o640)
	}
	argv := []string{"/bin/cp", "--", template, launch.Firmware.VarsPath}
	if err := validateCPArgv(argv, launch.WorkloadID); err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy uefi vars: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func validateCPArgv(argv []string, id string) error {
	if len(argv) != 4 || argv[0] != "/bin/cp" || argv[1] != "--" {
		return fmt.Errorf("uefi vars copy argv is invalid")
	}
	if !strings.Contains(argv[2], "OVMF_VARS") {
		return fmt.Errorf("uefi vars template is invalid")
	}
	prefix := filepath.Join("/var/lib/ndl/runtime/qemu", id) + string(os.PathSeparator)
	if !strings.HasPrefix(argv[3], prefix) {
		return fmt.Errorf("uefi vars destination must be per-VM")
	}
	return nil
}

// CleanupLaunch removes runtime sockets and TAP devices. It never deletes
// user volumes or last-applied identity.
func (e *Engine) CleanupLaunch(id string, launch vmspec.Launch) error {
	if err := e.CleanupFailedLaunch(id); err != nil {
		return err
	}
	if !e.SkipHostCmds {
		if err := e.cleanupTAPs(launch); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) DeleteRuntime(ctx context.Context, id string) error {
	if err := ValidateWorkloadID(id); err != nil {
		return err
	}
	_ = e.ForceStop(ctx, id)
	_ = e.EnableAutostart(ctx, id, false)
	launch, _ := e.ReadLaunch(id)
	if err := e.CleanupLaunch(id, launch); err != nil {
		return err
	}
	known := []string{
		e.runtimeDir(id) + "/cidata.fat",
		e.runtimeDir(id) + "/vars.fd",
		e.runtimeDir(id) + "/cidata-user-data",
		e.runtimeDir(id) + "/cidata-network-config",
		e.vncPath(id), e.serialPath(id), e.qmpPath(id), e.qgaPath(id), e.guestPath(id),
	}
	if launch.NoCloud != nil && launch.NoCloud.ImagePath != "" {
		known = append(known, launch.NoCloud.ImagePath)
	}
	if launch.Firmware.VarsPath != "" {
		known = append(known, launch.Firmware.VarsPath)
	}
	for _, p := range known {
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, e.runtimeDir(id)+"/") && !strings.HasPrefix(p, e.workloadDir(id)+"/") {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// ReadLaunch returns the frozen Launch from last-applied state.
func (e *Engine) ReadLaunch(id string) (vmspec.Launch, error) {
	applied, err := e.ReadApplied(id)
	if err != nil {
		return vmspec.Launch{}, err
	}
	if applied.Launch.WorkloadID != "" {
		return applied.Launch, nil
	}
	return vmspec.Launch{}, fmt.Errorf("frozen launch config is missing")
}
