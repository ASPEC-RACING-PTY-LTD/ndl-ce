package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Regression tests for the October 2026 root-disk exhaustion: retention that
// kept the wrong points, garbage collection that never reclaimed partially
// dead packs, failed captures and crashes that left data behind, upload jobs
// that spun forever, and remote copies that were never expired.

func daysAgo(base time.Time, d int) int64 { return base.AddDate(0, 0, -d).UnixNano() }

func pointsEveryDay(base time.Time, days int) []PointState {
	var out []PointState
	for i := 0; i < days; i++ {
		out = append(out, PointState{BackupID: "d" + itoa(i), CreatedAtNS: daysAgo(base, i)})
	}
	return out
}

func keptIDs(all, expired []PointState) map[string]bool {
	gone := map[string]bool{}
	for _, s := range expired {
		gone[s.BackupID] = true
	}
	kept := map[string]bool{}
	for _, s := range all {
		if !gone[s.BackupID] {
			kept[s.BackupID] = true
		}
	}
	return kept
}

func TestRetentionOneDailyWeeklyMonthlyKeepsSpreadPoints(t *testing.T) {
	// Thursday 8 October 2026, nightly points for 70 days.
	base := time.Date(2026, 10, 8, 19, 30, 0, 0, time.UTC)
	pts := pointsEveryDay(base, 70)
	kept := keptIDs(pts, RetentionPolicy{Daily: 1, Weekly: 1, Monthly: 1}.SelectExpired(pts))
	// Today; Sunday 4 Oct (newest of the previous ISO week); 30 Sep (newest
	// of September). The old behaviour kept 8, 7 and 6 October.
	want := map[string]bool{"d0": true, "d4": true, "d8": true}
	if len(kept) != len(want) {
		t.Fatalf("kept %v, want %v", kept, want)
	}
	for id := range want {
		if !kept[id] {
			t.Fatalf("kept %v, want %v", kept, want)
		}
	}
}

func TestRetentionTwoDailyOneWeeklyOneMonthly(t *testing.T) {
	base := time.Date(2026, 10, 8, 19, 30, 0, 0, time.UTC)
	pts := pointsEveryDay(base, 70)
	kept := keptIDs(pts, RetentionPolicy{Daily: 2, Weekly: 1, Monthly: 1}.SelectExpired(pts))
	want := map[string]bool{"d0": true, "d1": true, "d4": true, "d8": true}
	if len(kept) != len(want) {
		t.Fatalf("kept %v, want %v", kept, want)
	}
	for id := range want {
		if !kept[id] {
			t.Fatalf("kept %v, want %v", kept, want)
		}
	}
}

func TestRetentionSameWeekKeepsOnlyNewest(t *testing.T) {
	// Three points in one week and month: weekly and monthly are already
	// covered by the daily point, so nothing else is kept.
	base := time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)
	pts := pointsEveryDay(base, 3)
	kept := keptIDs(pts, RetentionPolicy{Daily: 1, Weekly: 1, Monthly: 1}.SelectExpired(pts))
	if len(kept) != 1 || !kept["d0"] {
		t.Fatalf("kept %v, want only d0", kept)
	}
}

func TestRetentionNeverExpiresNewestPoint(t *testing.T) {
	base := time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)
	pts := pointsEveryDay(base, 5)
	kept := keptIDs(pts, RetentionPolicy{}.SelectExpired(pts))
	if len(kept) != 1 || !kept["d0"] {
		t.Fatalf("an empty policy must still keep the newest point, kept %v", kept)
	}
}

// capturePair writes a big and a small file, captures, then replaces only the
// big file and captures again. Both captures' chunks of the big file share
// packs with the small file's chunks.
func capturePair(t *testing.T, e *Engine, src string) (*PointState, *Manifest, *PointState) {
	t.Helper()
	writeFile(t, filepath.Join(src, "small.txt"), randBytes(1, 2<<10), 0o644)
	writeFile(t, filepath.Join(src, "big.bin"), randBytes(2, 600<<10), 0o644)
	_, s1, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "w", WorkloadName: "w"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "big.bin"), randBytes(3, 600<<10), 0o644)
	m2, s2, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "w", WorkloadName: "w"})
	if err != nil {
		t.Fatal(err)
	}
	return s1, m2, s2
}

