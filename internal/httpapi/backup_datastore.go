package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// backupLocation is one place backups can be stored on this host: a folder
// on a storage pool.
type backupLocation struct {
	PoolID         string `json:"pool_id"`
	Name           string `json:"name"`
	BackendType    string `json:"backend_type"`
	TargetPath     string `json:"target_path"`
	RepoPath       string `json:"repo_path"`
	TotalBytes     *int64 `json:"total_bytes"`
	UsableBytes    *int64 `json:"usable_bytes"`
	RootFilesystem bool   `json:"root_filesystem"`
	Recommended    bool   `json:"recommended"`
	Usable         bool   `json:"usable"`
	Reason         string `json:"reason,omitempty"`
}

// backupLocationFor describes a pool as a backup location.
func backupLocationFor(p appdb.StoragePool) backupLocation {
	loc := backupLocation{
		PoolID: p.ID, Name: p.Name, BackendType: p.BackendType,
		TotalBytes: p.TotalBytes, UsableBytes: p.UsableBytes, RootFilesystem: poolOnRootFS(p),
	}
	switch {
	case p.BackendType != storage.BackendDirectory && p.BackendType != storage.BackendZFS:
		loc.Reason = "This pool holds block volumes only and cannot store backup files."
	case !poolUsable(p):
		loc.Reason = "This pool is not available."
	case strings.TrimSpace(p.RootPath) == "" || !filepath.IsAbs(p.RootPath):
		loc.Reason = "This pool has no folder on the host."
	default:
		loc.Usable = true
		loc.TargetPath = filepath.Join(p.RootPath, "backups")
		loc.RepoPath = filepath.Join(p.RootPath, "backup-repo")
		if loc.RootFilesystem {
			loc.Reason = "Shares the host root disk. Backups here can fill the disk the operating system and PostgreSQL need."
		}
	}
	return loc
}

// listBackupLocations offers each storage pool as a place for backups, with
// the pools on their own disks marked as recommended.
func (s *Server) listBackupLocations(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	pools, err := s.Store.ListStoragePools(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]backupLocation, 0, len(pools))
	var best *backupLocation
	for _, pool := range pools {
		loc := backupLocationFor(pool)
		out = append(out, loc)
	}
	for i := range out {
		l := &out[i]
		if !l.Usable || l.RootFilesystem {
			continue
		}
		if best == nil || usableBytes(l.UsableBytes) > usableBytes(best.UsableBytes) {
			best = l
		}
	}
	if best != nil {
		best.Recommended = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func usableBytes(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// errRootFilesystem asks for an explicit confirmation before backups are put
// on the host root disk.
var errRootFilesystem = errConflict("this location is on the host root disk; backups there can fill the disk the operating system and PostgreSQL need. Choose a storage pool on another disk, or confirm the root disk explicitly")

// checkLocalBackupPath refuses locations that would put backups inside
// workload disks, the backup repository, control state or another target, and
// refuses the host root disk unless allowRoot is set.
func (s *Server) checkLocalBackupPath(ctx context.Context, clusterID, path, excludeTargetID string, allowRoot bool) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		return errBadRequest("the location must be an absolute path")
	}
	for _, bad := range []string{"/run", "/var/lib/ndl/control", "/var/lib/ndl/backup-repo", "/var/lib/ndl/keys", "/var/lib/ndl/agent", "/var/lib/ndl/runtime"} {
		if isUnderPath(path, bad) {
			return errBadRequest(path + " is reserved for No-dal or temporary files and cannot hold backups")
		}
	}
	pools, _ := s.Store.ListStoragePools(ctx, clusterID)
	for _, p := range pools {
		if p.RootPath == "" {
			continue
		}
		if isUnderPath(path, filepath.Join(p.RootPath, "volumes")) {
			return errBadRequest("the location is inside the workload disks of pool " + p.Name)
		}
		if isUnderPath(p.RootPath, path) && path != p.RootPath {
			return errBadRequest("the location contains storage pool " + p.Name)
		}
	}
	targets, _ := s.Store.ListBackupTargets(ctx, clusterID)
	for _, t := range targets {
		if t.ID == excludeTargetID || t.Kind != appdb.BackupLocal || !filepath.IsAbs(t.Locator) {
			continue
		}
		if isUnderPath(path, t.Locator) || isUnderPath(t.Locator, path) {
			return errConflict("the location overlaps backup target " + t.Name)
		}
	}
	if allowRoot || s.Backup == nil {
		return nil
	}
	res, err := s.Backup.CopyBackup(ctx, qemu.BackupRootFS, "", path)
	if err != nil {
		return errUnprocessable("could not check which disk the location is on: " + err.Error())
	}
	if res.Size == 1 {
		return errRootFilesystem
	}
	return nil
}

