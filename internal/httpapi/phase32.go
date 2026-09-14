package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/migrate"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/placement"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

const destAgentMissing = "dest agent is not connected; source remains running"
const destLocatorMissing = "dest volume locator missing"
const ociMigrateRecreate = "OCI migrate recreates; dest agent required"

// migrateUnavailable is AdaptMigrate(nil) and non-runtime clients. Methods
// fail; they never start a guest on the control unix agent.
type migrateUnavailable struct{}

func (migrateUnavailable) PrepareDest(context.Context, migrate.Request) error {
	return fmt.Errorf(destAgentMissing)
}
func (migrateUnavailable) CopyVolume(context.Context, migrate.VolumeCopy) error {
	return fmt.Errorf(destAgentMissing)
}
func (migrateUnavailable) StopSource(context.Context, string) error {
	return fmt.Errorf(destAgentMissing)
}
func (migrateUnavailable) StartDest(context.Context, string) error {
	return fmt.Errorf(destAgentMissing)
}
func (migrateUnavailable) LiveMigrate(context.Context, string) error {
	return fmt.Errorf(destAgentMissing)
}
func (migrateUnavailable) AbortDest(context.Context, string) error { return nil }
func (migrateUnavailable) SourceRunning(context.Context, string) bool {
	return true
}
func (migrateUnavailable) LocalAgentOnly() bool { return true }

// agentMigrate wraps the local unix agent. Dest on a worker is refused by
// destAgentReady before Run so StartDest cannot land on the control node.
type agentMigrate struct {
	agentrpc.Client
	mu       sync.Mutex
	destArgv map[string][]string
}

func (a *agentMigrate) LocalAgentOnly() bool {
	return strings.TrimSpace(a.TCPAddr) == ""
}

func (a *agentMigrate) PrepareDest(ctx context.Context, req migrate.Request) error {
	if req.Kind == migrate.KindCT || req.Kind == lxc.KindSystemContainer {
		if len(req.Disks) == 0 || strings.TrimSpace(req.Disks[0].DestPath) == "" {
			return nil
		}
		_, err := a.CopyBackup(ctx, qemu.BackupMkdir, "", req.Disks[0].DestPath)
		return err
	}
	msg := &agentv1.ComputeMigrate{
		Action: "prepare_incoming", WorkloadId: req.WorkloadID,
		Cpus: int32(req.CPUs), MemoryBytes: req.MemoryBytes,
		Machine: req.Machine, Accel: req.Accel,
	}
	if len(req.Disks) > 0 {
		msg.VolumeId = req.Disks[0].VolumeID
		msg.DiskPath = req.Disks[0].DestPath
		if msg.DiskPath == "" {
			msg.DiskPath = req.Disks[0].SourcePath
		}
	}
	raw, err := a.ComputeMigrate(ctx, msg)
	if err != nil {
		return err
	}
	var res qemu.Result
	if json.Unmarshal(raw, &res) == nil && len(res.Argv) > 0 {
		a.mu.Lock()
		if a.destArgv == nil {
			a.destArgv = map[string][]string{}
		}
		a.destArgv[req.WorkloadID] = res.Argv
		a.mu.Unlock()
	}
	return nil
}

func (a *agentMigrate) LiveArgv(_ context.Context, id string) (source, dest []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.destArgv == nil {
		return nil, nil
	}
	return nil, a.destArgv[id]
}

// AdaptMigrate returns a migrate.Runtime for the local agent. A nil or
// empty client is unavailable, not a silent local start.
func AdaptMigrate(client any) migrate.Runtime {
	if client == nil {
		return migrateUnavailable{}
	}
	if c, ok := client.(agentrpc.Client); ok {
		if strings.TrimSpace(c.Socket) == "" && strings.TrimSpace(c.TCPAddr) == "" {
			return migrateUnavailable{}
		}
		return &agentMigrate{Client: c, destArgv: map[string][]string{}}
	}
	if v, ok := client.(migrate.Runtime); ok {
		return v
	}
	return migrateUnavailable{}
}

type migrateRequest struct {
	DestNodeID string `json:"dest_node_id"`
	Mode       string `json:"mode"`
}

func migrateModeFor(wl appdb.Workload) string {
	if wl.Kind == qemu.KindVM {
		return migrate.ModeLive
	}
	return migrate.ModeOffline
}

