package backuphost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backupscope"
	"github.com/no-dal/ndl-ce/internal/ctbackup"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// Host is the agent-side Backup Engine V2 runtime: one local repository,
// bounded capture, and per-target upload queues.
type Host struct {
	root   string
	repo   *backup.Repository
	eng    *backup.Engine
	docker *docker.Engine

	mu       sync.Mutex
	settings Settings
	queues   map[string]*backup.UploadQueue
	pending  int
	busy     map[string]struct{}
}

// Options open the host repository.
type Options struct {
	Root     string
	Settings Settings
	Docker   *docker.Engine
}

// Open creates or opens the local V2 repository. The master key is created
// once and stored at 0600; it is never returned.
func Open(opts Options) (*Host, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		root = DefaultRoot
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	keys, err := loadOrCreateKeys(root)
	if err != nil {
		return nil, err
	}
	repo, err := backup.OpenRepository(root, keys)
	if err != nil {
		return nil, err
	}
	cfg := backup.Config{
		MaxLocalBytes:    opts.Settings.withDefaults().MaxLocalBytes,
		MinHostFreeBytes: opts.Settings.withDefaults().MinHostFreeBytes,
	}
	h := &Host{
		root:     root,
		repo:     repo,
		eng:      backup.NewEngine(repo, cfg),
		docker:   opts.Docker,
		settings: opts.Settings.withDefaults(),
		queues:   map[string]*backup.UploadQueue{},
		busy:     map[string]struct{}{},
	}
	if err := h.resumeTargets(context.Background()); err != nil {
		return nil, err
	}
	return h, nil
}

func loadOrCreateKeys(root string) (*backup.Keys, error) {
	path := filepath.Join(root, "master.key")
	raw, err := os.ReadFile(path)
	if err == nil {
		return backup.NewKeys(raw)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	master, err := backup.GenerateMaster()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, master, 0o600); err != nil {
		return nil, err
	}
	return backup.NewKeys(master)
}

// Handle dispatches a V2 backup-copy action. dest is request JSON.
func (h *Host) Handle(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	var req Request
	if strings.TrimSpace(dest) != "" && strings.HasPrefix(strings.TrimSpace(dest), "{") {
		if err := json.Unmarshal([]byte(dest), &req); err != nil {
			return storage.CopyResult{}, fmt.Errorf("backup v2 request: %w", err)
		}
	}
	if req.Action == "" {
		req.Action = action
	}
	if req.Settings.MaxLocalBytes > 0 || req.Settings.MinHostFreeBytes > 0 || req.Settings.UploadWorkers > 0 ||
		req.Settings.CaptureConcurrency > 0 || req.Settings.BandwidthLimitBPS > 0 || req.Settings.CacheRetentionHours > 0 {
		h.applySettings(req.Settings)
	}
	var out Result
	var err error
	switch req.Action {
	case ActionCapture:
		out, err = h.capture(ctx, src, req)
	case ActionRestore:
		out, err = h.restore(ctx, src, req)
	case ActionPreview:
		out, err = h.preview(src, req)
	case ActionStatus, ActionWorkspace:
		out, err = h.status(req)
	case ActionExpire:
		out, err = h.expire(req)
	case ActionEnqueue:
		out, err = h.enqueue(ctx, req)
	default:
		return storage.CopyResult{}, fmt.Errorf("unsupported backup v2 action")
	}
	if err != nil {
		return storage.CopyResult{}, err
	}
	extra, _ := json.Marshal(out)
	return storage.CopyResult{
		Dest:   firstNonEmpty(out.Locator, src),
		SHA256: out.BackupID,
		Size:   out.LogicalBytes,
		Format: backup.Format,
		Extra:  string(extra),
	}, nil
}

func (h *Host) applySettings(s Settings) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s = s.withDefaults()
	h.settings = s
	h.eng = backup.NewEngine(h.repo, backup.Config{
		MaxLocalBytes: s.MaxLocalBytes, MinHostFreeBytes: s.MinHostFreeBytes,
	})
	for _, q := range h.queues {
		if q != nil {
			q.SetBandwidthLimit(s.BandwidthLimitBPS)
		}
	}
	h.pruneCachesLocked()
}

