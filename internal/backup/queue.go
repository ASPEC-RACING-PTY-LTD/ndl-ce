package backup

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Remote object key layout. Packs are global and content-named so a chunk
// deduplicated across workloads is uploaded exactly once and is addressable by
// any restore point that references it. Manifests and their location maps live
// under a per-namespace prefix.
func objectKeyPack(packID string) string {
	return "backups/packs/" + packID + ".pack"
}

func objectKeyManifest(namespace, backupID string) string {
	return "backups/" + namespace + "/manifests/" + backupID + ".snap"
}

func objectKeyLocmap(namespace, backupID string) string {
	return "backups/" + namespace + "/manifests/" + backupID + ".locmap"
}

type uploadJob struct {
	Namespace string `json:"namespace"`
	BackupID  string `json:"backup_id"`
	PackID    string `json:"pack_id"`
}

// UploadQueue uploads pack objects to a Target independently of capture. It is a
// bounded worker pool fed from an on-disk job directory, so pending uploads
// survive a process restart and resume automatically. Capture never blocks on
// upload: a slow remote transfer for one workload does not stop the next
// workload from being captured.
type UploadQueue struct {
	repo        *Repository
	target      Target
	workers     int
	maxAttempts int
	baseBackoff time.Duration
	limitBPS    int64

	mu       sync.Mutex
	inflight map[string]bool
	attempts map[string]int

	jobs   chan string
	wake   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewUploadQueue builds a queue with a bounded number of upload workers.
func NewUploadQueue(repo *Repository, target Target, workers int) *UploadQueue {
	return NewUploadQueueLimited(repo, target, workers, 0)
}

// NewUploadQueueLimited builds an upload queue that enforces a byte-per-second
// ceiling when limitBPS is greater than zero.
func NewUploadQueueLimited(repo *Repository, target Target, workers int, limitBPS int64) *UploadQueue {
	if workers <= 0 {
		workers = 4
	}
	return &UploadQueue{
		repo:        repo,
		target:      target,
		workers:     workers,
		maxAttempts: 8,
		baseBackoff: 20 * time.Millisecond,
		limitBPS:    limitBPS,
		inflight:    map[string]bool{},
		attempts:    map[string]int{},
		jobs:        make(chan string, workers*2),
		wake:        make(chan struct{}, 1),
	}
}

func (q *UploadQueue) SetBandwidthLimit(bps int64) {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.limitBPS = bps
	q.mu.Unlock()
}

func (q *UploadQueue) bandwidthLimit() int64 {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.limitBPS
}

func (q *UploadQueue) queueDir() string { return filepath.Join(q.repo.root, "state", "queue") }

// Start launches the workers and the dispatcher and immediately picks up any
// jobs already persisted on disk (restart recovery).
func (q *UploadQueue) Start(ctx context.Context) error {
	if err := os.MkdirAll(q.queueDir(), 0o750); err != nil {
		return err
	}
	q.retryFailed()
	q.ctx, q.cancel = context.WithCancel(ctx)
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker()
	}
	q.wg.Add(1)
	go q.dispatcher()
	q.signal()
	return nil
}

// Stop cancels the queue and waits for workers to finish the in-flight upload.
func (q *UploadQueue) Stop() {
	if q.cancel != nil {
		q.cancel()
	}
	q.wg.Wait()
}

func (q *UploadQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// EnqueueBackup persists an upload job for every pack in the restore point and
// marks it queued. It returns immediately without uploading anything.
func (q *UploadQueue) EnqueueBackup(state *PointState) error {
	if err := os.MkdirAll(q.queueDir(), 0o750); err != nil {
		return err
	}
	// The extra job without a pack completes the restore point even when
	// its capture deduplicated everything and committed no new pack;
	// without it such a point stayed queued forever.
	for _, packID := range append(append([]string(nil), state.Packs...), "") {
		job := uploadJob{Namespace: state.Namespace, BackupID: state.BackupID, PackID: packID}
		raw, err := json.Marshal(job)
		if err != nil {
			return err
		}
		name := state.Namespace + "~~" + state.BackupID + "~~" + firstNonEmpty(packID, "manifest") + ".job"
		if err := writeFileAtomic(filepath.Join(q.queueDir(), name), raw); err != nil {
			return err
		}
	}
	state.Remote = RemoteQueued
	if err := q.repo.writeState(state); err != nil {
		return err
	}
	q.signal()
	return nil
}

// retryFailed re-arms jobs that exhausted their attempts in an earlier run.
func (q *UploadQueue) retryFailed() {
	entries, err := os.ReadDir(q.queueDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".failed" {
			continue
		}
		old := filepath.Join(q.queueDir(), e.Name())
		_ = os.Rename(old, strings.TrimSuffix(old, ".failed")+".job")
	}
}

// RemoveJobs drops every queued or failed upload job of one restore point.
// Expiring a restore point calls it so its packs are not uploaded later.
func (q *UploadQueue) RemoveJobs(namespace, backupID string) int {
	if q == nil {
		return 0
	}
	return removeJobs(q.queueDir(), namespace, backupID)
}

func removeJobs(dir, namespace, backupID string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	prefix := namespace + "~~" + backupID + "~~"
	n := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			n++
		}
	}
	return n
}

