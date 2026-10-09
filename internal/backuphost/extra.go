package backuphost

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/backup"
)

// CaptureModeDisk captures one VM disk image instead of a directory tree.
const CaptureModeDisk = "disk"

// VerifyResult is the outcome of verifying one local restore point.
type VerifyResult struct {
	BackupID  string `json:"backup_id"`
	Namespace string `json:"namespace"`
	OK        bool   `json:"ok"`
	Checked   int    `json:"checked_chunks"`
	Error     string `json:"error,omitempty"`
}

// verifySampleChunks is how many chunks per restore point are decrypted.
const verifySampleChunks = 64

// verify checks every local restore point: all chunks present, and a sample
// decrypted and authenticated. It reads only; nothing is repaired or removed.
func (h *Host) verify(ctx context.Context) ([]VerifyResult, error) {
	states, err := h.repo.ListStates()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	out := make([]VerifyResult, 0, len(states))
	for _, s := range states {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		res := VerifyResult{BackupID: s.BackupID, Namespace: s.Namespace}
		m, err := h.repo.LoadManifest(s.Namespace, s.BackupID)
		if err == nil {
			res.Checked, err = h.repo.VerifySample(m, verifySampleChunks)
		}
		if err != nil {
			res.Error = err.Error()
		} else {
			res.OK = true
		}
		out = append(out, res)
	}
	return out, nil
}

// exportKey returns the repository key as hex and records that it was
// exported, so the UI stops asking for it to be saved.
func (h *Host) exportKey() (string, error) {
	raw, err := os.ReadFile(filepath.Join(h.root, "master.key"))
	if err != nil {
		return "", err
	}
	if h.keyPath != "" {
		_ = os.WriteFile(h.keyPath+".exported", []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	} else {
		_ = os.WriteFile(filepath.Join(h.root, "master.key.exported"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	}
	return hex.EncodeToString(raw), nil
}

func (h *Host) keyExported() bool {
	marker := filepath.Join(h.root, "master.key.exported")
	if h.keyPath != "" {
		marker = h.keyPath + ".exported"
	}
	_, err := os.Stat(marker)
	return err == nil
}

// expireRemoteOnly removes a restore point's remote copy but keeps it locally,
// for offsite retention that is shorter than local retention.
func (h *Host) expireRemoteOnly(ctx context.Context, req Request) (Result, error) {
	if req.Namespace == "" || req.BackupID == "" || !hasTarget(req.Target) {
		return Result{}, fmt.Errorf("remote expiry requires namespace, backup_id and target")
	}
	h.mu.Lock()
	queues := make([]*backup.UploadQueue, 0, len(h.queues))
	for _, q := range h.queues {
		queues = append(queues, q)
	}
	h.mu.Unlock()
	for _, q := range queues {
		q.RemoveJobs(req.Namespace, req.BackupID)
	}
	res, err := h.expireRemote(ctx, req.Target, req.Namespace, req.BackupID)
	if err != nil {
		return Result{}, err
	}
	if err := h.repo.SetRemoteState(req.Namespace, req.BackupID, backup.RemoteNone); err != nil && !os.IsNotExist(err) {
		return Result{}, err
	}
	out, err := h.status(req)
	out.RemoteExpire = &res
	return out, err
}

// stageDisk prepares a directory holding one file, disk.qcow2, for capture.
// The frozen disk is hard-linked (no copy) unless it has a backing chain, in
// which case it is converted into one self-contained image next to the disk,
// on the same pool rather than the root disk. Earlier staging left by a crash
// is removed first.
func stageDisk(ctx context.Context, disk string, flatten bool) (string, func(), error) {
	disk = filepath.Clean(disk)
	if !filepath.IsAbs(disk) {
		return "", nil, fmt.Errorf("disk path must be absolute")
	}
	st, err := os.Stat(disk)
	if err != nil {
		return "", nil, err
	}
	if !st.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%s is not a disk image file", disk)
	}
	parent := filepath.Dir(disk)
	if entries, err := os.ReadDir(parent); err == nil {
		cutoff := time.Now().Add(-time.Hour)
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), ".ndl-backup-") {
				if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
					_ = os.RemoveAll(filepath.Join(parent, e.Name()))
				}
			}
		}
	}
	if !flatten && hasBackingFile(ctx, disk) {
		// The frozen image only holds the top layer; capture a
		// self-contained copy so the backup restores on its own.
		flatten = true
	}
	stage, err := os.MkdirTemp(parent, ".ndl-backup-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(stage) }
	target := filepath.Join(stage, "disk.qcow2")
	if flatten {
		out, err := exec.CommandContext(ctx, "qemu-img", "convert", "-q", "-O", "qcow2", disk, target).CombinedOutput()
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("qemu-img convert: %w: %s", err, strings.TrimSpace(string(out)))
		}
	} else if err := os.Link(disk, target); err != nil {
		cleanup()
		return "", nil, err
	}
	return stage, cleanup, nil
}