func (h *Host) capture(ctx context.Context, source string, req Request) (Result, error) {
	if source == "" {
		return Result{}, fmt.Errorf("capture source is required")
	}
	if err := h.acquireCapture(ctx, req.WorkloadID); err != nil {
		return Result{}, err
	}
	defer h.releaseCapture(req.WorkloadID)

	mode := strings.TrimSpace(req.CaptureMode)
	if mode == "" {
		mode = backup.CaptureModeFull
	}
	dockerInfo, hint := h.dockerInventory(ctx, req.WorkloadID, req.WorkloadName)
	bp := req.Blueprint
	bp.CaptureMode = mode
	if bp.Docker == nil && dockerInfo != nil {
		bp.Docker = dockerInfo
	}

	prev, err := backupscope.Discover(backupscope.Options{
		Root: source, WorkloadID: req.WorkloadID, Name: req.WorkloadName,
		Mode: mode, Selection: req.Selection, Docker: hint,
	})
	if err != nil {
		return Result{}, err
	}
	includes, excludes := backupscope.PlanCapture(prev)
	excludes = append(excludes, defaultTechnicalExcludes(source)...)
	bp.Includes = includes
	bp.Excludes = excludes

	unit := strings.TrimSpace(req.Unit)
	pre := ctbackup.RunGuestHookDetailed(ctx, unit, source, ctbackup.GuestHookPre)
	if pre.Err != nil {
		_ = ctbackup.RunGuestHook(ctx, unit, source, ctbackup.GuestHookPost)
		return Result{}, pre.Err
	}
	defer func() { _ = ctbackup.RunGuestHook(ctx, unit, source, ctbackup.GuestHookPost) }()

	info := backup.ConsistencyReport{
		Requested: firstNonEmpty(req.Consistency, backup.ConsistencyCrash),
		Unit:      unit,
		HookPath:  ctbackup.GuestHookPre,
		HookRan:   pre.Ran,
		HookOK:    pre.Ran,
	}
	if pre.Ran {
		info.Result = backup.ConsistencyApp
		info.Note = "guest pre-hook ran successfully"
	} else {
		info.Result = backup.ConsistencyCrash
		if unit != "" && !pre.Present {
			info.Note = "guest pre-hook is not installed; capture is crash-consistent"
		} else if unit == "" {
			info.Note = "no guest consistency hook was configured"
		}
	}
	man, state, err := h.eng.Capture(ctx, backup.CaptureOptions{
		Source: source, WorkloadID: req.WorkloadID, WorkloadName: req.WorkloadName,
		Consistency: info.Result, ConsistencyInfo: info, Blueprint: bp, Includes: includes, Excludes: excludes,
	})
	if err != nil {
		return Result{}, err
	}
	if req.Target.ID != "" || req.Target.Kind != "" || req.Target.Locator != "" {
		if err := h.rememberTarget(req.Target); err != nil {
			return Result{}, err
		}
		q, err := h.queueFor(req.Target)
		if err != nil {
			return Result{}, err
		}
		if err := q.EnqueueBackup(state); err != nil {
			return Result{}, err
		}
		state, _ = h.repo.LoadState(state.Namespace, state.BackupID)
	}
	repoBytes, _ := h.repo.SizeOnDisk()
	out := resultFrom(man, state)
	out.RepoBytes = repoBytes
	out.PendingUploads = h.pendingJobs()
	out.Preview = prev
	out.Blueprint = man.Blueprint
	out.Locator = "ndl-cab://backups/" + state.Namespace + "/" + state.BackupID
	return out, nil
}

