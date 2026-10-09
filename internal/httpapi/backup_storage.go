package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/backuppack"
	"github.com/no-dal/ndl-ce/internal/objstore"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// storageEvent is one retention, cleanup or maintenance outcome kept for the
// storage view, so a failed cleanup is visible instead of silently ignored.
type storageEvent struct {
	At         time.Time `json:"at"`
	Kind       string    `json:"kind"`
	OK         bool      `json:"ok"`
	WorkloadID string    `json:"workload_id,omitempty"`
	Message    string    `json:"message"`
}

const maxStorageEvents = 200

func (s *Server) recordStorageEvent(kind string, ok bool, workloadID, msg string) {
	if !ok {
		log.Printf("backup storage: %s failed: %s", kind, msg)
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	s.storageEvents = append(s.storageEvents, storageEvent{
		At: s.now(), Kind: kind, OK: ok, WorkloadID: workloadID, Message: msg,
	})
	if n := len(s.storageEvents); n > maxStorageEvents {
		s.storageEvents = append([]storageEvent(nil), s.storageEvents[n-maxStorageEvents:]...)
	}
}

func (s *Server) recentStorageEvents() []storageEvent {
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	out := append([]storageEvent(nil), s.storageEvents...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// retainBackupIDs applies the policy's daily/weekly/monthly rotation to
// artifacts sorted newest first. It shares the engine's rules: a bucket
// already covered by a kept artifact is not kept twice, and the newest
// artifact is always kept.
func retainBackupIDs(arts []appdb.BackupArtifact, keepDaily, keepWeekly, keepMonthly int) map[string]struct{} {
	sorted := append([]appdb.BackupArtifact(nil), arts...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.After(sorted[j].CreatedAt) })
	times := make([]time.Time, len(sorted))
	for i, a := range sorted {
		times[i] = a.CreatedAt.UTC()
	}
	keep := map[string]struct{}{}
	for i := range backup.RetainNewest(times, keepDaily, keepWeekly, keepMonthly) {
		keep[sorted[i].ID] = struct{}{}
	}
	return keep
}

// pruneResult summarizes one retention pass.
type pruneResult struct {
	Expired int
	Failed  int
	V2      int
}

// pruneBackupArtifacts enforces the policy's retention for one workload and
// target. It runs after every attempt, successful or not: retention only
// ever removes points beyond the policy and always keeps the newest, so a run
// of failing backups can no longer leave a full repository full forever.
//
// An artifact row is removed only after its data was deleted. When deletion
// fails the row stays, the failure is recorded, and the next pass retries;
// previously the row was dropped regardless, which stranded the data where
// nothing would ever find it again.
func (s *Server) pruneBackupArtifacts(ctx context.Context, clusterID, workloadID, targetID string, pol appdb.BackupPolicy) pruneResult {
	var res pruneResult
	arts, err := s.Store.ListBackupArtifactsForWorkload(ctx, clusterID, workloadID, targetID)
	if err != nil {
		s.recordStorageEvent("retention", false, workloadID, "could not list backups: "+err.Error())
		return res
	}
	// Protected backups are neither removed nor counted by the rotation.
	protected, _ := s.Store.ListProtectedBackupArtifacts(ctx, clusterID)
	var rotating []appdb.BackupArtifact
	for _, a := range arts {
		if !protected[a.ID] {
			rotating = append(rotating, a)
		}
	}
	keep := retainBackupIDs(rotating, pol.KeepDaily, pol.KeepWeekly, pol.KeepMonthly)
	tgt, _ := s.Store.GetBackupTarget(ctx, clusterID, targetID)
	var keptV2 []appdb.BackupArtifact
	for _, a := range rotating {
		if _, ok := keep[a.ID]; ok {
			if a.Format == backup.Format && a.BackupID != "" {
				keptV2 = append(keptV2, a)
			}
			continue
		}
		if err := s.deleteArtifactData(ctx, clusterID, a, tgt); err != nil {
			res.Failed++
			s.recordStorageEvent("retention", false, workloadID,
				fmt.Sprintf("backup %s from %s was not removed and will be retried: %v", a.ID, a.CreatedAt.UTC().Format(time.RFC3339), err))
			continue
		}
		if err := s.Store.DeleteBackupArtifact(ctx, clusterID, a.ID); err != nil {
			res.Failed++
			s.recordStorageEvent("retention", false, workloadID, "backup data was removed but its record was not: "+err.Error())
			continue
		}
		res.Expired++
		if a.Format == backup.Format {
			res.V2++
		}
	}
	if res.Expired > 0 {
		s.recordStorageEvent("retention", true, workloadID, fmt.Sprintf("removed %d expired backup(s)", res.Expired))
	}
	if tgt != nil && isObjectBackupKind(tgt.Kind) {
		off, _ := s.Store.GetBackupPolicyOffsite(ctx, clusterID, pol.ID)
		if off.Enabled() {
			s.pruneOffsite(ctx, clusterID, workloadID, *tgt, keptV2, off, &res)
		}
	}
	return res
}

// pruneOffsite removes the remote copy of locally kept restore points that
// fall outside the policy's offsite retention. The local copy stays, so a
// short offsite retention keeps the remote bucket small without losing local
// restore points.
func (s *Server) pruneOffsite(ctx context.Context, clusterID, workloadID string, tgt appdb.BackupTarget, kept []appdb.BackupArtifact, off appdb.BackupOffsite, res *pruneResult) {
	keepRemote := retainBackupIDs(kept, off.KeepDaily, off.KeepWeekly, off.KeepMonthly)
	removed := 0
	for _, a := range kept {
		if _, ok := keepRemote[a.ID]; ok || !hasRemoteCopy(a.RemoteState) {
			continue
		}
		req := backuphost.Request{
			Action: backuphost.ActionExpire, Namespace: a.Namespace, BackupID: a.BackupID,
			RemoteOnly: true, Target: s.v2TargetSpec(ctx, tgt),
		}
		raw, _ := json.Marshal(req)
		if _, err := s.Backup.CopyBackup(ctx, qemu.BackupV2Expire, "", string(raw)); err != nil {
			res.Failed++
			s.recordStorageEvent("offsite-retention", false, workloadID, fmt.Sprintf("remote copy of backup %s was not removed and will be retried: %v", a.ID, err))
			continue
		}
		a.RemoteState = string(backup.RemoteNone)
		_ = s.Store.UpdateBackupArtifact(ctx, a)
		removed++
		res.V2++
	}
	if removed > 0 {
		s.recordStorageEvent("offsite-retention", true, workloadID, fmt.Sprintf("removed the remote copy of %d backup(s); local copies are kept", removed))
	}
}

func hasRemoteCopy(state string) bool {
	switch backup.RemoteState(state) {
	case backup.RemoteQueued, backup.RemoteUploading, backup.RemoteVerifying, backup.RemoteProtected, backup.RemoteFailed:
		return true
	}
	return false
}

// deleteArtifactData removes one artifact's stored data.
func (s *Server) deleteArtifactData(ctx context.Context, clusterID string, a appdb.BackupArtifact, tgt *appdb.BackupTarget) error {
	if s.Backup == nil {
		return errUnavailable("backup agent is unavailable")
	}
	if a.Format == backup.Format {
		if a.BackupID == "" || a.Namespace == "" {
			return nil // nothing was ever written for it
		}
		req := backuphost.Request{
			Action: backuphost.ActionExpire, Namespace: a.Namespace, BackupID: a.BackupID, DeferGC: true,
		}
		if tgt != nil {
			// The engine removes the remote manifest and queues the remote
			// packs for a sweep; legacy ObjectKey deletion does not apply.
			req.Target = s.v2TargetSpec(ctx, *tgt)
		}
		raw, _ := json.Marshal(req)
		_, err := s.Backup.CopyBackup(ctx, qemu.BackupV2Expire, "", string(raw))
		return err
	}
	if a.ObjectKey != "" {
		if tgt == nil {
			return fmt.Errorf("the backup target no longer exists, so the remote copy cannot be removed")
		}
		pass, enc, err := s.Store.BackupCredentials(ctx, clusterID, tgt.ID)
		if err != nil {
			return fmt.Errorf("backup target credentials are unavailable: %w", err)
		}
		action := objstore.ActionDel
		if isPackArtifact(a) {
			action = objstore.ActionDelPack
		}
		req := objstore.Request{
			Action: action, Provider: tgt.Kind, Endpoint: tgt.Endpoint, Region: tgt.Region,
			Bucket: tgt.Bucket, Key: a.ObjectKey, AccessKeyID: tgt.Username, SecretAccessKey: pass,
		}
		// Deleting does not need the encryption key; a target without one
		// must still be able to expire its objects.
		if key, err := objstore.ParseKey(enc); err == nil {
			req.EncryptionKey = key
		}
		out, err := s.objectRPC().ObjectBackup(ctx, req)
		if err != nil {
			return err
		}
		if strings.EqualFold(out.Status, "unavailable") || strings.EqualFold(out.Status, appdb.BackupUnavailable) {
			return fmt.Errorf("%s", firstNonEmpty(out.Reason, "object delete is unavailable"))
		}
		return nil
	}
	if strings.HasPrefix(a.Locator, "s3://") {
		return nil
	}
	action := qemu.BackupDelete
	if a.Format == backuppack.FormatNDLB {
		action = qemu.BackupRmTree
	}
	_, err := s.Backup.CopyBackup(ctx, action, "", a.Locator)
	return err
}

// afterBackupAttempt enforces retention for one workload after a policy run
// touched it, whatever the outcome of the run.
func (s *Server) afterBackupAttempt(ctx context.Context, clusterID, workloadID, targetID, policyID string) pruneResult {
	if policyID == "" {
		return pruneResult{}
	}
	pol, _ := s.Store.GetBackupPolicy(ctx, clusterID, policyID)
	if pol == nil {
		return pruneResult{}
	}
	return s.pruneBackupArtifacts(ctx, clusterID, workloadID, targetID, *pol)
}

// runBackupMaintenance asks the agent to collect garbage, compact the
// repository and sweep remote packs left by expired restore points.
func (s *Server) runBackupMaintenance(ctx context.Context, reason string) (*backuphost.GCReport, error) {
	if s.Backup == nil {
		return nil, errUnavailable("backup agent is unavailable")
	}
	raw, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionGC, Reason: reason})
	res, err := s.Backup.CopyBackup(ctx, qemu.BackupV2GC, "", string(raw))
	if err != nil {
		s.recordStorageEvent("maintenance", false, "", err.Error())
		return nil, err
	}
	var out backuphost.Result
	if res.Extra != "" {
		_ = json.Unmarshal([]byte(res.Extra), &out)
	}
	rep := out.GC
	if rep == nil {
		return nil, nil
	}
	switch {
	case rep.Error != "":
		s.recordStorageEvent("maintenance", false, "", rep.Error)
	case len(rep.RemoteErrs) > 0:
		for k, v := range rep.RemoteErrs {
			s.recordStorageEvent("remote-cleanup", false, "", k+": "+v)
		}
	default:
		freed := rep.Local.BytesFreed
		for _, r := range rep.Remote {
			freed += r.BytesFreed
		}
		s.recordStorageEvent("maintenance", true, "", fmt.Sprintf("reclaimed %s", humanBytes(freed)))
	}
	return rep, nil
}

