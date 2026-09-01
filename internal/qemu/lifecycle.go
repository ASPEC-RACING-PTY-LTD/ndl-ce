package qemu

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
)

// ValidateWorkloadID requires a UUID. systemd unit names and last-applied
// identity are the same string.
func ValidateWorkloadID(id string) error {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return fmt.Errorf("workload_id must be a UUID")
	}
	return nil
}

// ForceStop sends SIGKILL to the main QEMU process via systemd, then stops the unit.
// Argv is allowlisted constants plus the validated unit name. No shell string.
func (e *Engine) ForceStop(ctx context.Context, id string) error {
	if err := ValidateWorkloadID(id); err != nil {
		return err
	}
	if e.SkipHostCmds {
		return nil
	}
	unit := unitName(id)
	_, killErr := e.run(ctx, BinSystemctl, "kill", "--kill-whom=main", "--signal=SIGKILL", unit)
	_, stopErr := e.run(ctx, BinSystemctl, "stop", unit)
	if stopErr != nil {
		if killErr != nil {
			return fmt.Errorf("systemctl kill: %v; systemctl stop: %w", killErr, stopErr)
		}
		return stopErr
	}
	return nil
}

// CleanupFailedLaunch removes stale QMP/serial/VNC/qga sockets when the unit
// is not running. It never deletes the VolumeHandle qcow2 and never deletes
// qemu-last-applied.json (that file is identity).
func (e *Engine) CleanupFailedLaunch(id string) error {
	if err := ValidateWorkloadID(id); err != nil {
		return err
	}
	if !e.SkipHostCmds {
		state, err := e.unitState(context.Background(), id)
		if err != nil {
			return fmt.Errorf("cleanup refused: unit state unknown: %w", err)
		}
		switch strings.TrimSpace(state) {
		case "activating", "active", "reloading", "deactivating":
			return nil
		}
	}
	for _, p := range []string{e.qmpPath(id), e.serialPath(id), e.vncPath(id), e.qgaPath(id)} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Discover reads last-applied and unit state. Identity is the applied UUID.
func (e *Engine) Discover(id string) (Discovery, error) {
	if err := ValidateWorkloadID(id); err != nil {
		return Discovery{}, err
	}
	applied, err := e.ReadApplied(id)
	if err != nil {
		return Discovery{}, err
	}
	got := strings.TrimSpace(applied.Spec.WorkloadID)
	if got == "" {
		return Discovery{}, fmt.Errorf("last-applied identity is missing")
	}
	if err := ValidateWorkloadID(got); err != nil {
		return Discovery{}, err
	}
	if got != strings.TrimSpace(id) {
		return Discovery{}, fmt.Errorf("last-applied identity %s does not match %s", got, id)
	}
	disc := Discovery{
		WorkloadID: got,
		VolumeID:   applied.Spec.VolumeID,
		DiskPath:   applied.Spec.DiskPath,
		Applied:    applied,
		Status:     StatusStopped,
	}
	if e.SkipHostCmds {
		disc.Reason = "fixture"
		return disc, nil
	}
	ctx := context.Background()
	state, err := e.unitState(ctx, id)
	if err == nil {
		disc.UnitState = strings.TrimSpace(state)
	}
	active, _ := e.unitActive(ctx, id)
	disc.UnitActive = active
	if active {
		disc.Status = StatusRunning
		return disc, nil
	}
	if strings.Contains(disc.UnitState, "failed") {
		disc.Status = StatusFailed
		disc.Reason = "qemu exited"
	}
	return disc, nil
}