func (s *Server) migrateWorkload(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeMigrate)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, id)
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	var req migrateRequest
	_ = readJSON(r, &req)
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		if row.Kind == qemu.KindVM {
			mode = migrate.ModeLive
		} else {
			mode = migrate.ModeOffline
		}
	}
	dest, err := s.migrateDest(r.Context(), p.User.ClusterID, *row, req.DestNodeID)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	out, code, msg := s.runMigrate(r.Context(), *row, dest, mode)
	if code != http.StatusOK {
		writeErr(w, code, msg)
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "compute.migrate", "ok", id)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getWorkloadMigrate(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	j, err := s.Store.GetLatestMigrateJob(r.Context(), p.User.ClusterID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if j == nil {
		writeErr(w, http.StatusNotFound, "no migrate job")
		return
	}
	writeJSON(w, http.StatusOK, migrateJobJSON(*j))
}

func (s *Server) migrateDest(ctx context.Context, clusterID string, wl appdb.Workload, destID string) (*appdb.Node, error) {
	sourceID := wl.NodeID
	if sourceID == "" {
		sourceID = wl.DesiredNodeID
	}
	if strings.TrimSpace(destID) != "" {
		n, err := s.Store.GetNodeByID(ctx, clusterID, destID)
		if err != nil || n == nil {
			return nil, errUnprocessable("dest node is not found")
		}
		if n.RevokedAt != nil {
			return nil, errUnprocessable("dest node is revoked")
		}
		if maint, _ := s.Store.GetNodeMaintenance(ctx, clusterID, n.ID); maint != nil {
			return nil, errUnprocessable("dest node is in maintenance")
		}
		if n.ID == sourceID {
			return nil, errUnprocessable("dest must be a different node")
		}
		return n, nil
	}
	placed, _, err := s.placeCreate(ctx, clusterID, createWorkloadRequest{
		Placement:              placement.ModeAutomatic,
		AntiAffinityWorkloadID: wl.ID,
		CPUs:                   wl.CPUs,
		MemoryBytes:            wl.MemoryBytes,
	})
	if err != nil {
		return nil, err
	}
	if placed.ID == sourceID {
		return nil, errUnprocessable("no eligible dest node")
	}
	return placed, nil
}

func (s *Server) destEligibleLocal(ctx context.Context, dest *appdb.Node) bool {
	if dest == nil {
		return false
	}
	return s.applyLocal(ctx, dest.ClusterID, dest.ID)
}

func (s *Server) destAgentReady(ctx context.Context, dest *appdb.Node) bool {
	_, ok := s.destRuntime(ctx, dest)
	return ok
}

func (s *Server) runMigrate(ctx context.Context, wl appdb.Workload, dest *appdb.Node, mode string) (map[string]any, int, string) {
	sourceID := wl.NodeID
	if sourceID == "" {
		sourceID = wl.OwnerNodeID
	}
	if (wl.Kind == lxc.KindSystemContainer || wl.Kind == migrate.KindCT) && dest != nil {
		if _, destOK := s.destAgentClient(ctx, dest); destOK {
			return s.runMigrateCT(ctx, wl, dest, mode)
		}
	}
	rt, ok := s.destRuntime(ctx, dest)
	if !ok {
		return nil, http.StatusFailedDependency, destAgentMissing
	}
	if wl.Kind == oci.KindOCI || wl.Kind == migrate.KindOCI {
		return nil, http.StatusUnprocessableEntity, ociMigrateRecreate
	}
	if mode == migrate.ModeLive && wl.Kind != qemu.KindVM {
		return nil, http.StatusUnprocessableEntity, "live migrate is VM-only; CT and OCI use offline"
	}
	cpuHost := workloadCPUHost(wl)
	shared, disks, derr := s.migrateDisks(ctx, wl, dest)
	if derr != nil {
		return nil, http.StatusUnprocessableEntity, derr.Error()
	}
	op := s.startOp(ctx, wl.ClusterID, dest.ID, "workload.migrate", mode, 10)
	job := appdb.MigrateJob{
		ID: uuid.NewString(), ClusterID: wl.ClusterID, WorkloadID: wl.ID, OperationID: op.ID,
		SourceNodeID: sourceID, DestNodeID: dest.ID, Mode: mode, State: "running",
		EpochAtStart: wl.OwnershipEpoch, SourceRunning: wl.Status == "running",
	}
	if err := s.Store.CreateMigrateJob(ctx, job); err != nil {
		return nil, http.StatusInternalServerError, "could not record migrate job"
	}
	res, err := migrate.Run(ctx, rt, migrate.Request{
		WorkloadID: wl.ID, Kind: wl.Kind, Mode: mode,
		SourceNodeID: sourceID, DestNodeID: dest.ID, Epoch: wl.OwnershipEpoch,
		SharedStorage: shared, CPUHost: cpuHost, Disks: disks,
		CPUs: wl.CPUs, MemoryBytes: wl.MemoryBytes, SourceArgv: argvFromJSON(wl.AppliedJSON),
	})
	job.State = res.State
	job.SourceRunning = res.SourceRunning
	job.DestRunning = res.DestRunning
	job.Reason = res.Reason
	if err != nil {
		job.Reason = err.Error()
		if res.Reason != "" {
			job.Reason = res.Reason
		}
		_ = s.Store.UpdateMigrateJob(ctx, job)
		s.finishOp(ctx, op, "failed", job.Reason, 0)
		cur, _ := s.Store.GetWorkload(ctx, wl.ClusterID, wl.ID)
		if cur != nil {
			cur.Status = wl.Status
			if res.SourceRunning {
				cur.Status = "running"
				cur.Reason = "live migrate failed; source remains running"
			}
			_ = s.Store.UpdateWorkloadObserved(ctx, *cur)
		}
		return migrateJobJSON(job), http.StatusConflict, job.Reason
	}
	newEpoch, terr := s.Store.TransferWorkloadOwnership(ctx, wl.ClusterID, wl.ID, dest.ID, wl.OwnershipEpoch)
	if terr != nil {
		_ = rt.AbortDest(ctx, wl.ID)
		job.State = migrate.StateFail
		job.DestRunning = false
		job.Reason = terr.Error()
		_ = s.Store.UpdateMigrateJob(ctx, job)
		s.finishOp(ctx, op, "failed", job.Reason, 0)
		return migrateJobJSON(job), http.StatusConflict, job.Reason
	}
	if err := s.Store.UpdateMigrateJob(ctx, job); err != nil {
		return nil, http.StatusInternalServerError, "could not record migrate job"
	}
	s.finishOp(ctx, op, "succeeded", res.Reason, 100)
	cur, _ := s.Store.GetWorkload(ctx, wl.ClusterID, wl.ID)
	if cur != nil {
		cur.Status = "running"
		if !res.DestRunning {
			cur.Status = "stopped"
		}
		cur.Reason = res.Reason
		cur.UnitActive = res.DestRunning
		cur.OwnershipEpoch = newEpoch
		_ = s.Store.UpdateWorkloadObserved(ctx, *cur)
		if latest, _ := s.Store.GetWorkload(ctx, wl.ClusterID, wl.ID); latest != nil {
			cur = latest
		}
	}
	out := migrateJobJSON(job)
	out["epoch"] = newEpoch
	if cur != nil {
		out["workload"] = s.workloadJSON(ctx, *cur)
	}
	return out, http.StatusOK, ""
}

func migrateJobJSON(j appdb.MigrateJob) map[string]any {
	return map[string]any{
		"id": j.ID, "workload_id": j.WorkloadID, "operation_id": j.OperationID,
		"source_node_id": j.SourceNodeID, "dest_node_id": j.DestNodeID,
		"mode": j.Mode, "state": j.State, "epoch_at_start": j.EpochAtStart,
		"source_running": j.SourceRunning, "dest_running": j.DestRunning, "reason": j.Reason,
	}
}

func workloadCPUHost(w appdb.Workload) bool {
	if qemu.CPUHost(argvFromJSON(w.AppliedJSON)) || qemu.CPUHost(argvFromJSON(w.SpecJSON)) {
		return true
	}
	return false
}

func argvFromJSON(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var applied qemu.Applied
	if json.Unmarshal(raw, &applied) == nil && len(applied.Argv) > 0 {
		return applied.Argv
	}
	var wrap struct {
		Argv []string `json:"argv"`
	}
	_ = json.Unmarshal(raw, &wrap)
	return wrap.Argv
}

func refuseMigrateExtraDataDisks(spec vmspec.Spec, bootVolID string, disks []appdb.WorkloadDisk) error {
	for _, d := range spec.Disks {
		if d.Role == vmspec.DiskRoleData && strings.TrimSpace(d.VolumeID) != "" && d.VolumeID != bootVolID {
			return errUnprocessable("migrate of additional data disks is not implemented")
		}
	}
	for _, d := range disks {
		if d.Role == vmspec.DiskRoleData && strings.TrimSpace(d.VolumeID) != "" && d.VolumeID != bootVolID {
			return errUnprocessable("migrate of additional data disks is not implemented")
		}
	}
	return nil
}

func migrateBootVolumeID(spec vmspec.Spec, disks []appdb.WorkloadDisk) string {
	for _, d := range disks {
		if d.Role == vmspec.DiskRoleBoot && strings.TrimSpace(d.VolumeID) != "" {
			return d.VolumeID
		}
	}
	for _, d := range spec.Disks {
		if d.Role == vmspec.DiskRoleBoot && strings.TrimSpace(d.VolumeID) != "" {
			return d.VolumeID
		}
	}
	if len(disks) > 0 {
		return disks[0].VolumeID
	}
	return ""
}

func (s *Server) migrateDisks(ctx context.Context, wl appdb.Workload, dest *appdb.Node) (bool, []migrate.VolumeCopy, error) {
	disks, _ := s.Store.ListWorkloadDisks(ctx, wl.ClusterID, wl.ID)
	out := []migrate.VolumeCopy{}
	spec, _ := vmspec.Parse(wl.SpecJSON)
	if err := refuseMigrateExtraDataDisks(spec, migrateBootVolumeID(spec, disks), disks); err != nil {
		return false, nil, err
	}
	if len(disks) == 0 {
		return true, out, nil
	}
	pools, _ := s.Store.ListStoragePools(ctx, wl.ClusterID)
	sharedAll := true
	for _, d := range disks {
		vol, _ := s.Store.GetVolume(ctx, wl.ClusterID, d.VolumeID)
		if vol == nil {
			return false, nil, fmt.Errorf("workload volume is missing")
		}
		if vol.Status != storage.StatusAvailable && vol.Status != storage.StatusWarning {
			return false, nil, fmt.Errorf("storage is unavailable")
		}
		pool, _ := s.Store.GetStoragePool(ctx, wl.ClusterID, vol.PoolID)
		if pool == nil || (pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning) {
			return false, nil, fmt.Errorf("storage pool is unavailable")
		}
		backend := ""
		root := ""
		if pool != nil {
			backend = pool.BackendType
			root = pool.RootPath
		}
		var ds *appdb.Datastore
		if pool != nil {
			ds, _ = s.Store.GetDatastore(ctx, pool.ID)
		}
		src := vol.BackendRef
		if src != "" && !strings.HasPrefix(src, "/") && root != "" {
			joined, err := storage.JoinUnder(root, src)
			if err != nil {
				return false, nil, fmt.Errorf("volume locator is invalid")
			}
			src = joined
		}
		if sharedVolume(backend, src, ds) {
			out = append(out, migrate.VolumeCopy{VolumeID: vol.ID, SourcePath: src, DestPath: src})
			continue
		}
		if err := refuseQemuImgCopyDest(backend); err != nil {
			return false, nil, err
		}
		sharedAll = false
		destPath, err := destVolumeLocator(dest, vol, src, pools)
		if err != nil {
			return false, nil, err
		}
		out = append(out, migrate.VolumeCopy{VolumeID: vol.ID, SourcePath: src, DestPath: destPath})
	}
	return sharedAll, out, nil
}

func destVolumeLocator(dest *appdb.Node, vol *appdb.Volume, src string, pools []appdb.StoragePool) (string, error) {
	if dest == nil || vol == nil {
		return "", fmt.Errorf(destLocatorMissing)
	}
	destRoot := ""
	destHasDownDirectory := false
	for _, p := range pools {
		if p.NodeID != dest.ID || strings.TrimSpace(p.RootPath) == "" {
			continue
		}
		if p.Status != storage.StatusAvailable && p.Status != storage.StatusWarning {
			if p.BackendType == storage.BackendDirectory {
				destHasDownDirectory = true
			}
			continue
		}
		if sharedVolume(p.BackendType, p.RootPath, nil) {
			continue
		}
		if refuseQemuImgCopyDest(p.BackendType) != nil {
			continue
		}
		destRoot = p.RootPath
		if vol.Class != "" && strings.Contains(p.RootPath, vol.Class) {
			break
		}
	}
	if destRoot == "" && !destHasDownDirectory {
		// Directory pools are node-local copies of the same path. A worker
		// that has not registered its own pool still receives files under
		// the cluster default Directory root.
		for _, p := range pools {
			if p.BackendType != storage.BackendDirectory {
				continue
			}
			if p.Status != storage.StatusAvailable && p.Status != storage.StatusWarning {
				continue
			}
			if strings.TrimSpace(p.RootPath) == storage.DefaultPoolPath {
				destRoot = p.RootPath
				break
			}
		}
	}
	if destRoot == "" {
		return "", fmt.Errorf(destLocatorMissing)
	}
	name := dest.ID + "-" + vol.ID
	class := vol.Class
	if class == "" {
		class = storage.ClassVMDisk
	}
	if !storage.ValidClass(class) {
		return "", fmt.Errorf(destLocatorMissing)
	}
	destPath, err := storage.JoinUnder(destRoot, path.Join("volumes", class, name))
	if err != nil || destPath == src || destPath == "" {
		return "", fmt.Errorf(destLocatorMissing)
	}
	return destPath, nil
}

func (s *Server) runMigrateCT(ctx context.Context, wl appdb.Workload, dest *appdb.Node, mode string) (map[string]any, int, string) {
	sourceID := wl.NodeID
	if sourceID == "" {
		sourceID = wl.OwnerNodeID
	}
	if dest == nil || dest.ID == sourceID {
		return nil, http.StatusUnprocessableEntity, "dest must be a different node"
	}
	if mode == migrate.ModeLive {
		return nil, http.StatusUnprocessableEntity, "live migrate is VM-only; CT and OCI use offline"
	}
	destWL, destWLOK := s.destWorkloads(ctx, dest)
	destBK, destBKOK := s.destBackup(ctx, dest)
	destClient, destOK := s.destAgentClient(ctx, dest)
	if !destWLOK || !destBKOK || !destOK || destWL == nil || destBK == nil {
		return nil, http.StatusFailedDependency, destAgentMissing
	}
	vol, pool, srcRoot, err := s.bootVolumeLocator(ctx, wl.ClusterID, wl)
	if err != nil {
		return nil, http.StatusUnprocessableEntity, err.Error()
	}
	pools, _ := s.Store.ListStoragePools(ctx, wl.ClusterID)
	destPath, err := destVolumeLocator(dest, vol, srcRoot, pools)
	if err != nil {
		return nil, http.StatusUnprocessableEntity, err.Error()
	}
	nics, _ := s.Store.ListWorkloadNICs(ctx, wl.ClusterID, wl.ID)
	netID, bridge := "", ""
	var netw *appdb.Network
	if len(nics) > 0 {
		netID = nics[0].NetworkID
		if n, nerr := s.Store.GetNetwork(ctx, wl.ClusterID, netID); nerr == nil && n != nil {
			netw = n
			bridge = n.BridgeName
		}
	}
	if netID == "" {
		if iso, ierr := s.isolatedNetworkID(ctx, wl.ClusterID); ierr == nil {
			netID = iso
			if n, nerr := s.Store.GetNetwork(ctx, wl.ClusterID, netID); nerr == nil && n != nil {
				netw = n
				bridge = n.BridgeName
			}
		}
	}
	op := s.startOp(ctx, wl.ClusterID, dest.ID, "workload.migrate", mode, 10)
	job := appdb.MigrateJob{
		ID: uuid.NewString(), ClusterID: wl.ClusterID, WorkloadID: wl.ID, OperationID: op.ID,
		SourceNodeID: sourceID, DestNodeID: dest.ID, Mode: migrate.ModeOffline, State: "running",
		EpochAtStart: wl.OwnershipEpoch, SourceRunning: wl.Status == "running",
	}
	if err := s.Store.CreateMigrateJob(ctx, job); err != nil {
		return nil, http.StatusInternalServerError, "could not record migrate job"
	}
	fail := func(msg string, srcRunning bool) (map[string]any, int, string) {
		job.State = migrate.StateFail
		job.Reason = msg
		job.SourceRunning = srcRunning
		_ = s.Store.UpdateMigrateJob(ctx, job)
		s.finishOp(ctx, op, "failed", msg, 0)
		return migrateJobJSON(job), http.StatusConflict, msg
	}
	if b, nerr := s.ensureDestIsolatedNet(ctx, dest, netw); nerr != nil {
		return fail(nerr.Error(), true)
	} else if b != "" {
		bridge = b
	}
	if _, err := destBK.CopyBackup(ctx, qemu.BackupMkdir, "", destPath); err != nil {
		return fail(err.Error(), true)
	}
	packDir := filepath.Join("/var/lib/ndl/control", "tmp", "migrate")
	if err := os.MkdirAll(packDir, 0o750); err != nil {
		return fail(err.Error(), true)
	}
	pack := filepath.Join(packDir, job.ID+".tar.gz")
	if s.Backup == nil {
		return fail(destAgentMissing, true)
	}
	archived, err := s.Backup.CopyBackup(ctx, qemu.BackupArchive, srcRoot, pack)
	if err != nil {
		return fail(err.Error(), true)
	}
	pack = resolvedMigratePack(pack, archived)
	defer func() { _ = os.Remove(pack) }()
	if _, err := os.Stat(pack); err != nil {
		return fail("migrate pack missing after archive", true)
	}
	pullURL, stopServe, err := serveMigratePack(pack, destAdvertiseHint(destClient))
	if err != nil {
		return fail(err.Error(), true)
	}
	defer stopServe()
	if err := destClient.PullVolume(ctx, migrate.VolumeCopy{VolumeID: vol.ID, SourcePath: pullURL, DestPath: destPath}); err != nil {
		return fail(err.Error(), true)
	}
	nesting := true
	spec := lxc.Spec{
		WorkloadID: wl.ID, Name: wl.Name, ImagePin: firstNonEmpty(wl.ImagePin, "imported"),
		CPUs: wl.CPUs, MemoryBytes: wl.MemoryBytes, VolumeID: vol.ID,
		RootfsPath: destPath, NetworkID: netID, BridgeName: bridge,
		Privileged: wl.Privileged, UIDMap: wl.UIDMap, GIDMap: wl.GIDMap,
		SkipImage: true, NoStart: true, Nesting: &nesting,
		IP: destGuestIP(),
	}
	if _, err := destWL.CreateCT(ctx, spec); err != nil {
		return fail(err.Error(), true)
	}
	if wl.Autostart {
		on := true
		if _, err := destWL.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: wl.ID, Action: lxc.ActionApplySpec, Autostart: &on}); err != nil {
			return fail(err.Error(), true)
		}
	}
	srcRunning := wl.Status == "running"
	if srcRunning && s.Workloads != nil {
		if _, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: wl.ID, Action: "stop"}); err != nil {
			return fail(err.Error(), true)
		}
	}
	if _, err := destWL.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: wl.ID, Action: "start"}); err != nil {
		if srcRunning && s.Workloads != nil {
			_, _ = s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: wl.ID, Action: "start"})
		}
		return fail(err.Error(), srcRunning)
	}
	if rel, rerr := filepath.Rel(pool.RootPath, destPath); rerr == nil && rel != "" && !strings.HasPrefix(rel, "..") {
		_ = s.Store.UpdateVolumeLocator(ctx, wl.ClusterID, vol.ID, filepath.ToSlash(rel))
	}
	newEpoch, terr := s.Store.TransferWorkloadOwnership(ctx, wl.ClusterID, wl.ID, dest.ID, wl.OwnershipEpoch)
	if terr != nil {
		_, _ = destWL.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: wl.ID, Action: "stop"})
		if srcRunning && s.Workloads != nil {
			_, _ = s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: wl.ID, Action: "start"})
		}
		return fail(terr.Error(), srcRunning)
	}
	job.State = migrate.StateOK
	job.DestRunning = true
	job.SourceRunning = false
	job.Reason = "offline migrate completed"
	_ = s.Store.UpdateMigrateJob(ctx, job)
	s.finishOp(ctx, op, "succeeded", job.Reason, 100)
	cur, _ := s.Store.GetWorkload(ctx, wl.ClusterID, wl.ID)
	if cur != nil {
		cur.Status = lxc.StatusRunning
		cur.Reason = job.Reason
		cur.UnitActive = true
		cur.OwnershipEpoch = newEpoch
		_ = s.Store.UpdateWorkloadObserved(ctx, *cur)
	}
	out := migrateJobJSON(job)
	out["epoch"] = newEpoch
	if cur != nil {
		out["workload"] = s.workloadJSON(ctx, *cur)
	}
	return out, http.StatusOK, ""
}

