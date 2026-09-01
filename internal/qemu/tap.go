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

const (
	BinIP      = "/usr/sbin/ip"
	BinIPAlt   = "/bin/ip"
	BinQEMUImg = "/usr/bin/qemu-img"
)

func ipBin() string {
	if _, err := os.Stat(BinIP); err == nil {
		return BinIP
	}
	return BinIPAlt
}

func (e *Engine) prepareTAPs(launch vmspec.Launch) error {
	for _, n := range launch.NICs {
		if err := e.ensureTAP(n); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) cleanupTAPs(launch vmspec.Launch) error {
	for _, n := range launch.NICs {
		want := n.TAPName
		if want == "" {
			continue
		}
		if want != vmspec.TAPName(launch.WorkloadID, indexOfTAP(launch, want)) && !strings.HasPrefix(want, "nv") {
			return fmt.Errorf("refusing to delete unmanaged interface %s", want)
		}
		if err := e.deleteTAP(want); err != nil {
			return err
		}
	}
	return nil
}

func indexOfTAP(launch vmspec.Launch, name string) int {
	for i, n := range launch.NICs {
		if n.TAPName == name {
			return i
		}
	}
	return 0
}

func (e *Engine) ensureTAP(n vmspec.LaunchNIC) error {
	if e.SkipHostCmds {
		return nil
	}
	if err := createTAPDevice(n.TAPName); err != nil {
		return err
	}
	if err := e.ipRun("link", "set", "dev", n.TAPName, "master", n.BridgeName); err != nil {
		return err
	}
	return e.ipRun("link", "set", "dev", n.TAPName, "up")
}

func (e *Engine) deleteTAP(name string) error {
	if e.SkipHostCmds {
		return nil
	}
	if name == "" || strings.ContainsAny(name, " \n") {
		return fmt.Errorf("tap name is invalid")
	}
	_ = e.ipRun("link", "set", "dev", name, "down")
	err := e.ipRun("link", "delete", "dev", name)
	if err != nil && strings.Contains(err.Error(), "Cannot find device") {
		return nil
	}
	return err
}

func (e *Engine) ipRun(args ...string) error {
	bin := ipBin()
	argv := append([]string{bin}, args...)
	if err := validateIPArgv(argv); err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", bin, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func validateIPArgv(argv []string) error {
	if len(argv) < 4 || (argv[0] != BinIP && argv[0] != BinIPAlt) {
		return fmt.Errorf("ip argv is invalid")
	}
	if argv[1] != "link" {
		return fmt.Errorf("ip argv is not a link operation")
	}
	for _, a := range argv {
		if strings.ContainsAny(a, "\n\r\x00;$") {
			return fmt.Errorf("ip argv contains a banned character")
		}
	}
	return nil
}

// ConvertRequest is a typed offline qemu-img convert. Never used on a live disk.
type ConvertRequest struct {
	SourcePath   string
	SourceFormat string
	DestPath     string
	DestFormat   string
}

func (e *Engine) ConvertOffline(ctx context.Context, req ConvertRequest) error {
	if err := ValidateDiskPath(req.DestPath); err != nil {
		return err
	}
	if err := ValidateDiskPath(req.SourcePath); err != nil && !strings.HasPrefix(req.SourcePath, "/var/lib/ndl/storage/") {
		return fmt.Errorf("source image locator is invalid")
	}
	if strings.Contains(req.SourcePath, "..") || strings.ContainsAny(req.SourcePath, ",=\n") {
		return fmt.Errorf("source image locator is invalid")
	}
	if req.SourceFormat == "" {
		req.SourceFormat = "qcow2"
	}
	if req.DestFormat == "" {
		req.DestFormat = "qcow2"
	}
	if req.SourceFormat != "qcow2" && req.SourceFormat != "raw" {
		return fmt.Errorf("source format must be qcow2 or raw")
	}
	if req.DestFormat != "qcow2" && req.DestFormat != "raw" {
		return fmt.Errorf("dest format must be qcow2 or raw")
	}
	if err := e.AssertDiskOffline(ctx, req.DestPath); err != nil {
		return err
	}
	if err := e.AssertDiskOffline(ctx, req.SourcePath); err != nil {
		return err
	}
	if e.SkipHostCmds {
		return nil
	}
	argv := []string{BinQEMUImg, "convert", "-f", req.SourceFormat, "-O", req.DestFormat, req.SourcePath, req.DestPath}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img convert: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// AssertDiskOffline refuses qemu-img mutation of a disk attached to a live VM.
// Unknown applied state or unknown unit state is a refusal, not a pass.
func (e *Engine) AssertDiskOffline(ctx context.Context, diskPath string) error {
	if diskPath == "" {
		return fmt.Errorf("disk path is required")
	}
	want := filepath.Clean(diskPath)
	entries, err := os.ReadDir(filepath.Join(e.dataDir(), "workloads"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("qemu-img refused: cannot list applied VMs: %w", err)
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		id := ent.Name()
		if err := ValidateWorkloadID(id); err != nil {
			continue
		}
		if _, statErr := os.Stat(e.appliedPath(id)); os.IsNotExist(statErr) {
			continue
		}
		applied, err := e.ReadApplied(id)
		if err != nil {
			return fmt.Errorf("qemu-img refused: cannot prove disk is offline (workload %s): %w", id, err)
		}
		if !appliedContainsDisk(applied, want) {
			continue
		}
		if err := e.unitProvenStopped(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func appliedContainsDisk(applied Applied, want string) bool {
	paths := []string{applied.Spec.DiskPath}
	for _, d := range applied.Launch.Disks {
		paths = append(paths, d.Path)
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if filepath.Clean(p) == want {
			return true
		}
	}
	return false
}

// unitProvenStopped fails closed when systemd cannot prove the unit is down.
func (e *Engine) unitProvenStopped(ctx context.Context, id string) error {
	if e.LiveUnits != nil {
		if e.LiveUnits[id] {
			return fmt.Errorf("qemu-img refused: volume is attached to a running VM")
		}
		return nil
	}
	if e.SkipHostCmds {
		return nil
	}
	state, err := e.unitState(ctx, id)
	if err != nil {
		return fmt.Errorf("qemu-img refused: cannot prove VM %s is stopped: %w", id, err)
	}
	switch strings.TrimSpace(state) {
	case "activating", "active", "reloading":
		return fmt.Errorf("qemu-img refused: volume is attached to a running VM")
	}
	return nil
}
