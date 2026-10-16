package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

// After recovery every workload exists as a definition, held stopped. Each
// is brought back bare (its base image, for Smart and Custom backups),
// its data restored from its newest offsite restore point, started, and
// rehydrated: repositories, images and compose projects. Its power is then
// set back to what it was.

// Workload restore states.
const (
	restorePending        = "pending"
	restoreRunning        = "running"
	restoreDone           = "restored"
	restoreNeedsAttention = "needs_attention"
	restoreFailed         = "failed"
)

type workloadRestore struct {
	Status      string                      `json:"status"`
	Step        string                      `json:"step,omitempty"`
	BackupID    string                      `json:"backup_id,omitempty"`
	CaptureMode string                      `json:"capture_mode,omitempty"`
	BackedUpAt  *time.Time                  `json:"backed_up_at,omitempty"`
	StartedAt   *time.Time                  `json:"started_at,omitempty"`
	FinishedAt  *time.Time                  `json:"finished_at,omitempty"`
	Error       string                      `json:"error,omitempty"`
	Rehydrate   *backuphost.RehydrateResult `json:"rehydrate,omitempty"`
	// Created and DataRestored let a retry resume: rehydrate again, or
	// clear a half-made container and start over.
	Created      bool `json:"created,omitempty"`
	DataRestored bool `json:"data_restored,omitempty"`
}

type restoreApprovals struct {
	UseTags    []string          `json:"use_tags"`
	RepoTokens map[string]string `json:"repo_tokens"`
}

func (s *Server) setWorkloadRestore(ctx context.Context, clusterID, workloadID string, update func(*workloadRestore)) {
	s.platform.mu.Lock()
	defer s.platform.mu.Unlock()
	st := s.loadPlatformState(ctx, clusterID)
	if st.Recovery == nil {
		return
	}
	if st.Recovery.Workloads == nil {
		st.Recovery.Workloads = map[string]*workloadRestore{}
	}
	cur := st.Recovery.Workloads[workloadID]
	if cur == nil {
		cur = &workloadRestore{Status: restorePending}
		st.Recovery.Workloads[workloadID] = cur
	}
	update(cur)
	s.savePlatformState(ctx, clusterID, st)
}

// newestOffsitePoint is the workload's newest V2 restore point in an object
// storage target.
func (s *Server) newestOffsitePoint(ctx context.Context, clusterID, workloadID string) (*appdb.BackupArtifact, *appdb.BackupTarget) {
	arts, _ := s.Store.ListBackupArtifacts(ctx, clusterID)
	sort.Slice(arts, func(i, j int) bool { return arts[i].CreatedAt.After(arts[j].CreatedAt) })
	for i := range arts {
		a := arts[i]
		if a.WorkloadID != workloadID || a.Format != backup.Format || a.BackupID == "" || a.RemoteState != string(backup.RemoteProtected) {
			continue
		}
		run, _ := s.Store.GetBackupRun(ctx, clusterID, a.RunID)
		if run == nil {
			continue
		}
		tgt, _ := s.Store.GetBackupTarget(ctx, clusterID, run.TargetID)
		if tgt == nil || !isObjectBackupKind(tgt.Kind) {
			continue
		}
		return &a, tgt
	}
	return nil, nil
}

