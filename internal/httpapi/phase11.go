package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/objstore"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

const (
	ctBackupReason   = "Directory system container backups are not available. They are not ZFS. Use a ZFS dataset for system container backup send."
	zfsRestoreReason = "ZFS send artifacts restore with zfs recv in a later backup phase. qemu-img is not used on a ZFS stream."
	restoreConfirm   = "restore"
	backupPurpose    = "ndl-backup"
)

// BackupRPC is the privileged agent surface for checksummed backup copies.
type BackupRPC interface {
	CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error)
	ConvertImport(ctx context.Context, req qemu.ConvertRequest) error
	ExtractArchive(ctx context.Context, src, dest string) error
}

type backupUnavailable struct{}

func (backupUnavailable) CopyBackup(context.Context, string, string, string) (storage.CopyResult, error) {
	return storage.CopyResult{}, errUnavailable("backup agent is unavailable")
}

func (backupUnavailable) ConvertImport(context.Context, qemu.ConvertRequest) error {
	return errUnavailable("disk convert agent is unavailable")
}

func (backupUnavailable) ExtractArchive(context.Context, string, string) error {
	return errUnavailable("archive extract agent is unavailable")
}

func AdaptBackup(client any) BackupRPC {
	if v, ok := client.(BackupRPC); ok {
		return v
	}
	return backupUnavailable{}
}

func backupTargetJSON(t appdb.BackupTarget) map[string]any {
	out := map[string]any{
		"id": t.ID, "name": t.Name, "kind": t.Kind, "locator": t.Locator, "status": t.Status,
	}
	if t.Username != "" {
		out["username"] = t.Username
	}
	if t.Endpoint != "" {
		out["endpoint"] = t.Endpoint
	}
	if t.Region != "" {
		out["region"] = t.Region
	}
	if t.Bucket != "" {
		out["bucket"] = t.Bucket
	}
	if t.Prefix != "" {
		out["prefix"] = t.Prefix
	}
	if isObjectBackupKind(t.Kind) {
		out["no_check_bucket"] = t.NoCheckBucket
		out["has_encryption_key"] = true
	}
	return out
}

