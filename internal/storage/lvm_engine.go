package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// LVMOp is a typed LVM action. There is no generic argv field.
type LVMOp struct {
	Action     string   `json:"action"`
	PoolID     string   `json:"pool_id"`
	Name       string   `json:"name"`
	VGUUID     string   `json:"vg_uuid,omitempty"`
	Disks      []string `json:"disks,omitempty"`
	VolumeID   string   `json:"volume_id,omitempty"`
	Class      string   `json:"class,omitempty"`
	SizeBytes  int64    `json:"size_bytes,omitempty"`
	Snapshot   string   `json:"snapshot,omitempty"`
	RootDevice string   `json:"-"`
}

// LVMResult is honest apply/observe outcome. Incremental send is always false.
type LVMResult struct {
	Status          string       `json:"status"`
	Reason          string       `json:"reason,omitempty"`
	PoolID          string       `json:"pool_id,omitempty"`
	Name            string       `json:"name,omitempty"`
	VGUUID          string       `json:"vg_uuid,omitempty"`
	LVUUID          string       `json:"lv_uuid,omitempty"`
	ThinPool        string       `json:"thin_pool,omitempty"`
	RootPath        string       `json:"root_path,omitempty"`
	BackendRef      string       `json:"backend_ref,omitempty"`
	Argv            []string     `json:"argv,omitempty"`
	Capabilities    Capabilities `json:"capabilities"`
	Incremental     bool         `json:"incremental_send"`
	MetadataPercent *float64     `json:"metadata_percent,omitempty"`
	Warnings        []string     `json:"warnings,omitempty"`
	WarningText     []string     `json:"warning_text,omitempty"`
	Capacity        Capacity     `json:"capacity"`
}

// LVMEngine runs typed lvm argv. SkipHostCmds is the Cloud-safe default.
type LVMEngine struct {
	Run          func(ctx context.Context, argv []string) (string, error)
	SkipHostCmds bool
	Now          func() time.Time
	Installed    *bool
}