// restoreRecoveredWorkload brings one recovered workload back.
func (s *Server) restoreRecoveredWorkload(ctx context.Context, clusterID, workloadID string, ap restoreApprovals) error {
	st := s.loadPlatformState(ctx, clusterID)
	if st.Recovery == nil {
		return errConflict("this NDL-CE was not recovered from a platform backup")
	}
	if !st.Recovery.ConnectionVerified {
		return errConflict("the bucket connection was not verified; fix the backup target first")
	}
	wl, err := s.Store.GetWorkload(ctx, clusterID, workloadID)
	if err != nil || wl == nil {
		return errNotFound("workload not found")
	}
	var prev workloadRestore
	if cur := st.Recovery.Workloads[workloadID]; cur != nil {
		if cur.Status == restoreRunning {
			return errConflict("this workload is already being restored")
		}
		prev = *cur
	}
	art, tgt := s.newestOffsitePoint(ctx, clusterID, workloadID)
	if art == nil {
		s.setWorkloadRestore(ctx, clusterID, workloadID, func(r *workloadRestore) {
			r.Status, r.Error = restoreFailed, "there is no offsite restore point for this workload"
		})
		return errUnprocessable("there is no offsite restore point for this workload")
	}
	var bp backup.Blueprint
	if art.BlueprintJSON != "" {
		_ = json.Unmarshal([]byte(art.BlueprintJSON), &bp)
	}
	mode := firstNonEmpty(art.CaptureMode, bp.CaptureMode, backup.CaptureModeFull)
	now := s.now()
	at := art.CreatedAt
	resume := prev.DataRestored && prev.BackupID == art.BackupID
	s.setWorkloadRestore(ctx, clusterID, workloadID, func(r *workloadRestore) {
		*r = workloadRestore{Status: restoreRunning, Step: "preparing", BackupID: art.BackupID, CaptureMode: mode, BackedUpAt: &at, StartedAt: &now,
			Created: prev.Created, DataRestored: resume}
	})
	step := func(name string) {
		s.setWorkloadRestore(ctx, clusterID, workloadID, func(r *workloadRestore) {
			switch name {
			case "created":
				r.Created = true
				return
			case "data restored":
				r.DataRestored = true
				return
			}
			r.Step = name
		})
	}
	finish := func(err error, rh *backuphost.RehydrateResult) error {
		done := s.now()
		s.setWorkloadRestore(ctx, clusterID, workloadID, func(r *workloadRestore) {
			r.FinishedAt, r.Rehydrate, r.Step = &done, rh, ""
			switch {
			case err != nil:
				r.Status, r.Error = restoreFailed, err.Error()
			case rh != nil && rh.NeedsAttention:
				r.Status, r.Error = restoreNeedsAttention, ""
			default:
				r.Status, r.Error = restoreDone, ""
			}
		})
		return err
	}
	power := st.Recovery.Power[workloadID]
	switch wl.Kind {
	case lxc.KindSystemContainer:
		rh, err := s.restoreRecoveredCT(ctx, clusterID, *wl, *art, tgt, bp, mode, power, ap, prev, resume, step)
		return finish(err, rh)
	case vmspec.KindVM:
		return finish(s.restoreRecoveredVM(ctx, clusterID, *wl, *art, power, step), nil)
	}
	return finish(fmt.Errorf("restoring %s workloads after recovery is not supported; restore it from Backups", wl.Kind), nil)
}