func humanBytes(n int64) string {
	const gib = int64(1) << 30
	switch {
	case n >= 1<<40:
		return fmt.Sprintf("%.1f TiB", float64(n)/float64(int64(1)<<40))
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/float64(1<<20))
	}
	return fmt.Sprintf("%d B", n)
}

// Scope detail stored with an artifact is capped. Smart capture can resolve
// to well over a hundred thousand paths; storing every one in each row made
// single rows about 100 MB, filled PostgreSQL on the root disk and turned the
// artifact list into a gigabyte response. The manifest keeps the full scope.
const maxStoredScopeEntries = 200

// compactV2Result trims the per-path scope detail of a capture result.
func compactV2Result(out backuphost.Result) (backuphost.Result, map[string]int) {
	cut := map[string]int{}
	if n := len(out.Preview.Items); n > maxStoredScopeEntries {
		items := append(out.Preview.Items[:0:0], out.Preview.Items...)
		sort.SliceStable(items, func(i, j int) bool { return items[i].Bytes > items[j].Bytes })
		out.Preview.Items = items[:maxStoredScopeEntries]
		cut["items"] = n
	}
	for i := range out.Preview.Items {
		if n := len(out.Preview.Items[i].Paths); n > 20 {
			out.Preview.Items[i].Paths = out.Preview.Items[i].Paths[:20]
		}
	}
	trim := func(name string, list []string) []string {
		if len(list) <= maxStoredScopeEntries {
			return list
		}
		cut[name] = len(list)
		return list[:maxStoredScopeEntries]
	}
	out.Preview.Includes = trim("preview_includes", out.Preview.Includes)
	out.Preview.Excludes = trim("preview_excludes", out.Preview.Excludes)
	out.Blueprint.Includes = trim("includes", out.Blueprint.Includes)
	out.Blueprint.Excludes = trim("excludes", out.Blueprint.Excludes)
	out.Points = nil
	return out, cut
}

