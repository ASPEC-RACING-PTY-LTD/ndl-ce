package storage

import (
	"context"
	"fmt"
	"strings"
)

// DistributedOp is a typed RBD/OSD action. There is no generic argv field.
type DistributedOp struct {
	Action     string `json:"action"`
	PoolID     string `json:"pool_id"`
	Locator    string `json:"locator,omitempty"`
	CephPool   string `json:"ceph_pool,omitempty"`
	CephUser   string `json:"ceph_user,omitempty"`
	CephxKey   string `json:"cephx_key,omitempty"`
	Keyring    string `json:"keyring_path,omitempty"`
	VolumeID   string `json:"volume_id,omitempty"`
	Class      string `json:"class,omitempty"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	Disk       string `json:"disk,omitempty"`
	Image      string `json:"image,omitempty"`
	BackendRef string `json:"backend_ref,omitempty"`
	RootDevice string `json:"root_device,omitempty"`
}

// DistributedResult is honest cluster/OSD outcome. Keys are never copied here.
type DistributedResult struct {
	Status       string       `json:"status"`
	Reason       string       `json:"reason,omitempty"`
	PoolID       string       `json:"pool_id,omitempty"`
	BackendType  string       `json:"backend_type,omitempty"`
	BackendRef   string       `json:"backend_ref,omitempty"`
	RootPath     string       `json:"root_path,omitempty"`
	Kind         string       `json:"kind,omitempty"`
	Format       string       `json:"format,omitempty"`
	Class        string       `json:"class,omitempty"`
	Argv         []string     `json:"argv,omitempty"`
	OSDStarted   bool         `json:"osd_started,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
	Incremental  bool         `json:"incremental_send"`
	Warnings     []string     `json:"warnings,omitempty"`
	WarningText  []string     `json:"warning_text,omitempty"`
}

// DistributedEngine runs typed rbd and ceph-volume argv. SkipHostCmds is the Cloud-safe default.
type DistributedEngine struct {
	Run          func(ctx context.Context, argv []string) (string, error)
	SkipHostCmds bool
	ClusterUp    *bool
	OSDRunning   *bool
	Installed    *bool
}

func (e DistributedEngine) installed() bool {
	if e.Installed != nil {
		return *e.Installed
	}
	return !e.SkipHostCmds
}

func (e DistributedEngine) clusterUp() bool {
	if e.ClusterUp != nil {
		return *e.ClusterUp
	}
	return !e.SkipHostCmds
}

// Apply executes one typed distributed-storage operation. Keys never appear in argv.
func (e DistributedEngine) Apply(ctx context.Context, op DistributedOp) (DistributedResult, error) {
	action := strings.ToLower(strings.TrimSpace(op.Action))
	switch action {
	case "status", "observe":
		return e.observe(ctx, op)
	case "attach", "mount", "add":
		return e.attach(ctx, op)
	case "create-volume":
		return e.createVolume(ctx, op)
	case "osd-create":
		return e.osdCreate(ctx, op)
	case "osd-observe":
		return e.osdObserve()
	default:
		return DistributedResult{}, fmt.Errorf("distributed action is unsupported")
	}
}

func (e DistributedEngine) attach(ctx context.Context, op DistributedOp) (DistributedResult, error) {
	mons, pool, err := ParseDistributedLocator(op.Locator)
	if err != nil {
		return DistributedResult{}, err
	}
	user, err := ParseCephUser(firstNonEmpty(op.CephUser, DefaultCephUser))
	if err != nil {
		return DistributedResult{}, err
	}
	keyring := strings.TrimSpace(op.Keyring)
	if keyring == "" {
		keyring, err = KeyringPath(op.PoolID)
		if err != nil {
			return DistributedResult{}, err
		}
	}
	if strings.TrimSpace(op.CephxKey) != "" && !e.SkipHostCmds {
		if _, werr := WriteKeyring(op.PoolID, user, op.CephxKey); werr != nil {
			return DistributedResult{}, werr
		}
	}
	argv, err := RBDListArgv(user, keyring, strings.Join(mons, ","), pool)
	if err != nil {
		return DistributedResult{}, err
	}
	if ArgvContainsSecret(argv, op.CephxKey) {
		return DistributedResult{}, fmt.Errorf("cephx key must not appear in argv")
	}
	res := DistributedResult{
		PoolID: op.PoolID, BackendType: BackendDistributed, Argv: argv,
		RootPath: RBDDevPrefix + pool, Capabilities: DistributedCapabilities(),
		Incremental: false, Warnings: []string{WarnSharedFilesystem},
		WarningText: []string{ClusterDownMsg},
	}
	if e.SkipHostCmds {
		if e.clusterUp() {
			res.Status = StatusAvailable
			res.Reason = "Fake distributed cluster. Host rbd was not run."
			res.Warnings = nil
			res.WarningText = nil
			return res, nil
		}
		res.Status = StatusUnavailable
		res.Reason = ClusterDownMsg
		if e.Installed != nil && !*e.Installed {
			res.Reason = DistributedMissing
		}
		return res, nil
	}
	if !e.installed() {
		res.Status = StatusUnavailable
		res.Reason = DistributedMissing
		return res, nil
	}
	if err := e.exec(ctx, argv); err != nil {
		res.Status = StatusUnavailable
		res.Reason = ClusterDownMsg
		return res, nil
	}
	res.Status = StatusAvailable
	res.Reason = ""
	res.Warnings = nil
	res.WarningText = nil
	return res, nil
}