func isUnderPath(p, dir string) bool {
	p, dir = filepath.Clean(p), filepath.Clean(dir)
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// poolBackupPath resolves a pool choice to a folder for a backup target.
func (s *Server) poolBackupPath(ctx context.Context, clusterID, poolID, name string) (string, error) {
	pool, err := s.Store.GetStoragePool(ctx, clusterID, poolID)
	if err != nil || pool == nil {
		return "", errNotFound("storage pool not found")
	}
	loc := backupLocationFor(*pool)
	if !loc.Usable {
		return "", errUnprocessable(loc.Reason)
	}
	base := loc.TargetPath
	if pool.BackendType == storage.BackendZFS {
		if base, err = s.zfsBackupDataset(ctx, *pool, "ndl-backups", loc.TargetPath); err != nil {
			return "", err
		}
	}
	return filepath.Join(base, slugName(name)), nil
}

// zfsBackupDataset makes sure a ZFS pool has its own mounted dataset for
// backup data and returns where it is mounted. A ZFS pool's own folder is not
// mounted (each volume is its own dataset), so writing backups into it would
// land on the root disk.
func (s *Server) zfsBackupDataset(ctx context.Context, pool appdb.StoragePool, leaf, mount string) (string, error) {
	res, err := s.zfs().ZFSPool(ctx, storage.ZFSOp{
		Action: "ensure-dataset", PoolID: pool.ID, Name: s.zfsPoolName(ctx, pool), VolumeID: leaf, DestPath: mount,
	})
	if err != nil {
		return "", errUnavailable("the backup dataset on " + pool.Name + " could not be prepared: " + err.Error())
	}
	if res.Status != storage.StatusAvailable || strings.TrimSpace(res.BackendRef) == "" {
		return "", errUnprocessable("the backup dataset on " + pool.Name + " could not be prepared: " + firstNonEmpty(res.Reason, "zfs did not report a mount"))
	}
	return filepath.Clean(res.BackendRef), nil
}

func slugName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ' || r == '.':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "backups"
	}
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

// relocateRequest chooses the backup repository location by pool or path.
type relocateRequest struct {
	Path                string `json:"path"`
	PoolID              string `json:"pool_id"`
	AllowRootFilesystem bool   `json:"allow_root_filesystem"`
}

func (s *Server) resolveRepoPath(ctx context.Context, clusterID string, req relocateRequest) (string, error) {
	path := strings.TrimSpace(req.Path)
	if req.PoolID != "" {
		pool, err := s.Store.GetStoragePool(ctx, clusterID, req.PoolID)
		if err != nil || pool == nil {
			return "", errNotFound("storage pool not found")
		}
		loc := backupLocationFor(*pool)
		if !loc.Usable {
			return "", errUnprocessable(loc.Reason)
		}
		path = loc.RepoPath
		if pool.BackendType == storage.BackendZFS {
			if path, err = s.zfsBackupDataset(ctx, *pool, "ndl-backup-repo", loc.RepoPath); err != nil {
				return "", err
			}
		}
	}
	if path == "" {
		return "", errBadRequest("choose a storage pool or a path")
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", errBadRequest("the location must be an absolute path")
	}
	if !isUnderPath(path, "/var/lib/ndl/backup-repo") {
		if err := s.checkLocalBackupPath(ctx, clusterID, path, "", req.AllowRootFilesystem); err != nil {
			return "", err
		}
	}
	return path, nil
}