// v2StatsJSON is the stored form of a capture result.
func v2StatsJSON(out backuphost.Result) (stats, blueprint string) {
	slim, cut := compactV2Result(out)
	raw, _ := json.Marshal(slim)
	if len(cut) > 0 {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			m["scope_truncated"] = cut
			raw, _ = json.Marshal(m)
		}
	}
	bp, _ := json.Marshal(slim.Blueprint)
	return string(raw), string(bp)
}

// CompactStoredBackupStats rewrites artifact rows whose stored scope detail
// is oversized. It runs once at control start, one row at a time, and only
// touches derived statistics, never backup data or restore metadata.
func (s *Server) CompactStoredBackupStats(ctx context.Context) int {
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil {
		return 0
	}
	n, err := s.Store.CompactBackupArtifactStats(ctx, cluster.ID, 1<<20, compactStoredStats)
	if err != nil {
		log.Printf("backup storage: compacting stored backup statistics: %v", err)
	}
	return n
}

func compactStoredStats(stats, blueprint string) (string, string) {
	var out backuphost.Result
	if json.Unmarshal([]byte(stats), &out) != nil {
		return stats, blueprint
	}
	if blueprint != "" {
		_ = json.Unmarshal([]byte(blueprint), &out.Blueprint)
	}
	return v2StatsJSON(out)
}