func (h *Host) restore(ctx context.Context, dest string, req Request) (Result, error) {
	if dest == "" {
		dest = req.Dest
	}
	if dest == "" {
		return Result{}, fmt.Errorf("restore destination is required")
	}
	ns := req.Namespace
	id := req.BackupID
	if ns == "" || id == "" {
		return Result{}, fmt.Errorf("restore requires namespace and backup_id")
	}
	man, err := h.repo.LoadManifest(ns, id)
	if err != nil {
		if req.Target.Kind != "" && isObjectKind(req.Target.Kind) {
			tgt, err := h.remoteTarget(req.Target)
			if err != nil {
				return Result{}, err
			}
			tmp, err := os.MkdirTemp(h.root, "remote-restore-")
			if err != nil {
				return Result{}, err
			}
			defer func() { _ = os.RemoveAll(tmp) }()
			repo, man, err := backup.FetchRemote(ctx, tgt, h.repo.Keys(), ns, id, tmp)
			if err != nil {
				return Result{}, err
			}
			if err := repo.Restore(ctx, man, backup.RestoreOptions{Dest: dest, Chown: req.Chown}); err != nil {
				return Result{}, err
			}
			return Result{BackupID: id, Namespace: ns, Locator: dest, CaptureMode: man.Blueprint.CaptureMode, Blueprint: man.Blueprint}, nil
		}
		return Result{}, err
	}
	if err := h.repo.Restore(ctx, man, backup.RestoreOptions{Dest: dest, Chown: req.Chown}); err != nil {
		return Result{}, err
	}
	return Result{BackupID: id, Namespace: ns, Locator: dest, CaptureMode: man.Blueprint.CaptureMode, Blueprint: man.Blueprint}, nil
}