func backupPolicyJSON(p appdb.BackupPolicy) map[string]any {
	out := map[string]any{
		"id": p.ID, "name": p.Name, "workload_id": p.WorkloadID, "target_id": p.TargetID,
		"schedule": p.Schedule, "keep_daily": p.KeepDaily, "keep_weekly": p.KeepWeekly, "keep_monthly": p.KeepMonthly,
	}
	if p.LastRunAt != nil {
		out["last_run_at"] = p.LastRunAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return out
}

func backupRunJSON(r appdb.BackupRun) map[string]any {
	out := map[string]any{
		"id": r.ID, "target_id": r.TargetID, "workload_id": r.WorkloadID,
		"status": r.Status, "started_at": r.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if r.PolicyID != "" {
		out["policy_id"] = r.PolicyID
	}
	if r.SnapshotID != "" {
		out["snapshot_id"] = r.SnapshotID
	}
	if r.Error != "" {
		out["error"] = r.Error
	}
	if r.FinishedAt != nil {
		out["finished_at"] = r.FinishedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if r.RestoredWorkloadID != "" {
		out["restored_workload_id"] = r.RestoredWorkloadID
	}
	if r.TransferredBytes > 0 {
		out["transferred_bytes"] = r.TransferredBytes
	}
	if r.Incremental {
		out["incremental"] = true
	}
	return out
}

func backupArtifactJSON(a appdb.BackupArtifact) map[string]any {
	status := a.VerifyStatus
	if status == "" {
		status = appdb.BackupUnverified
	}
	out := map[string]any{
		"id": a.ID, "run_id": a.RunID, "workload_id": a.WorkloadID,
		"checksum_sha256": a.ChecksumSHA256, "size_bytes": a.SizeBytes,
		"locator": a.Locator, "format": a.Format,
		"created_at":    a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"verify_status": status,
	}
	if a.Encrypted {
		out["encrypted"] = true
	}
	if a.TransferredBytes > 0 {
		out["transferred_bytes"] = a.TransferredBytes
	}
	if a.ObjectKey != "" {
		out["object_key"] = a.ObjectKey
	}
	if a.ParentArtifactID != "" {
		out["parent_artifact_id"] = a.ParentArtifactID
	}
	if a.VerifyError != "" {
		out["verify_error"] = a.VerifyError
	}
	if a.LastTestedAt != nil {
		out["last_tested_at"] = a.LastTestedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if a.ThrowawayWorkloadID != "" {
		out["throwaway_workload_id"] = a.ThrowawayWorkloadID
	}
	appdb.FillArtifactLocality(&a)
	out["locality"] = a.Locality
	if a.PullURL != "" {
		out["pull_url"] = a.PullURL
	}
	return out
}

func (s *Server) probeBackupTarget(ctx context.Context, kind, locator string) string {
	exists := false
	if s.Backup != nil {
		res, err := s.Backup.CopyBackup(ctx, qemu.BackupStat, "", locator)
		exists = err == nil && res.Size > 0
	}
	switch kind {
	case appdb.BackupLocal:
		if exists {
			return appdb.BackupAvailable
		}
		return appdb.BackupNotConfigured
	case appdb.BackupNFS, appdb.BackupSMB:
		if exists {
			return appdb.BackupAvailable
		}
		return appdb.BackupUnavailable
	default:
		if isObjectBackupKind(kind) {
			return appdb.BackupNotConfigured
		}
		return appdb.BackupUnavailable
	}
}

func (s *Server) listBackupTargets(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListBackupTargets(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, t := range items {
		status := t.Status
		if isObjectBackupKind(t.Kind) {
			status = s.probeObjectTarget(r.Context(), t)
		} else {
			status = s.probeBackupTarget(r.Context(), t.Kind, t.Locator)
		}
		if status != t.Status {
			_ = s.Store.UpdateBackupTargetStatus(r.Context(), p.User.ClusterID, t.ID, status)
			t.Status = status
		}
		out = append(out, backupTargetJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createBackupTarget(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	var req struct {
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		Locator       string `json:"locator"`
		Username      string `json:"username"`
		Password      string `json:"password"`
		Endpoint      string `json:"endpoint"`
		Region        string `json:"region"`
		Bucket        string `json:"bucket"`
		Prefix        string `json:"prefix"`
		NoCheckBucket bool   `json:"no_check_bucket"`
		EncryptionKey string `json:"encryption_key"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name and kind are required")
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if isObjectBackupKind(kind) {
		if err := validateObjectTarget(kind, req.Endpoint, req.Bucket); err != nil {
			writeErr(w, statusFor(err), err.Error())
			return
		}
		if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
			writeErr(w, http.StatusBadRequest, "access key id and secret access key are required")
			return
		}
		encHex := strings.TrimSpace(req.EncryptionKey)
		if encHex == "" {
			generated, _, err := objstore.GenerateKey()
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			encHex = generated
		} else if _, err := objstore.ParseKey(encHex); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		region := strings.TrimSpace(req.Region)
		if region == "" {
			region = objstore.DefaultRegion(kind)
		}
		prefix := strings.Trim(strings.TrimSpace(req.Prefix), "/")
		row := appdb.BackupTarget{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name),
			Kind: kind, Locator: objstore.Locator(req.Bucket, prefix), Status: appdb.BackupNotConfigured,
			Username: strings.TrimSpace(req.Username), Endpoint: strings.TrimSpace(req.Endpoint),
			Region: region, Bucket: strings.TrimSpace(req.Bucket), Prefix: prefix, NoCheckBucket: req.NoCheckBucket,
		}
		if err := s.Store.CreateBackupTarget(r.Context(), row, req.Password, encHex); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		status := s.probeObjectTarget(r.Context(), row)
		if status != row.Status {
			if err := s.Store.UpdateBackupTargetStatus(r.Context(), p.User.ClusterID, row.ID, status); err != nil {
				writeErr(w, http.StatusInternalServerError, "could not record backup target")
				return
			}
			row.Status = status
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "backup.target.create", "ok", row.ID)
		writeJSON(w, http.StatusCreated, backupTargetJSON(row))
		return
	}
	if strings.TrimSpace(req.Locator) == "" {
		writeErr(w, http.StatusBadRequest, "name, kind, and locator are required")
		return
	}
	if kind != appdb.BackupLocal && kind != appdb.BackupNFS && kind != appdb.BackupSMB {
		writeErr(w, http.StatusBadRequest, "kind must be local, nfs, smb, s3, r2, aws, b2, or minio")
		return
	}
	locator := strings.TrimSpace(req.Locator)
	if strings.Contains(locator, "..") {
		writeErr(w, http.StatusBadRequest, "locator must be an absolute path without traversal")
		return
	}
	// UNC //server/share is not a Unix artifact path. Engine Stat skips
	// AllowedArtifactPath for that prefix; HTTP must not 400 a typed SMB locator.
	unc := strings.HasPrefix(locator, "//")
	unixAbs := strings.HasPrefix(locator, "/") && !unc
	if unixAbs {
		if err := storage.AllowedArtifactPath(locator); err != nil {
			writeErr(w, http.StatusBadRequest, "backup locator is not allowed")
			return
		}
	}
	if kind == appdb.BackupNFS && !unixAbs {
		if _, _, err := storage.ParseNFSLocator(locator); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if kind == appdb.BackupSMB && !unixAbs {
		if _, _, err := storage.ParseSMBLocator(locator); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if kind == appdb.BackupLocal {
		if !unixAbs {
			writeErr(w, http.StatusBadRequest, "local locator must be an absolute path")
			return
		}
		if s.Backup == nil {
			writeErr(w, http.StatusBadGateway, "backup agent is unavailable")
			return
		}
		if _, err := s.Backup.CopyBackup(r.Context(), qemu.BackupMkdir, "", locator); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "local backup directory could not be created")
			return
		}
	}
	status := s.probeBackupTarget(r.Context(), kind, locator)
	row := appdb.BackupTarget{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name),
		Kind: kind, Locator: locator, Status: status, Username: strings.TrimSpace(req.Username),
	}
	if err := s.Store.CreateBackupTarget(r.Context(), row, req.Password, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.target.create", "ok", row.ID)
	writeJSON(w, http.StatusCreated, backupTargetJSON(row))
}

func (s *Server) listBackupPolicies(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListBackupPolicies(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, backupPolicyJSON(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createBackupPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	var req struct {
		Name        string `json:"name"`
		WorkloadID  string `json:"workload_id"`
		TargetID    string `json:"target_id"`
		Schedule    string `json:"schedule"`
		KeepDaily   int    `json:"keep_daily"`
		KeepWeekly  int    `json:"keep_weekly"`
		KeepMonthly int    `json:"keep_monthly"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" || req.WorkloadID == "" || req.TargetID == "" {
		writeErr(w, http.StatusBadRequest, "name, workload_id, and target_id are required")
		return
	}
	if req.Schedule == "" {
		req.Schedule = appdb.BackupNightly
	}
	if req.Schedule != appdb.BackupNightly {
		writeErr(w, http.StatusBadRequest, "schedule must be nightly")
		return
	}
	wl, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, req.WorkloadID)
	if err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	tgt, err := s.Store.GetBackupTarget(r.Context(), p.User.ClusterID, req.TargetID)
	if err != nil || tgt == nil {
		writeErr(w, http.StatusNotFound, "backup target not found")
		return
	}
	_ = wl
	_ = tgt
	if req.KeepDaily < 0 || req.KeepWeekly < 0 || req.KeepMonthly < 0 {
		writeErr(w, http.StatusBadRequest, "retention counts cannot be negative")
		return
	}
	if req.KeepDaily == 0 && req.KeepWeekly == 0 && req.KeepMonthly == 0 {
		req.KeepDaily, req.KeepWeekly, req.KeepMonthly = 7, 4, 3
	}
	row := appdb.BackupPolicy{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name),
		WorkloadID: req.WorkloadID, TargetID: req.TargetID, Schedule: req.Schedule,
		KeepDaily: req.KeepDaily, KeepWeekly: req.KeepWeekly, KeepMonthly: req.KeepMonthly,
	}
	if err := s.Store.CreateBackupPolicy(r.Context(), row); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.policy.create", "ok", row.ID)
	writeJSON(w, http.StatusCreated, backupPolicyJSON(row))
}

func (s *Server) listBackupRuns(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListBackupRuns(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, backupRunJSON(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) listBackupArtifacts(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListBackupArtifacts(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, backupArtifactJSON(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) runBackup(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	var req struct {
		WorkloadID string `json:"workload_id"`
		TargetID   string `json:"target_id"`
		PolicyID   string `json:"policy_id"`
	}
	if err := readJSON(r, &req); err != nil || req.WorkloadID == "" || req.TargetID == "" {
		writeErr(w, http.StatusBadRequest, "workload_id and target_id are required")
		return
	}
	run, err := s.executeBackup(r.Context(), p.User.ClusterID, req.WorkloadID, req.TargetID, req.PolicyID)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.run", run.Status, run.ID)
	writeJSON(w, http.StatusAccepted, backupRunJSON(run))
}

// TickNightlyBackups runs due nightly policies. It does not fake NFS or SMB success.
func (s *Server) TickNightlyBackups(ctx context.Context) {
	if !s.nightlyBusy.CompareAndSwap(false, true) {
		return
	}
	defer s.nightlyBusy.Store(false)
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil || cluster.SetupCompletedAt == nil {
		return
	}
	policies, err := s.Store.ListBackupPolicies(ctx, cluster.ID)
	if err != nil {
		return
	}
	now := s.now()
	for _, pol := range policies {
		if pol.Schedule != appdb.BackupNightly {
			continue
		}
		if pol.LastRunAt != nil && now.Sub(*pol.LastRunAt) < 23*time.Hour {
			continue
		}
		runs, _ := s.Store.ListBackupRuns(ctx, cluster.ID)
		busy := false
		for _, r := range runs {
			if r.PolicyID == pol.ID && r.Status == appdb.BackupRunning {
				busy = true
				break
			}
		}
		if busy {
			continue
		}
		_, _ = s.executeBackup(ctx, cluster.ID, pol.WorkloadID, pol.TargetID, pol.ID)
	}
}

func (s *Server) executeBackup(ctx context.Context, clusterID, workloadID, targetID, policyID string) (appdb.BackupRun, error) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	wl, err := s.Store.GetWorkload(ctx, clusterID, workloadID)
	if err != nil || wl == nil {
		return appdb.BackupRun{}, errNotFound("workload not found")
	}
	_, pool, _, locErr := s.bootVolumeLocator(ctx, clusterID, *wl)
	native := locErr == nil && pool != nil && (pool.BackendType == storage.BackendZFS || pool.BackendType == storage.BackendLVM)
	if locErr == nil && pool != nil && pool.BackendType == storage.BackendISCSI {
		return appdb.BackupRun{}, errUnprocessable(iscsiSnapReason)
	}
	if locErr == nil && pool != nil && pool.BackendType == storage.BackendDistributed {
		return appdb.BackupRun{}, errUnprocessable(distSnapReason)
	}
	if !native && (wl.Kind == lxc.KindSystemContainer || wl.Kind != vmspec.KindVM) {
		return appdb.BackupRun{}, errUnprocessable(ctBackupReason)
	}
	tgt, err := s.Store.GetBackupTarget(ctx, clusterID, targetID)
	if err != nil || tgt == nil {
		return appdb.BackupRun{}, errNotFound("backup target not found")
	}
	status := s.probeBackupTarget(ctx, tgt.Kind, tgt.Locator)
	if isObjectBackupKind(tgt.Kind) {
		status = s.probeObjectTarget(ctx, *tgt)
	}
	_ = s.Store.UpdateBackupTargetStatus(ctx, clusterID, tgt.ID, status)
	tgt.Status = status
	if !backupTargetAllowsRun(*tgt) {
		return appdb.BackupRun{}, errUnprocessable("backup target is unavailable")
	}
	if s.Backup == nil {
		return appdb.BackupRun{}, errUnavailable("backup agent is unavailable")
	}
	if !native && s.VM == nil {
		return appdb.BackupRun{}, errUnavailable("backup agent is unavailable")
	}
	run := appdb.BackupRun{
		ID: uuid.NewString(), ClusterID: clusterID, PolicyID: policyID, TargetID: targetID,
		WorkloadID: workloadID, Status: appdb.BackupRunning, StartedAt: s.now(),
	}
	if err := s.Store.CreateBackupRun(ctx, run); err != nil {
		return appdb.BackupRun{}, err
	}
	fail := func(msg string) (appdb.BackupRun, error) {
		now := s.now()
		run.Status = appdb.BackupFailed
		run.Error = msg
		run.FinishedAt = &now
		if err := s.Store.UpdateBackupRun(ctx, run); err != nil {
			return run, errInternal("could not record backup run")
		}
		return run, nil
	}
	snap, frozen, err := s.snapshotForBackup(ctx, clusterID, *wl, run.ID)
	if err != nil {
		return fail(err.Error())
	}
	run.SnapshotID = snap.ID
	artifactID := uuid.NewString()
	objectKind := isObjectBackupKind(tgt.Kind)
	stageDir := tgt.Locator
	if objectKind {
		tmp, err := os.MkdirTemp("", "ndl-backup-")
		if err != nil {
			return fail(err.Error())
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		stageDir = tmp
	}
	format := "qcow2"
	dest := filepath.Join(stageDir, artifactID+".qcow2")
	var parentID string
	incremental := false
	fromSnap := ""
	if snap.Mechanism == appdb.MechanismZFS {
		format = "zfs"
		dest = filepath.Join(stageDir, artifactID+".zfs")
		if objectKind {
			prev, _ := s.Store.ListBackupArtifactsForWorkload(ctx, clusterID, workloadID, targetID)
			for _, a := range prev {
				if a.Format != "zfs" {
					continue
				}
				parentID = a.ID
				if pr, _ := s.Store.GetBackupRun(ctx, clusterID, a.RunID); pr != nil && pr.SnapshotID != "" {
					if ps, _ := s.Store.GetSnapshot(ctx, clusterID, pr.SnapshotID); ps != nil {
						fromSnap = s.snapshotTag(ps.BackendRef)
						incremental = fromSnap != ""
					}
				}
				break
			}
		}
		res, err := s.zfs().ZFSPool(ctx, storage.ZFSOp{
			Action: "send", PoolID: pool.ID, Name: s.zfsPoolName(ctx, *pool),
			VolumeID: snap.VolumeID, Snapshot: s.snapshotTag(snap.BackendRef), DestPath: dest, FromSnap: fromSnap,
		})
		if err != nil {
			return fail(err.Error())
		}
		if res.Status != storage.StatusAvailable {
			return fail(firstNonEmpty(res.Reason, "zfs send failed"))
		}
		art := appdb.BackupArtifact{
			ID: artifactID, ClusterID: clusterID, RunID: run.ID, WorkloadID: workloadID,
			Locator: dest, Format: "zfs", ParentArtifactID: parentID,
		}
		if objectKind {
			put, err := s.putObjectArtifact(ctx, *tgt, artifactID, dest, format)
			if err != nil {
				return fail(err.Error())
			}
			if put.Status == appdb.BackupUnavailable || strings.EqualFold(put.Status, "unavailable") {
				return fail(firstNonEmpty(put.Reason, "object upload is unavailable"))
			}
			art.Locator = objstore.Locator(tgt.Bucket, put.Key)
			art.ObjectKey = put.Key
			art.Encrypted = true
			art.ChecksumSHA256 = put.PlaintextSHA256
			art.SizeBytes = put.PlaintextSize
			art.TransferredBytes = put.TransferredBytes
			run.TransferredBytes = put.TransferredBytes
			run.Incremental = incremental && put.TransferredBytes > 0
			_ = s.Store.UpdateBackupTargetStatus(ctx, clusterID, tgt.ID, appdb.BackupAvailable)
		}
		if err := s.Store.CreateBackupArtifact(ctx, art); err != nil {
			return fail(err.Error())
		}
	} else {
		res, err := s.Backup.CopyBackup(ctx, qemu.BackupCopy, frozen, dest)
		if err != nil {
			return fail(err.Error())
		}
		art := appdb.BackupArtifact{
			ID: artifactID, ClusterID: clusterID, RunID: run.ID, WorkloadID: workloadID,
			ChecksumSHA256: res.SHA256, SizeBytes: res.Size, Locator: dest, Format: firstNonEmpty(res.Format, "qcow2"),
		}
		if objectKind {
			put, err := s.putObjectArtifact(ctx, *tgt, artifactID, dest, firstNonEmpty(res.Format, "qcow2"))
			if err != nil {
				return fail(err.Error())
			}
			if put.Status == appdb.BackupUnavailable || strings.EqualFold(put.Status, "unavailable") {
				return fail(firstNonEmpty(put.Reason, "object upload is unavailable"))
			}
			art.Locator = objstore.Locator(tgt.Bucket, put.Key)
			art.ObjectKey = put.Key
			art.Encrypted = true
			art.ChecksumSHA256 = put.PlaintextSHA256
			art.SizeBytes = put.PlaintextSize
			art.TransferredBytes = put.TransferredBytes
			run.TransferredBytes = put.TransferredBytes
			_ = s.Store.UpdateBackupTargetStatus(ctx, clusterID, tgt.ID, appdb.BackupAvailable)
		}
		if err := s.Store.CreateBackupArtifact(ctx, art); err != nil {
			return fail(err.Error())
		}
	}
	now := s.now()
	run.Status = appdb.BackupSucceeded
	run.FinishedAt = &now
	if err := s.Store.UpdateBackupRun(ctx, run); err != nil {
		return run, errInternal("could not record backup run")
	}
	if policyID != "" {
		_ = s.Store.UpdateBackupPolicyLastRun(ctx, clusterID, policyID, now)
		if pol, _ := s.Store.GetBackupPolicy(ctx, clusterID, policyID); pol != nil {
			s.pruneBackupArtifacts(ctx, clusterID, workloadID, targetID, *pol)
		}
	}
	return run, nil
}

func (s *Server) snapshotForBackup(ctx context.Context, clusterID string, row appdb.Workload, runID string) (appdb.Snapshot, string, error) {
	existing, err := s.Store.ListSnapshots(ctx, clusterID, row.ID)
	if err != nil {
		return appdb.Snapshot{}, "", err
	}
	vol, pool, tip, err := s.bootVolumeLocator(ctx, clusterID, row)
	if err != nil {
		return appdb.Snapshot{}, "", err
	}
	if pool.BackendType == storage.BackendZFS {
		tag := "backup-" + runID[:8]
		res, err := s.zfs().ZFSPool(ctx, storage.ZFSOp{
			Action: "snapshot", PoolID: pool.ID, Name: s.zfsPoolName(ctx, *pool),
			VolumeID: vol.ID, Snapshot: tag,
		})
		if err != nil {
			return appdb.Snapshot{}, "", err
		}
		if res.Status != storage.StatusAvailable {
			return appdb.Snapshot{}, "", errUnprocessable(firstNonEmpty(res.Reason, storage.ZFSMissing))
		}
		snap := appdb.Snapshot{
			ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: row.ID, VolumeID: vol.ID,
			Name: tag, PurposeTag: tag, Mechanism: appdb.MechanismZFS, BackendRef: res.BackendRef,
			Status: appdb.SnapshotAvailable,
		}
		if err := s.Store.CreateSnapshot(ctx, snap); err != nil {
			return appdb.Snapshot{}, "", err
		}
		return snap, res.BackendRef, nil
	}
	if pool.BackendType == storage.BackendLVM {
		tag := "backup-" + runID[:8]
		res, err := s.lvm().LVMPool(ctx, storage.LVMOp{
			Action: "snapshot", PoolID: pool.ID, Name: s.lvmVGName(ctx, *pool),
			VolumeID: vol.ID, Snapshot: tag,
		})
		if err != nil {
			return appdb.Snapshot{}, "", err
		}
		if res.Status != storage.StatusAvailable {
			return appdb.Snapshot{}, "", errUnprocessable(firstNonEmpty(res.Reason, storage.LVMMissing))
		}
		snap := appdb.Snapshot{
			ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: row.ID, VolumeID: vol.ID,
			Name: tag, PurposeTag: tag, Mechanism: appdb.MechanismLVM, BackendRef: res.BackendRef,
			Status: appdb.SnapshotAvailable,
		}
		if err := s.Store.CreateSnapshot(ctx, snap); err != nil {
			return appdb.Snapshot{}, "", err
		}
		return snap, res.BackendRef, nil
	}
	if pool.BackendType == storage.BackendISCSI {
		return appdb.Snapshot{}, "", errUnprocessable(iscsiSnapReason)
	}
	if pool.BackendType == storage.BackendDistributed {
		return appdb.Snapshot{}, "", errUnprocessable(distSnapReason)
	}
	depth := overlayChainDepth(vol.BackendRef, existing)
	if depth >= qemu.ChainMax {
		return appdb.Snapshot{}, "", errConflict("qcow2 overlay chain cap is 16")
	}
	snapID := uuid.NewString()
	overlayRel := path.Join("volumes", storage.ClassVMDisk, vol.ID+"--"+snapID+".qcow2")
	overlay, jerr := storage.JoinUnder(pool.RootPath, overlayRel)
	if jerr != nil {
		return appdb.Snapshot{}, "", errConflict("overlay locator is invalid")
	}
	parentID := ""
	if depth > 0 && len(existing) > 0 {
		parentID = existing[len(existing)-1].ID
	}
	_, err = s.VM.SnapshotVM(ctx, qemu.OverlayRequest{
		Action: qemu.OverlayCreate, WorkloadID: row.ID, OverlayPath: overlay, BackingPath: tip,
		ChainDepth: depth, ChainMax: qemu.ChainMax,
	})
	if err != nil {
		return appdb.Snapshot{}, "", err
	}
	if err := s.Store.UpdateVolumeLocator(ctx, clusterID, vol.ID, overlayRel); err != nil {
		return appdb.Snapshot{}, "", err
	}
	frozen, err := storage.JoinUnder(pool.RootPath, vol.BackendRef)
	if err != nil {
		return appdb.Snapshot{}, "", errConflict("volume locator is invalid")
	}
	snap := appdb.Snapshot{
		ID: snapID, ClusterID: clusterID, WorkloadID: row.ID, VolumeID: vol.ID,
		Name: "backup-" + runID[:8], PurposeTag: backupPurpose,
		Mechanism: appdb.MechanismOverlay, BackendRef: vol.BackendRef, ParentID: parentID,
		ChainDepth: depth + 1, Status: appdb.SnapshotAvailable,
	}
	if err := s.Store.CreateSnapshot(ctx, snap); err != nil {
		return appdb.Snapshot{}, "", err
	}
	return snap, frozen, nil
}

func (s *Server) pruneBackupArtifacts(ctx context.Context, clusterID, workloadID, targetID string, pol appdb.BackupPolicy) {
	arts, err := s.Store.ListBackupArtifactsForWorkload(ctx, clusterID, workloadID, targetID)
	if err != nil {
		return
	}
	keep := retainBackupIDs(arts, pol.KeepDaily, pol.KeepWeekly, pol.KeepMonthly)
	for _, a := range arts {
		if _, ok := keep[a.ID]; ok {
			continue
		}
		if s.Backup != nil && !strings.HasPrefix(a.Locator, "s3://") && a.ObjectKey == "" {
			_, _ = s.Backup.CopyBackup(ctx, qemu.BackupDelete, "", a.Locator)
		}
		if a.ObjectKey != "" {
			if tgt, _ := s.Store.GetBackupTarget(ctx, clusterID, targetID); tgt != nil {
				pass, enc, _ := s.Store.BackupCredentials(ctx, clusterID, tgt.ID)
				if key, err := objstore.ParseKey(enc); err == nil {
					_, _ = s.objectRPC().ObjectBackup(ctx, objstore.Request{
						Action: objstore.ActionDel, Provider: tgt.Kind, Endpoint: tgt.Endpoint, Region: tgt.Region,
						Bucket: tgt.Bucket, Key: a.ObjectKey, AccessKeyID: tgt.Username, SecretAccessKey: pass, EncryptionKey: key,
					})
				}
			}
		}
		_ = s.Store.DeleteBackupArtifact(ctx, clusterID, a.ID)
	}
}

func retainBackupIDs(arts []appdb.BackupArtifact, keepDaily, keepWeekly, keepMonthly int) map[string]struct{} {
	keep := map[string]struct{}{}
	seenDay := map[string]struct{}{}
	seenWeek := map[string]struct{}{}
	seenMonth := map[string]struct{}{}
	daily, weekly, monthly := 0, 0, 0
	for _, a := range arts {
		day := a.CreatedAt.UTC().Format("2006-01-02")
		y, w := a.CreatedAt.UTC().ISOWeek()
		weekKey := fmt.Sprintf("%d-%d", y, w)
		month := a.CreatedAt.UTC().Format("2006-01")
		if daily < keepDaily {
			if _, ok := seenDay[day]; !ok {
				keep[a.ID] = struct{}{}
				seenDay[day] = struct{}{}
				daily++
				continue
			}
		}
		if weekly < keepWeekly {
			if _, ok := seenWeek[weekKey]; !ok {
				keep[a.ID] = struct{}{}
				seenWeek[weekKey] = struct{}{}
				weekly++
				continue
			}
		}
		if monthly < keepMonthly {
			if _, ok := seenMonth[month]; !ok {
				keep[a.ID] = struct{}{}
				seenMonth[month] = struct{}{}
				monthly++
			}
		}
	}
	return keep
}

func (s *Server) resolveRestoreDest(ctx context.Context, clusterID, targetNodeID string) (*appdb.Node, error) {
	if strings.TrimSpace(targetNodeID) == "" {
		n, err := s.Store.GetNode(ctx, clusterID)
		if err != nil || n == nil {
			return nil, errUnprocessable("local node is not enrolled")
		}
		return n, nil
	}
	n, err := s.Store.GetNodeByID(ctx, clusterID, targetNodeID)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, errNotFound("target node not found")
	}
	if n.RevokedAt != nil {
		return nil, errUnprocessable("target node is revoked")
	}
	if maint, _ := s.Store.GetNodeMaintenance(ctx, clusterID, n.ID); maint != nil {
		return nil, errUnprocessable("target node is in maintenance")
	}
	return n, nil
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRestore)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	var req struct {
		Mode         string `json:"mode"`
		TargetNodeID string `json:"target_node_id"`
	}
	if err := readJSON(r, &req); err != nil || (req.Mode != "new" && req.Mode != "replace") {
		writeErr(w, http.StatusBadRequest, "mode must be new or replace")
		return
	}
	if req.Mode == "replace" && strings.TrimSpace(r.Header.Get("X-Nodal-Confirm")) != restoreConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "replace requires X-Nodal-Confirm: restore")
		return
	}
	art, err := s.Store.GetBackupArtifact(r.Context(), p.User.ClusterID, id)
	if err != nil || art == nil {
		writeErr(w, http.StatusNotFound, "artifact not found")
		return
	}
	src, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, art.WorkloadID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if src == nil && req.Mode == "replace" {
		writeErr(w, http.StatusUnprocessableEntity, "replace requires the original workload to still exist")
		return
	}
	if src != nil && src.Kind != vmspec.KindVM {
		writeErr(w, http.StatusUnprocessableEntity, "restore of system containers is not implemented")
		return
	}
	dest, err := s.resolveRestoreDest(r.Context(), p.User.ClusterID, strings.TrimSpace(req.TargetNodeID))
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if req.Mode == "replace" && src != nil && dest.ID != src.NodeID && dest.ID != src.DesiredNodeID {
		writeErr(w, http.StatusUnprocessableEntity, "replace stays on the current node; use mode new to restore onto another node")
		return
	}
	origRun, _ := s.Store.GetBackupRun(r.Context(), p.User.ClusterID, art.RunID)
	if origRun == nil || origRun.TargetID == "" {
		writeErr(w, http.StatusUnprocessableEntity, "restore cannot locate the original backup target")
		return
	}
	if art.Format == "zfs" {
		writeErr(w, http.StatusUnprocessableEntity, zfsRestoreReason)
		return
	}
	run := appdb.BackupRun{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, TargetID: origRun.TargetID,
		WorkloadID: art.WorkloadID, Status: appdb.BackupRunning, StartedAt: s.now(),
	}
	if err := s.Store.CreateBackupRun(r.Context(), run); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var restoredID string
	if req.Mode == "new" {
		if src == nil {
			restoredID, err = s.restoreOrphanVM(r.Context(), p.User.ClusterID, *art, dest)
		} else {
			restoredID, err = s.restoreNewVM(r.Context(), p.User.ClusterID, *src, *art, false, dest)
		}
	} else {
		err = s.restoreReplaceVM(r.Context(), p.User.ClusterID, *src, *art)
		restoredID = src.ID
	}
	now := s.now()
	run.FinishedAt = &now
	run.RestoredWorkloadID = restoredID
	if err != nil {
		run.Status = appdb.BackupFailed
		run.Error = err.Error()
		_ = s.Store.UpdateBackupRun(r.Context(), run)
		writeErr(w, statusFor(err), err.Error())
		return
	}
	run.Status = appdb.BackupSucceeded
	if err := s.Store.UpdateBackupRun(r.Context(), run); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record backup run")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.restore."+req.Mode, "ok", restoredID)
	if s.applyLocal(r.Context(), p.User.ClusterID, dest.ID) && (art.ObjectKey != "" || strings.HasPrefix(art.Locator, "s3://")) {
		s.audit(r, p.User.ClusterID, p.User.ID, "secret.use", "ok", dest.ID)
	}
	writeJSON(w, http.StatusAccepted, backupRunJSON(run))
}

func (s *Server) restoreNewVM(ctx context.Context, clusterID string, src appdb.Workload, art appdb.BackupArtifact, isolated bool, dest *appdb.Node) (string, error) {
	if dest == nil {
		node, err := s.Store.GetNode(ctx, clusterID)
		if err != nil || node == nil {
			return "", errUnprocessable("local node is not enrolled")
		}
		dest = node
	}
	local := s.applyLocal(ctx, clusterID, dest.ID)
	if local && (s.VM == nil || s.Backup == nil || s.Storage == nil) {
		return "", errUnavailable("backup agent is unavailable")
	}
	vol, pool, _, err := s.bootVolumeLocator(ctx, clusterID, src)
	if err != nil {
		return "", err
	}
	if err := refuseQemuImgCopyDest(pool.BackendType); err != nil {
		return "", err
	}
	spec, specErr := vmspec.Parse(src.SpecJSON)
	if specErr != nil {
		spec = vmspec.Spec{Name: src.Name, CPUs: src.CPUs, MemoryBytes: src.MemoryBytes, Firmware: src.Firmware}
	}
	for _, d := range spec.Disks {
		if d.Role == vmspec.DiskRoleData && d.VolumeID != "" && d.VolumeID != vol.ID {
			return "", errUnprocessable("restore of additional data disks is not implemented")
		}
	}
	newID := uuid.NewString()
	newVolID := uuid.NewString()
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	if local {
		res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
			VolumeID: newVolID, PoolID: pool.ID, RootPath: pool.RootPath,
			Class: storage.ClassVMDisk, Size: vol.SizeBytes, Format: firstNonEmpty(vol.Format, storage.FormatQCOW2),
		}, hint)
		if err != nil && !strings.Contains(err.Error(), "duplicate") {
			return "", err
		}
		backend := res.Handle.BackendRef
		if backend == "" {
			backend = path.Join("volumes", storage.ClassVMDisk, newVolID+".qcow2")
		}
		newVol := appdb.Volume{
			ID: newVolID, ClusterID: clusterID, NodeID: dest.ID, PoolID: pool.ID,
			Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: firstNonEmpty(vol.Format, storage.FormatQCOW2),
			SizeBytes: vol.SizeBytes, Status: storage.StatusAvailable, BackendType: storage.BackendDirectory, BackendRef: backend,
		}
		if err := s.Store.CreateVolume(ctx, newVol); err != nil {
			return "", err
		}
		diskPath, err := storage.JoinUnder(pool.RootPath, newVol.BackendRef)
		if err != nil {
			return "", errConflict("volume locator is invalid")
		}
		srcPath, cleanup, err := s.materializeArtifact(ctx, clusterID, art)
		if err != nil {
			return "", err
		}
		defer cleanup()
		if _, err := s.Backup.CopyBackup(ctx, qemu.BackupReplace, srcPath, diskPath); err != nil {
			return "", err
		}
	} else {
		newVol := appdb.Volume{
			ID: newVolID, ClusterID: clusterID, NodeID: dest.ID, PoolID: pool.ID,
			Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: firstNonEmpty(vol.Format, storage.FormatQCOW2),
			SizeBytes: vol.SizeBytes, Status: storage.StatusUnavailable, BackendType: storage.BackendDirectory,
			BackendRef: path.Join("volumes", storage.ClassVMDisk, newVolID+".qcow2"),
		}
		if err := s.Store.CreateVolume(ctx, newVol); err != nil {
			return "", err
		}
	}
	spec.Name = uniqueRestoredName(spec.Name, newID)
	spec.CloudImageID = ""
	if isolated {
		spec.Name = "verify-" + newID[:8]
		spec.USBs = nil
		spec.PCIHosts = nil
		isoID, err := s.isolatedNetworkID(ctx, clusterID)
		if err != nil {
			return "", err
		}
		spec.NICs = []vmspec.NIC{{Model: "virtio", NetworkID: isoID}}
	}
	for i := range spec.Disks {
		if spec.Disks[i].Role == vmspec.DiskRoleBoot || spec.Disks[i].VolumeID == vol.ID {
			spec.Disks[i].VolumeID = newVolID
		}
	}
	if len(spec.Disks) == 0 {
		spec.Disks = []vmspec.Disk{{Role: vmspec.DiskRoleBoot, VolumeID: newVolID, Format: "qcow2"}}
	}
	for i := range spec.NICs {
		spec.NICs[i].MAC = ""
		spec.NICs[i].ID = ""
	}
	spec = vmspec.PersistNICs(newID, spec)
	spec, _, err = vmspec.AllocatePCI(spec)
	if err != nil {
		return "", err
	}
	row := appdb.Workload{
		ID: newID, ClusterID: clusterID, NodeID: dest.ID, OwnerNodeID: dest.ID, DesiredNodeID: dest.ID,
		Name: spec.Name, Kind: vmspec.KindVM, Status: qemu.StatusStopped, DesiredPower: "running",
		CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes, SpecJSON: vmspec.MustJSON(spec),
		Autostart: spec.Autostart, Firmware: spec.Firmware,
		MigrateBlockers: json.RawMessage(`[]`),
	}
	if !local {
		row.Status = "unavailable"
		row.Reason = "cross-node restore recorded; dest agent is not connected"
	}
	if err := s.Store.CreateWorkload(ctx, row); err != nil {
		return "", err
	}
	if err := s.Store.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID, VolumeID: newVolID,
		Role: vmspec.DiskRoleBoot, Slot: 0, Format: firstNonEmpty(vol.Format, storage.FormatQCOW2),
	}); err != nil {
		return "", errInternal("could not record VM disk")
	}
	nics, _ := s.Store.ListWorkloadNICs(ctx, clusterID, src.ID)
	if isolated {
		isoID, err := s.isolatedNetworkID(ctx, clusterID)
		if err != nil {
			return "", err
		}
		if err := s.Store.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{
			ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID,
			NetworkID: isoID, Model: "virtio",
		}); err != nil {
			return "", errInternal("could not record VM NIC")
		}
	} else {
		for _, n := range nics {
			if err := s.Store.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{
				ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID,
				NetworkID: n.NetworkID, Model: n.Model,
			}); err != nil {
				return "", errInternal("could not record VM NIC")
			}
		}
	}
	if !local {
		return newID, nil
	}
	if _, err := s.reprepareVM(ctx, clusterID, row); err != nil {
		return "", err
	}
	if _, err := s.VM.LifecycleVM(ctx, newID, "start", spec.Autostart); err != nil {
		return "", err
	}
	_ = s.Store.UpdateWorkloadObserved(ctx, appdb.Workload{ID: newID, Status: qemu.StatusRunning})
	return newID, nil
}