// storageReport is everything the storage view needs in one response.
type storageReport struct {
	Workspace   map[string]any   `json:"workspace"`
	Pools       []map[string]any `json:"pools"`
	Warnings    []string         `json:"warnings"`
	Events      []storageEvent   `json:"events"`
	Retention   []map[string]any `json:"retention"`
	Unmanaged   []map[string]any `json:"unmanaged_restore_points"`
	OrphanedArt []map[string]any `json:"deleted_workload_backups"`
}

// getBackupStorage reports where backup and workload storage goes, what
// retention is keeping, and anything that needs an operator.
func (s *Server) getBackupStorage(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	ctx := r.Context()
	clusterID := p.User.ClusterID
	settings, _ := s.Store.GetBackupWorkspaceSettings(ctx, clusterID)
	if settings == nil {
		settings = appdb.DefaultBackupWorkspaceSettings(clusterID)
	}
	st, stErr := s.v2Status(ctx, clusterID, *settings)
	rep := storageReport{Workspace: workspaceJSON(*settings, st), Events: s.recentStorageEvents()}
	if stErr != nil {
		rep.Warnings = append(rep.Warnings, "The backup agent did not answer: "+stErr.Error())
	}
	rep.Warnings = append(rep.Warnings, workspaceWarnings(*settings, st)...)

	pools, _ := s.Store.ListStoragePools(ctx, clusterID)
	for _, pool := range pools {
		item := map[string]any{
			"id": pool.ID, "name": pool.Name, "backend_type": pool.BackendType, "status": pool.Status,
			"root_path": pool.RootPath, "warnings": pool.Warnings,
			"total_bytes": pool.TotalBytes, "usable_bytes": pool.UsableBytes,
			"allocated_bytes": pool.AllocatedBytes, "provisioned_bytes": pool.ProvisionedBytes,
			"root_filesystem": poolOnRootFS(pool),
		}
		rep.Pools = append(rep.Pools, item)
	}
	if def := preferredDefaultPool(pools); def != nil && poolOnRootFS(*def) {
		rep.Warnings = append(rep.Warnings, "New workloads are placed on the "+def.Name+" pool, which shares the host root disk. Add a storage pool on a separate disk.")
	}

	arts, _ := s.Store.ListBackupArtifacts(ctx, clusterID)
	workloads, _ := s.Store.ListWorkloads(ctx, clusterID)
	alive := map[string]string{}
	for _, wl := range workloads {
		alive[wl.ID] = wl.Name
	}
	known := map[string]bool{}
	type agg struct {
		count   int
		logical int64
		newest  time.Time
	}
	byWorkload := map[string]*agg{}
	for _, a := range arts {
		if a.BackupID != "" {
			known[a.BackupID] = true
		}
		g := byWorkload[a.WorkloadID]
		if g == nil {
			g = &agg{}
			byWorkload[a.WorkloadID] = g
		}
		g.count++
		g.logical += firstInt64(a.LogicalBytes, a.SizeBytes)
		if a.CreatedAt.After(g.newest) {
			g.newest = a.CreatedAt
		}
	}
	for id, g := range byWorkload {
		row := map[string]any{
			"workload_id": id, "workload_name": alive[id], "artifacts": g.count,
			"logical_bytes": g.logical, "newest": g.newest.UTC().Format(time.RFC3339),
		}
		if _, ok := alive[id]; ok {
			rep.Retention = append(rep.Retention, row)
		} else {
			rep.OrphanedArt = append(rep.OrphanedArt, row)
		}
	}
	sort.Slice(rep.Retention, func(i, j int) bool {
		return rep.Retention[i]["logical_bytes"].(int64) > rep.Retention[j]["logical_bytes"].(int64)
	})
	if len(rep.OrphanedArt) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d deleted workload(s) still have backups. Retention no longer runs for them; delete them from Backups when they are no longer needed.", len(rep.OrphanedArt)))
	}
	for _, pt := range st.Points {
		if known[pt.BackupID] {
			continue
		}
		rep.Unmanaged = append(rep.Unmanaged, map[string]any{
			"backup_id": pt.BackupID, "namespace": pt.Namespace, "workload_id": pt.WorkloadID,
			"workload_name": pt.WorkloadName, "created_at_ns": pt.CreatedAtNS,
			"logical_bytes": pt.LogicalBytes, "recovered": pt.Recovered,
		})
	}
	if len(rep.Unmanaged) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d restore point(s) in the local repository have no backup record, so retention cannot remove them.", len(rep.Unmanaged)))
	}
	writeJSON(w, http.StatusOK, rep)
}

