package storage

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// ZFSOp is a typed ZFS/zpool action. There is no generic argv field.
type ZFSOp struct {
	Action     string   `json:"action"`
	PoolID     string   `json:"pool_id"`
	Name       string   `json:"name"`
	GUID       string   `json:"guid"`
	Disks      []string `json:"disks,omitempty"`
	VolumeID   string   `json:"volume_id,omitempty"`
	Class      string   `json:"class,omitempty"`
	SizeBytes  int64    `json:"size_bytes,omitempty"`
	Snapshot   string   `json:"snapshot,omitempty"`
	FromSnap   string   `json:"from_snapshot,omitempty"`
	DestPath   string   `json:"dest_path,omitempty"`
	Force      bool     `json:"force,omitempty"`
	RootDevice string   `json:"-"`
}

// ZFSResult is honest apply/observe outcome.
type ZFSResult struct {
	Status       string       `json:"status"`
	Reason       string       `json:"reason,omitempty"`
	PoolID       string       `json:"pool_id,omitempty"`
	Name         string       `json:"name,omitempty"`
	GUID         string       `json:"guid,omitempty"`
	RootPath     string       `json:"root_path,omitempty"`
	BackendRef   string       `json:"backend_ref,omitempty"`
	Dataset      string       `json:"dataset,omitempty"`
	Argv         []string     `json:"argv,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
	Incremental  bool         `json:"incremental_send"`
}

// ZFSEngine runs typed zpool/zfs argv. SkipHostCmds is the Cloud-safe default.
type ZFSEngine struct {
	Run          func(ctx context.Context, argv []string) (string, error)
	SkipHostCmds bool
	Now          func() time.Time
	Installed    *bool
}

func (e ZFSEngine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

func (e ZFSEngine) installed() bool {
	if e.Installed != nil {
		return *e.Installed
	}
	return !e.SkipHostCmds
}

// Apply executes one typed ZFS operation.
func (e ZFSEngine) Apply(ctx context.Context, op ZFSOp) (ZFSResult, error) {
	if err := RefuseForceImport(op.Force); err != nil {
		return ZFSResult{}, err
	}
	res := ZFSResult{Capabilities: ZFSCapabilities(), Incremental: true, PoolID: op.PoolID, Name: op.Name}
	switch strings.ToLower(strings.TrimSpace(op.Action)) {
	case "status", "observe":
		return e.observeOne(ctx, op)
	case "import":
		guid, err := ParseZPoolGUID(op.GUID)
		if err != nil {
			return ZFSResult{}, err
		}
		argv, err := ZFSImportArgv(guid)
		if err != nil {
			return ZFSResult{}, err
		}
		res.GUID, res.Argv = guid, argv
		if err := e.exec(ctx, argv); err != nil {
			res.Status = StatusUnavailable
			res.Reason = err.Error()
			return res, nil
		}
		res.Status = StatusAvailable
		res.RootPath = ZFSMountRoot + "/" + guid
		return res, nil
	case "create-pool":
		name, err := ParseZFSName(op.Name)
		if err != nil {
			return ZFSResult{}, err
		}
		var disks []string
		for _, d := range op.Disks {
			loc, err := ParseDiskLocator(d, op.RootDevice)
			if err != nil {
				return ZFSResult{}, err
			}
			disks = append(disks, loc)
		}
		argv, err := ZFSCreatePoolArgv(name, disks)
		if err != nil {
			return ZFSResult{}, err
		}
		res.Name, res.Argv = name, argv
		if err := e.exec(ctx, argv); err != nil {
			res.Status = StatusFailed
			res.Reason = err.Error()
			return res, nil
		}
		res.Status = StatusAvailable
		res.RootPath = ZFSMountRoot + "/" + name
		if guidArgv, err := ZFSGetGUIDArgv(name); err == nil {
			res.Argv = append(res.Argv, strings.Join(guidArgv, " "))
			if out, err := e.output(ctx, guidArgv); err == nil {
				if g, perr := ParseZPoolGUID(strings.TrimSpace(out)); perr == nil {
					res.GUID = g
					res.RootPath = ZFSMountRoot + "/" + g
				}
			}
		}
		return res, nil
	case "create-volume":
		return e.createVolume(ctx, op)
	case "snapshot":
		ds, err := DatasetName(op.Name, op.VolumeID)
		if err != nil {
			return ZFSResult{}, err
		}
		argv, err := ZFSSnapshotArgv(ds, op.Snapshot)
		if err != nil {
			return ZFSResult{}, err
		}
		res.Dataset, res.Argv, res.BackendRef = ds, argv, ds+"@"+op.Snapshot
		if err := e.exec(ctx, argv); err != nil {
			res.Status = StatusFailed
			res.Reason = err.Error()
			return res, nil
		}
		res.Status = StatusAvailable
		return res, nil
	case "rollback":
		ds, err := DatasetName(op.Name, op.VolumeID)
		if err != nil {
			return ZFSResult{}, err
		}
		argv, err := ZFSRollbackArgv(ds, op.Snapshot)
		if err != nil {
			return ZFSResult{}, err
		}
		res.Dataset, res.Argv, res.BackendRef = ds, argv, ds+"@"+op.Snapshot
		if err := e.exec(ctx, argv); err != nil {
			res.Status = StatusFailed
			res.Reason = err.Error()
			return res, nil
		}
		res.Status = StatusAvailable
		return res, nil
	case "send":
		ds, err := DatasetName(op.Name, op.VolumeID)
		if err != nil {
			return ZFSResult{}, err
		}
		argv, err := ZFSSendArgv(ds, op.Snapshot, op.FromSnap)
		if err != nil {
			return ZFSResult{}, err
		}
		res.Dataset, res.Argv = ds, argv
		if op.DestPath != "" {
			dest, err := ParseSendDest(op.DestPath)
			if err != nil {
				return ZFSResult{}, err
			}
			if err := e.sendTo(ctx, argv, dest); err != nil {
				res.Status = StatusFailed
				res.Reason = err.Error()
				return res, nil
			}
			res.BackendRef = dest
		}
		res.Status = StatusAvailable
		return res, nil
	default:
		return ZFSResult{}, fmt.Errorf("zfs action is unsupported")
	}
}

func (e ZFSEngine) createVolume(ctx context.Context, op ZFSOp) (ZFSResult, error) {
	ds, err := DatasetName(op.Name, op.VolumeID)
	if err != nil {
		return ZFSResult{}, err
	}
	res := ZFSResult{Capabilities: ZFSCapabilities(), Incremental: true, PoolID: op.PoolID, Name: op.Name, Dataset: ds}
	switch op.Class {
	case ClassVMDisk:
		argv, err := ZFSCreateZVolArgv(ds, op.SizeBytes)
		if err != nil {
			return ZFSResult{}, err
		}
		res.Argv = argv
		res.BackendRef = ZVolPath(ds)
	case ClassContainerRoot, ClassISO, ClassTemplate, ClassBackupStaging:
		mount := ZFSMountRoot + "/" + op.PoolID + "/volumes/" + op.Class + "/" + op.VolumeID
		argv, err := ZFSCreateDatasetArgv(ds, mount)
		if err != nil {
			return ZFSResult{}, err
		}
		res.Argv = argv
		res.BackendRef = mount
	default:
		return ZFSResult{}, fmt.Errorf("storage class is unsupported on ZFS")
	}
	if err := e.exec(ctx, res.Argv); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	res.Status = StatusAvailable
	return res, nil
}

func (e ZFSEngine) observeOne(ctx context.Context, op ZFSOp) (ZFSResult, error) {
	res := ZFSResult{Capabilities: ZFSCapabilities(), Incremental: true, PoolID: op.PoolID, Name: op.Name, GUID: op.GUID}
	if !e.installed() {
		res.Status = StatusUnavailable
		res.Reason = ZFSMissing
		return res, nil
	}
	name := op.Name
	if name == "" {
		res.Status = StatusUnavailable
		res.Reason = "zpool name is not reported"
		return res, nil
	}
	argv, err := ZFSStatusArgv(name)
	if err != nil {
		return ZFSResult{}, err
	}
	res.Argv = argv
	out, err := e.output(ctx, argv)
	if err != nil {
		res.Status = StatusUnavailable
		res.Reason = "pool is faulted or missing. Desired rows remain."
		return res, nil
	}
	lower := strings.ToLower(out)
	if strings.Contains(lower, "faulted") || strings.Contains(lower, "unavailable") || strings.Contains(lower, "no such pool") {
		res.Status = StatusUnavailable
		res.Reason = "pool is faulted or missing. Desired rows remain."
		return res, nil
	}
	res.Status = StatusAvailable
	if op.GUID != "" {
		res.RootPath = ZFSMountRoot + "/" + op.GUID
	}
	return res, nil
}

// ObserveHints reports ZFS pools. Pulled disks stay unavailable with nil capacity.
func (e ZFSEngine) ObserveHints(ctx context.Context, hints []PoolHint) []ObservedPool {
	var out []ObservedPool
	for _, h := range hints {
		if h.BackendType != BackendZFS {
			continue
		}
		op := ZFSOp{Action: "observe", PoolID: h.PoolID, Name: h.Backing.Device, GUID: h.Backing.FSUUID}
		res, err := e.observeOne(ctx, op)
		obs := ObservedPool{
			PoolID: h.PoolID, BackendType: BackendZFS, RootPath: h.RootPath,
			Status: StatusUnavailable, Capabilities: ZFSCapabilities(), ObservedAt: e.now(),
		}
		if err != nil {
			obs.Reason = err.Error()
			out = append(out, obs)
			continue
		}
		obs.Status = res.Status
		obs.Reason = res.Reason
		if res.RootPath != "" {
			obs.RootPath = res.RootPath
		}
		if res.Status != StatusAvailable {
			obs.Capacity = Capacity{}
			obs.Writable = false
		} else {
			obs.Writable = true
		}
		out = append(out, obs)
	}
	return out
}

func (e ZFSEngine) sendTo(ctx context.Context, argv []string, dest string) error {
	if e.SkipHostCmds {
		return fmt.Errorf("host commands skipped; zfs send was not run")
	}
	out, err := e.output(ctx, argv)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, []byte(out), 0o600)
}

func (e ZFSEngine) exec(ctx context.Context, argv []string) error {
	if e.SkipHostCmds {
		return fmt.Errorf("host commands skipped; zfs was not run")
	}
	if !e.installed() {
		return fmt.Errorf(ZFSMissing)
	}
	_, err := e.output(ctx, argv)
	return err
}

func (e ZFSEngine) output(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 || (argv[0] != ZPoolBin && argv[0] != ZFSBin) {
		return "", fmt.Errorf("zfs argv is not typed")
	}
	for _, a := range argv {
		if a == "-f" || a == "-F" || strings.EqualFold(a, "--force") {
			return "", fmt.Errorf(ZFSForceRefuse)
		}
		if strings.Contains(a, "bash") || strings.Contains(a, "/bin/sh") {
			return "", fmt.Errorf("shell is not a typed zfs action")
		}
	}
	if e.SkipHostCmds {
		return "", fmt.Errorf("host commands skipped; zfs was not run")
	}
	if e.Run == nil {
		return "", fmt.Errorf(ZFSMissing)
	}
	return e.Run(ctx, argv)
}