func (h *Host) preview(source string, req Request) (Result, error) {
	if source == "" {
		return Result{}, fmt.Errorf("preview source is required")
	}
	_, hint := h.dockerInventory(context.Background(), req.WorkloadID, req.WorkloadName)
	mode := firstNonEmpty(req.CaptureMode, backup.CaptureModeFull)
	prev, err := backupscope.Discover(backupscope.Options{
		Root: source, WorkloadID: req.WorkloadID, Name: req.WorkloadName,
		Mode: mode, Selection: req.Selection, Docker: hint,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Preview: prev, CaptureMode: mode, WorkloadID: req.WorkloadID, WorkloadName: req.WorkloadName}, nil
}

func (h *Host) status(_ Request) (Result, error) {
	states, err := h.repo.ListStates()
	if err != nil && !os.IsNotExist(err) {
		return Result{}, err
	}
	points := make([]PointView, 0, len(states))
	protected := 0
	for _, s := range states {
		mode, consistency := "", ""
		var logical, physical int64
		if man, err := h.repo.LoadManifest(s.Namespace, s.BackupID); err == nil {
			mode = man.Blueprint.CaptureMode
			consistency = man.Consistency
			logical = man.Stats.LogicalBytes
			physical = man.Stats.PhysicalNewData
		}
		points = append(points, PointView{
			BackupID: s.BackupID, Namespace: s.Namespace, WorkloadID: s.WorkloadID,
			WorkloadName: s.WorkloadName, CreatedAtNS: s.CreatedAtNS,
			LocalComplete: s.LocalComplete, Remote: s.Remote, CaptureMode: mode,
			Consistency: consistency, LogicalBytes: logical, PhysicalNewData: physical,
			UploadStartedNS: s.UploadStartedNS, UploadEndedNS: s.UploadEndedNS,
		})
		if s.Remote == backup.RemoteProtected {
			protected++
		}
	}
	repoBytes, _ := h.repo.SizeOnDisk()
	free, _ := hostFreeBytes(h.root)
	h.mu.Lock()
	active := len(h.busy)
	settings := h.settings
	h.mu.Unlock()
	pending := h.pendingJobs()
	return Result{
		Points: points, Protected: protected, RepoBytes: repoBytes, PendingUploads: pending,
		Workspace: WorkspaceStatus{
			Root: h.root, RepoBytes: repoBytes, MaxLocalBytes: settings.MaxLocalBytes,
			MinHostFreeBytes: settings.MinHostFreeBytes, HostFreeBytes: free,
			PendingUploads: pending, CaptureBusy: active > 0, CaptureActive: active,
			CaptureConcurrency: settings.CaptureConcurrency, UploadWorkers: settings.UploadWorkers,
			BandwidthLimitBPS: settings.BandwidthLimitBPS, CacheRetentionHours: settings.CacheRetentionHours,
		},
	}, nil
}

func (h *Host) expire(req Request) (Result, error) {
	if req.Namespace == "" || req.BackupID == "" {
		return Result{}, fmt.Errorf("expire requires namespace and backup_id")
	}
	if err := h.repo.DeleteRestorePoint(req.Namespace, req.BackupID); err != nil && !os.IsNotExist(err) {
		return Result{}, err
	}
	if _, err := h.repo.CollectGarbage(); err != nil {
		return Result{}, err
	}
	return h.status(req)
}

func (h *Host) enqueue(ctx context.Context, req Request) (Result, error) {
	if req.Namespace == "" || req.BackupID == "" {
		return Result{}, fmt.Errorf("enqueue requires namespace and backup_id")
	}
	state, err := h.repo.LoadState(req.Namespace, req.BackupID)
	if err != nil {
		return Result{}, err
	}
	if err := h.rememberTarget(req.Target); err != nil {
		return Result{}, err
	}
	q, err := h.queueFor(req.Target)
	if err != nil {
		return Result{}, err
	}
	if err := q.EnqueueBackup(state); err != nil {
		return Result{}, err
	}
	return h.status(req)
}

func resultFrom(man *backup.Manifest, state *backup.PointState) Result {
	dedupe := 0.0
	if man.Stats.ChunksTotal > 0 {
		dedupe = float64(man.Stats.ChunksReused) / float64(man.Stats.ChunksTotal)
	}
	remote := backup.RemoteNone
	if state != nil {
		remote = state.Remote
	}
	return Result{
		BackupID: man.BackupID, Namespace: man.Namespace, WorkloadID: man.WorkloadID,
		WorkloadName: man.WorkloadName, LocalComplete: true, Remote: remote,
		CaptureMode: man.Blueprint.CaptureMode, Consistency: man.Consistency,
		LogicalBytes: man.Stats.LogicalBytes, BytesRead: man.Stats.BytesRead,
		PhysicalNewData: man.Stats.PhysicalNewData, ChunksTotal: man.Stats.ChunksTotal,
		ChunksNew: man.Stats.ChunksNew, ChunksReused: man.Stats.ChunksReused,
		FilesScanned: man.Stats.FilesScanned, FilesUnchanged: man.Stats.FilesUnchanged,
		FilesChanged: man.Stats.FilesChanged, PacksCommitted: man.Stats.PacksCommitted,
		DurationNanos: man.Stats.DurationNanos, DedupeRatio: dedupe,
		ConsistencyInfo: man.ConsistencyInfo,
	}
}

func (h *Host) acquireCapture(ctx context.Context, workloadID string) error {
	id := strings.TrimSpace(workloadID)
	if id == "" {
		id = "anonymous"
	}
	for {
		h.mu.Lock()
		if h.busy == nil {
			h.busy = map[string]struct{}{}
		}
		if _, ok := h.busy[id]; ok {
			h.mu.Unlock()
			return fmt.Errorf("a backup is already running for this workload")
		}
		slots := h.settings.CaptureConcurrency
		if slots < 1 {
			slots = DefaultCaptureSlots
		}
		if len(h.busy) < slots {
			h.busy[id] = struct{}{}
			h.mu.Unlock()
			return nil
		}
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (h *Host) releaseCapture(workloadID string) {
	id := strings.TrimSpace(workloadID)
	if id == "" {
		id = "anonymous"
	}
	h.mu.Lock()
	delete(h.busy, id)
	h.mu.Unlock()
}

func (h *Host) pruneCachesLocked() {
	hours := h.settings.CacheRetentionHours
	if hours <= 0 || h.repo == nil {
		return
	}
	dir := filepath.Join(h.root, "cache")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

func defaultTechnicalExcludes(root string) []string {
	var out []string
	for _, name := range []string{"proc", "sys", "dev", "run"} {
		out = append(out, filepath.Join(root, name))
	}
	return out
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func hostFreeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

func isObjectKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "s3", "r2", "aws", "b2", "minio":
		return true
	default:
		return false
	}
}
