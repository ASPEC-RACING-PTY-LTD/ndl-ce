package qemu

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

const (
	OverlayCreate   = "create"
	OverlayRollback = "rollback"
	OverlayFlatten  = "flatten"
	ChainMax        = 16
)

// OverlayRequest is a typed Directory qcow2 overlay operation.
// qemu-img is never used while the disk is attached to a live unit.
type OverlayRequest struct {
	Action      string
	WorkloadID  string
	OverlayPath string
	BackingPath string
	ChainDepth  int
	ChainMax    int
}

// OverlayResult is the locator after a successful overlay operation.
type OverlayResult struct {
	WorkloadID  string `json:"workload_id"`
	OverlayPath string `json:"overlay_path"`
	BackingPath string `json:"backing_path,omitempty"`
	Mechanism   string `json:"mechanism"`
	Quiesced    bool   `json:"quiesced"`
	LiveQMP     bool   `json:"live_qmp"`
}

// OverlayDisk applies create, rollback, or flatten. Live qemu-img is refused.
func (e *Engine) OverlayDisk(ctx context.Context, req OverlayRequest) (OverlayResult, error) {
	if err := ValidateWorkloadID(req.WorkloadID); err != nil {
		return OverlayResult{}, err
	}
	if err := ValidateDiskPath(req.OverlayPath); err != nil {
		return OverlayResult{}, fmt.Errorf("overlay path: %w", err)
	}
	if req.BackingPath != "" {
		if err := ValidateDiskPath(req.BackingPath); err != nil {
			return OverlayResult{}, fmt.Errorf("backing path: %w", err)
		}
	}
	max := req.ChainMax
	if max <= 0 {
		max = ChainMax
	}
	action := strings.TrimSpace(req.Action)
	if action == "" {
		action = OverlayCreate
	}
	switch action {
	case OverlayCreate:
		return e.overlayCreate(ctx, req, max)
	case OverlayRollback:
		return e.overlayRollback(ctx, req)
	case OverlayFlatten:
		return e.overlayFlatten(ctx, req)
	default:
		return OverlayResult{}, fmt.Errorf("unsupported snapshot action")
	}
}

func (e *Engine) overlayCreate(ctx context.Context, req OverlayRequest, max int) (OverlayResult, error) {
	if req.BackingPath == "" {
		return OverlayResult{}, fmt.Errorf("backing path is required")
	}
	if req.OverlayPath == req.BackingPath {
		return OverlayResult{}, fmt.Errorf("overlay path must not equal backing path")
	}
	if req.ChainDepth >= max {
		return OverlayResult{}, fmt.Errorf("qcow2 overlay chain cap is %d", max)
	}
	running := e.unitProvenStopped(ctx, req.WorkloadID) != nil && e.diskAttached(req.WorkloadID, req.BackingPath)
	if running {
		quiesced := e.guestFsfreeze(req.WorkloadID, true)
		err := e.qmpExternalSnapshot(req.WorkloadID, req.OverlayPath)
		_ = e.guestFsfreeze(req.WorkloadID, false)
		if err != nil {
			return OverlayResult{}, fmt.Errorf("live overlay requires QMP; qemu-img is refused: %w", err)
		}
		if err := e.retargetBootDisk(req.WorkloadID, req.OverlayPath); err != nil {
			return OverlayResult{}, err
		}
		return OverlayResult{
			WorkloadID: req.WorkloadID, OverlayPath: req.OverlayPath, BackingPath: req.BackingPath,
			Mechanism: "qcow2-overlay", Quiesced: quiesced, LiveQMP: true,
		}, nil
	}
	if err := e.AssertDiskOffline(ctx, req.BackingPath); err != nil {
		return OverlayResult{}, err
	}
	if err := e.AssertDiskOffline(ctx, req.OverlayPath); err != nil {
		return OverlayResult{}, err
	}
	if err := e.createOverlayFile(ctx, req.OverlayPath, req.BackingPath); err != nil {
		return OverlayResult{}, err
	}
	if err := e.retargetBootDisk(req.WorkloadID, req.OverlayPath); err != nil {
		return OverlayResult{}, err
	}
	return OverlayResult{
		WorkloadID: req.WorkloadID, OverlayPath: req.OverlayPath, BackingPath: req.BackingPath,
		Mechanism: "qcow2-overlay",
	}, nil
}