// workspaceWarnings flags backup configuration that can fill the host.
func workspaceWarnings(settings appdb.BackupWorkspaceSettings, st backuphost.Result) []string {
	var out []string
	ws := st.Workspace
	if ws.KeyNote != "" {
		out = append(out, ws.KeyNote)
	}
	if ws.HostTotalBytes > 0 && settings.MaxLocalBytes > ws.HostTotalBytes/4 {
		out = append(out, fmt.Sprintf("The local backup repository may grow to %s, more than a quarter of the %s disk it is on. Lower the limit or move the repository to a larger pool.", humanBytes(settings.MaxLocalBytes), humanBytes(ws.HostTotalBytes)))
	}
	if settings.MaxLocalBytes > 0 && st.RepoBytes > settings.MaxLocalBytes*9/10 {
		out = append(out, fmt.Sprintf("The local backup repository uses %s of its %s limit. New backups stop at the limit.", humanBytes(st.RepoBytes), humanBytes(settings.MaxLocalBytes)))
	}
	if ws.HostFreeBytes > 0 && ws.EffectiveReserveBytes > 0 && ws.HostFreeBytes < ws.EffectiveReserveBytes*2 {
		out = append(out, fmt.Sprintf("Only %s is free where backups are stored; backups stop when %s is left.", humanBytes(ws.HostFreeBytes), humanBytes(ws.EffectiveReserveBytes)))
	}
	if ws.FailedUploads > 0 {
		out = append(out, fmt.Sprintf("%d upload job(s) gave up after repeated errors; they retry when the agent restarts or the backup is enqueued again.", ws.FailedUploads))
	}
	if gc := ws.LastGC; gc != nil {
		if gc.Error != "" {
			out = append(out, "Backup cleanup failed: "+gc.Error)
		}
		if gc.Local.RepackSkipped != "" {
			out = append(out, "Backup compaction was skipped: "+gc.Local.RepackSkipped)
		}
		for _, r := range gc.Remote {
			if r.ForeignPoints > 0 {
				out = append(out, fmt.Sprintf("%d remote restore point(s) were written with a different repository key and cannot be read or cleaned up automatically.", r.ForeignPoints))
				break
			}
		}
	}
	return out
}