// hasBackingFile reports whether a qcow2 image depends on a backing file.
func hasBackingFile(ctx context.Context, disk string) bool {
	out, err := exec.CommandContext(ctx, "qemu-img", "info", "-U", "--output=json", disk).Output()
	if err != nil {
		return false
	}
	var info struct {
		Backing string `json:"backing-filename"`
	}
	return json.Unmarshal(out, &info) == nil && info.Backing != ""
}

// WipeResult reports a remote wipe.
type WipeResult struct {
	ObjectsDeleted int      `json:"objects_deleted"`
	BytesDeleted   int64    `json:"bytes_deleted"`
	Failed         int      `json:"failed"`
	Prefixes       []string `json:"prefixes"`
}

// wipeRemote deletes every No-dal backup object in a target, so backups can
// start again from nothing. Only keys under backups/ (and the same layout under
// the target prefix) are touched; other objects in the bucket are left alone.
// Local restore points are kept and marked local-only. It refuses while a
// capture runs.
func (h *Host) wipeRemote(ctx context.Context, req Request) (Result, error) {
	if !hasTarget(req.Target) {
		return Result{}, fmt.Errorf("wipe requires a target")
	}
	h.mu.Lock()
	busy := len(h.busy)
	queues := make([]*backup.UploadQueue, 0, len(h.queues))
	for _, q := range h.queues {
		queues = append(queues, q)
	}
	h.mu.Unlock()
	if busy > 0 {
		return Result{}, fmt.Errorf("a backup is running; wipe the target when it finishes")
	}
	tgt, err := h.remoteTarget(req.Target)
	if err != nil {
		return Result{}, err
	}
	rc, ok := tgt.(backup.RemoteCleaner)
	if !ok {
		return Result{}, fmt.Errorf("this target cannot list or delete objects")
	}
	// Pending uploads would recreate objects after the wipe.
	states, _ := h.repo.ListStates()
	for _, s := range states {
		for _, q := range queues {
			q.RemoveJobs(s.Namespace, s.BackupID)
		}
	}
	res := WipeResult{Prefixes: []string{"backups/"}}
	if p := strings.Trim(strings.TrimSpace(req.Target.Prefix), "/"); p != "" {
		res.Prefixes = append(res.Prefixes, p+"/backups/")
	}
	for _, prefix := range res.Prefixes {
		objs, err := rc.List(ctx, prefix)
		if err != nil {
			return Result{}, fmt.Errorf("list %s: %w", prefix, err)
		}
		for _, o := range objs {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			if err := rc.Delete(ctx, o.Key); err != nil {
				res.Failed++
				continue
			}
			res.ObjectsDeleted++
			res.BytesDeleted += o.Size
		}
	}
	for _, s := range states {
		if s.Remote != backup.RemoteNone {
			_ = h.repo.SetRemoteState(s.Namespace, s.BackupID, backup.RemoteNone)
		}
	}
	h.repo.ClearSweep(targetKey(req.Target))
	out, err := h.status(req)
	out.Wipe = &res
	if res.Failed > 0 {
		return out, fmt.Errorf("%d object(s) could not be deleted; run the wipe again", res.Failed)
	}
	return out, err
}