func (e *Engine) overlayRollback(ctx context.Context, req OverlayRequest) (OverlayResult, error) {
	if req.BackingPath == "" {
		return OverlayResult{}, fmt.Errorf("snapshot backing path is required")
	}
	if req.OverlayPath == req.BackingPath {
		return OverlayResult{}, fmt.Errorf("overlay path must not equal backing path")
	}
	if err := e.AssertDiskOffline(ctx, req.BackingPath); err != nil {
		return OverlayResult{}, err
	}
	if err := e.AssertDiskOffline(ctx, req.OverlayPath); err != nil {
		return OverlayResult{}, err
	}
	if err := e.createOverlayFile(ctx, req.OverlayPath, req.BackingPath); err != nil {
		return OverlayResult{}, err
	}
	if err := e.retargetBootDisk(req.WorkloadID, req.OverlayPath); err != nil {
		return OverlayResult{}, err
	}
	return OverlayResult{
		WorkloadID: req.WorkloadID, OverlayPath: req.OverlayPath, BackingPath: req.BackingPath,
		Mechanism: "qcow2-overlay",
	}, nil
}

func (e *Engine) overlayFlatten(ctx context.Context, req OverlayRequest) (OverlayResult, error) {
	if req.BackingPath == "" {
		return OverlayResult{}, fmt.Errorf("current tip path is required")
	}
	if err := e.ConvertOffline(ctx, ConvertRequest{
		SourcePath: req.BackingPath, DestPath: req.OverlayPath,
		SourceFormat: "qcow2", DestFormat: "qcow2",
	}); err != nil {
		return OverlayResult{}, err
	}
	if err := e.retargetBootDisk(req.WorkloadID, req.OverlayPath); err != nil {
		return OverlayResult{}, err
	}
	return OverlayResult{
		WorkloadID: req.WorkloadID, OverlayPath: req.OverlayPath, BackingPath: req.BackingPath,
		Mechanism: "qcow2-overlay",
	}, nil
}

func (e *Engine) createOverlayFile(ctx context.Context, dest, backing string) error {
	if e.SkipHostCmds {
		return fmt.Errorf("qemu-img overlay is unavailable")
	}
	argv, err := storage.QEMUCreateBackingArgv(BinQEMUImg, dest, backing)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img overlay: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (e *Engine) diskAttached(id, diskPath string) bool {
	applied, err := e.ReadApplied(id)
	if err != nil {
		return true
	}
	return appliedContainsDisk(applied, diskPath)
}

func (e *Engine) retargetBootDisk(id, newPath string) error {
	applied, err := e.ReadApplied(id)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if applied.Launch.WorkloadID != "" {
		for i := range applied.Launch.Disks {
			if applied.Launch.Disks[i].Role == vmspec.DiskRoleBoot || applied.Launch.Disks[i].Role == "" {
				applied.Launch.Disks[i].Path = newPath
			}
		}
		argv, err := e.CompileLaunch(applied.Launch)
		if err != nil {
			return err
		}
		return e.writeLaunch(applied.Launch, argv)
	}
	if applied.Spec.DiskPath != "" {
		applied.Spec.DiskPath = newPath
		argv, err := e.compile(applied.Spec)
		if err != nil {
			return err
		}
		return e.writeFrozen(applied.Spec, argv)
	}
	return nil
}

func (e *Engine) qmpExternalSnapshot(id, overlay string) error {
	if e.SkipHostCmds {
		return fmt.Errorf("qmp is unavailable")
	}
	q, err := e.dialQMP(id, 3*time.Second)
	if err != nil {
		return err
	}
	defer q.Close()
	_, err = q.exec("blockdev-snapshot-sync", map[string]any{
		"node-name":     "disk0",
		"snapshot-file": overlay,
		"format":        "qcow2",
		"mode":          "absolute-paths",
	})
	return err
}

func (e *Engine) guestFsfreeze(id string, freeze bool) bool {
	if e.SkipHostCmds {
		return false
	}
	cmd := "guest-fsfreeze-thaw"
	if freeze {
		cmd = "guest-fsfreeze-freeze"
	}
	c, err := net.DialTimeout("unix", e.qgaPath(id), time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	raw, err := json.Marshal(map[string]any{"execute": cmd})
	if err != nil {
		return false
	}
	if _, err := c.Write(append(raw, '\n')); err != nil {
		return false
	}
	buf := make([]byte, 4096)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = c.Read(buf)
	return err == nil
}
