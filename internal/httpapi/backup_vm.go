package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// executeVMBackupV2 backs up a VM disk on a Directory pool through the
// deduplicating engine. It takes a point-in-time overlay, captures the frozen
// image underneath it, and then merges the overlay back so the disk chain
// never grows. Layers left by earlier backups are merged first.
func (s *Server) executeVMBackupV2(ctx context.Context, clusterID string, wl appdb.Workload, tgt appdb.BackupTarget, run *appdb.BackupRun, artifactID string) error {
	if n, err := s.mergeBackupLayers(ctx, clusterID, wl); err != nil {
		s.recordStorageEvent("vm-backup", false, wl.ID, fmt.Sprintf("merged %d earlier backup layer(s), then stopped: %v", n, err))
	} else if n > 0 {
		s.recordStorageEvent("vm-backup", true, wl.ID, fmt.Sprintf("merged %d disk layer(s) left by earlier backups", n))
	}
	snap, frozen, err := s.snapshotForBackup(ctx, clusterID, wl, run.ID)
	if err != nil {
		return err
	}
	run.SnapshotID = snap.ID
	defer func() {
		// The merge must run even when the backup was cancelled (for example
		// paused by a platform update), so the guest is not left on a
		// backup layer.
		mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		if err := s.commitBackupLayer(mctx, clusterID, wl, snap); err != nil {
			s.recordStorageEvent("vm-backup", false, wl.ID, "the backup snapshot could not be merged back and stays in the disk chain until the next backup: "+err.Error())
		}
	}()
	vol, _, _, err := s.bootVolumeLocator(ctx, clusterID, wl)
	if err != nil {
		return err
	}
	settings, _ := s.Store.GetBackupWorkspaceSettings(ctx, clusterID)
	if settings == nil {
		settings = appdb.DefaultBackupWorkspaceSettings(clusterID)
	}
	req := backuphost.Request{
		Action: backuphost.ActionCapture, WorkloadID: wl.ID, WorkloadName: wl.Name,
		CaptureMode: backuphost.CaptureModeDisk, DiskPath: frozen, Blueprint: s.v2Blueprint(ctx, clusterID, wl, vol),
		Settings: backuphost.Settings{
			MaxLocalBytes: settings.MaxLocalBytes, MinHostFreeBytes: settings.MinHostFreeBytes,
			CaptureConcurrency: settings.CaptureConcurrency, UploadWorkers: settings.UploadWorkers,
			BandwidthLimitBPS: settings.BandwidthLimitBPS,
		},
		Target: s.v2TargetSpec(ctx, tgt),
	}
	raw, _ := json.Marshal(req)
	res, err := s.Backup.CopyBackup(ctx, qemu.BackupV2Capture, frozen, string(raw))
	if err != nil {
		return err
	}
	return s.recordV2Artifact(ctx, clusterID, wl, tgt, run, artifactID, res)
}

// backupLayers returns the overlay snapshots of a boot volume, oldest first.
func (s *Server) backupLayers(ctx context.Context, clusterID string, wl appdb.Workload, volID string) []appdb.Snapshot {
	items, _ := s.Store.ListSnapshots(ctx, clusterID, wl.ID)
	var out []appdb.Snapshot
	for _, it := range items {
		if it.VolumeID == volID && it.Mechanism == appdb.MechanismOverlay {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ChainDepth != out[j].ChainDepth {
			return out[i].ChainDepth < out[j].ChainDepth
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// mergeBackupLayers merges overlays that earlier backups left on top of the
// disk, newest first, and stops at the first snapshot a user took.
func (s *Server) mergeBackupLayers(ctx context.Context, clusterID string, wl appdb.Workload) (int, error) {
	merged := 0
	for i := 0; i <= qemu.ChainMax; i++ {
		vol, _, _, err := s.bootVolumeLocator(ctx, clusterID, wl)
		if err != nil {
			return merged, err
		}
		layers := s.backupLayers(ctx, clusterID, wl, vol.ID)
		if len(layers) == 0 || layers[len(layers)-1].PurposeTag != backupPurpose {
			return merged, nil
		}
		if err := s.commitBackupLayer(ctx, clusterID, wl, layers[len(layers)-1]); err != nil {
			return merged, err
		}
		merged++
	}
	return merged, nil
}

// commitBackupLayer merges the overlay created by snap into the image it
// froze and records the disk as that image again. It only acts while snap is
// the top of the chain.
func (s *Server) commitBackupLayer(ctx context.Context, clusterID string, wl appdb.Workload, snap appdb.Snapshot) error {
	if s.VM == nil {
		return errUnavailable("vm agent is unavailable")
	}
	vol, pool, tip, err := s.bootVolumeLocator(ctx, clusterID, wl)
	if err != nil {
		return err
	}
	if pool.BackendType != storage.BackendDirectory {
		return nil
	}
	layers := s.backupLayers(ctx, clusterID, wl, vol.ID)
	if len(layers) == 0 || layers[len(layers)-1].ID != snap.ID {
		return fmt.Errorf("a newer snapshot sits on top of the backup layer")
	}
	backing, err := storage.JoinUnder(pool.RootPath, snap.BackendRef)
	if err != nil {
		return err
	}
	if vol.BackendRef == snap.BackendRef || filepath.Clean(backing) == filepath.Clean(tip) {
		return s.Store.DeleteSnapshot(ctx, clusterID, snap.ID)
	}
	if _, err := s.VM.SnapshotVM(ctx, qemu.OverlayRequest{
		Action: qemu.OverlayCommit, WorkloadID: wl.ID, OverlayPath: tip, BackingPath: backing,
	}); err != nil {
		return err
	}
	if err := s.Store.UpdateVolumeLocator(ctx, clusterID, vol.ID, snap.BackendRef); err != nil {
		return err
	}
	return s.Store.DeleteSnapshot(ctx, clusterID, snap.ID)
}

// materializeV2Disk restores a V2 VM disk backup into staging next to the
// backup repository (on its disk, not in /tmp) and returns the image path.
func (s *Server) materializeV2Disk(ctx context.Context, clusterID string, art appdb.BackupArtifact) (string, func(), error) {
	settings, _ := s.Store.GetBackupWorkspaceSettings(ctx, clusterID)
	if settings == nil {
		settings = appdb.DefaultBackupWorkspaceSettings(clusterID)
	}
	st, _ := s.v2Status(ctx, clusterID, *settings)
	root := firstNonEmpty(st.Workspace.Root, backuphost.DefaultRoot)
	dest := filepath.Join(filepath.Dir(root), "backup-restore-staging", art.ID)
	var tgt *appdb.BackupTarget
	if run, _ := s.Store.GetBackupRun(ctx, clusterID, art.RunID); run != nil && run.TargetID != "" {
		tgt, _ = s.Store.GetBackupTarget(ctx, clusterID, run.TargetID)
	}
	cleanup := func() { _, _ = s.Backup.CopyBackup(context.Background(), qemu.BackupRmTree, "", dest) }
	if err := s.restoreV2Root(ctx, art, dest, tgt); err != nil {
		cleanup()
		return "", nil, err
	}
	return filepath.Join(dest, "disk.qcow2"), cleanup, nil
}