func (s *Server) restoreRecoveredCT(ctx context.Context, clusterID string, wl appdb.Workload, art appdb.BackupArtifact, tgt *appdb.BackupTarget, bp backup.Blueprint, mode, power string, ap restoreApprovals, prev workloadRestore, resume bool, step func(string)) (*backuphost.RehydrateResult, error) {
	if s.Workloads == nil || s.Backup == nil || s.Storage == nil {
		return nil, errUnavailable("the agent is unavailable")
	}
	vol, pool, rootfs, err := s.recoveryVolumeLocator(ctx, clusterID, wl)
	if err != nil {
		return nil, err
	}
	if vol == nil || pool == nil || rootfs == "" {
		return nil, errors.New("the workload has no root volume")
	}
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	volReq := storage.CreateVolumeRequest{
		VolumeID: vol.ID, PoolID: pool.ID, RootPath: pool.RootPath,
		Class: storage.ClassContainerRoot, Size: vol.SizeBytes, Format: storage.FormatDirectory,
	}
	if resume {
		return s.startAndRehydrate(ctx, clusterID, wl, rootfs, bp, mode, power, ap, step)
	}
	if entries, err := os.ReadDir(rootfs); err == nil && len(entries) > 0 {
		if !prev.Created && prev.Status != restoreFailed {
			return nil, errConflict("the workload's root already has files that recovery did not make; restore it from Backups instead")
		}
		// Left by an earlier attempt that failed: start over.
		step("clearing an earlier attempt")
		_, _ = s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: wl.ID, Action: "delete"})
		if err := s.Storage.DestroyDirectoryVolume(ctx, volReq, hint); err != nil {
			return nil, fmt.Errorf("an earlier attempt could not be cleared: %w", err)
		}
	}
	step("creating the volume")
	if _, err := s.Storage.CreateDirectoryVolume(ctx, volReq, hint); err != nil && !strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "exist") {
		return nil, err
	}
	var spec lxc.Spec
	if len(wl.SpecJSON) > 0 {
		_ = json.Unmarshal(wl.SpecJSON, &spec)
	}
	if spec.WorkloadID == "" && len(wl.AppliedJSON) > 0 {
		var applied lxc.Applied
		if json.Unmarshal(wl.AppliedJSON, &applied) == nil {
			spec = applied.Spec
		}
	}
	spec.WorkloadID, spec.Name, spec.RootfsPath, spec.VolumeID = wl.ID, firstNonEmpty(spec.Name, wl.Name), rootfs, vol.ID
	spec.ImagePin = firstNonEmpty(spec.ImagePin, wl.ImagePin, bp.BaseImage)
	if spec.CPUs < 1 {
		spec.CPUs = wl.CPUs
	}
	if spec.MemoryBytes < 1 {
		spec.MemoryBytes = wl.MemoryBytes
	}
	spec.UIDMap = firstNonEmpty(spec.UIDMap, wl.UIDMap)
	spec.GIDMap = firstNonEmpty(spec.GIDMap, wl.GIDMap)
	spec.Privileged = spec.Privileged || wl.Privileged
	spec.NoStart = true
	if nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, wl.ID); len(nics) > 0 {
		if netw, _ := s.Store.GetNetwork(ctx, clusterID, nics[0].NetworkID); netw != nil {
			spec.NetworkID, spec.BridgeName = netw.ID, netw.BridgeName
		}
	}
	restore := func() error {
		step("restoring data")
		if err := s.restoreV2Root(ctx, art, rootfs, tgt); err != nil {
			return err
		}
		step("data restored")
		return nil
	}
	// A Full backup is the whole root; Smart and Custom ones go on top of
	// the base image.
	if mode == backup.CaptureModeFull {
		if err := restore(); err != nil {
			return nil, err
		}
		spec.SkipImage = true
		step("creating the container")
		if _, err := s.Workloads.CreateCT(ctx, spec); err != nil {
			return nil, err
		}
		step("created")
	} else {
		spec.SkipImage = false
		step("creating the container from its base image")
		if _, err := s.Workloads.CreateCT(ctx, spec); err != nil {
			return nil, err
		}
		step("created")
		if err := restore(); err != nil {
			return nil, err
		}
	}
	return s.startAndRehydrate(ctx, clusterID, wl, rootfs, bp, mode, power, ap, step)
}

// startAndRehydrate starts a restored container, puts back what Smart left
// out, and gives it back its power.
func (s *Server) startAndRehydrate(ctx context.Context, clusterID string, wl appdb.Workload, rootfs string, bp backup.Blueprint, mode, power string, ap restoreApprovals, step func(string)) (*backuphost.RehydrateResult, error) {
	step("starting")
	if _, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: wl.ID, Action: "start"}); err != nil {
		return nil, err
	}
	var rh *backuphost.RehydrateResult
	if mode != backup.CaptureModeFull {
		step("cloning repositories and restoring images")
		raw, _ := json.Marshal(backuphost.Request{
			Action: backuphost.ActionRehydrate, WorkloadID: wl.ID, WorkloadName: wl.Name, Blueprint: bp,
			UseTags: ap.UseTags, RepoTokens: ap.RepoTokens,
		})
		res, err := s.Backup.CopyBackup(ctx, qemu.BackupV2Rehydrate, rootfs, string(raw))
		if err != nil {
			return nil, fmt.Errorf("the data was restored, but repositories and images were not: %w", err)
		}
		var out backuphost.Result
		_ = json.Unmarshal([]byte(res.Extra), &out)
		rh = out.Rehydrate
	}
	return rh, s.settlePower(ctx, clusterID, wl, power, func() error {
		_, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: wl.ID, Action: "stop"})
		return err
	})
}