func TestCompactionReclaimsPartiallyDeadPacks(t *testing.T) {
	cfg := smallCfg()
	cfg.PackTarget = 8 << 20 // one pack per capture: small and big share it
	e := testEngine(t, cfg)
	src := t.TempDir()
	s1, m2, _ := capturePair(t, e, src)
	if err := e.Repo().DeleteRestorePoint(s1.Namespace, s1.BackupID); err != nil {
		t.Fatal(err)
	}
	plain, err := e.Repo().CollectGarbage()
	if err != nil {
		t.Fatal(err)
	}
	if plain.PacksDeleted != 0 || plain.DeadBytes < 400<<10 {
		t.Fatalf("the first pack still holds small.txt, so whole-pack GC cannot free it: %+v", plain)
	}
	before, _ := e.Repo().SizeOnDisk()
	stats, err := e.Repo().CollectGarbageWith(GCOptions{RepackLiveRatio: DefaultRepackLiveRatio})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := e.Repo().SizeOnDisk()
	if stats.PacksRepacked != 1 || stats.BytesFreed < 400<<10 || after >= before-400<<10 {
		t.Fatalf("compaction must reclaim the dead big.bin chunks: %+v before=%d after=%d", stats, before, after)
	}
	dst := t.TempDir()
	if err := e.Repo().Restore(context.Background(), m2, RestoreOptions{Dest: dst}); err != nil {
		t.Fatalf("the remaining restore point must restore after compaction: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dst, "small.txt"))
	if !bytes.Equal(got, randBytes(1, 2<<10)) {
		t.Fatal("small.txt changed after compaction")
	}
	// Reopening rebuilds the index from the compacted packs.
	repo2, err := OpenRepository(e.Repo().Root(), testKeys(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo2.Verify(m2); err != nil {
		t.Fatalf("index after reopen: %v", err)
	}
}

func TestCompactionRespectsFreeSpaceReserve(t *testing.T) {
	cfg := smallCfg()
	cfg.PackTarget = 8 << 20
	e := testEngine(t, cfg)
	s1, _, _ := capturePair(t, e, t.TempDir())
	_ = e.Repo().DeleteRestorePoint(s1.Namespace, s1.BackupID)
	stats, err := e.Repo().CollectGarbageWith(GCOptions{RepackLiveRatio: DefaultRepackLiveRatio, ReserveBytes: 1 << 62})
	if err != nil {
		t.Fatal(err)
	}
	if hostFreeSupported() && (stats.PacksRepacked != 0 || stats.RepackSkipped == "") {
		t.Fatalf("compaction must not cross the reserve: %+v", stats)
	}
}

func hostFreeSupported() bool {
	free, err := hostFreeBytes(os.TempDir())
	return err == nil && free < 1<<62
}

func TestFailedCaptureGarbageIsReclaimed(t *testing.T) {
	cfg := smallCfg()
	e := testEngine(t, cfg)
	src := t.TempDir()
	for i := 0; i < 8; i++ {
		writeFile(t, filepath.Join(src, "f"+itoa(i)+".bin"), randBytes(int64(40+i), 200<<10), 0o644)
	}
	// Stop the capture after a few packs were committed, the way the
	// workspace ceiling or a cancelled request does.
	e.cfg.MaxLocalBytes = 300 << 10
	_, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "x", WorkloadName: "x"})
	if !errors.Is(err, ErrWorkspaceFull) {
		t.Fatalf("expected the workspace limit to stop the capture, got %v", err)
	}
	u, _ := e.Repo().Usage()
	if u.PackBytes == 0 || u.RestorePoints != 0 {
		t.Fatalf("expected committed packs without a restore point: %+v", u)
	}
	stats, err := e.Repo().CollectGarbage()
	if err != nil {
		t.Fatal(err)
	}
	u, _ = e.Repo().Usage()
	if stats.PacksDeleted == 0 || u.PackBytes != 0 {
		t.Fatalf("garbage from the failed capture must be reclaimed: %+v %+v", stats, u)
	}
	// With room again the next capture succeeds: exhaustion is recoverable.
	e.cfg.MaxLocalBytes = 0
	if _, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "x", WorkloadName: "x"}); err != nil {
		t.Fatalf("capture after recovery: %v", err)
	}
}

func TestHostReserveStopsCaptureCleanly(t *testing.T) {
	if !hostFreeSupported() {
		t.Skip("free space is not measurable here")
	}
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.bin"), randBytes(5, 128<<10), 0o644)
	e.cfg.MinHostFreeBytes = 1 << 62
	_, _, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "r", WorkloadName: "r"})
	if !errors.Is(err, ErrWorkspaceFull) || !strings.Contains(err.Error(), "reserve") {
		t.Fatalf("expected a clear reserve error, got %v", err)
	}
	if u, _ := e.Repo().Usage(); u.PackBytes != 0 {
		t.Fatalf("a refused capture must not write packs: %+v", u)
	}
}