// runBackupMaintenanceAPI runs repository maintenance now.
func (s *Server) runBackupMaintenanceAPI(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.BackupCreate); err != nil {
		return
	}
	rep, err := s.runBackupMaintenance(r.Context(), "operator")
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gc": rep})
}

// getBackupTargetUsage measures what a target holds. It lists and reads the
// target but never deletes anything.
func (s *Server) getBackupTargetUsage(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	tgt, err := s.Store.GetBackupTarget(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || tgt == nil {
		writeErr(w, http.StatusNotFound, "backup target not found")
		return
	}
	if s.Backup == nil {
		writeErr(w, http.StatusBadGateway, "backup agent is unavailable")
		return
	}
	raw, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionRemoteUsage, Target: s.v2TargetSpec(r.Context(), *tgt)})
	res, err := s.Backup.CopyBackup(r.Context(), qemu.BackupV2RemoteUsage, "", string(raw))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	var out backuphost.Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	writeJSON(w, http.StatusOK, map[string]any{"target_id": tgt.ID, "usage": out.RemoteUsage})
}

// relocateBackupRepository points the agent at a new, empty repository
// directory, such as a folder on a large storage pool. Backup data is never
// moved or deleted by this call.
func (s *Server) relocateBackupRepository(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	var req relocateRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "choose a storage pool or a path")
		return
	}
	if s.Backup == nil {
		writeErr(w, http.StatusBadGateway, "backup agent is unavailable")
		return
	}
	path, err := s.resolveRepoPath(r.Context(), p.User.ClusterID, req)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	raw, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionRelocate, Path: path})
	res, err := s.Backup.CopyBackup(r.Context(), qemu.BackupV2Relocate, "", string(raw))
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.recordStorageEvent("relocate", true, "", "backup repository is now at "+res.Dest)
	writeJSON(w, http.StatusOK, map[string]any{"root": res.Dest})
}