func (s *Server) restoreReplaceVM(ctx context.Context, clusterID string, src appdb.Workload, art appdb.BackupArtifact) error {
	if s.VM == nil || s.Backup == nil {
		return errUnavailable("backup agent is unavailable")
	}
	vol, _, tip, err := s.bootVolumeLocator(ctx, clusterID, src)
	if err != nil {
		return err
	}
	spec, specErr := vmspec.Parse(src.SpecJSON)
	if specErr != nil {
		spec = vmspec.Spec{Name: src.Name, CPUs: src.CPUs, MemoryBytes: src.MemoryBytes, Firmware: src.Firmware}
	}
	for _, d := range spec.Disks {
		if d.Role == vmspec.DiskRoleData && d.VolumeID != "" && vol != nil && d.VolumeID != vol.ID {
			return errUnprocessable("restore of additional data disks is not implemented")
		}
	}
	if _, err := s.VM.LifecycleVM(ctx, src.ID, "stop", false); err != nil {
		return err
	}
	srcPath, cleanup, err := s.materializeArtifact(ctx, clusterID, art)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := s.Backup.CopyBackup(ctx, qemu.BackupReplace, srcPath, tip); err != nil {
		return err
	}
	if _, err := s.reprepareVM(ctx, clusterID, src); err != nil {
		return err
	}
	if src.DesiredPower == "running" {
		if _, err := s.VM.LifecycleVM(ctx, src.ID, "start", src.Autostart); err != nil {
			return err
		}
	}
	return nil
}

