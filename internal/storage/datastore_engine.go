package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DatastoreOp is a typed NFS/SMB/iSCSI action. There is no generic argv field.
type DatastoreOp struct {
	Action   string `json:"action"`
	PoolID   string `json:"pool_id"`
	Kind     string `json:"kind"`
	Locator  string `json:"locator"`
	Portal   string `json:"portal,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	IQN      string `json:"iqn,omitempty"`
}

// DatastoreResult is honest mount/login/observe outcome.
type DatastoreResult struct {
	Status       string       `json:"status"`
	Reason       string       `json:"reason,omitempty"`
	PoolID       string       `json:"pool_id,omitempty"`
	Kind         string       `json:"kind,omitempty"`
	RootPath     string       `json:"root_path,omitempty"`
	BackendRef   string       `json:"backend_ref,omitempty"`
	Argv         []string     `json:"argv,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
	Incremental  bool         `json:"incremental_send"`
	Warnings     []string     `json:"warnings,omitempty"`
	WarningText  []string     `json:"warning_text,omitempty"`
}

// DatastoreEngine runs typed mount/iscsiadm argv. SkipHostCmds is the Cloud-safe default.
type DatastoreEngine struct {
	Run          func(ctx context.Context, argv []string) (string, error)
	SkipHostCmds bool
	Now          func() time.Time
	Installed    *bool
	Mounted      map[string]bool
	Devices      map[string]bool
}

