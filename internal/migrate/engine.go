package migrate

import (
	"context"
	"fmt"
	"strings"

	"github.com/no-dal/ndl-ce/internal/qemu"
)

const (
	ModeOffline = "offline"
	ModeLive    = "live"
	KindVM      = "vm"
	KindCT      = "system-container"
	KindOCI     = "oci"
	StateOK     = "succeeded"
	StateFail   = "failed"
)

// Request is one ownership transfer. Dest is a different node UUID.
type Request struct {
	WorkloadID    string
	Kind          string
	Mode          string
	SourceNodeID  string
	DestNodeID    string
	Epoch         int
	SharedStorage bool
	CPUHost       bool
	Disks         []VolumeCopy
	CPUs          int
	MemoryBytes   int64
	Machine       string
	Accel         string
	SourceArgv    []string
	DestArgv      []string
}

// VolumeCopy is a typed volume locator pair. Paths are locators, not identity.
type VolumeCopy struct {
	VolumeID   string
	SourcePath string
	DestPath   string
}

// Result is honest about whether the source is still running.
type Result struct {
	State         string
	Epoch         int
	SourceRunning bool
	DestRunning   bool
	Reason        string
}

// Runtime is the privileged source/dest surface. It is not Host.Exec.
type Runtime interface {
	PrepareDest(ctx context.Context, req Request) error
	CopyVolume(ctx context.Context, vol VolumeCopy) error
	StopSource(ctx context.Context, id string) error
	StartDest(ctx context.Context, id string) error
	LiveMigrate(ctx context.Context, id string) error
	AbortDest(ctx context.Context, id string) error
	SourceRunning(ctx context.Context, id string) bool
}

// Run moves a workload. Failed live migrate leaves the source running and
// aborts dest so a second copy is not started on the wrong node.
func Run(ctx context.Context, rt Runtime, req Request) (Result, error) {
	if strings.TrimSpace(req.WorkloadID) == "" {
		return Result{}, fmt.Errorf("workload_id is required")
	}
	if req.SourceNodeID == "" || req.DestNodeID == "" || req.SourceNodeID == req.DestNodeID {
		return Result{}, fmt.Errorf("dest must be a different node")
	}
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = ModeOffline
	}
	if mode != ModeOffline && mode != ModeLive {
		return Result{}, fmt.Errorf("mode must be offline or live")
	}
	if mode == ModeLive && req.Kind != KindVM {
		return Result{}, fmt.Errorf("live migrate is VM-only; CT and OCI use offline")
	}
	if mode == ModeLive && req.CPUHost {
		return Result{}, fmt.Errorf("cpu host does not live-migrate; use offline")
	}
	srcRunning := rt.SourceRunning(ctx, req.WorkloadID)
	if err := rt.PrepareDest(ctx, req); err != nil {
		return Result{State: StateFail, SourceRunning: srcRunning, Reason: err.Error()}, err
	}
	if mode == ModeLive {
		if err := checkLiveABI(ctx, rt, req); err != nil {
			_ = rt.AbortDest(ctx, req.WorkloadID)
			still := rt.SourceRunning(ctx, req.WorkloadID)
			return Result{State: StateFail, SourceRunning: still, DestRunning: false, Reason: "ABI differs; source remains running"}, err
		}
		if err := rt.LiveMigrate(ctx, req.WorkloadID); err != nil {
			_ = rt.AbortDest(ctx, req.WorkloadID)
			still := rt.SourceRunning(ctx, req.WorkloadID)
			return Result{State: StateFail, SourceRunning: still, DestRunning: false, Reason: "live migrate failed; source remains running"}, err
		}
		return Result{State: StateOK, Epoch: req.Epoch + 1, SourceRunning: false, DestRunning: true, Reason: "live migrate completed"}, nil
	}
	if srcRunning {
		if err := rt.StopSource(ctx, req.WorkloadID); err != nil {
			_ = rt.AbortDest(ctx, req.WorkloadID)
			return Result{State: StateFail, SourceRunning: rt.SourceRunning(ctx, req.WorkloadID), Reason: err.Error()}, err
		}
	}
	if !req.SharedStorage {
		for _, vol := range req.Disks {
			if err := rt.CopyVolume(ctx, vol); err != nil {
				_ = rt.AbortDest(ctx, req.WorkloadID)
				return Result{State: StateFail, SourceRunning: false, Reason: err.Error()}, err
			}
		}
	}
	if err := rt.StartDest(ctx, req.WorkloadID); err != nil {
		_ = rt.AbortDest(ctx, req.WorkloadID)
		return Result{State: StateFail, SourceRunning: false, DestRunning: false, Reason: err.Error()}, err
	}
	return Result{State: StateOK, Epoch: req.Epoch + 1, SourceRunning: false, DestRunning: true, Reason: "offline migrate completed"}, nil
}

// ABIRuntime optionally records source and dest argv after PrepareDest.
type ABIRuntime interface {
	LiveArgv(ctx context.Context, id string) (source, dest []string)
}

func checkLiveABI(ctx context.Context, rt Runtime, req Request) error {
	srcArgv, destArgv := req.SourceArgv, req.DestArgv
	if ar, ok := rt.(ABIRuntime); ok {
		sArgv, dArgv := ar.LiveArgv(ctx, req.WorkloadID)
		if len(sArgv) > 0 {
			srcArgv = sArgv
		}
		if len(dArgv) > 0 {
			destArgv = dArgv
		}
	}
	if !qemu.SameABI(srcArgv, destArgv) {
		return fmt.Errorf("ABI differs; source remains running")
	}
	return nil
}