func (s *Server) restoreRecoveredVM(ctx context.Context, clusterID string, wl appdb.Workload, art appdb.BackupArtifact, power string, step func(string)) error {
	if s.VM == nil || s.Backup == nil || s.Storage == nil {
		return errUnavailable("the agent is unavailable")
	}
	vol, pool, tip, err := s.recoveryVolumeLocator(ctx, clusterID, wl)
	if err != nil {
		return err
	}
	if vol == nil || pool == nil {
		return errors.New("the workload has no boot disk")
	}
	if _, err := os.Stat(tip); err == nil {
		return errConflict("the workload's disk already exists; restore it from Backups instead")
	}
	step("creating the disk")
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	if _, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
		VolumeID: vol.ID, PoolID: pool.ID, RootPath: pool.RootPath,
		Class: storage.ClassVMDisk, Size: vol.SizeBytes, Format: firstNonEmpty(vol.Format, storage.FormatQCOW2),
	}, hint); err != nil && !strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "exist") {
		return err
	}
	step("restoring the disk")
	src, cleanup, err := s.materializeArtifact(ctx, clusterID, art)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := s.Backup.CopyBackup(ctx, qemu.BackupReplace, src, tip); err != nil {
		return err
	}
	if _, err := s.reprepareVM(ctx, clusterID, wl); err != nil {
		return err
	}
	if power == "running" {
		step("starting")
		if _, err := s.VM.LifecycleVM(ctx, wl.ID, "start", wl.Autostart); err != nil {
			return err
		}
	}
	return s.settlePower(ctx, clusterID, wl, power, func() error { return nil })
}

// settlePower gives the workload back the power it had before recovery.
func (s *Server) settlePower(ctx context.Context, clusterID string, wl appdb.Workload, power string, stop func() error) error {
	if power == "" {
		power = "stopped"
	}
	if power != "running" {
		if err := stop(); err != nil {
			return err
		}
	}
	cur, err := s.Store.GetWorkload(ctx, clusterID, wl.ID)
	if err != nil || cur == nil {
		return err
	}
	cur.DesiredPower = power
	return s.Store.UpdateWorkloadSpec(ctx, *cur)
}