func (e DatastoreEngine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

func (e DatastoreEngine) installed() bool {
	if e.Installed != nil {
		return *e.Installed
	}
	return !e.SkipHostCmds
}

// Apply executes one typed datastore operation. Passwords never appear in argv.
func (e DatastoreEngine) Apply(ctx context.Context, op DatastoreOp) (DatastoreResult, error) {
	kind := strings.ToLower(strings.TrimSpace(op.Kind))
	switch strings.ToLower(strings.TrimSpace(op.Action)) {
	case "status", "observe":
		return e.observeOne(ctx, op)
	case "mount", "add", "create":
		switch kind {
		case BackendNFS:
			return e.mountNFS(ctx, op)
		case BackendSMB:
			return e.mountSMB(ctx, op)
		case BackendISCSI:
			return e.loginISCSI(ctx, op)
		default:
			return DatastoreResult{}, fmt.Errorf("datastore kind is unsupported")
		}
	default:
		return DatastoreResult{}, fmt.Errorf("datastore action is unsupported")
	}
}

func (e DatastoreEngine) mountNFS(ctx context.Context, op DatastoreOp) (DatastoreResult, error) {
	server, export, err := ParseNFSLocator(op.Locator)
	if err != nil {
		return DatastoreResult{}, err
	}
	mount, err := DatastoreMountPath(BackendNFS, op.PoolID)
	if err != nil {
		return DatastoreResult{}, err
	}
	argv, err := NFSMountArgv(server, export, mount)
	if err != nil {
		return DatastoreResult{}, err
	}
	res := DatastoreResult{
		PoolID: op.PoolID, Kind: BackendNFS, Argv: argv, RootPath: mount,
		Capabilities: NFSCapabilities(), Incremental: false,
		Warnings: []string{WarnSharedFilesystem}, WarningText: []string{NFSShareMsg},
	}
	if err := e.ensureDir(mount); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	if err := e.exec(ctx, argv); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	if e.SkipHostCmds && !e.mountPresent(BackendNFS, mount) {
		res.Status = StatusUnavailable
		res.Reason = NFSMissing
		return res, nil
	}
	res.Status = StatusAvailable
	return res, nil
}

func (e DatastoreEngine) mountSMB(ctx context.Context, op DatastoreOp) (DatastoreResult, error) {
	server, share, err := ParseSMBLocator(op.Locator)
	if err != nil {
		return DatastoreResult{}, err
	}
	mount, err := DatastoreMountPath(BackendSMB, op.PoolID)
	if err != nil {
		return DatastoreResult{}, err
	}
	cred, err := CredPath(op.PoolID)
	if err != nil {
		return DatastoreResult{}, err
	}
	if err := e.writeCred(cred, op.Username, op.Password); err != nil {
		return DatastoreResult{}, err
	}
	argv, err := SMBMountArgv(server, share, cred, mount)
	if err != nil {
		return DatastoreResult{}, err
	}
	res := DatastoreResult{
		PoolID: op.PoolID, Kind: BackendSMB, Argv: argv, RootPath: mount,
		Capabilities: SMBCapabilities(), Incremental: false,
		Warnings: []string{WarnSharedFilesystem}, WarningText: []string{NFSShareMsg},
	}
	if err := e.ensureDir(mount); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	if err := e.exec(ctx, argv); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	if e.SkipHostCmds && !e.mountPresent(BackendSMB, mount) {
		res.Status = StatusUnavailable
		res.Reason = SMBMissing
		return res, nil
	}
	res.Status = StatusAvailable
	return res, nil
}

func (e DatastoreEngine) loginISCSI(ctx context.Context, op DatastoreOp) (DatastoreResult, error) {
	iqn := op.IQN
	if iqn == "" {
		iqn = op.Locator
	}
	iqn, err := ParseIQN(iqn)
	if err != nil {
		return DatastoreResult{}, err
	}
	portal, err := ParsePortal(op.Portal)
	if err != nil {
		return DatastoreResult{}, err
	}
	disc, err := ISCSIDiscoveryArgv(portal)
	if err != nil {
		return DatastoreResult{}, err
	}
	login, err := ISCSILoginArgv(iqn, portal)
	if err != nil {
		return DatastoreResult{}, err
	}
	dev, err := ISCSIDevicePath(portal, iqn)
	if err != nil {
		return DatastoreResult{}, err
	}
	res := DatastoreResult{
		PoolID: op.PoolID, Kind: BackendISCSI, Argv: append(append([]string{}, disc...), login...),
		RootPath: dev, BackendRef: dev, Capabilities: ISCSICapabilities(), Incremental: false,
		Warnings: []string{WarnSharedFilesystem}, WarningText: []string{ISCSIBlockMsg},
	}
	if err := e.exec(ctx, disc); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	if err := e.exec(ctx, login); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	if e.SkipHostCmds && !e.devicePresent(dev) {
		res.Status = StatusUnavailable
		res.Reason = ISCSIMissing
		return res, nil
	}
	res.Status = StatusAvailable
	return res, nil
}

func (e DatastoreEngine) observeOne(_ context.Context, op DatastoreOp) (DatastoreResult, error) {
	kind := strings.ToLower(strings.TrimSpace(op.Kind))
	res := DatastoreResult{PoolID: op.PoolID, Kind: kind, Capabilities: capForKind(kind), Incremental: false}
	if !e.installed() {
		res.Status = StatusUnavailable
		switch kind {
		case BackendSMB:
			res.Reason = SMBMissing
		case BackendISCSI:
			res.Reason = ISCSIMissing
		default:
			res.Reason = NFSMissing
		}
		return res, nil
	}
	switch kind {
	case BackendNFS, BackendSMB:
		mount, err := DatastoreMountPath(kind, op.PoolID)
		if err != nil {
			return DatastoreResult{}, err
		}
		res.RootPath = mount
		if e.mountPresent(kind, mount) {
			res.Status = StatusAvailable
			res.Warnings = []string{WarnSharedFilesystem}
			res.WarningText = []string{NFSShareMsg}
			return res, nil
		}
		res.Status = StatusUnavailable
		if kind == BackendSMB {
			res.Reason = SMBMissing
		} else {
			res.Reason = NFSMissing
		}
		return res, nil
	case BackendISCSI:
		iqn := op.IQN
		if iqn == "" {
			iqn = op.Locator
		}
		dev, err := ISCSIDevicePath(op.Portal, iqn)
		if err != nil {
			return DatastoreResult{}, err
		}
		res.RootPath = dev
		res.BackendRef = dev
		if e.devicePresent(dev) {
			res.Status = StatusAvailable
			res.Warnings = []string{WarnSharedFilesystem}
			res.WarningText = []string{ISCSIBlockMsg}
			return res, nil
		}
		res.Status = StatusUnavailable
		res.Reason = ISCSIMissing
		return res, nil
	default:
		return DatastoreResult{}, fmt.Errorf("datastore kind is unsupported")
	}
}

// ObserveHints reports NFS/SMB/iSCSI pools. A down share stays unavailable; rows remain.
func (e DatastoreEngine) ObserveHints(ctx context.Context, hints []PoolHint) []ObservedPool {
	var out []ObservedPool
	for _, h := range hints {
		if h.BackendType != BackendNFS && h.BackendType != BackendSMB && h.BackendType != BackendISCSI {
			continue
		}
		op := DatastoreOp{Action: "observe", PoolID: h.PoolID, Kind: h.BackendType, Locator: h.Backing.Device, Portal: h.Backing.MountPoint, IQN: h.Backing.FSUUID}
		if h.BackendType == BackendISCSI {
			op.Portal = h.Backing.Device
			op.IQN = h.Backing.FSUUID
		}
		res, err := e.observeOne(ctx, op)
		obs := ObservedPool{
			PoolID: h.PoolID, BackendType: h.BackendType, RootPath: h.RootPath,
			Status: StatusUnavailable, Capabilities: capForKind(h.BackendType), ObservedAt: e.now(),
		}
		if err != nil {
			obs.Reason = err.Error()
			out = append(out, obs)
			continue
		}
		obs.Status = res.Status
		obs.Reason = res.Reason
		obs.Warnings = res.Warnings
		obs.WarningText = res.WarningText
		if res.RootPath != "" {
			obs.RootPath = res.RootPath
		}
		if res.Status == StatusUnavailable {
			obs.Capacity = Capacity{}
			obs.Writable = false
		} else {
			obs.Writable = true
		}
		out = append(out, obs)
	}
	return out
}

func (e DatastoreEngine) mountPresent(kind, mount string) bool {
	if e.Mounted != nil {
		return e.Mounted[mount]
	}
	if e.SkipHostCmds {
		return false
	}
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	cover, ok := CoveringMount(mount, ParseMountinfo(string(b)))
	if !ok || cover.MountPoint != mount {
		return false
	}
	ft := strings.ToLower(cover.FSType)
	if kind == BackendSMB {
		return ft == "cifs" || ft == "smb3" || ft == "smb"
	}
	return ft == "nfs" || ft == "nfs4"
}

func (e DatastoreEngine) devicePresent(dev string) bool {
	if e.Devices != nil {
		return e.Devices[dev]
	}
	if e.SkipHostCmds {
		return false
	}
	_, err := os.Stat(dev)
	return err == nil
}

func (e DatastoreEngine) ensureDir(mount string) error {
	if e.SkipHostCmds {
		return nil
	}
	return os.MkdirAll(mount, 0o755)
}

func (e DatastoreEngine) writeCred(path, user, pass string) error {
	if strings.Contains(pass, "\n") || strings.Contains(user, "\n") {
		return fmt.Errorf("credentials contain a banned character")
	}
	if e.SkipHostCmds {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body := "username=" + user + "\npassword=" + pass + "\n"
	return os.WriteFile(path, []byte(body), 0o600)
}

func (e DatastoreEngine) exec(ctx context.Context, argv []string) error {
	if err := refuseDatastoreArgv(argv); err != nil {
		return err
	}
	if e.SkipHostCmds {
		return nil
	}
	if !e.installed() {
		return fmt.Errorf("network datastore tools are not installed")
	}
	if e.Run == nil {
		return fmt.Errorf("network datastore tools are not installed")
	}
	_, err := e.Run(ctx, argv)
	return err
}
