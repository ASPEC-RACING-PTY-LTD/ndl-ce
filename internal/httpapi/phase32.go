package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/migrate"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/placement"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
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

func (a *agentMigrate) LocalAgentOnly() bool { return true }

func (a *agentMigrate) PrepareDest(ctx context.Context, req migrate.Request) error {
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
	jobs, err := s.Store.ListMigrateJobs(r.Context(), p.User.ClusterID, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, j := range jobs {
		if j.WorkloadID == id {
			writeJSON(w, http.StatusOK, migrateJobJSON(j))
			return
		}
	}
	writeErr(w, http.StatusNotFound, "no migrate job")
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
	if dest == nil || s.Migrate == nil {
		return false
	}
	if _, ok := s.Migrate.(migrateUnavailable); ok {
		return false
	}
	if lo, ok := s.Migrate.(interface{ LocalAgentOnly() bool }); ok && lo.LocalAgentOnly() {
		return s.destEligibleLocal(ctx, dest)
	}
	return true
}

func (s *Server) runMigrate(ctx context.Context, wl appdb.Workload, dest *appdb.Node, mode string) (map[string]any, int, string) {
	sourceID := wl.NodeID
	if sourceID == "" {
		sourceID = wl.OwnerNodeID
	}
	if s.Migrate == nil || !s.destAgentReady(ctx, dest) {
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
	_ = s.Store.CreateMigrateJob(ctx, job)
	res, err := migrate.Run(ctx, s.Migrate, migrate.Request{
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
		_ = s.Migrate.AbortDest(ctx, wl.ID)
		job.State = migrate.StateFail
		job.DestRunning = false
		job.Reason = terr.Error()
		_ = s.Store.UpdateMigrateJob(ctx, job)
		s.finishOp(ctx, op, "failed", job.Reason, 0)
		return migrateJobJSON(job), http.StatusConflict, job.Reason
	}
	_ = s.Store.UpdateMigrateJob(ctx, job)
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

func (s *Server) migrateDisks(ctx context.Context, wl appdb.Workload, dest *appdb.Node) (bool, []migrate.VolumeCopy, error) {
	disks, _ := s.Store.ListWorkloadDisks(ctx, wl.ClusterID, wl.ID)
	out := []migrate.VolumeCopy{}
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
		pool, _ := s.Store.GetStoragePool(ctx, wl.ClusterID, vol.PoolID)
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
			src = path.Join(root, src)
		}
		if sharedVolume(backend, src, ds) {
			out = append(out, migrate.VolumeCopy{VolumeID: vol.ID, SourcePath: src, DestPath: src})
			continue
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
	for _, p := range pools {
		if p.NodeID != dest.ID || strings.TrimSpace(p.RootPath) == "" {
			continue
		}
		if sharedVolume(p.BackendType, p.RootPath, nil) {
			continue
		}
		destRoot = p.RootPath
		if vol.Class != "" && strings.Contains(p.RootPath, vol.Class) {
			break
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