// exportBackupKey returns the backup repository key once confirmed, so it can
// be stored away from this host. Losing it makes every remote backup
// unreadable.
func (s *Server) exportBackupKey(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != "export-backup-key" {
		writeErr(w, http.StatusConflict, "exporting the backup key requires X-Nodal-Confirm: export-backup-key")
		return
	}
	if s.Backup == nil {
		writeErr(w, http.StatusBadGateway, "backup agent is unavailable")
		return
	}
	raw, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionKey})
	res, err := s.Backup.CopyBackup(r.Context(), qemu.BackupV2Key, "", string(raw))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	var out backuphost.Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	if out.Key == "" {
		writeErr(w, http.StatusBadGateway, "the agent did not return a key")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.key.export", "ok", "")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"key":    out.Key,
		"format": "hex",
		"note":   "Store this key away from this host. It decrypts every backup in this repository and on its remote targets.",
	})
}

// protectBackupArtifact marks an artifact as protected from retention.
func (s *Server) protectBackupArtifact(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	var req struct {
		Protected bool `json:"protected"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "protected is required")
		return
	}
	id := r.PathValue("id")
	if err := s.Store.SetBackupArtifactProtected(r.Context(), p.User.ClusterID, id, req.Protected); err != nil {
		writeErr(w, http.StatusNotFound, "backup not found")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.artifact.protect", fmt.Sprintf("%v", req.Protected), id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "protected": req.Protected})
}

// zfsCaptureSource takes a ZFS snapshot of a container's dataset and returns
// the snapshot's view of rootfs, so the backup reads a fixed point in time
// instead of a filesystem that changes underneath it. The snapshot is
// destroyed by release. Pools other than ZFS, or a failed snapshot, fall back
// to reading rootfs directly.
func (s *Server) zfsCaptureSource(ctx context.Context, clusterID string, wl appdb.Workload, vol *appdb.Volume, rootfs, runID string) (string, func()) {
	noop := func() {}
	if vol == nil || !strings.HasPrefix(vol.BackendRef, storage.ZFSMountRoot+"/") {
		return rootfs, noop
	}
	pool, err := s.Store.GetStoragePool(ctx, clusterID, vol.PoolID)
	if err != nil || pool == nil || pool.BackendType != storage.BackendZFS {
		return rootfs, noop
	}
	rel, err := filepath.Rel(vol.BackendRef, rootfs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return rootfs, noop
	}
	tag := "ndl-v2-" + strings.ReplaceAll(runID, "-", "")[:12]
	op := storage.ZFSOp{Action: "snapshot", PoolID: pool.ID, Name: s.zfsPoolName(ctx, *pool), VolumeID: vol.ID, Snapshot: tag}
	res, err := s.zfs().ZFSPool(ctx, op)
	if err != nil || res.Status != storage.StatusAvailable {
		msg := "snapshot failed"
		if err != nil {
			msg = err.Error()
		} else if res.Reason != "" {
			msg = res.Reason
		}
		s.recordStorageEvent("zfs-snapshot", false, wl.ID, "backing up the live filesystem because the ZFS snapshot failed: "+msg)
		return rootfs, noop
	}
	release := func() {
		op.Action = "destroy-snapshot"
		if res, err := s.zfs().ZFSPool(context.Background(), op); err != nil || res.Status != storage.StatusAvailable {
			reason := res.Reason
			if err != nil {
				reason = err.Error()
			}
			s.recordStorageEvent("zfs-snapshot", false, wl.ID, "backup snapshot "+tag+" could not be removed: "+reason)
		}
	}
	return filepath.Join(vol.BackendRef, ".zfs", "snapshot", tag, rel), release
}