func resolvedMigratePack(requested string, archived storage.CopyResult) string {
	if dest := strings.TrimSpace(archived.Dest); dest != "" {
		return dest
	}
	return requested
}

func destAdvertiseHint(c agentrpc.Client) net.IP {
	host, _, err := net.SplitHostPort(strings.TrimSpace(c.TCPAddr))
	if err != nil {
		host = strings.TrimSpace(c.TCPAddr)
	}
	return net.ParseIP(host)
}

func serveMigratePack(pack string, destHint net.IP) (string, func(), error) {
	if _, err := os.Stat(pack); err != nil {
		return "", nil, fmt.Errorf("migrate pack missing: %w", err)
	}
	host, err := advertiseIPv4(destHint)
	if err != nil {
		return "", nil, err
	}
	ln, err := net.Listen("tcp", host+":0")
	if err != nil {
		ln, err = net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			return "", nil, err
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/pack", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, pack)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	addr := ln.Addr().String()
	if host != "" && !strings.HasPrefix(addr, host) {
		_, port, _ := net.SplitHostPort(addr)
		if port != "" {
			addr = net.JoinHostPort(host, port)
		}
	}
	return "http://" + addr + "/pack", stop, nil
}

type advertiseAddr struct {
	name string
	ip   net.IP
	mask net.IPMask
}

func advertiseIPv4(destHint net.IP) (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	var addrs []advertiseAddr
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifaceAddrs, _ := iface.Addrs()
		for _, a := range ifaceAddrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP == nil || ipn.IP.To4() == nil || ipn.IP.IsLoopback() {
				continue
			}
			addrs = append(addrs, advertiseAddr{name: iface.Name, ip: ipn.IP.To4(), mask: ipn.Mask})
		}
	}
	if ip := pickAdvertiseIPv4(addrs, destHint); ip != "" {
		return ip, nil
	}
	return "", fmt.Errorf("no advertise IPv4 for dest pull")
}