// FailedJobs counts upload jobs that gave up and wait for a retry.
func (q *UploadQueue) FailedJobs() int {
	entries, err := os.ReadDir(q.queueDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".failed" {
			n++
		}
	}
	return n
}

// PendingJobs counts persisted job files not yet completed.
func (q *UploadQueue) PendingJobs() int {
	entries, err := os.ReadDir(q.queueDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".job" {
			n++
		}
	}
	return n
}

func (q *UploadQueue) dispatcher() {
	defer q.wg.Done()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		q.scan()
		select {
		case <-q.ctx.Done():
			return
		case <-q.wake:
		case <-ticker.C:
		}
	}
}

func (q *UploadQueue) scan() {
	entries, err := os.ReadDir(q.queueDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".job" {
			continue
		}
		name := e.Name()
		q.mu.Lock()
		if q.inflight[name] {
			q.mu.Unlock()
			continue
		}
		q.inflight[name] = true
		q.mu.Unlock()
		select {
		case q.jobs <- name:
		case <-q.ctx.Done():
			q.mu.Lock()
			q.inflight[name] = false
			q.mu.Unlock()
			return
		}
	}
}

func (q *UploadQueue) worker() {
	defer q.wg.Done()
	for {
		select {
		case <-q.ctx.Done():
			return
		case name := <-q.jobs:
			q.process(name)
		}
	}
}

func (q *UploadQueue) process(name string) {
	path := filepath.Join(q.queueDir(), name)
	defer func() {
		q.mu.Lock()
		q.inflight[name] = false
		q.mu.Unlock()
	}()
	raw, err := os.ReadFile(path)
	if err != nil {
		return // Already completed by another pass.
	}
	var job uploadJob
	if err := json.Unmarshal(raw, &job); err != nil {
		// An unreadable job can never succeed; keep it out of the hot loop.
		_ = os.Rename(path, strings.TrimSuffix(path, ".job")+".failed")
		return
	}
	if _, err := q.repo.LoadState(job.Namespace, job.BackupID); err != nil {
		// The restore point was expired before it was uploaded.
		_ = os.Remove(path)
		return
	}
	q.markUploading(job)
	if job.PackID == "" {
		_ = os.Remove(path)
		q.maybeComplete(job)
		return
	}
	data, err := os.ReadFile(filepath.Join(q.repo.packDir(), job.PackID+".pack"))
	if err != nil {
		// Compaction moved the pack's live chunks into another pack, or
		// garbage collection removed it. Completion works out which packs
		// the restore point still needs and uploads those.
		_ = os.Remove(path)
		q.maybeComplete(job)
		return
	}
	if err := throttleBandwidth(q.ctx, int64(len(data)), q.bandwidthLimit()); err != nil {
		q.backoff(name)
		return
	}
	if err := q.target.Put(q.ctx, objectKeyPack(job.PackID), data); err != nil {
		q.backoff(name)
		return
	}
	_ = os.Remove(path)
	q.mu.Lock()
	delete(q.attempts, name)
	q.mu.Unlock()
	q.maybeComplete(job)
}

func (q *UploadQueue) backoff(name string) {
	q.mu.Lock()
	q.attempts[name]++
	n := q.attempts[name]
	q.mu.Unlock()
	if n >= q.maxAttempts {
		// Park the job so the dispatcher stops retrying it every tick; it is
		// re-armed on restart or when the restore point is enqueued again.
		// The restore point is marked failed so the UI never claims it is
		// still queued.
		q.markFailed(name)
		path := filepath.Join(q.queueDir(), name)
		_ = os.Rename(path, strings.TrimSuffix(path, ".job")+".failed")
		q.mu.Lock()
		delete(q.attempts, name)
		q.mu.Unlock()
		return
	}
	d := q.baseBackoff * time.Duration(1<<uint(min(n, 6)))
	jitter := time.Duration(rand.Int63n(int64(q.baseBackoff) + 1))
	select {
	case <-time.After(d + jitter):
	case <-q.ctx.Done():
		return
	}
	q.signal()
}

