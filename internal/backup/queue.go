package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
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

// retryPolicy paces failed uploads. Object stores throttle and drop
// connections for seconds to minutes at a time; retries back off
// exponentially up to max, a job that keeps failing is parked as .failed and
// re-armed after rearm, so a long outage never needs a restart to recover.
type retryPolicy struct {
	base     time.Duration
	max      time.Duration
	attempts int
	rearm    time.Duration
	tick     time.Duration
}

// queueRetry is the production policy; tests shorten it.
var queueRetry = retryPolicy{base: 2 * time.Second, max: 2 * time.Minute, attempts: 10, rearm: 30 * time.Minute, tick: time.Second}

// UploadQueue uploads pack objects to a Target independently of capture. It
// is a bounded worker pool fed from an on-disk job directory, so pending
// uploads survive a process restart and resume automatically. Capture never
// blocks on upload.
//
// Restore points upload one at a time, oldest first: the workers share the
// packs of one backup, then its manifest and location map are uploaded and
// verified, and only then does the next backup start. A restore point is
// marked protected only after every pack it needs is confirmed on the target
// and the manifest and location map read back at their expected size.
type UploadQueue struct {
	repo      *Repository
	target    Target
	workers   int
	retry     retryPolicy
	limitBPS  int64
	targetKey string

	mu        sync.Mutex
	inflight  map[string]bool
	attempts  map[string]int
	notBefore map[string]time.Time
	lastRearm time.Time

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
		repo:      repo,
		target:    target,
		workers:   workers,
		retry:     queueRetry,
		limitBPS:  limitBPS,
		inflight:  map[string]bool{},
		attempts:  map[string]int{},
		notBefore: map[string]time.Time{},
		jobs:      make(chan string, workers),
		wake:      make(chan struct{}, 1),
	}
}