func pickAdvertiseIPv4(addrs []advertiseAddr, dest net.IP) string {
	var sameNDL, sameAny, ndlAny, fallback string
	for _, a := range addrs {
		if a.ip == nil {
			continue
		}
		ip := a.ip.String()
		same := dest != nil && a.mask != nil && dest.To4() != nil && dest.Mask(a.mask).Equal(a.ip.Mask(a.mask))
		if same && strings.HasPrefix(a.name, "ndl") && sameNDL == "" {
			sameNDL = ip
		}
		if same && sameAny == "" {
			sameAny = ip
		}
		if strings.HasPrefix(a.name, "ndl") && ndlAny == "" {
			ndlAny = ip
		}
		if fallback == "" {
			fallback = ip
		}
	}
	if sameNDL != "" {
		return sameNDL
	}
	if sameAny != "" {
		return sameAny
	}
	if ndlAny != "" {
		return ndlAny
	}
	return fallback
}

func sharedVolume(backend, locator string, ds *appdb.Datastore) bool {
	switch backend {
	case storage.BackendNFS, storage.BackendSMB, storage.BackendISCSI:
		return ds != nil && strings.TrimSpace(ds.Locator) != ""
	case storage.BackendDistributed:
		loc := strings.TrimSpace(locator)
		return loc != "" && (strings.HasPrefix(loc, "/dev/rbd") || strings.HasPrefix(loc, "/dev/nbd") || strings.HasPrefix(loc, "rbd:"))
	default:
		return false
	}
}
