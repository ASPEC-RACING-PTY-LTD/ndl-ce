package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/migrate"
	"github.com/no-dal/ndl-ce/internal/placement"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

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

func (s *Server) runMigrate(ctx context.Context, wl appdb.Workload, dest *appdb.Node, mode string) (map[string]any, int, string) {
	sourceID := wl.NodeID
	if sourceID == "" {
		sourceID = wl.OwnerNodeID
	}
	if s.Migrate == nil {
		return nil, http.StatusFailedDependency, "dest agent is not connected; source remains running"
	}
	if mode == migrate.ModeLive && wl.Kind != qemu.KindVM {
		return nil, http.StatusUnprocessableEntity, "live migrate is VM-only; CT and OCI use offline"
	}
	cpuHost := workloadCPUHost(wl)
	shared, disks := s.migrateDisks(ctx, wl)
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

func (s *Server) migrateDisks(ctx context.Context, wl appdb.Workload) (bool, []migrate.VolumeCopy) {
	disks, _ := s.Store.ListWorkloadDisks(ctx, wl.ClusterID, wl.ID)
	shared := true
	out := []migrate.VolumeCopy{}
	if len(disks) == 0 {
		return true, out
	}
	for _, d := range disks {
		vol, _ := s.Store.GetVolume(ctx, wl.ClusterID, d.VolumeID)
		if vol == nil {
			continue
		}
		pool, _ := s.Store.GetStoragePool(ctx, wl.ClusterID, vol.PoolID)
		if pool == nil || !sharedBackend(pool.BackendType) {
			shared = false
		}
		src := vol.BackendRef
		if src != "" && !strings.HasPrefix(src, "/") && pool != nil && pool.RootPath != "" {
			src = path.Join(pool.RootPath, src)
		}
		out = append(out, migrate.VolumeCopy{VolumeID: vol.ID, SourcePath: src, DestPath: src})
	}
	return shared, out
}

func sharedBackend(kind string) bool {
	switch kind {
	case storage.BackendNFS, storage.BackendSMB, storage.BackendISCSI, storage.BackendZFS, storage.BackendDistributed:
		return true
	default:
		return false
	}
}
