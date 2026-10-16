package backuphost

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlatformObjectsStayUnderTheirPrefix(t *testing.T) {
	h := &Host{}
	spec := TargetSpec{Kind: "r2", Endpoint: "https://x.invalid", Bucket: "b"}
	for _, key := range []string{"backups/packs/x.pack", "ndl-platform/../backups/x", ""} {
		if _, err := h.platformObject(context.Background(), Request{Target: spec, ObjectOp: "get", ObjectKey: key}); err == nil || !strings.Contains(err.Error(), "ndl-platform/") {
			t.Fatalf("%q must be refused: %v", key, err)
		}
	}
	if _, err := h.platformObject(context.Background(), Request{Target: TargetSpec{Kind: "local", Locator: t.TempDir()}, ObjectOp: "get", ObjectKey: "ndl-platform/pointer.json"}); err == nil {
		t.Fatal("only object storage holds the platform backup")
	}
}

func TestRecoveredKeyIsInstalledOnlyOnAnEmptyRepository(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "a.txt"), []byte("hello"))
	lost, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer lost.Close()
	key, err := lost.exportKey()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "kept.key")
	fresh, err := Open(Options{Root: t.TempDir(), KeyPath: keyPath, Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err := fresh.Handle(context.Background(), ActionKeyImport, "", mustJSON(Request{Action: ActionKeyImport, ImportKey: key})); err != nil {
		t.Fatal(err)
	}
	if got, _ := fresh.exportKey(); got != key {
		t.Fatal("the recovered key must be in use")
	}
	if raw, _ := os.ReadFile(keyPath); hex.EncodeToString(raw) != key {
		t.Fatal("the kept copy outside the repository must match")
	}
	if _, err := fresh.Handle(context.Background(), ActionCapture, src, mustJSON(Request{Action: ActionCapture, WorkloadID: "w", WorkloadName: "w", CaptureMode: "full"})); err != nil {
		t.Fatal(err)
	}
	other, _ := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1}})
	defer other.Close()
	otherKey, _ := other.exportKey()
	if _, err := fresh.Handle(context.Background(), ActionKeyImport, "", mustJSON(Request{Action: ActionKeyImport, ImportKey: otherKey})); err == nil {
		t.Fatal("a repository with restore points keeps its key")
	}
}