func (e DistributedEngine) observe(ctx context.Context, op DistributedOp) (DistributedResult, error) {
	res, err := e.attach(ctx, op)
	if err != nil {
		return DistributedResult{
			Status: StatusUnavailable, Reason: err.Error(), PoolID: op.PoolID,
			BackendType: BackendDistributed, Capabilities: DistributedCapabilities(), Incremental: false,
		}, nil
	}
	if res.Status != StatusAvailable {
		res.Reason = firstNonEmpty(res.Reason, ClusterDownMsg)
	}
	return res, nil
}

func (e DistributedEngine) createVolume(ctx context.Context, op DistributedOp) (DistributedResult, error) {
	if op.Class != "" && op.Class != ClassVMDisk {
		return DistributedResult{}, fmt.Errorf("distributed pools store VM disk RBDs")
	}
	mons, pool, err := ParseDistributedLocator(op.Locator)
	if err != nil {
		return DistributedResult{}, err
	}
	if op.CephPool != "" {
		pool, err = ParseCephPool(op.CephPool)
		if err != nil {
			return DistributedResult{}, err
		}
	}
	image := strings.TrimSpace(op.Image)
	if image == "" {
		image = strings.TrimSpace(op.VolumeID)
	}
	image, err = ParseCephImage(image)
	if err != nil {
		return DistributedResult{}, err
	}
	user, err := ParseCephUser(firstNonEmpty(op.CephUser, DefaultCephUser))
	if err != nil {
		return DistributedResult{}, err
	}
	keyring := strings.TrimSpace(op.Keyring)
	if keyring == "" {
		keyring, err = KeyringPath(op.PoolID)
		if err != nil {
			return DistributedResult{}, err
		}
	}
	if strings.TrimSpace(op.CephxKey) != "" && !e.SkipHostCmds {
		if _, werr := WriteKeyring(op.PoolID, user, op.CephxKey); werr != nil {
			return DistributedResult{}, werr
		}
	}
	monArg := strings.Join(mons, ",")
	createArgv, err := RBDCreateArgv(user, keyring, monArg, pool, image, op.SizeBytes)
	if err != nil {
		return DistributedResult{}, err
	}
	mapArgv, err := RBDMapArgv(user, keyring, monArg, pool, image)
	if err != nil {
		return DistributedResult{}, err
	}
	if ArgvContainsSecret(createArgv, op.CephxKey) || ArgvContainsSecret(mapArgv, op.CephxKey) {
		return DistributedResult{}, fmt.Errorf("cephx key must not appear in argv")
	}
	dev, err := RBDDevicePath(pool, image)
	if err != nil {
		return DistributedResult{}, err
	}
	res := DistributedResult{
		PoolID: op.PoolID, BackendType: BackendDistributed, BackendRef: dev,
		Kind: KindBlock, Format: FormatRaw, Class: ClassVMDisk,
		Argv:         append(append([]string{}, createArgv...), mapArgv...),
		Capabilities: DistributedCapabilities(), Incremental: false,
	}
	if !e.clusterUp() {
		res.Status = StatusUnavailable
		res.Reason = ClusterDownMsg
		return res, nil
	}
	if err := e.exec(ctx, createArgv); err != nil {
		res.Status = StatusUnavailable
		res.Reason = firstNonEmpty(err.Error(), ClusterDownMsg)
		return res, nil
	}
	if err := e.exec(ctx, mapArgv); err != nil {
		res.Status = StatusUnavailable
		res.Reason = firstNonEmpty(err.Error(), ClusterDownMsg)
		return res, nil
	}
	if e.SkipHostCmds && e.clusterUp() {
		res.Status = StatusAvailable
		res.Reason = "Fake RBD handle. Host rbd map was not run."
		return res, nil
	}
	res.Status = StatusAvailable
	return res, nil
}

func (e DistributedEngine) osdCreate(ctx context.Context, op DistributedOp) (DistributedResult, error) {
	disk, err := ParseOSDDisk(op.Disk, op.RootDevice)
	if err != nil {
		return DistributedResult{}, err
	}
	argv, err := OSDCreateArgv(disk)
	if err != nil {
		return DistributedResult{}, err
	}
	res := DistributedResult{
		PoolID: op.PoolID, BackendType: BackendDistributed, Argv: argv,
		Capabilities: DistributedCapabilities(), Incremental: false,
	}
	if err := e.exec(ctx, argv); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	if e.SkipHostCmds {
		res.Status = StatusUnavailable
		res.OSDStarted = false
		res.Reason = OSDNotStarted + " Host ceph-volume was not run on this node."
		return res, nil
	}
	res.Status = StatusAvailable
	res.OSDStarted = true
	res.Reason = "OSD create was requested via ceph-volume on an extra disk."
	return res, nil
}

func (e DistributedEngine) osdObserve() (DistributedResult, error) {
	res := DistributedResult{
		BackendType: BackendDistributed, Capabilities: DistributedCapabilities(), Incremental: false,
		Status: StatusUnavailable, Reason: OSDNotStarted,
	}
	if e.OSDRunning != nil && *e.OSDRunning {
		res.Status = StatusAvailable
		res.OSDStarted = true
		res.Reason = "A ceph-osd process is running. Default No-dal install does not start OSDs."
		return res, nil
	}
	return res, nil
}

func (e DistributedEngine) exec(ctx context.Context, argv []string) error {
	if e.SkipHostCmds {
		return nil
	}
	if e.Run == nil {
		return fmt.Errorf("distributed runtime command runner is missing")
	}
	_, err := e.Run(ctx, argv)
	return err
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