// staleTempPrefixes are the temporary staging directories control creates for
// backups to object storage and for restores. A VM backup stages a full disk
// copy there, so one left behind by a crash can be tens of gigabytes.
var staleTempPrefixes = []string{"ndl-backup-", "ndl-restore-"}

// SweepStaleTempDirs removes staging directories a previous control process
// left in dir when it crashed or restarted mid-operation. It runs at start-up,
// before this process creates any, and skips anything modified in the last
// ten minutes as a margin.
func SweepStaleTempDirs(dir string) (removed int, bytes int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	cutoff := time.Now().Add(-10 * time.Minute)
	for _, e := range entries {
		if !e.IsDir() || !hasAnyPrefix(e.Name(), staleTempPrefixes) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		size := dirSize(p)
		if os.RemoveAll(p) == nil {
			removed++
			bytes += size
		}
	}
	return removed, bytes
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func dirSize(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

const (
	maintenanceEvery = 23 * time.Hour
	verifyEvery      = 7 * 24 * time.Hour
)

// runScheduledBackupJobs runs repository maintenance daily and verification
// weekly, independently of whether any backup policy ran or succeeded.
func (s *Server) runScheduledBackupJobs(ctx context.Context, clusterID string) {
	if s.Backup == nil {
		return
	}
	now := s.now()
	s.storageMu.Lock()
	dueGC := now.Sub(s.lastMaintenance) >= maintenanceEvery
	dueVerify := now.Sub(s.lastVerify) >= verifyEvery
	if dueGC {
		s.lastMaintenance = now
	}
	if dueVerify {
		s.lastVerify = now
	}
	s.storageMu.Unlock()
	if dueGC {
		_, _ = s.runBackupMaintenance(ctx, "daily")
	}
	if dueVerify {
		_, _ = s.runBackupVerify(ctx, clusterID)
	}
}

// runBackupVerify checks every local restore point and records the result on
// its backup.
func (s *Server) runBackupVerify(ctx context.Context, clusterID string) (int, error) {
	raw, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionVerify})
	res, err := s.Backup.CopyBackup(ctx, qemu.BackupV2Verify, "", string(raw))
	if err != nil {
		s.recordStorageEvent("verify", false, "", err.Error())
		return 0, err
	}
	var out backuphost.Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	arts, _ := s.Store.ListBackupArtifacts(ctx, clusterID)
	byBackup := map[string]appdb.BackupArtifact{}
	for _, a := range arts {
		if a.BackupID != "" {
			byBackup[a.BackupID] = a
		}
	}
	failed := 0
	now := s.now()
	for _, v := range out.Verify {
		a, ok := byBackup[v.BackupID]
		if !ok {
			continue
		}
		a.LastTestedAt = &now
		if v.OK {
			a.VerifyStatus, a.VerifyError = appdb.BackupVerified, ""
		} else {
			a.VerifyStatus, a.VerifyError = "failed", v.Error
			failed++
			s.recordStorageEvent("verify", false, a.WorkloadID, fmt.Sprintf("backup %s failed verification: %s", a.ID, v.Error))
		}
		_ = s.Store.UpdateBackupArtifactVerify(ctx, a)
	}
	if failed == 0 {
		s.recordStorageEvent("verify", true, "", fmt.Sprintf("verified %d restore point(s)", len(out.Verify)))
	}
	return len(out.Verify), nil
}

// runBackupVerifyAPI verifies every local restore point now.
func (s *Server) runBackupVerifyAPI(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	if s.Backup == nil {
		writeErr(w, http.StatusBadGateway, "backup agent is unavailable")
		return
	}
	n, err := s.runBackupVerify(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"verified": n})
}