// SetTargetKey names the target for the uploaded-pack index. Without it
// every pack is checked with a HEAD before a restore point is protected.
func (q *UploadQueue) SetTargetKey(key string) {
	if q != nil {
		q.targetKey = key
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

func (q *UploadQueue) legacyQueueDir() string { return filepath.Join(q.repo.root, "state", "queue") }

// queueDir holds this target's jobs. Each target has its own directory, so a
// job is only ever uploaded, and its restore point only ever marked protected,
// by the target it was queued for.
func (q *UploadQueue) queueDir() string {
	if q.targetKey == "" {
		return q.legacyQueueDir()
	}
	return filepath.Join(q.legacyQueueDir(), sanitize(q.targetKey))
}

// adoptLegacyJobs moves jobs queued before targets had their own directory
// into this queue.
func (q *UploadQueue) adoptLegacyJobs() {
	if q.targetKey == "" {
		return
	}
	entries, err := os.ReadDir(q.legacyQueueDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || (filepath.Ext(e.Name()) != ".job" && filepath.Ext(e.Name()) != ".failed") {
			continue
		}
		_ = os.Rename(filepath.Join(q.legacyQueueDir(), e.Name()), filepath.Join(q.queueDir(), e.Name()))
	}
}

// Discard stops the queue and deletes its pending jobs and uploaded-pack
// index, for a target that no longer exists.
func (q *UploadQueue) Discard() {
	if q == nil {
		return
	}
	q.Stop()
	if q.targetKey == "" {
		return
	}
	_ = os.RemoveAll(q.queueDir())
	q.repo.ClearUploaded(q.targetKey)
}

// Start launches the workers and the dispatcher and immediately picks up any
// jobs already persisted on disk (restart recovery).
func (q *UploadQueue) Start(ctx context.Context) error {
	if err := os.MkdirAll(q.queueDir(), 0o750); err != nil {
		return err
	}
	q.adoptLegacyJobs()
	q.retryFailed()
	q.lastRearm = time.Now()
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

func jobName(namespace, backupID, packID string) string {
	return namespace + "~~" + backupID + "~~" + firstNonEmpty(packID, "manifest") + ".job"
}

func isManifestJob(name string) bool { return strings.HasSuffix(name, "~~manifest.job") }

// jobGroup is the restore point a job file belongs to.
func jobGroup(name string) string {
	if i := strings.LastIndex(name, "~~"); i > 0 {
		return name[:i]
	}
	return name
}

// EnqueueBackup persists an upload job for every pack in the restore point and
// marks it queued. It returns immediately without uploading anything.
func (q *UploadQueue) EnqueueBackup(state *PointState) error {
	if err := os.MkdirAll(q.queueDir(), 0o750); err != nil {
		return err
	}
	var total int64
	for _, packID := range state.Packs {
		if st, err := os.Stat(filepath.Join(q.repo.packDir(), packID+".pack")); err == nil {
			total += st.Size()
		}
	}
	// The manifest job completes the restore point once its packs are up,
	// even when its capture deduplicated everything and committed no pack.
	for _, packID := range append(append([]string(nil), state.Packs...), "") {
		job := uploadJob{Namespace: state.Namespace, BackupID: state.BackupID, PackID: packID}
		raw, err := json.Marshal(job)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(q.queueDir(), jobName(state.Namespace, state.BackupID, packID)), raw); err != nil {
			return err
		}
	}
	q.repo.stateMu.Lock()
	state.Remote = RemoteQueued
	state.RemoteError = ""
	state.UploadedBytes, state.UploadTotalBytes = 0, total
	err := q.repo.writeState(state)
	q.repo.stateMu.Unlock()
	if err != nil {
		return err
	}
	q.signal()
	return nil
}

// retryFailed re-arms jobs that exhausted their attempts.
func (q *UploadQueue) retryFailed() int {
	entries, err := os.ReadDir(q.queueDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".failed" {
			continue
		}
		old := filepath.Join(q.queueDir(), e.Name())
		if os.Rename(old, strings.TrimSuffix(old, ".failed")+".job") == nil {
			n++
		}
	}
	return n
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
	return q.countExt(".failed")
}

// PendingJobs counts persisted job files not yet completed.
func (q *UploadQueue) PendingJobs() int {
	return q.countExt(".job")
}

func (q *UploadQueue) countExt(ext string) int {
	entries, err := os.ReadDir(q.queueDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		// Manifest jobs are bookkeeping that waits on the packs; only pack
		// uploads count.
		if filepath.Ext(e.Name()) == ext && !strings.HasSuffix(strings.TrimSuffix(e.Name(), ext), "~~manifest") {
			n++
		}
	}
	return n
}

func (q *UploadQueue) dispatcher() {
	defer q.wg.Done()
	ticker := time.NewTicker(q.retry.tick)
	defer ticker.Stop()
	for {
		if time.Since(q.lastRearm) >= q.retry.rearm {
			q.lastRearm = time.Now()
			if n := q.retryFailed(); n > 0 {
				log.Printf("backup upload: retrying %d parked upload job(s)", n)
			}
		}
		q.scan()
		select {
		case <-q.ctx.Done():
			return
		case <-q.wake:
		case <-ticker.C:
		}
	}
}

// scan hands out the jobs of the oldest restore point that still has work.
// Its manifest job goes out only once none of its pack jobs remain.
func (q *UploadQueue) scan() {
	entries, err := os.ReadDir(q.queueDir())
	if err != nil {
		return
	}
	type group struct {
		oldest time.Time
		jobs   []string
	}
	groups := map[string]*group{}
	parked := map[string]bool{}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".failed" {
			parked[jobGroup(e.Name())] = true
		}
	}
	for _, e := range entries {
		// A restore point with a parked upload waits for the re-arm as a
		// whole, and does not hold up the backups queued after it.
		if filepath.Ext(e.Name()) != ".job" || parked[jobGroup(e.Name())] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		g := groups[jobGroup(e.Name())]
		if g == nil {
			g = &group{oldest: info.ModTime()}
			groups[jobGroup(e.Name())] = g
		}
		if info.ModTime().Before(g.oldest) {
			g.oldest = info.ModTime()
		}
		g.jobs = append(g.jobs, e.Name())
	}
	if len(groups) == 0 {
		return
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := groups[keys[i]], groups[keys[j]]
		if !a.oldest.Equal(b.oldest) {
			return a.oldest.Before(b.oldest)
		}
		return keys[i] < keys[j]
	})
	g := groups[keys[0]]
	sort.Strings(g.jobs)
	packsLeft := 0
	for _, name := range g.jobs {
		if !isManifestJob(name) {
			packsLeft++
		}
	}
	now := time.Now()
	for _, name := range g.jobs {
		if isManifestJob(name) && packsLeft > 0 {
			continue
		}
		q.mu.Lock()
		if q.inflight[name] || now.Before(q.notBefore[name]) {
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
		q.signal()
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
		if err := q.complete(job); err != nil {
			q.fail(name, job, fmt.Errorf("finishing the upload: %w", err))
			return
		}
		q.done(name)
		return
	}
	packPath := filepath.Join(q.repo.packDir(), job.PackID+".pack")
	data, err := os.ReadFile(packPath)
	if err != nil {
		// Compaction moved the pack's live chunks into another pack, or
		// garbage collection removed it. Completion works out which packs
		// the restore point still needs and uploads those.
		q.done(name)
		return
	}
	if err := q.putPack(job.PackID, data); err != nil {
		q.fail(name, job, fmt.Errorf("uploading pack %s: %w", shortID(job.PackID), err))
		return
	}
	q.done(name)
	q.addProgress(job, int64(len(data)))
}

func (q *UploadQueue) putPack(packID string, data []byte) error {
	if err := throttleBandwidth(q.ctx, int64(len(data)), q.bandwidthLimit()); err != nil {
		return err
	}
	if err := q.target.Put(q.ctx, objectKeyPack(packID), data); err != nil {
		return err
	}
	_ = q.repo.MarkUploaded(q.targetKey, packID)
	return nil
}

func (q *UploadQueue) done(name string) {
	_ = os.Remove(filepath.Join(q.queueDir(), name))
	q.mu.Lock()
	delete(q.attempts, name)
	delete(q.notBefore, name)
	q.mu.Unlock()
}

// fail records the error, logs it and schedules a retry; after the last
// attempt the job is parked and the restore point marked failed.
func (q *UploadQueue) fail(name string, job uploadJob, err error) {
	if q.ctx.Err() != nil {
		return // shutting down; the job resumes on the next start
	}
	q.mu.Lock()
	q.attempts[name]++
	n := q.attempts[name]
	q.mu.Unlock()
	msg := sanitizeRemoteErr(err)
	if n >= q.retry.attempts {
		log.Printf("backup upload: %s/%s gave up after %d attempts: %s (retried again in %s)", job.Namespace, job.BackupID, n, msg, q.retry.rearm)
		q.updateState(job, func(st *PointState) bool {
			st.Remote, st.RemoteError = RemoteFailed, msg
			return true
		})
		path := filepath.Join(q.queueDir(), name)
		_ = os.Rename(path, strings.TrimSuffix(path, ".job")+".failed")
		q.mu.Lock()
		delete(q.attempts, name)
		delete(q.notBefore, name)
		q.mu.Unlock()
		return
	}
	d := q.retry.base << uint(min(n-1, 16))
	if d > q.retry.max || d <= 0 {
		d = q.retry.max
	}
	d += time.Duration(rand.Int63n(int64(d)/4 + 1))
	log.Printf("backup upload: %s/%s attempt %d failed, retrying in %s: %s", job.Namespace, job.BackupID, n, d.Round(time.Second), msg)
	q.updateState(job, func(st *PointState) bool {
		if st.RemoteError == msg {
			return false
		}
		st.RemoteError = msg
		return true
	})
	q.mu.Lock()
	q.notBefore[name] = time.Now().Add(d)
	q.mu.Unlock()
}

// updateState applies fn to the restore point's state under the state lock;
// fn returns whether to write it back.
func (q *UploadQueue) updateState(job uploadJob, fn func(*PointState) bool) {
	q.repo.stateMu.Lock()
	defer q.repo.stateMu.Unlock()
	state, err := q.repo.LoadState(job.Namespace, job.BackupID)
	if err != nil {
		return
	}
	if fn(state) {
		_ = q.repo.writeState(state)
	}
}

func (q *UploadQueue) markUploading(job uploadJob) {
	q.updateState(job, func(st *PointState) bool {
		if st.Remote != RemoteQueued && st.Remote != RemoteFailed {
			return false
		}
		st.Remote = RemoteUploading
		if st.UploadStartedNS == 0 {
			st.UploadStartedNS = time.Now().UnixNano()
		}
		return true
	})
}

func (q *UploadQueue) addProgress(job uploadJob, n int64) {
	q.updateState(job, func(st *PointState) bool {
		st.UploadedBytes += n
		return true
	})
}

// complete uploads any pack the restore point needs that the target lacks,
// then the manifest and location map, and verifies them. Errors leave the
// manifest job in the queue for a retry.
func (q *UploadQueue) complete(job uploadJob) error {
	if _, err := q.repo.LoadState(job.Namespace, job.BackupID); err != nil {
		return nil // expired meanwhile
	}
	// A restore point needs every pack its chunks live in, including packs
	// written by other captures through deduplication and packs produced by
	// compaction, not only the packs its own capture committed.
	needed, err := q.repo.packsFor(job.Namespace, job.BackupID)
	if err != nil {
		return err
	}
	known := q.repo.LoadUploaded(q.targetKey)
	for _, packID := range needed {
		if _, ok := known[packID]; ok && q.targetKey != "" {
			continue
		}
		local, err := os.Stat(filepath.Join(q.repo.packDir(), packID+".pack"))
		if err != nil {
			return fmt.Errorf("pack %s is missing locally: %w", shortID(packID), err)
		}
		ok, size, err := q.target.Head(q.ctx, objectKeyPack(packID))
		if err != nil {
			return fmt.Errorf("checking pack %s: %w", shortID(packID), err)
		}
		if ok && (size <= 0 || size == local.Size()) {
			_ = q.repo.MarkUploaded(q.targetKey, packID)
			continue
		}
		data, err := os.ReadFile(filepath.Join(q.repo.packDir(), packID+".pack"))
		if err != nil {
			return err
		}
		if err := q.putPack(packID, data); err != nil {
			return fmt.Errorf("uploading pack %s: %w", shortID(packID), err)
		}
	}
	manRaw, err := os.ReadFile(filepath.Join(q.repo.root, "snapshots", job.Namespace, job.BackupID+".snap"))
	if err != nil {
		return err
	}
	q.updateState(job, func(st *PointState) bool {
		st.Remote = RemoteVerifying
		return true
	})
	// Upload a self-contained location map so a remote-only host can find
	// every chunk this restore point needs, including chunks stored in packs
	// written by other workloads via deduplication.
	locRaw, err := q.repo.buildLocmap(job.Namespace, job.BackupID)
	if err != nil {
		return err
	}
	for _, obj := range []struct {
		key  string
		data []byte
	}{{objectKeyManifest(job.Namespace, job.BackupID), manRaw}, {objectKeyLocmap(job.Namespace, job.BackupID), locRaw}} {
		if err := throttleBandwidth(q.ctx, int64(len(obj.data)), q.bandwidthLimit()); err != nil {
			return err
		}
		if err := q.target.Put(q.ctx, obj.key, obj.data); err != nil {
			return fmt.Errorf("uploading %s: %w", filepath.Base(obj.key), err)
		}
		ok, size, err := q.target.Head(q.ctx, obj.key)
		if err != nil {
			return fmt.Errorf("verifying %s: %w", filepath.Base(obj.key), err)
		}
		if !ok || (size > 0 && size != int64(len(obj.data))) {
			return fmt.Errorf("verifying %s: the target reports %d bytes, expected %d", filepath.Base(obj.key), size, len(obj.data))
		}
	}
	var uploaded int64
	q.updateState(job, func(st *PointState) bool {
		st.Remote, st.RemoteError = RemoteProtected, ""
		st.UploadEndedNS = time.Now().UnixNano()
		if st.UploadTotalBytes > 0 {
			st.UploadedBytes = st.UploadTotalBytes
		}
		uploaded = st.UploadedBytes
		return true
	})
	log.Printf("backup upload: %s/%s protected (%d bytes uploaded)", job.Namespace, job.BackupID, uploaded)
	return nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// sanitizeRemoteErr keeps an upload error short and on one line. Transport
// errors carry the HTTP status and the store's error code, never credentials.
func sanitizeRemoteErr(err error) string {
	msg := strings.Join(strings.Fields(err.Error()), " ")
	if strings.Contains(msg, "401 Unauthorized") || strings.Contains(msg, "AccessDenied") || strings.Contains(msg, "403 Forbidden") {
		msg += " (the access key is not allowed to do this; it needs Object Read & Write on the bucket)"
	}
	if len(msg) > 400 {
		msg = msg[:400]
	}
	return msg
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
