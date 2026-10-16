package backup

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckRemoteFindsMissingAndForgedPoints(t *testing.T) {
	e := testEngine(t, smallCfg())
	mem := newMemTransport()
	target := TransportTarget{T: mem, Bucket: "b"}
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "data/a.bin"), randBytes(7, 3<<20), 0o644)
	_, st, err := e.Capture(context.Background(), CaptureOptions{Source: src, WorkloadID: "w", WorkloadName: "w"})
	if err != nil {
		t.Fatal(err)
	}
	q := NewUploadQueue(e.Repo(), target, 2)
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q.Stop()
	_ = q.EnqueueBackup(st)
	waitFor(t, 10*time.Second, func() bool {
		ps, _ := e.Repo().LoadState(st.Namespace, st.BackupID)
		return ps != nil && ps.Remote == RemoteProtected
	})
	ctx := context.Background()
	if c := CheckRemote(ctx, target, testKeys(t), st.Namespace, st.BackupID, true, 0); !c.Present || !c.Deep || c.PacksChecked != c.Packs || c.Error != "" {
		t.Fatalf("deep %+v", c)
	}
	if c := CheckRemote(ctx, target, testKeys(t), st.Namespace, "nope", false, 0); c.Present || c.Error == "" {
		t.Fatalf("missing point %+v", c)
	}
	// A pack gone from the bucket is found by the deep check only.
	packs, _ := e.Repo().packsFor(st.Namespace, st.BackupID)
	_ = target.Delete(ctx, objectKeyPack(packs[0]))
	if c := CheckRemote(ctx, target, testKeys(t), st.Namespace, st.BackupID, false, 0); !c.Present {
		t.Fatalf("quick check only looks at the manifest: %+v", c)
	}
	if c := CheckRemote(ctx, target, testKeys(t), st.Namespace, st.BackupID, true, 0); c.Deep || !strings.Contains(c.Error, "missing pack") {
		t.Fatalf("deep check must find the missing pack: %+v", c)
	}
	// Another key cannot authenticate the manifest.
	other, _ := NewKeys(randBytes(99, 32))
	if c := CheckRemote(ctx, target, other, st.Namespace, st.BackupID, true, 0); c.Deep || !strings.Contains(c.Error, "authentication") {
		t.Fatalf("wrong key %+v", c)
	}
}