func TestOpenRecoversFromCrashLeftovers(t *testing.T) {
	root := t.TempDir()
	repo, err := OpenRepository(root, testKeys(t))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(repo, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.txt"), randBytes(9, 50<<10), 0o644)
	m, st, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "c", WorkloadName: "c"})
	if err != nil {
		t.Fatal(err)
	}
	// Crash leftovers: half-written files, a pack without its index, and a
	// committed manifest whose state sidecar was never written.
	writeFile(t, filepath.Join(root, "packs", "pack-dead.pack.tmp"), make([]byte, 4096), 0o640)
	writeFile(t, filepath.Join(root, "packs", "pack-orphan.pack"), make([]byte, 8192), 0o640)
	writeFile(t, filepath.Join(root, "snapshots", st.Namespace, "x.state.tmp"), []byte("{"), 0o640)
	writeFile(t, filepath.Join(root, "state", "queue", "j.job.tmp"), []byte("{"), 0o640)
	if err := os.Remove(filepath.Join(root, "snapshots", st.Namespace, st.BackupID+".state")); err != nil {
		t.Fatal(err)
	}

	repo2, err := OpenRepository(root, testKeys(t))
	if err != nil {
		t.Fatal(err)
	}
	rec := repo2.Recovery()
	if rec.TempFilesRemoved != 3 || rec.OrphanPacksRemoved != 1 || rec.StatesRebuilt != 1 {
		t.Fatalf("recovery %+v", rec)
	}
	assertNoTempFiles(t, root)
	if _, err := os.Stat(filepath.Join(root, "packs", "pack-orphan.pack")); !os.IsNotExist(err) {
		t.Fatal("orphan pack must be removed")
	}
	ps, err := repo2.LoadState(st.Namespace, st.BackupID)
	if err != nil || !ps.Recovered || !ps.LocalComplete {
		t.Fatalf("state must be rebuilt and marked recovered: %+v %v", ps, err)
	}
	dst := t.TempDir()
	if err := repo2.Restore(context.Background(), m, RestoreOptions{Dest: dst}); err != nil {
		t.Fatalf("the recovered restore point must still restore: %v", err)
	}
}

func TestGCStopsWhenAManifestCannotBeRead(t *testing.T) {
	e := testEngine(t, smallCfg())
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.txt"), randBytes(10, 50<<10), 0o644)
	_, st, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "g", WorkloadName: "g"})
	if err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(e.Repo().Root(), "snapshots", st.Namespace, st.BackupID+".snap")
	if err := os.WriteFile(snap, []byte("corrupt"), 0o640); err != nil {
		t.Fatal(err)
	}
	before, _ := e.Repo().Usage()
	_, err = e.Repo().CollectGarbageWith(GCOptions{RepackLiveRatio: DefaultRepackLiveRatio})
	if err == nil || !strings.Contains(err.Error(), "no backup data was removed") {
		t.Fatalf("GC must refuse to guess, got %v", err)
	}
	after, _ := e.Repo().Usage()
	if after.PackBytes != before.PackBytes {
		t.Fatal("GC deleted packs while a manifest was unreadable")
	}
}

func TestExpiredPointUploadJobsAreDropped(t *testing.T) {
	e := testEngine(t, smallCfg())
	target := NewMemTarget()
	target.Delay = 200 * time.Millisecond
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.bin"), randBytes(11, 400<<10), 0o644)
	_, st, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "q", WorkloadName: "q"})
	if err != nil {
		t.Fatal(err)
	}
	q := NewUploadQueue(e.Repo(), target, 1)
	if err := q.EnqueueBackup(st); err != nil {
		t.Fatal(err)
	}
	if err := e.Repo().DeleteRestorePoint(st.Namespace, st.BackupID); err != nil {
		t.Fatal(err)
	}
	if n := q.RemoveJobs(st.Namespace, st.BackupID); n == 0 {
		t.Fatal("expected queued jobs to be removed")
	}
	if q.PendingJobs() != 0 {
		t.Fatal("jobs of an expired point must not stay queued")
	}
	// A job left behind (for example from before an upgrade) is dropped by
	// the worker instead of retried forever.
	if err := q.EnqueueBackup(&PointState{Namespace: st.Namespace, BackupID: st.BackupID, Packs: st.Packs}); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(e.Repo().Root(), "snapshots", st.Namespace, st.BackupID+".state"))
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q.Stop()
	waitFor(t, 5*time.Second, func() bool { return q.PendingJobs() == 0 })
	if target.PutCount() != 0 {
		t.Fatalf("nothing of an expired point may be uploaded, got %d puts", target.PutCount())
	}
}

