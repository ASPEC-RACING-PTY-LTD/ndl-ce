package backuphost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/diskguard"
)

// GCReport is the outcome of the last repository maintenance run.
type GCReport struct {
	Reason     string                              `json:"reason"`
	StartedAt  time.Time                           `json:"started_at"`
	FinishedAt time.Time                           `json:"finished_at"`
	Local      backup.GCStats                      `json:"local"`
	Error      string                              `json:"error,omitempty"`
	Remote     map[string]backup.RemoteSweepResult `json:"remote,omitempty"`
	RemoteErrs map[string]string                   `json:"remote_errors,omitempty"`
}

// loadOrCreateKeys returns the repository key. The key is kept in two
// places: inside the repository, and at keyPath outside it. The copy outside
// is what makes remote backups restorable after the local repository is
// deleted to recover space, which an operator may reasonably do because the
// repository looks like a cache.
func loadOrCreateKeys(root, keyPath string) (*backup.Keys, string, error) {
	repoKey := filepath.Join(root, "master.key")
	repoRaw, repoErr := os.ReadFile(repoKey)
	if repoErr != nil && !os.IsNotExist(repoErr) {
		return nil, "", repoErr
	}
	var keepRaw []byte
	if keyPath != "" {
		raw, err := os.ReadFile(keyPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, "", err
		}
		keepRaw = raw
	}
	note := ""
	switch {
	case repoErr == nil:
		// The repository's own key always wins: its data is sealed with it.
		if keyPath != "" && keepRaw == nil {
			if err := writeKey(keyPath, repoRaw); err != nil {
				return nil, "", err
			}
		} else if keyPath != "" && string(keepRaw) != string(repoRaw) {
			note = "The backup repository key differs from the preserved key at " + keyPath + ". Remote backups made with the preserved key cannot be restored with the current repository; keep that file."
		}
		k, err := backup.NewKeys(repoRaw)
		return k, note, err
	case keepRaw != nil:
		// The repository was deleted but the key survived: reuse it so
		// existing remote restore points stay readable.
		if err := writeKey(repoKey, keepRaw); err != nil {
			return nil, "", err
		}
		k, err := backup.NewKeys(keepRaw)
		return k, note, err
	}
	master, err := backup.GenerateMaster()
	if err != nil {
		return nil, "", err
	}
	if keyPath != "" {
		if err := writeKey(keyPath, master); err != nil {
			return nil, "", err
		}
	}
	if err := writeKey(repoKey, master); err != nil {
		return nil, "", err
	}
	k, err := backup.NewKeys(master)
	return k, note, err
}

func writeKey(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// removeStaleRestoreDirs deletes remote-restore staging directories left by a
// restore that was interrupted by a crash. Restores run inside the agent, so
// none can still be running when it starts.
func removeStaleRestoreDirs(root string) (int, int64) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0
	}
	n := 0
	var bytes int64
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "remote-restore-") {
			continue
		}
		p := filepath.Join(root, e.Name())
		size := treeBytes(p)
		if os.RemoveAll(p) == nil {
			n++
			bytes += size
		}
	}
	return n, bytes
}

func treeBytes(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			n += info.Size()
		}
		return nil
	})
	return n
}

// fsStat returns the total and free bytes of the filesystem holding path.
func fsStat(path string) (total, free int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return int64(st.Blocks) * int64(st.Bsize), int64(st.Bavail) * int64(st.Bsize), nil
}

// effectiveReserve is the free space backups must leave on the repository
// filesystem. With host protection on it is never below the point at which
// disk protection stops bulk writes, so a backup that starts while the disk
// is healthy also stops before the host is in trouble, rather than running
// on to the much smaller configured reserve.
func (h *Host) effectiveReserve(configured int64) int64 {
	if !h.protectHost {
		return configured
	}
	total, _, err := fsStat(h.root)
	if err != nil || total <= 0 {
		return configured
	}
	_, critical, _ := diskguard.DefaultPolicy().Thresholds(total)
	if critical > configured {
		return critical
	}
	return configured
}

func (h *Host) engineConfig(s Settings) backup.Config {
	return backup.Config{
		MaxLocalBytes:    s.MaxLocalBytes,
		MinHostFreeBytes: h.effectiveReserve(s.MinHostFreeBytes),
	}
}

// scheduleGC asks for maintenance once no capture is running. A failed or
// cancelled capture can leave committed packs that no manifest references;
// they are reclaimed here instead of waiting for the next retention run.
func (h *Host) scheduleGC(reason string) {
	h.mu.Lock()
	h.gcPending = reason
	idle := len(h.busy) == 0
	h.mu.Unlock()
	if idle {
		go h.runPendingGC()
	}
}