func uniqueRestoredName(base, id string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "restored"
	}
	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	return base + "-restore-" + short
}

func (s *Server) restoreOrphanVM(ctx context.Context, clusterID string, art appdb.BackupArtifact, dest *appdb.Node) (string, error) {
	if dest == nil {
		node, err := s.Store.GetNode(ctx, clusterID)
		if err != nil || node == nil {
			return "", errUnprocessable("local node is not enrolled")
		}
		dest = node
	}
	local := s.applyLocal(ctx, clusterID, dest.ID)
	if local && (s.VM == nil || s.Backup == nil || s.Storage == nil) {
		return "", errUnavailable("backup agent is unavailable")
	}
	pools, err := s.Store.ListStoragePools(ctx, clusterID)
	if err != nil || len(pools) == 0 {
		return "", errUnprocessable("no storage pool is available for restore")
	}
	pool := pools[0]
	if pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning {
		return "", errConflict("storage pool is unavailable")
	}
	if err := refuseQemuImgCopyDest(pool.BackendType); err != nil {
		return "", err
	}
	nets, err := s.Store.ListNetworks(ctx, clusterID)
	if err != nil || len(nets) == 0 {
		return "", errUnprocessable("no network is available for restore")
	}
	if _, _, err := s.resolveWorkloadNetwork(ctx, clusterID, nets[0].ID); err != nil {
		return "", err
	}
	newID := uuid.NewString()
	newVolID := uuid.NewString()
	backend := path.Join("volumes", storage.ClassVMDisk, newVolID+".qcow2")
	volStatus := storage.StatusUnavailable
	if local {
		hint := appdb.PoolHints([]appdb.StoragePool{pool})[0]
		res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
			VolumeID: newVolID, PoolID: pool.ID, RootPath: pool.RootPath,
			Class: storage.ClassVMDisk, Size: vmspec.DefaultDiskBytes, Format: storage.FormatQCOW2,
		}, hint)
		if err != nil && !strings.Contains(err.Error(), "duplicate") {
			return "", err
		}
		if res.Handle.BackendRef != "" {
			backend = res.Handle.BackendRef
		}
		diskPath, err := storage.JoinUnder(pool.RootPath, backend)
		if err != nil {
			return "", errConflict("volume locator is invalid")
		}
		srcPath, cleanup, err := s.materializeArtifact(ctx, clusterID, art)
		if err != nil {
			return "", err
		}
		defer cleanup()
		if _, err := s.Backup.CopyBackup(ctx, qemu.BackupReplace, srcPath, diskPath); err != nil {
			return "", err
		}
		volStatus = storage.StatusAvailable
	}
	newVol := appdb.Volume{
		ID: newVolID, ClusterID: clusterID, NodeID: dest.ID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: storage.KindBlock, Format: storage.FormatQCOW2,
		SizeBytes: vmspec.DefaultDiskBytes, Status: volStatus, BackendType: storage.BackendDirectory, BackendRef: backend,
	}
	if err := s.Store.CreateVolume(ctx, newVol); err != nil {
		return "", err
	}
	spec := vmspec.Spec{
		Name: uniqueRestoredName("restored", newID), CPUs: vmspec.DefaultCPUs, MemoryBytes: vmspec.DefaultMemory,
		Firmware: vmspec.FirmwareBIOS,
		Disks:    []vmspec.Disk{{Role: vmspec.DiskRoleBoot, VolumeID: newVolID, Format: "qcow2"}},
		NICs:     []vmspec.NIC{{NetworkID: nets[0].ID}},
	}
	spec = vmspec.PersistNICs(newID, spec)
	spec, _, err = vmspec.AllocatePCI(spec)
	if err != nil {
		return "", err
	}
	row := appdb.Workload{
		ID: newID, ClusterID: clusterID, NodeID: dest.ID, OwnerNodeID: dest.ID, DesiredNodeID: dest.ID,
		Name: spec.Name, Kind: vmspec.KindVM, Status: qemu.StatusStopped, DesiredPower: "running",
		CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes, SpecJSON: vmspec.MustJSON(spec), Firmware: spec.Firmware,
		MigrateBlockers: json.RawMessage(`[]`),
	}
	if !local {
		row.Status = "unavailable"
		row.Reason = "cross-node restore recorded; dest agent is not connected"
	}
	if err := s.Store.CreateWorkload(ctx, row); err != nil {
		return "", err
	}
	if err := s.Store.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID, VolumeID: newVolID,
		Role: vmspec.DiskRoleBoot, Format: storage.FormatQCOW2,
	}); err != nil {
		return "", errInternal("could not record VM disk")
	}
	if err := s.Store.CreateWorkloadNIC(ctx, appdb.WorkloadNIC{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: newID, NetworkID: nets[0].ID, Model: vmspec.NICModelVirtio,
	}); err != nil {
		return "", errInternal("could not record VM NIC")
	}
	if !local {
		return newID, nil
	}
	if _, err := s.reprepareVM(ctx, clusterID, row); err != nil {
		return "", err
	}
	if _, err := s.VM.LifecycleVM(ctx, newID, "start", false); err != nil {
		return "", err
	}
	_ = s.Store.UpdateWorkloadObserved(ctx, appdb.Workload{ID: newID, Status: qemu.StatusRunning})
	return newID, nil
}