func (q *UploadQueue) markUploading(job uploadJob) {
	state, err := q.repo.LoadState(job.Namespace, job.BackupID)
	if err != nil {
		return
	}
	if state.Remote == RemoteQueued {
		state.Remote = RemoteUploading
		if state.UploadStartedNS == 0 {
			state.UploadStartedNS = time.Now().UnixNano()
		}
		_ = q.repo.writeState(state)
	}
}

func (q *UploadQueue) markFailed(jobName string) {
	raw, err := os.ReadFile(filepath.Join(q.queueDir(), jobName))
	if err != nil {
		return
	}
	var job uploadJob
	if json.Unmarshal(raw, &job) != nil {
		return
	}
	state, err := q.repo.LoadState(job.Namespace, job.BackupID)
	if err != nil {
		return
	}
	state.Remote = RemoteFailed
	_ = q.repo.writeState(state)
}

// maybeComplete promotes a restore point to protected once every one of its
// packs is present remotely and its manifest has been uploaded. Completion uses
// a bounded number of HEAD checks at the end rather than a per-chunk HEAD on the
// hot path.
func (q *UploadQueue) maybeComplete(job uploadJob) {
	state, err := q.repo.LoadState(job.Namespace, job.BackupID)
	if err != nil {
		return
	}
	if q.jobsPending(job.Namespace, job.BackupID) {
		return // another worker completes the restore point
	}
	// A restore point needs every pack its chunks live in, including packs
	// written by other captures through deduplication and packs produced by
	// compaction, not only the packs its own capture committed.
	needed, err := q.repo.packsFor(job.Namespace, job.BackupID)
	if err != nil {
		return
	}
	for _, packID := range needed {
		ok, _, err := q.target.Head(q.ctx, objectKeyPack(packID))
		if err != nil {
			return
		}
		if ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(q.repo.packDir(), packID+".pack"))
		if err != nil {
			return
		}
		if err := throttleBandwidth(q.ctx, int64(len(data)), q.bandwidthLimit()); err != nil {
			return
		}
		if err := q.target.Put(q.ctx, objectKeyPack(packID), data); err != nil {
			return
		}
	}
	manRaw, err := os.ReadFile(filepath.Join(q.repo.root, "snapshots", job.Namespace, job.BackupID+".snap"))
	if err != nil {
		return
	}
	state.Remote = RemoteVerifying
	_ = q.repo.writeState(state)
	if err := throttleBandwidth(q.ctx, int64(len(manRaw)), q.bandwidthLimit()); err != nil {
		return
	}
	if err := q.target.Put(q.ctx, objectKeyManifest(job.Namespace, job.BackupID), manRaw); err != nil {
		return
	}
	// Upload a self-contained location map so a remote-only host can find every
	// chunk this restore point needs, including chunks stored in packs written
	// by other workloads via deduplication.
	locRaw, err := q.repo.buildLocmap(job.Namespace, job.BackupID)
	if err != nil {
		return
	}
	if err := throttleBandwidth(q.ctx, int64(len(locRaw)), q.bandwidthLimit()); err != nil {
		return
	}
	if err := q.target.Put(q.ctx, objectKeyLocmap(job.Namespace, job.BackupID), locRaw); err != nil {
		return
	}
	state.Remote = RemoteProtected
	state.UploadEndedNS = time.Now().UnixNano()
	_ = q.repo.writeState(state)
}

// jobsPending reports whether any upload job of the restore point is still
// queued or in flight.
func (q *UploadQueue) jobsPending(namespace, backupID string) bool {
	entries, err := os.ReadDir(q.queueDir())
	if err != nil {
		return false
	}
	prefix := namespace + "~~" + backupID + "~~"
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && filepath.Ext(e.Name()) == ".job" {
			return true
		}
	}
	return false
}

func throttleBandwidth(ctx context.Context, n, bps int64) error {
	if n <= 0 || bps <= 0 {
		return nil
	}
	wait := time.Duration(n) * time.Second / time.Duration(bps)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
