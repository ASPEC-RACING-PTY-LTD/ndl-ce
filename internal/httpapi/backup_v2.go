package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/backupscope"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) getBackupWorkspace(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	settings, err := s.Store.GetBackupWorkspaceSettings(r.Context(), p.User.ClusterID)
	if err != nil || settings == nil {
		settings = appdb.DefaultBackupWorkspaceSettings(p.User.ClusterID)
	}
	st, _ := s.v2Status(r.Context(), p.User.ClusterID, *settings)
	writeJSON(w, http.StatusOK, workspaceJSON(*settings, st))
}

func (s *Server) patchBackupWorkspace(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	var req struct {
		MaxLocalBytes      *int64 `json:"max_local_bytes"`
		MinHostFreeBytes   *int64 `json:"min_host_free_bytes"`
		CaptureConcurrency *int   `json:"capture_concurrency"`
		UploadWorkers      *int   `json:"upload_workers"`
		BandwidthLimitBPS  *int64 `json:"bandwidth_limit_bps"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid workspace settings")
		return
	}
	cur, err := s.Store.GetBackupWorkspaceSettings(r.Context(), p.User.ClusterID)
	if err != nil || cur == nil {
		cur = appdb.DefaultBackupWorkspaceSettings(p.User.ClusterID)
	}
	if req.MaxLocalBytes != nil {
		cur.MaxLocalBytes = *req.MaxLocalBytes
	}
	if req.MinHostFreeBytes != nil {
		cur.MinHostFreeBytes = *req.MinHostFreeBytes
	}
	if req.CaptureConcurrency != nil {
		cur.CaptureConcurrency = *req.CaptureConcurrency
	}
	if req.UploadWorkers != nil {
		cur.UploadWorkers = *req.UploadWorkers
	}
	if req.BandwidthLimitBPS != nil {
		cur.BandwidthLimitBPS = *req.BandwidthLimitBPS
	}
	if cur.MaxLocalBytes < 0 || cur.MinHostFreeBytes < 0 || cur.CaptureConcurrency < 0 || cur.UploadWorkers < 0 || cur.BandwidthLimitBPS < 0 {
		writeErr(w, http.StatusBadRequest, "workspace limits cannot be negative")
		return
	}
	if cur.CaptureConcurrency == 0 {
		cur.CaptureConcurrency = 1
	}
	if cur.UploadWorkers == 0 {
		cur.UploadWorkers = backuphost.DefaultUploadWorkers
	}
	if err := s.Store.UpsertBackupWorkspaceSettings(r.Context(), *cur); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	st, _ := s.v2Status(r.Context(), p.User.ClusterID, *cur)
	writeJSON(w, http.StatusOK, workspaceJSON(*cur, st))
}

func (s *Server) listBackupRestorePoints(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	s.syncV2State(r.Context(), p.User.ClusterID)
	items, err := s.Store.ListBackupRestorePoints(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, pt := range items {
		out = append(out, restorePointJSON(pt))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) previewBackupScope(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	var req struct {
		WorkloadIDs []string `json:"workload_ids"`
		CaptureMode string   `json:"capture_mode"`
		ScopeJSON   json.RawMessage `json:"scope_json"`
	}
	if err := readJSON(r, &req); err != nil || len(req.WorkloadIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "workload_ids are required")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.CaptureMode))
	if mode == "" {
		mode = appdb.BackupCaptureSmart
	}
	selections := decodePolicySelections(string(req.ScopeJSON))
	var previews []map[string]any
	var protected, excluded, full int64
	for _, id := range uniqueTrimmed(req.WorkloadIDs) {
		wl, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, id)
		if err != nil || wl == nil {
			writeErr(w, http.StatusNotFound, "workload not found")
			return
		}
		prev, err := s.previewWorkload(r.Context(), p.User.ClusterID, *wl, mode, selections[id])
		if err != nil {
			writeErr(w, statusFor(err), err.Error())
			return
		}
		protected += prev.ProtectedBytes
		excluded += prev.ExcludedBytes
		full += prev.FullBytes
		previews = append(previews, scopePreviewJSON(prev))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": previews,
		"protected_bytes": protected, "excluded_bytes": excluded, "full_bytes": full,
		"capture_mode": mode,
	})
}

func (s *Server) previewWorkload(ctx context.Context, clusterID string, wl appdb.Workload, mode string, sel backupscope.Selection) (backupscope.Preview, error) {
	if s.Backup == nil {
		return backupscope.Preview{}, errUnavailable("backup agent is unavailable")
	}
	_, _, rootfs, err := s.bootVolumeLocator(ctx, clusterID, wl)
	if err != nil {
		return backupscope.Preview{}, err
	}
	req := backuphost.Request{
		Action: backuphost.ActionPreview, WorkloadID: wl.ID, WorkloadName: wl.Name,
		CaptureMode: mode, Selection: sel,
	}
	raw, _ := json.Marshal(req)
	res, err := s.Backup.CopyBackup(ctx, qemu.BackupV2Preview, rootfs, string(raw))
	if err != nil {
		return backupscope.Preview{}, err
	}
	var out backuphost.Result
	if res.Extra != "" {
		_ = json.Unmarshal([]byte(res.Extra), &out)
	}
	if out.Preview.WorkloadID == "" {
		out.Preview.WorkloadID = wl.ID
		out.Preview.WorkloadName = wl.Name
	}
	return out.Preview, nil
}

func (s *Server) executeDirectoryCTBackupV2(ctx context.Context, clusterID string, wl appdb.Workload, vol *appdb.Volume, rootfs string, tgt appdb.BackupTarget, run *appdb.BackupRun, artifactID, captureMode string, sel backupscope.Selection) error {
	if s.Backup == nil {
		return errUnavailable("backup agent is unavailable")
	}
	settings, _ := s.Store.GetBackupWorkspaceSettings(ctx, clusterID)
	if settings == nil {
		settings = appdb.DefaultBackupWorkspaceSettings(clusterID)
	}
	bp := s.v2Blueprint(ctx, clusterID, wl, vol)
	bp.CaptureMode = firstNonEmpty(captureMode, appdb.BackupCaptureSmart)
	unit := ""
	if wl.Status == lxc.StatusRunning || wl.UnitActive {
		unit = lxc.UnitName(wl.ID)
	}
	req := backuphost.Request{
		Action: backuphost.ActionCapture, WorkloadID: wl.ID, WorkloadName: wl.Name,
		Unit: unit, CaptureMode: bp.CaptureMode, Blueprint: bp, Selection: sel,
		Settings: backuphost.Settings{
			MaxLocalBytes: settings.MaxLocalBytes, MinHostFreeBytes: settings.MinHostFreeBytes,
			CaptureConcurrency: settings.CaptureConcurrency, UploadWorkers: settings.UploadWorkers,
			BandwidthLimitBPS: settings.BandwidthLimitBPS,
		},
		Target: s.v2TargetSpec(ctx, tgt),
	}
	raw, _ := json.Marshal(req)
	res, err := s.Backup.CopyBackup(ctx, qemu.BackupV2Capture, rootfs, string(raw))
	if err != nil {
		return err
	}
	var out backuphost.Result
	if res.Extra != "" {
		_ = json.Unmarshal([]byte(res.Extra), &out)
	}
	stats, _ := json.Marshal(out)
	blueprint, _ := json.Marshal(out.Blueprint)
	art := appdb.BackupArtifact{
		ID: artifactID, ClusterID: clusterID, RunID: run.ID, WorkloadID: wl.ID,
		ChecksumSHA256: firstNonEmpty(res.SHA256, out.BackupID), SizeBytes: firstInt64(out.LogicalBytes, res.Size),
		Locator: firstNonEmpty(out.Locator, res.Dest), Format: backup.Format, Encrypted: true,
		EngineVersion: "v2", BackupID: out.BackupID, Namespace: out.Namespace,
		LocalComplete: true, RemoteState: string(out.Remote),
		LogicalBytes: out.LogicalBytes, PhysicalNewData: out.PhysicalNewData,
		ChunksNew: out.ChunksNew, ChunksReused: out.ChunksReused,
		CaptureDurationNS: out.DurationNanos, Consistency: out.Consistency,
		CaptureMode: out.CaptureMode, BlueprintJSON: string(blueprint), StatsJSON: string(stats),
	}
	if out.Remote == backup.RemoteProtected || out.Remote == backup.RemoteVerifying || out.Remote == backup.RemoteUploading || out.Remote == backup.RemoteQueued {
		if isObjectBackupKind(tgt.Kind) {
			art.ObjectKey = "backups/" + out.Namespace + "/manifests/" + out.BackupID + ".snap"
		}
	}
	if err := s.Store.CreateBackupArtifact(ctx, art); err != nil {
		return err
	}
	_ = s.Store.UpsertBackupRestorePoint(ctx, appdb.BackupRestorePoint{
		ID: uuid.NewString(), ClusterID: clusterID, ArtifactID: art.ID, RunID: run.ID,
		WorkloadID: wl.ID, BackupID: out.BackupID, Namespace: out.Namespace,
		CaptureMode: art.CaptureMode, LocalComplete: true, RemoteState: art.RemoteState,
		LogicalBytes: art.LogicalBytes, PhysicalNewData: art.PhysicalNewData,
	})
	_ = s.Store.UpsertBackupRepository(ctx, appdb.BackupRepository{
		ID: uuid.NewString(), ClusterID: clusterID, RootPath: backuphost.DefaultRoot,
		SizeBytes: out.RepoBytes, Status: "local",
	})
	run.TransferredBytes = out.PhysicalNewData
	run.Incremental = out.ChunksReused > 0
	return nil
}

func (s *Server) v2Blueprint(ctx context.Context, clusterID string, wl appdb.Workload, vol *appdb.Volume) backup.Blueprint {
	bp := backup.Blueprint{
		Kind: backup.BlueprintKind, Version: 1, WorkloadID: wl.ID, Name: wl.Name,
		Hostname: wl.Name, WorkloadType: lxc.KindSystemContainer, BaseImage: wl.ImagePin,
		CPU: wl.CPUs, MemoryBytes: wl.MemoryBytes, Autostart: wl.Autostart,
	}
	if vol != nil {
		bp.StorageBytes = vol.SizeBytes
		bp.StoragePool = vol.PoolID
	}
	nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, wl.ID)
	for i, n := range nics {
		name := "eth0"
		if i > 0 {
			name = "eth" + strings.TrimPrefix(n.ID, "")[:1]
		}
		bridge := ""
		if netw, err := s.Store.GetNetwork(ctx, clusterID, n.NetworkID); err == nil && netw != nil {
			bridge = netw.BridgeName
		}
		bp.Interfaces = append(bp.Interfaces, backup.NetInterface{Name: name, MAC: n.MAC, Bridge: bridge})
	}
	return bp
}

func (s *Server) v2TargetSpec(ctx context.Context, tgt appdb.BackupTarget) backuphost.TargetSpec {
	spec := backuphost.TargetSpec{
		ID: tgt.ID, Kind: tgt.Kind, Locator: tgt.Locator, Endpoint: tgt.Endpoint,
		Region: tgt.Region, Bucket: tgt.Bucket, Prefix: tgt.Prefix, AccessKey: tgt.Username,
	}
	if pass, _, err := s.Store.BackupCredentials(ctx, tgt.ClusterID, tgt.ID); err == nil {
		spec.SecretKey = pass
	}
	return spec
}

func (s *Server) v2Status(ctx context.Context, clusterID string, settings appdb.BackupWorkspaceSettings) (backuphost.Result, error) {
	if s.Backup == nil {
		return backuphost.Result{}, errUnavailable("backup agent is unavailable")
	}
	req := backuphost.Request{Action: backuphost.ActionStatus, Settings: backuphost.Settings{
		MaxLocalBytes: settings.MaxLocalBytes, MinHostFreeBytes: settings.MinHostFreeBytes,
		CaptureConcurrency: settings.CaptureConcurrency, UploadWorkers: settings.UploadWorkers,
	}}
	raw, _ := json.Marshal(req)
	res, err := s.Backup.CopyBackup(ctx, qemu.BackupV2Status, "", string(raw))
	if err != nil {
		return backuphost.Result{}, err
	}
	var out backuphost.Result
	if res.Extra != "" {
		_ = json.Unmarshal([]byte(res.Extra), &out)
	}
	return out, nil
}

func (s *Server) syncV2State(ctx context.Context, clusterID string) {
	settings, _ := s.Store.GetBackupWorkspaceSettings(ctx, clusterID)
	if settings == nil {
		settings = appdb.DefaultBackupWorkspaceSettings(clusterID)
	}
	st, err := s.v2Status(ctx, clusterID, *settings)
	if err != nil {
		return
	}
	_ = s.Store.UpsertBackupRepository(ctx, appdb.BackupRepository{
		ID: uuid.NewString(), ClusterID: clusterID, RootPath: firstNonEmpty(st.Workspace.Root, backuphost.DefaultRoot),
		SizeBytes: st.RepoBytes, Status: "local",
	})
	arts, _ := s.Store.ListBackupArtifacts(ctx, clusterID)
	byBackup := map[string]appdb.BackupArtifact{}
	for _, a := range arts {
		if a.BackupID != "" {
			byBackup[a.BackupID] = a
		}
	}
	for _, pt := range st.Points {
		if a, ok := byBackup[pt.BackupID]; ok {
			a.RemoteState = string(pt.Remote)
			a.LocalComplete = pt.LocalComplete
			_ = s.Store.UpdateBackupArtifact(ctx, a)
		}
	}
}

func (s *Server) restoreV2Root(ctx context.Context, art appdb.BackupArtifact, dest string, tgt *appdb.BackupTarget) error {
	req := backuphost.Request{
		Action: backuphost.ActionRestore, Namespace: art.Namespace, BackupID: art.BackupID, Dest: dest, Chown: true,
	}
	if tgt != nil {
		req.Target = s.v2TargetSpec(ctx, *tgt)
	}
	raw, _ := json.Marshal(req)
	_, err := s.Backup.CopyBackup(ctx, qemu.BackupV2Restore, dest, string(raw))
	return err
}

func decodePolicySelections(raw string) map[string]backupscope.Selection {
	out := map[string]backupscope.Selection{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	var wrap struct {
		Workloads map[string]backupscope.Selection `json:"workloads"`
	}
	if json.Unmarshal([]byte(raw), &wrap) == nil && wrap.Workloads != nil {
		return wrap.Workloads
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func selectionFor(raw string, workloadID string) backupscope.Selection {
	return decodePolicySelections(raw)[workloadID]
}

func workspaceJSON(s appdb.BackupWorkspaceSettings, st backuphost.Result) map[string]any {
	return map[string]any{
		"max_local_bytes": s.MaxLocalBytes, "min_host_free_bytes": s.MinHostFreeBytes,
		"capture_concurrency": s.CaptureConcurrency, "upload_workers": s.UploadWorkers,
		"bandwidth_limit_bps": s.BandwidthLimitBPS,
		"repo_bytes": st.RepoBytes, "pending_uploads": st.PendingUploads,
		"protected_workloads": st.Protected, "host_free_bytes": st.Workspace.HostFreeBytes,
		"capture_busy": st.Workspace.CaptureBusy, "root": firstNonEmpty(st.Workspace.Root, backuphost.DefaultRoot),
	}
}

func restorePointJSON(p appdb.BackupRestorePoint) map[string]any {
	return map[string]any{
		"id": p.ID, "artifact_id": p.ArtifactID, "run_id": p.RunID, "workload_id": p.WorkloadID,
		"backup_id": p.BackupID, "namespace": p.Namespace, "capture_mode": p.CaptureMode,
		"local_complete": p.LocalComplete, "remote_state": p.RemoteState,
		"logical_bytes": p.LogicalBytes, "physical_new_data": p.PhysicalNewData,
		"created_at": p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func scopePreviewJSON(p backupscope.Preview) map[string]any {
	items := make([]map[string]any, 0, len(p.Items))
	for _, it := range p.Items {
		row := map[string]any{
			"id": it.ID, "kind": it.Kind, "label": it.Label, "paths": it.Paths,
			"bytes": it.Bytes, "selected": it.Selected, "default_on": it.DefaultOn,
			"reproducible": it.Reproducible,
		}
		if len(it.Warnings) > 0 {
			row["warnings"] = it.Warnings
		}
		items = append(items, row)
	}
	warns := make([]map[string]any, 0, len(p.Warnings))
	for _, w := range p.Warnings {
		warns = append(warns, map[string]any{"level": w.Level, "message": w.Message, "item_id": w.ItemID, "path": w.Path})
	}
	return map[string]any{
		"workload_id": p.WorkloadID, "workload_name": p.WorkloadName, "mode": p.Mode,
		"items": items, "warnings": warns,
		"protected_bytes": p.ProtectedBytes, "excluded_bytes": p.ExcludedBytes, "full_bytes": p.FullBytes,
	}
}

func firstInt64(vs ...int64) int64 {
	for _, v := range vs {
		if v != 0 {
			return v
		}
	}
	return 0
}

func captureModeLabel(mode string) string {
	switch mode {
	case appdb.BackupCaptureCustom:
		return "Custom"
	case appdb.BackupCaptureFull:
		return "Full Machine"
	default:
		return "Smart Application Data"
	}
}

func protectionLabel(remote string, local bool, format string) string {
	if format != backup.Format {
		if local {
			return "Local complete"
		}
		return "Legacy"
	}
	switch remote {
	case string(backup.RemoteQueued):
		return "Queued"
	case string(backup.RemoteUploading):
		return "Uploading"
	case string(backup.RemoteVerifying):
		return "Remote verifying"
	case string(backup.RemoteProtected):
		return "Protected"
	case string(backup.RemoteFailed):
		return "Failed"
	default:
		if local {
			return "Local complete"
		}
		return "Capturing"
	}
}
