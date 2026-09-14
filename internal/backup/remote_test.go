package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// memTransport is a minimal in-memory objstore.Transport for exercising the
// TransportTarget adapter.
type memTransport struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func newMemTransport() *memTransport { return &memTransport{objs: map[string][]byte{}} }

func (m *memTransport) Put(_ context.Context, bucket, object string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[bucket+"/"+object] = append([]byte(nil), body...)
	return nil
}

func (m *memTransport) Get(_ context.Context, bucket, object string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objs[bucket+"/"+object]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}

func (m *memTransport) Head(_ context.Context, bucket, object string) (bool, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objs[bucket+"/"+object]
	if !ok {
		return false, 0, nil
	}
	return true, int64(len(b)), nil
}

func (m *memTransport) Delete(_ context.Context, bucket, object string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objs, bucket+"/"+object)
	return nil
}

// TestRemoteProtectAndRestoreViaTransport captures two workloads that share
// data, uploads through the objstore Transport adapter, and then restores one
// of them on a host with no local repository. This proves remote protection and
// disaster-recovery restore work end to end, including deduplicated chunks.
func TestRemoteProtectAndRestoreViaTransport(t *testing.T) {
	e := testEngine(t, smallCfg())
	target := TransportTarget{T: newMemTransport(), Bucket: "ndl-backups"}

	shared := randBytes(61, 1<<20)
	srcA := t.TempDir()
	writeFile(t, filepath.Join(srcA, "os/base.img"), shared, 0o644)
	writeFile(t, filepath.Join(srcA, "app/only-a.txt"), []byte("A specific"), 0o644)
	_, stateA, err := e.Capture(context.Background(), CaptureOptions{Source: srcA, WorkloadID: "a", WorkloadName: "wlA"})
	if err != nil {
		t.Fatal(err)
	}

	srcB := t.TempDir()
	writeFile(t, filepath.Join(srcB, "os/base.img"), shared, 0o644) // identical, deduped into A's packs
	writeFile(t, filepath.Join(srcB, "app/only-b.txt"), []byte("B specific content here"), 0o644)
	manB, stateB, err := e.Capture(context.Background(), CaptureOptions{Source: srcB, WorkloadID: "b", WorkloadName: "wlB"})
	if err != nil {
		t.Fatal(err)
	}

	q := NewUploadQueue(e.Repo(), target, 4)
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q.Stop()
	if err := q.EnqueueBackup(stateA); err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueueBackup(stateB); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool {
		ps, _ := e.Repo().LoadState(stateB.Namespace, stateB.BackupID)
		return ps != nil && ps.Remote == RemoteProtected
	})

	// Disaster recovery: fetch B into a brand-new repository from the remote
	// only, then restore. B's shared chunks live in packs uploaded during A.
	recoverRoot := t.TempDir()
	repo2, man2, err := FetchRemote(context.Background(), target, testKeys(t), stateB.Namespace, stateB.BackupID, recoverRoot)
	if err != nil {
		t.Fatalf("fetch remote: %v", err)
	}
	if man2.BackupID != manB.BackupID {
		t.Fatalf("recovered wrong manifest")
	}
	dst := t.TempDir()
	if err := repo2.Restore(context.Background(), man2, RestoreOptions{Dest: dst}); err != nil {
		t.Fatalf("remote restore: %v", err)
	}
	gotShared, _ := os.ReadFile(filepath.Join(dst, "os/base.img"))
	if !bytes.Equal(gotShared, shared) {
		t.Fatalf("shared deduped content did not restore from remote")
	}
	gotB, _ := os.ReadFile(filepath.Join(dst, "app/only-b.txt"))
	if string(gotB) != "B specific content here" {
		t.Fatalf("workload-specific content did not restore from remote")
	}
}
