package qemu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (e *Engine) writeFrozen(spec Spec, argv []string) error {
	if err := e.ensureDirs(spec.WorkloadID); err != nil {
		return err
	}
	argFile := ArgvFile{WorkloadID: spec.WorkloadID, Argv: argv}
	raw, err := json.MarshalIndent(argFile, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(e.argvPath(spec.WorkloadID), append(raw, '\n'), 0o640); err != nil {
		return err
	}
	applied := Applied{SchemaVersion: LastAppliedSchema, Spec: spec, Argv: argv, AppliedAt: e.now()}
	b, err := json.MarshalIndent(applied, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(e.appliedPath(spec.WorkloadID), append(b, '\n'), 0o640)
}

func (e *Engine) ReadApplied(id string) (Applied, error) {
	b, err := os.ReadFile(e.appliedPath(id))
	if err != nil {
		return Applied{}, err
	}
	var row Applied
	if err := json.Unmarshal(b, &row); err != nil {
		return Applied{}, err
	}
	return row, nil
}

func (e *Engine) chownRuntime(id string) error {
	u, err := user.Lookup(QEMUUser)
	if err != nil {
		return fmt.Errorf("qemu user %s is missing", QEMUUser)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}
	for _, p := range []string{e.runtimeDir(id), e.workloadDir(id), e.argvPath(id), e.appliedPath(id)} {
		if err := os.Chown(p, uid, gid); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	// Per-VM runtime is 0750 so world cannot connect to QEMU unix sockets
	// even when parent dirs are traversable by ndl-qemu.
	if err := os.Chmod(e.runtimeDir(id), 0o750); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (e *Engine) chownDisk(path string) error {
	if path == "" {
		return nil
	}
	if err := ensurePathTraverse(path); err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	u, err := user.Lookup(QEMUUser)
	if err != nil {
		return fmt.Errorf("qemu user %s is missing", QEMUUser)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}
	return os.Chown(path, uid, gid)
}

// Prepare writes frozen argv and last-applied. It does not start QEMU.
func (e *Engine) Prepare(spec Spec) (Result, error) {
	if err := ValidateWorkloadID(spec.WorkloadID); err != nil {
		return Result{}, err
	}
	if spec.Accel == "" {
		spec.Accel = DetectAccel()
	}
	if spec.Machine == "" {
		spec.Machine = DefaultMachine
	}
	if spec.MemoryBytes == 0 {
		spec.MemoryBytes = DefaultMemory
	}
	if spec.CPUs == 0 {
		spec.CPUs = 1
	}
	if spec.DiskFormat == "" {
		spec.DiskFormat = "qcow2"
	}
	if spec.PCIDiskAddr == "" {
		spec.PCIDiskAddr = "0x5"
	}
	if spec.PCISerialAddr == "" {
		spec.PCISerialAddr = "0x6"
	}
	argv, err := e.compile(spec)
	if err != nil {
		return Result{}, err
	}
	if err := e.writeFrozen(spec, argv); err != nil {
		return Result{}, err
	}
	if !e.SkipHostCmds {
		if err := e.chownRuntime(spec.WorkloadID); err != nil {
			return Result{}, err
		}
		if err := e.chownDisk(spec.DiskPath); err != nil {
			return Result{}, err
		}
	}
	return Result{WorkloadID: spec.WorkloadID, Status: StatusStopped, Machine: spec.Machine, Accel: spec.Accel, Argv: argv}, nil
}

// Start starts nodal-vm@<uuid>. systemd owns the QEMU process.
func (e *Engine) Start(ctx context.Context, id string) error {
	if err := ValidateWorkloadID(id); err != nil {
		return err
	}
	if e.SkipHostCmds {
		return fmt.Errorf("host commands skipped; qemu unit was not started")
	}
	if e.AlreadyRunning(ctx, id) {
		return nil
	}
	if err := e.CleanupFailedLaunch(id); err != nil {
		return err
	}
	_, err := e.run(ctx, BinSystemctl, "start", unitName(id))
	if err != nil {
		if cErr := e.CleanupFailedLaunch(id); cErr != nil {
			return fmt.Errorf("%w; cleanup: %v", err, cErr)
		}
		return err
	}
	return nil
}

// AlreadyRunning is true when systemd reports the unit as live.
// A second start must not rewrite frozen argv or leak another process.
func (e *Engine) AlreadyRunning(ctx context.Context, id string) bool {
	if e.LiveUnits != nil {
		return e.LiveUnits[id]
	}
	if e.SkipHostCmds {
		return false
	}
	state, err := e.unitState(ctx, id)
	if err != nil {
		return false
	}
	switch strings.TrimSpace(state) {
	case "activating", "active", "reloading":
		return true
	}
	return false
}

func (e *Engine) preventSecondUnit(ctx context.Context, id string) error {
	if e.AlreadyRunning(ctx, id) {
		return ErrAlreadyRunning
	}
	return nil
}

// Stop issues ACPI then stops the unit.
func (e *Engine) Stop(ctx context.Context, id string) error {
	if err := ValidateWorkloadID(id); err != nil {
		return err
	}
	if e.SkipHostCmds {
		return fmt.Errorf("host commands skipped; qemu unit was not stopped")
	}
	if q, err := e.dialQMP(id, 2*time.Second); err == nil {
		_ = q.powerdown()
		q.Close()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if active, _ := e.unitActive(ctx, id); !active {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	_, err := e.run(ctx, BinSystemctl, "stop", unitName(id))
	return err
}

func (e *Engine) EnableAutostart(ctx context.Context, id string, on bool) error {
	if e.SkipHostCmds {
		return nil
	}
	action := "disable"
	if on {
		action = "enable"
	}
	_, err := e.run(ctx, BinSystemctl, action, unitName(id))
	return err
}

func (e *Engine) Observe(ctx context.Context, id string) Observed {
	obs := Observed{
		WorkloadID:   id,
		Status:       StatusUnavailable,
		QMP:          e.qmpPath(id),
		SerialSocket: e.serialPath(id),
		VNCSocket:    e.vncPath(id),
		QGASocket:    e.qgaPath(id),
		GuestSocket:  e.guestPath(id),
	}
	if applied, err := e.ReadApplied(id); err == nil {
		obs.Machine = applied.Spec.Machine
		obs.Accel = applied.Spec.Accel
		if applied.Launch.Machine != "" {
			obs.Machine = applied.Launch.Machine
		}
		if applied.Launch.Accel != "" {
			obs.Accel = applied.Launch.Accel
		}
		obs.PCI = applied.Launch.PCI
	}
	if e.LiveUnits != nil {
		if e.LiveUnits[id] {
			obs.Status = StatusRunning
			obs.UnitActive = true
			obs.Reason = "fixture live unit"
			return obs
		}
		obs.Status = StatusStopped
		obs.Reason = "fixture"
		return obs
	}
	if e.SkipHostCmds {
		obs.Status = StatusStopped
		obs.Reason = "fixture"
		return obs
	}
	active, _ := e.unitActive(ctx, id)
	obs.UnitActive = active
	state, _ := e.unitState(ctx, id)
	state = strings.TrimSpace(state)
	if applied, err := e.ReadApplied(id); err == nil && applied.Launch.PCI != nil {
		obs.PCI = applied.Launch.PCI
	}
	switch state {
	case "activating":
		obs.Status = StatusStarting
		obs.Reason = "unit activating"
		return obs
	case "deactivating":
		obs.Status = StatusStopping
		obs.Reason = "unit deactivating"
		return obs
	}
	if !active {
		if _, err := e.ReadApplied(id); err == nil {
			obs.Status = StatusStopped
		}
		if strings.Contains(state, "failed") {
			obs.Status = StatusCrashed
			obs.Reason = "qemu exited"
		}
		return obs
	}
	obs.Status = StatusRunning
	if q, err := e.dialQMP(id, 3*time.Second); err == nil {
		if st, err := q.queryStatus(); err == nil && st != "" {
			obs.Reason = "qmp:" + st
		} else {
			obs.Reason = "qmp connected"
		}
		if live, err := q.queryPCI(); err == nil && len(obs.PCI) > 0 {
			match := pciMatchesLaunch(live, obs.PCI)
			obs.PCILiveMatch = &match
		}
		q.Close()
	} else {
		obs.Reason = "unit active, qmp not connected"
	}
	if uid, err := e.mainPIDUser(ctx, id); err == nil {
		obs.RunningAs = uid
		if uid == "0" {
			obs.Reason = "qemu is running as root"
		}
	}
	return obs
}

func (e *Engine) ReconnectQMP(id string) error {
	q, err := e.dialQMP(id, 5*time.Second)
	if err != nil {
		return err
	}
	defer q.Close()
	_, err = q.queryStatus()
	return err
}

func (e *Engine) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (e *Engine) unitActive(ctx context.Context, id string) (bool, error) {
	out, err := e.run(ctx, BinSystemctl, "is-active", unitName(id))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "active", nil
}

func (e *Engine) unitState(ctx context.Context, id string) (string, error) {
	out, err := e.run(ctx, BinSystemctl, "show", "-p", "ActiveState", "--value", unitName(id))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (e *Engine) mainPIDUser(ctx context.Context, id string) (string, error) {
	out, err := e.run(ctx, BinSystemctl, "show", "-p", "MainPID", "--value", unitName(id))
	if err != nil {
		return "", err
	}
	pid := strings.TrimSpace(string(out))
	if pid == "" || pid == "0" {
		return "", fmt.Errorf("no main pid")
	}
	st, err := os.ReadFile("/proc/" + pid + "/status")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(st), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) > 1 {
				return fields[1], nil
			}
		}
	}
	return "", fmt.Errorf("uid missing")
}