func TestExhaustedUploadJobIsParkedNotSpinning(t *testing.T) {
	e := testEngine(t, smallCfg())
	target := NewMemTarget()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f.bin"), randBytes(27, 64<<10), 0o644)
	_, st, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "p", WorkloadName: "p"})
	if err != nil {
		t.Fatal(err)
	}
	for _, packID := range st.Packs {
		target.FailFor(objectKeyPack(packID), 1000)
	}
	q := NewUploadQueue(e.Repo(), target, 1)
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueueBackup(st); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool { return q.FailedJobs() == len(st.Packs) })
	if q.PendingJobs() != 0 {
		t.Fatal("a parked job must not count as pending")
	}
	target.mu.Lock()
	left := target.failuresK[objectKeyPack(st.Packs[0])]
	target.mu.Unlock()
	time.Sleep(500 * time.Millisecond)
	target.mu.Lock()
	left2 := target.failuresK[objectKeyPack(st.Packs[0])]
	target.mu.Unlock()
	if left2 != left {
		t.Fatalf("a parked job kept retrying: %d -> %d remaining failures", left, left2)
	}
	q.Stop()

	// After a restart the job is retried and completes once the target works.
	for _, packID := range st.Packs {
		target.FailFor(objectKeyPack(packID), 0)
	}
	q2 := NewUploadQueue(e.Repo(), target, 1)
	if err := q2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q2.Stop()
	waitFor(t, 10*time.Second, func() bool {
		ps, _ := e.Repo().LoadState(st.Namespace, st.BackupID)
		return ps != nil && ps.Remote == RemoteProtected
	})
}