func (h *Host) runPendingGC() {
	h.mu.Lock()
	reason := h.gcPending
	if reason == "" || len(h.busy) > 0 {
		h.mu.Unlock()
		return
	}
	h.gcPending = ""
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	_ = h.runGC(ctx, reason)
}

// runGC collects local garbage, compacts mostly-dead packs and sweeps remote
// packs that expired restore points left behind. Only one run is active.
func (h *Host) runGC(ctx context.Context, reason string) GCReport {
	h.gcMu.Lock()
	defer h.gcMu.Unlock()
	h.mu.Lock()
	settings := h.settings
	h.mu.Unlock()
	rep := GCReport{Reason: reason, StartedAt: time.Now().UTC()}
	stats, err := h.repo.CollectGarbageWith(backup.GCOptions{
		RepackLiveRatio: backup.DefaultRepackLiveRatio,
		ReserveBytes:    h.effectiveReserve(settings.MinHostFreeBytes),
	})
	rep.Local = stats
	if err != nil {
		rep.Error = err.Error()
	}
	for _, spec := range h.rememberedTargets() {
		key := targetKey(spec)
		if h.repo.PendingSweep(key) == 0 {
			continue
		}
		tgt, err := h.remoteTarget(spec)
		if err != nil {
			continue
		}
		res, err := h.repo.SweepRemote(ctx, tgt, key)
		if rep.Remote == nil {
			rep.Remote = map[string]backup.RemoteSweepResult{}
		}
		rep.Remote[key] = res
		if err != nil {
			if rep.RemoteErrs == nil {
				rep.RemoteErrs = map[string]string{}
			}
			rep.RemoteErrs[key] = err.Error()
		}
	}
	rep.FinishedAt = time.Now().UTC()
	h.mu.Lock()
	h.lastGC = &rep
	h.mu.Unlock()
	if raw, err := json.Marshal(rep); err == nil {
		_ = os.WriteFile(filepath.Join(h.root, "state", "last-gc.json"), raw, 0o640)
	}
	return rep
}

func (h *Host) loadLastGC() {
	raw, err := os.ReadFile(filepath.Join(h.root, "state", "last-gc.json"))
	if err != nil {
		return
	}
	var rep GCReport
	if json.Unmarshal(raw, &rep) == nil {
		h.lastGC = &rep
	}
}

func targetKey(spec TargetSpec) string {
	return firstNonEmpty(spec.ID, spec.Bucket, spec.Locator, spec.Kind, "default")
}

func (h *Host) rememberedTargets() []TargetSpec {
	entries, err := os.ReadDir(h.targetDir())
	if err != nil {
		return nil
	}
	var out []TargetSpec
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(h.targetDir(), e.Name()))
		if err != nil {
			continue
		}
		var spec TargetSpec
		if json.Unmarshal(raw, &spec) == nil {
			out = append(out, spec)
		}
	}
	return out
}

func hasTarget(spec TargetSpec) bool {
	return spec.ID != "" || spec.Kind != "" || spec.Locator != "" || spec.Bucket != ""
}

// expireRemote removes an expired restore point from its target.
func (h *Host) expireRemote(ctx context.Context, spec TargetSpec, namespace, backupID string) (backup.RemoteExpireResult, error) {
	if err := h.rememberTarget(spec); err != nil {
		return backup.RemoteExpireResult{}, err
	}
	tgt, err := h.remoteTarget(spec)
	if err != nil {
		return backup.RemoteExpireResult{}, err
	}
	return h.repo.ExpireRemote(ctx, tgt, targetKey(spec), namespace, backupID)
}

func (h *Host) remoteUsage(ctx context.Context, spec TargetSpec) (*backup.RemoteUsage, error) {
	if !hasTarget(spec) {
		return nil, fmt.Errorf("remote usage requires a target")
	}
	tgt, err := h.remoteTarget(spec)
	if err != nil {
		return nil, err
	}
	u, err := h.repo.MeasureRemote(ctx, tgt, targetKey(spec))
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// RestorePoints counts local restore points.
func (h *Host) RestorePoints() int {
	states, _ := h.repo.ListStates()
	return len(states)
}

// Idle reports whether no capture, upload or maintenance is running.
func (h *Host) Idle() bool {
	h.mu.Lock()
	busy := len(h.busy)
	h.mu.Unlock()
	if busy > 0 || h.pendingJobs() > 0 {
		return false
	}
	if !h.gcMu.TryLock() {
		return false
	}
	h.gcMu.Unlock()
	return true
}

// Close stops the upload queues. The host must not be used afterwards.
func (h *Host) Close() {
	h.mu.Lock()
	qs := h.queues
	h.queues = map[string]*backup.UploadQueue{}
	h.mu.Unlock()
	for _, q := range qs {
		if q != nil {
			q.Stop()
		}
	}
}

// Root is the repository directory.
func (h *Host) Root() string { return h.root }