func (e LVMEngine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

func (e LVMEngine) installed() bool {
	if e.Installed != nil {
		return *e.Installed
	}
	return !e.SkipHostCmds
}

// Apply executes one typed LVM operation.
func (e LVMEngine) Apply(ctx context.Context, op LVMOp) (LVMResult, error) {
	switch strings.ToLower(strings.TrimSpace(op.Action)) {
	case "status", "observe":
		return e.observeOne(ctx, op)
	case "create-pool":
		return e.createPool(ctx, op)
	case "create-volume":
		return e.createVolume(ctx, op)
	case "snapshot":
		return e.snapshot(ctx, op)
	case "rollback":
		return e.rollback(ctx, op)
	case "send":
		return LVMResult{}, fmt.Errorf(LVMNoSend)
	default:
		return LVMResult{}, fmt.Errorf("lvm action is unsupported")
	}
}

func (e LVMEngine) createPool(ctx context.Context, op LVMOp) (LVMResult, error) {
	name, err := ParseVGName(op.Name)
	if err != nil {
		return LVMResult{}, err
	}
	var disks []string
	for _, d := range op.Disks {
		loc, err := ParseLVMDisk(d, op.RootDevice)
		if err != nil {
			return LVMResult{}, err
		}
		disks = append(disks, loc)
	}
	if len(disks) == 0 {
		return LVMResult{}, fmt.Errorf("at least one extra disk is required")
	}
	res := LVMResult{Capabilities: LVMCapabilities(), Incremental: false, PoolID: op.PoolID, Name: name, ThinPool: LVMThinPoolName}
	pv, err := PVCreateArgv(disks[0])
	if err != nil {
		return LVMResult{}, err
	}
	vg, err := VGCreateArgv(name, disks)
	if err != nil {
		return LVMResult{}, err
	}
	pool, err := LVCreateThinPoolArgv(name)
	if err != nil {
		return LVMResult{}, err
	}
	res.Argv = append(res.Argv, pv...)
	if err := e.exec(ctx, pv); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	res.Argv = append(res.Argv, vg...)
	if err := e.exec(ctx, vg); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	res.Argv = append(res.Argv, pool...)
	if err := e.exec(ctx, pool); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	res.Status = StatusAvailable
	res.RootPath = LVMMountRoot + "/" + name
	if guidArgv, err := VGUUIDArgv(name); err == nil {
		if out, err := e.output(ctx, guidArgv); err == nil {
			if g, perr := ParseVGUUID(strings.TrimSpace(out)); perr == nil {
				res.VGUUID = g
			}
		}
	}
	return res, nil
}

func (e LVMEngine) createVolume(ctx context.Context, op LVMOp) (LVMResult, error) {
	vg, err := ParseVGName(op.Name)
	if err != nil {
		return LVMResult{}, err
	}
	lv, err := ParseLVName(op.VolumeID)
	if err != nil {
		return LVMResult{}, err
	}
	res := LVMResult{Capabilities: LVMCapabilities(), Incremental: false, PoolID: op.PoolID, Name: vg, ThinPool: LVMThinPoolName}
	argv, err := LVCreateThinArgv(vg, lv, op.SizeBytes)
	if err != nil {
		return LVMResult{}, err
	}
	res.Argv = argv
	dev := LVMDevicePath(vg, lv)
	switch op.Class {
	case ClassVMDisk:
		res.BackendRef = dev
	case ClassContainerRoot, ClassISO, ClassTemplate, ClassBackupStaging:
		mount := LVMMountRoot + "/" + op.PoolID + "/volumes/" + op.Class + "/" + op.VolumeID
		res.BackendRef = mount
	default:
		return LVMResult{}, fmt.Errorf("storage class is unsupported on LVM-thin")
	}
	if err := e.exec(ctx, argv); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	if uuidArgv, err := LVUUIDArgv(vg, lv); err == nil {
		if out, err := e.output(ctx, uuidArgv); err == nil {
			res.LVUUID = strings.TrimSpace(out)
		}
	}
	if op.Class != ClassVMDisk {
		if err := e.prepareFS(ctx, dev, res.BackendRef); err != nil {
			res.Status = StatusFailed
			res.Reason = err.Error()
			return res, nil
		}
	}
	res.Status = StatusAvailable
	return res, nil
}

func (e LVMEngine) prepareFS(ctx context.Context, dev, mount string) error {
	mkfs, err := MkfsExt4Argv(dev)
	if err != nil {
		return err
	}
	if err := e.exec(ctx, mkfs); err != nil {
		return err
	}
	if !e.SkipHostCmds {
		if err := os.MkdirAll(mount, 0o755); err != nil {
			return err
		}
	}
	mnt, err := MountLVMArgv(dev, mount)
	if err != nil {
		return err
	}
	return e.exec(ctx, mnt)
}

func (e LVMEngine) snapshot(ctx context.Context, op LVMOp) (LVMResult, error) {
	vg, err := ParseVGName(op.Name)
	if err != nil {
		return LVMResult{}, err
	}
	origin, err := ParseLVName(op.VolumeID)
	if err != nil {
		return LVMResult{}, err
	}
	snap, err := ParseLVName(op.Snapshot)
	if err != nil {
		return LVMResult{}, err
	}
	argv, err := LVSnapshotArgv(vg, origin, snap)
	if err != nil {
		return LVMResult{}, err
	}
	res := LVMResult{
		Capabilities: LVMCapabilities(), Incremental: false, PoolID: op.PoolID, Name: vg,
		ThinPool: LVMThinPoolName, Argv: argv, BackendRef: LVMDevicePath(vg, snap),
	}
	if err := e.exec(ctx, argv); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	res.Status = StatusAvailable
	return res, nil
}

func (e LVMEngine) rollback(ctx context.Context, op LVMOp) (LVMResult, error) {
	vg, err := ParseVGName(op.Name)
	if err != nil {
		return LVMResult{}, err
	}
	snap, err := ParseLVName(op.Snapshot)
	if err != nil {
		return LVMResult{}, err
	}
	argv, err := LVMergeArgv(vg, snap)
	if err != nil {
		return LVMResult{}, err
	}
	res := LVMResult{
		Capabilities: LVMCapabilities(), Incremental: false, PoolID: op.PoolID, Name: vg,
		ThinPool: LVMThinPoolName, Argv: argv, BackendRef: LVMDevicePath(vg, snap),
	}
	if err := e.exec(ctx, argv); err != nil {
		res.Status = StatusFailed
		res.Reason = err.Error()
		return res, nil
	}
	res.Status = StatusAvailable
	return res, nil
}

func (e LVMEngine) observeOne(ctx context.Context, op LVMOp) (LVMResult, error) {
	res := LVMResult{Capabilities: LVMCapabilities(), Incremental: false, PoolID: op.PoolID, Name: op.Name, VGUUID: op.VGUUID, ThinPool: LVMThinPoolName}
	if !e.installed() {
		res.Status = StatusUnavailable
		res.Reason = LVMMissing
		return res, nil
	}
	name := op.Name
	if name == "" {
		res.Status = StatusUnavailable
		res.Reason = "volume group name is not reported"
		return res, nil
	}
	argv, err := VGSReportArgv(name)
	if err != nil {
		return LVMResult{}, err
	}
	res.Argv = argv
	out, err := e.output(ctx, argv)
	if err != nil {
		res.Status = StatusUnavailable
		res.Reason = "volume group is missing or incomplete. Desired rows remain."
		return res, nil
	}
	if strings.TrimSpace(out) == "" {
		res.Status = StatusUnavailable
		res.Reason = "volume group was not observed. Desired rows remain."
		return res, nil
	}
	rep, err := parseLVMReport(out)
	if err != nil {
		res.Status = StatusUnavailable
		res.Reason = "volume group report is unreadable. Desired rows remain."
		return res, nil
	}
	if vgPartial(rep) || pvMissing(rep) {
		res.Status = StatusUnavailable
		res.Reason = "physical volume is missing. Desired rows remain."
		res.Capacity = Capacity{}
		return res, nil
	}
	if lvsArgv, err := LVSReportArgv(name); err == nil {
		if lvsOut, err := e.output(ctx, lvsArgv); err == nil && strings.TrimSpace(lvsOut) != "" {
			if lvs, perr := parseLVMReport(lvsOut); perr == nil {
				rep.LV = append(rep.LV, lvs.LV...)
			}
		}
	}
	if pvsArgv, err := PVSReportArgv(name); err == nil {
		if pvsOut, err := e.output(ctx, pvsArgv); err == nil && strings.TrimSpace(pvsOut) != "" {
			if pvs, perr := parseLVMReport(pvsOut); perr == nil {
				if pvMissing(pvs) {
					res.Status = StatusUnavailable
					res.Reason = "physical volume is missing. Desired rows remain."
					res.Capacity = Capacity{}
					return res, nil
				}
			}
		}
	}
	res.Status = StatusAvailable
	res.RootPath = LVMMountRoot + "/" + name
	if u := firstVGUUID(rep); u != "" {
		res.VGUUID = u
	}
	if total, free := vgBytes(rep); total > 0 {
		res.Capacity.TotalBytes = int64ptr(total)
		res.Capacity.UsableBytes = int64ptr(free)
		alloc := total - free
		if alloc < 0 {
			alloc = 0
		}
		res.Capacity.AllocatedBytes = int64ptr(alloc)
	}
	if mp := thinMetadataPercent(rep); mp != nil {
		res.MetadataPercent = mp
		res.WarningText = append(res.WarningText, fmt.Sprintf("Thin pool metadata percent: %.1f", *mp))
		if *mp >= 80 {
			res.Status = StatusWarning
			res.Warnings = append(res.Warnings, WarnLVMMetadata)
			res.WarningText = append(res.WarningText, LVMMetadataMsg)
		}
	}
	return res, nil
}

// ObserveHints reports LVM-thin pools. Missing PVs stay unavailable with nil capacity.
func (e LVMEngine) ObserveHints(ctx context.Context, hints []PoolHint) []ObservedPool {
	var out []ObservedPool
	for _, h := range hints {
		if h.BackendType != BackendLVM {
			continue
		}
		op := LVMOp{Action: "observe", PoolID: h.PoolID, Name: h.Backing.Device, VGUUID: h.Backing.FSUUID}
		res, err := e.observeOne(ctx, op)
		obs := ObservedPool{
			PoolID: h.PoolID, BackendType: BackendLVM, RootPath: h.RootPath,
			Status: StatusUnavailable, Capabilities: LVMCapabilities(), ObservedAt: e.now(),
			Backing: h.Backing,
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
		obs.MetadataPercent = res.MetadataPercent
		obs.Backing.MetadataPercent = res.MetadataPercent
		obs.Backing.ThinPool = LVMThinPoolName
		if res.VGUUID != "" {
			obs.Backing.FSUUID = res.VGUUID
		}
		if res.RootPath != "" {
			obs.RootPath = res.RootPath
		}
		if res.Status == StatusUnavailable {
			obs.Capacity = Capacity{}
			obs.Writable = false
		} else {
			obs.Capacity = res.Capacity
			obs.Writable = true
		}
		out = append(out, obs)
	}
	return out
}

func (e LVMEngine) exec(ctx context.Context, argv []string) error {
	if e.SkipHostCmds {
		if err := refuseExportArgv(argv); err != nil {
			return err
		}
		return nil
	}
	if !e.installed() {
		return fmt.Errorf(LVMMissing)
	}
	_, err := e.output(ctx, argv)
	return err
}

func (e LVMEngine) output(ctx context.Context, argv []string) (string, error) {
	if err := refuseExportArgv(argv); err != nil {
		return "", err
	}
	for _, a := range argv {
		if a == "-f" && argv[0] != LVMMkfsBin {
			return "", fmt.Errorf("lvm force flag is refused")
		}
		if strings.EqualFold(a, "--force") {
			return "", fmt.Errorf("lvm force flag is refused")
		}
		if strings.Contains(a, "bash") || strings.Contains(a, "/bin/sh") {
			return "", fmt.Errorf("shell is not a typed lvm action")
		}
		if strings.Contains(strings.ToLower(a), "vgexport") {
			return "", fmt.Errorf(LVMExportRefuse)
		}
	}
	if e.SkipHostCmds {
		return "", nil
	}
	if e.Run == nil {
		return "", fmt.Errorf(LVMMissing)
	}
	return e.Run(ctx, argv)
}

type lvmReport struct {
	Report []lvmReportBlock `json:"report"`
	VG     []lvmVGRow       `json:"vg"`
	LV     []lvmLVRow       `json:"lv"`
	PV     []lvmPVRow       `json:"pv"`
}

type lvmReportBlock struct {
	VG []lvmVGRow `json:"vg"`
	LV []lvmLVRow `json:"lv"`
	PV []lvmPVRow `json:"pv"`
}

type lvmVGRow struct {
	VGName string `json:"vg_name"`
	VGUUID string `json:"vg_uuid"`
	VGSize string `json:"vg_size"`
	VGFree string `json:"vg_free"`
	VGAttr string `json:"vg_attr"`
}

type lvmLVRow struct {
	LVName          string `json:"lv_name"`
	LVUUID          string `json:"lv_uuid"`
	LVSize          string `json:"lv_size"`
	LVAttr          string `json:"lv_attr"`
	DataPercent     string `json:"data_percent"`
	MetadataPercent string `json:"metadata_percent"`
	PoolLV          string `json:"pool_lv"`
}

type lvmPVRow struct {
	PVName    string `json:"pv_name"`
	VGName    string `json:"vg_name"`
	PVMissing string `json:"pv_missing"`
}

func parseLVMReport(raw string) (lvmReport, error) {
	var wrap lvmReport
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		return lvmReport{}, err
	}
	for _, b := range wrap.Report {
		wrap.VG = append(wrap.VG, b.VG...)
		wrap.LV = append(wrap.LV, b.LV...)
		wrap.PV = append(wrap.PV, b.PV...)
	}
	return wrap, nil
}

func vgPartial(rep lvmReport) bool {
	for _, vg := range rep.VG {
		attr := strings.TrimSpace(vg.VGAttr)
		if len(attr) >= 4 && attr[3] == 'p' {
			return true
		}
		if len(attr) >= 3 && attr[2] == 'x' {
			return true
		}
	}
	return false
}

func pvMissing(rep lvmReport) bool {
	for _, pv := range rep.PV {
		miss := strings.ToLower(strings.TrimSpace(pv.PVMissing))
		if miss == "1" || miss == "true" || miss == "missing" || miss == "yes" {
			return true
		}
	}
	return false
}

func firstVGUUID(rep lvmReport) string {
	for _, vg := range rep.VG {
		if strings.TrimSpace(vg.VGUUID) != "" {
			return strings.TrimSpace(vg.VGUUID)
		}
	}
	return ""
}

func vgBytes(rep lvmReport) (total, free int64) {
	for _, vg := range rep.VG {
		total = parseBytes(vg.VGSize)
		free = parseBytes(vg.VGFree)
		if total > 0 {
			return total, free
		}
	}
	return 0, 0
}

func thinMetadataPercent(rep lvmReport) *float64 {
	for _, lv := range rep.LV {
		if strings.TrimSpace(lv.LVName) != LVMThinPoolName {
			continue
		}
		s := strings.TrimSpace(lv.MetadataPercent)
		if s == "" {
			continue
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		return &f
	}
	for _, lv := range rep.LV {
		s := strings.TrimSpace(lv.MetadataPercent)
		if s == "" {
			continue
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		return &f
	}
	return nil
}

func parseBytes(s string) int64 {
	s = strings.TrimSpace(strings.TrimSuffix(s, "B"))
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(f)
}