func TestProtectedPointIncludesPacksWrittenByOtherCaptures(t *testing.T) {
	e := testEngine(t, smallCfg())
	target := NewMemTarget()
	shared := randBytes(31, 256<<10)
	srcA := t.TempDir()
	writeFile(t, filepath.Join(srcA, "base.img"), shared, 0o644)
	if _, _, err := e.Capture(context.Background(), CaptureOptions{Source: srcA, WorkloadID: "a", WorkloadName: "a"}); err != nil {
		t.Fatal(err)
	}
	// B deduplicates everything against A's packs and A is never uploaded.
	srcB := t.TempDir()
	writeFile(t, filepath.Join(srcB, "base.img"), shared, 0o644)
	_, sB, err := e.Capture(context.Background(), CaptureOptions{Source: srcB, WorkloadID: "b", WorkloadName: "b"})
	if err != nil {
		t.Fatal(err)
	}
	q := NewUploadQueue(e.Repo(), target, 2)
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q.Stop()
	if len(sB.Packs) != 0 {
		t.Fatalf("B should have deduplicated every chunk, committed %d packs", len(sB.Packs))
	}
	if err := q.EnqueueBackup(sB); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool {
		ps, _ := e.Repo().LoadState(sB.Namespace, sB.BackupID)
		return ps != nil && ps.Remote == RemoteProtected
	})
	repo, man, err := FetchRemote(context.Background(), target, testKeys(t), sB.Namespace, sB.BackupID, t.TempDir())
	if err != nil {
		t.Fatalf("a protected restore point must be restorable from the remote alone: %v", err)
	}
	if err := repo.Restore(context.Background(), man, RestoreOptions{Dest: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func protect(t *testing.T, e *Engine, target Target, st *PointState) {
	t.Helper()
	q := NewUploadQueue(e.Repo(), target, 2)
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q.Stop()
	if err := q.EnqueueBackup(st); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool {
		ps, _ := e.Repo().LoadState(st.Namespace, st.BackupID)
		return ps != nil && ps.Remote == RemoteProtected
	})
}

func remotePacks(t *testing.T, target *MemTarget) int {
	t.Helper()
	objs, _ := target.List(context.Background(), "backups/packs/")
	return len(objs)
}

func TestRemoteExpiryRemovesOnlyUnreferencedPacks(t *testing.T) {
	e := testEngine(t, smallCfg())
	target := NewMemTarget()
	ctx := context.Background()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "keep.bin"), randBytes(41, 128<<10), 0o644)
	writeFile(t, filepath.Join(src, "churn.bin"), randBytes(42, 256<<10), 0o644)
	_, s1, err := e.Capture(ctx, CaptureOptions{Source: src, WorkloadID: "r", WorkloadName: "r"})
	if err != nil {
		t.Fatal(err)
	}
	protect(t, e, target, s1)
	writeFile(t, filepath.Join(src, "churn.bin"), randBytes(43, 256<<10), 0o644)
	m2, s2, err := e.Capture(ctx, CaptureOptions{Source: src, WorkloadID: "r", WorkloadName: "r"})
	if err != nil {
		t.Fatal(err)
	}
	protect(t, e, target, s2)

	// A restore point written with another key (the situation after the
	// local repository and its key were deleted) must neither block the
	// sweep nor be touched by it.
	other, _ := NewKeys(bytes.Repeat([]byte{0x17}, 32))
	foreignLoc, _ := other.SealBytes("locmap:old", []byte(`[{"id":"x","pack":"pack-foreign","off":0,"len":1}]`))
	_ = target.Put(ctx, objectKeyLocmap("old", "p1"), foreignLoc)
	_ = target.Put(ctx, objectKeyPack("pack-foreign"), []byte("old data"))

	packsBefore := remotePacks(t, target)
	if err := e.Repo().DeleteRestorePoint(s1.Namespace, s1.BackupID); err != nil {
		t.Fatal(err)
	}
	res, err := e.Repo().ExpireRemote(ctx, target, "r2", s1.Namespace, s1.BackupID)
	if err != nil || !res.ManifestDeleted || res.CandidatePacks == 0 {
		t.Fatalf("expire remote: %+v %v", res, err)
	}
	if ok, _, _ := target.Head(ctx, objectKeyManifest(s1.Namespace, s1.BackupID)); ok {
		t.Fatal("expired manifest must be deleted remotely")
	}
	sweep, err := e.Repo().SweepRemote(ctx, target, "r2")
	if err != nil {
		t.Fatal(err)
	}
	if sweep.PacksDeleted == 0 || sweep.StillInUse == 0 || sweep.ForeignPoints != 1 {
		t.Fatalf("sweep must delete churned packs and keep shared ones: %+v", sweep)
	}
	if remotePacks(t, target) >= packsBefore {
		t.Fatal("remote packs did not shrink")
	}
	if ok, _, _ := target.Head(ctx, objectKeyPack("pack-foreign")); !ok {
		t.Fatal("a pack of a foreign restore point must never be deleted")
	}
	// The remaining point still restores from the remote alone.
	repo, man, err := FetchRemote(ctx, target, testKeys(t), s2.Namespace, s2.BackupID, t.TempDir())
	if err != nil {
		t.Fatalf("remaining remote point broken by sweep: %v", err)
	}
	if err := repo.Restore(ctx, man, RestoreOptions{Dest: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	_ = m2

	// Expiring the last point frees the rest, except the foreign data.
	_ = e.Repo().DeleteRestorePoint(s2.Namespace, s2.BackupID)
	if _, err := e.Repo().ExpireRemote(ctx, target, "r2", s2.Namespace, s2.BackupID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Repo().SweepRemote(ctx, target, "r2"); err != nil {
		t.Fatal(err)
	}
	if n := remotePacks(t, target); n != 1 {
		t.Fatalf("only the foreign pack may remain, got %d packs", n)
	}
	if e.Repo().PendingSweep("r2") != 0 {
		t.Fatal("sweep candidates must be cleared")
	}
	u, err := e.Repo().MeasureRemote(ctx, target, "r2")
	if err != nil {
		t.Fatal(err)
	}
	if u.ForeignPoints != 1 || u.PackCount != 1 || u.RestorePoints != 1 {
		t.Fatalf("remote usage %+v", u)
	}
}

func TestSweepKeepsPacksALocalPointStillNeeds(t *testing.T) {
	e := testEngine(t, smallCfg())
	target := NewMemTarget()
	ctx := context.Background()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.bin"), randBytes(51, 128<<10), 0o644)
	_, s1, err := e.Capture(ctx, CaptureOptions{Source: src, WorkloadID: "l", WorkloadName: "l"})
	if err != nil {
		t.Fatal(err)
	}
	protect(t, e, target, s1)
	// s2 reuses every chunk of s1 and has not been uploaded yet.
	_, s2, err := e.Capture(ctx, CaptureOptions{Source: src, WorkloadID: "l2", WorkloadName: "l2"})
	if err != nil {
		t.Fatal(err)
	}
	_ = e.Repo().DeleteRestorePoint(s1.Namespace, s1.BackupID)
	if _, err := e.Repo().ExpireRemote(ctx, target, "t", s1.Namespace, s1.BackupID); err != nil {
		t.Fatal(err)
	}
	sweep, err := e.Repo().SweepRemote(ctx, target, "t")
	if err != nil {
		t.Fatal(err)
	}
	if sweep.PacksDeleted != 0 {
		t.Fatalf("packs a local restore point needs must stay remote: %+v", sweep)
	}
	_ = s2
}
