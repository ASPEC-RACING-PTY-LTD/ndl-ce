package backuphost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/backup"
)

func TestLiveSmallestContainerReadOnly(t *testing.T) {
	rootfs := os.Getenv("NDL_LIVE_BACKUP_ROOTFS")
	if rootfs == "" {
		t.Skip("set NDL_LIVE_BACKUP_ROOTFS for live read-only validation")
	}
	id := os.Getenv("NDL_LIVE_BACKUP_ID")
	name := os.Getenv("NDL_LIVE_BACKUP_NAME")
	if name == "" {
		name = "live"
	}
	repo := t.TempDir()
	h, err := Open(Options{Root: repo, Settings: Settings{MaxLocalBytes: 8 << 30, MinHostFreeBytes: 1 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	prev, err := h.Handle(ctx, ActionPreview, rootfs, mustJSON(Request{
		Action: ActionPreview, WorkloadID: id, WorkloadName: name, CaptureMode: backup.CaptureModeSmart,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var preview Result
	_ = json.Unmarshal([]byte(prev.Extra), &preview)
	t.Logf("preview mode=%s protected=%d excluded=%d full=%d items=%d warnings=%d",
		preview.Preview.Mode, preview.Preview.ProtectedBytes, preview.Preview.ExcludedBytes,
		preview.Preview.FullBytes, len(preview.Preview.Items), len(preview.Preview.Warnings))

	t0 := time.Now()
	cap1, err := h.Handle(ctx, ActionCapture, rootfs, mustJSON(Request{
		Action: ActionCapture, WorkloadID: id, WorkloadName: name, CaptureMode: backup.CaptureModeSmart,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var r1 Result
	_ = json.Unmarshal([]byte(cap1.Extra), &r1)
	if !r1.LocalComplete || r1.CaptureMode != backup.CaptureModeSmart {
		t.Fatalf("smart1 %+v", r1)
	}
	t.Logf("smart1 id=%s logical=%d new=%d reused=%d dur=%s", r1.BackupID, r1.LogicalBytes, r1.PhysicalNewData, r1.ChunksReused, time.Since(t0))

	t1 := time.Now()
	cap2, err := h.Handle(ctx, ActionCapture, rootfs, mustJSON(Request{
		Action: ActionCapture, WorkloadID: id, WorkloadName: name, CaptureMode: backup.CaptureModeSmart,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var r2 Result
	_ = json.Unmarshal([]byte(cap2.Extra), &r2)
	t.Logf("smart2 id=%s logical=%d new=%d reused=%d dur=%s", r2.BackupID, r2.LogicalBytes, r2.PhysicalNewData, r2.ChunksReused, time.Since(t1))
	if r2.ChunksReused == 0 {
		t.Fatal("second smart capture should reuse chunks")
	}

	dst := t.TempDir()
	if _, err := h.Handle(ctx, ActionRestore, dst, mustJSON(Request{
		Action: ActionRestore, Namespace: r1.Namespace, BackupID: r1.BackupID, Dest: dst,
	})); err != nil {
		t.Fatal(err)
	}

	t2 := time.Now()
	capF, err := h.Handle(ctx, ActionCapture, rootfs, mustJSON(Request{
		Action: ActionCapture, WorkloadID: id, WorkloadName: name + "-full", CaptureMode: backup.CaptureModeFull,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var rf Result
	_ = json.Unmarshal([]byte(capF.Extra), &rf)
	if rf.CaptureMode != backup.CaptureModeFull || rf.LogicalBytes < r1.LogicalBytes {
		t.Fatalf("full should protect at least as much as smart: smart=%d full=%+v", r1.LogicalBytes, rf)
	}
	t.Logf("full id=%s logical=%d new=%d reused=%d dur=%s", rf.BackupID, rf.LogicalBytes, rf.PhysicalNewData, rf.ChunksReused, time.Since(t2))

	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "var/lib/app/data.db"), []byte("keep"))
	d1, err := h.Handle(ctx, ActionCapture, src, mustJSON(Request{
		Action: ActionCapture, WorkloadID: "disp-1", WorkloadName: "disposable", CaptureMode: backup.CaptureModeSmart,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var rd Result
	_ = json.Unmarshal([]byte(d1.Extra), &rd)
	if _, err := h.Handle(ctx, ActionExpire, "", mustJSON(Request{
		Action: ActionExpire, Namespace: rd.Namespace, BackupID: rd.BackupID,
	})); err != nil {
		t.Fatal(err)
	}
}