func (s *Server) recoveryJSON(ctx context.Context, clusterID string) map[string]any {
	st := s.loadPlatformState(ctx, clusterID)
	if st.Recovery == nil {
		return map[string]any{"recovered": false}
	}
	wls, _ := s.Store.ListWorkloads(ctx, clusterID)
	items := make([]map[string]any, 0, len(wls))
	for _, wl := range wls {
		r := st.Recovery.Workloads[wl.ID]
		if r == nil {
			r = &workloadRestore{Status: restorePending}
		}
		item := map[string]any{"workload_id": wl.ID, "name": wl.Name, "kind": wl.Kind, "power_before": st.Recovery.Power[wl.ID], "restore": r}
		if art, _ := s.newestOffsitePoint(ctx, clusterID, wl.ID); art != nil {
			item["backed_up_at"] = art.CreatedAt
			var bp backup.Blueprint
			if art.BlueprintJSON != "" && json.Unmarshal([]byte(art.BlueprintJSON), &bp) == nil {
				// What a restore cannot bring back, known before it runs.
				var lost []map[string]any
				for _, repo := range bp.Repos {
					if un := repo.Unsaved(); len(un) > 0 || repo.Unpushed {
						lost = append(lost, map[string]any{"path": repo.Path, "not_backed_up": un, "unpushed_commits": repo.UnpushedCommits, "unpushed": repo.Unpushed})
					}
				}
				item["not_backed_up"] = lost
				item["capture_mode"] = firstNonEmpty(art.CaptureMode, bp.CaptureMode)
			}
		} else {
			item["no_offsite_point"] = true
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
	rec := *st.Recovery
	rec.Workloads = nil
	return map[string]any{"recovered": true, "recovery": rec, "workloads": items}
}

func (s *Server) getRecovery(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, s.recoveryJSON(r.Context(), p.User.ClusterID))
}

// restoreRecoveredWorkloadAPI restores one workload, or with id "all" every
// workload not restored yet, in the background.
func (s *Server) restoreRecoveredWorkloadAPI(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOwner(w, r)
	if !ok {
		return
	}
	var ap restoreApprovals
	_ = readJSON(r, &ap)
	clusterID := p.User.ClusterID
	ctx := r.Context()
	st := s.loadPlatformState(ctx, clusterID)
	if st.Recovery == nil {
		writeErr(w, http.StatusConflict, "this NDL-CE was not recovered from a platform backup")
		return
	}
	if !st.Recovery.ConnectionVerified {
		writeErr(w, http.StatusConflict, "the bucket connection was not verified; fix the backup target first")
		return
	}
	id := r.PathValue("id")
	var ids []string
	if id == "all" {
		wls, _ := s.Store.ListWorkloads(ctx, clusterID)
		for _, wl := range wls {
			if cur := st.Recovery.Workloads[wl.ID]; cur == nil || cur.Status == restorePending || cur.Status == restoreFailed {
				ids = append(ids, wl.ID)
			}
		}
	} else {
		ids = []string{id}
	}
	for _, wid := range ids {
		s.setWorkloadRestore(ctx, clusterID, wid, func(r *workloadRestore) {
			if r.Status != restoreRunning {
				r.Status, r.Step = restorePending, "queued"
			}
		})
	}
	s.audit(r, clusterID, p.User.ID, "recovery.workload.restore", "started", id)
	go func() {
		bg := context.WithoutCancel(ctx)
		for _, wid := range ids {
			_ = s.restoreRecoveredWorkload(bg, clusterID, wid, ap)
		}
	}()
	writeJSON(w, http.StatusAccepted, s.recoveryJSON(ctx, clusterID))
}

// recoveryVolumeLocator finds a recovered workload's boot volume. Unlike
// bootVolumeLocator it does not require the volume to exist yet: after
// recovery it is created here.
func (s *Server) recoveryVolumeLocator(ctx context.Context, clusterID string, row appdb.Workload) (*appdb.Volume, *appdb.StoragePool, string, error) {
	disks, err := s.Store.ListWorkloadDisks(ctx, clusterID, row.ID)
	if err != nil {
		return nil, nil, "", err
	}
	volID := ""
	for _, d := range disks {
		if d.Role == vmspec.DiskRoleBoot || d.Role == "root" || volID == "" {
			volID = d.VolumeID
		}
	}
	if volID == "" {
		return nil, nil, "", errConflict("workload has no boot volume")
	}
	vol, err := s.Store.GetVolume(ctx, clusterID, volID)
	if err != nil || vol == nil {
		return nil, nil, "", errNotFound("volume is not found")
	}
	pool, err := s.Store.GetStoragePool(ctx, clusterID, vol.PoolID)
	if err != nil || pool == nil {
		return nil, nil, "", errNotFound("storage pool is not found")
	}
	if pool.BackendType != "" && pool.BackendType != storage.BackendDirectory {
		return nil, nil, "", errUnprocessable("only Directory pools are recreated after recovery; restore this workload from Backups")
	}
	tip, err := storage.HostVolumePath(pool.BackendType, pool.RootPath, vol.BackendRef)
	if err != nil {
		return nil, nil, "", errConflict("volume locator is invalid")
	}
	return vol, pool, tip, nil
}

func (s *Server) listBackupWorkloadOptions(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	opts, err := s.Store.ListBackupWorkloadOptions(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(opts))
	for _, o := range opts {
		items = append(items, map[string]any{"workload_id": o.WorkloadID, "keep_images": o.KeepImages})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["workload_id"].(string) < items[j]["workload_id"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// setBackupWorkloadOptions sets one workload's backup options. Keeping a
// copy of its images makes its backups bigger but restorable without the
// registry or a rebuild.
func (s *Server) setBackupWorkloadOptions(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	var req struct {
		KeepImages bool `json:"keep_images"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	id := r.PathValue("id")
	if wl, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, id); err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	if err := s.Store.SetBackupWorkloadOptions(r.Context(), appdb.BackupWorkloadOptions{ClusterID: p.User.ClusterID, WorkloadID: id, KeepImages: req.KeepImages}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.workload_options", "ok", id)
	writeJSON(w, http.StatusOK, map[string]any{"workload_id": id, "keep_images": req.KeepImages})
}
