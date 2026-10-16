package backuphost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A target the control plane no longer lists is forgotten: its queue stops
// and its saved spec is deleted, so it is not reopened after a restart.
func TestSyncForgetsDeletedTargets(t *testing.T) {
	root := t.TempDir()
	h, err := Open(Options{Root: root, Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	live := TargetSpec{ID: "live", Kind: "local", Locator: t.TempDir()}
	gone := TargetSpec{ID: "gone", Kind: "local", Locator: t.TempDir()}
	for _, spec := range []TargetSpec{live, gone} {
		if err := h.rememberTarget(spec); err != nil {
			t.Fatal(err)
		}
		if _, err := h.queueFor(spec); err != nil {
			t.Fatal(err)
		}
	}

	raw, _ := json.Marshal(Request{Action: ActionStatus, SyncTargets: true, Targets: []string{"live"}})
	if _, err := h.Handle(context.Background(), ActionStatus, "", string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.targetDir(), "gone.json")); !os.IsNotExist(err) {
		t.Fatalf("deleted target still remembered: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.targetDir(), "live.json")); err != nil {
		t.Fatalf("live target forgotten: %v", err)
	}
	h.mu.Lock()
	_, stillQueued := h.queues["gone"]
	h.mu.Unlock()
	if stillQueued {
		t.Fatal("deleted target still has an upload queue")
	}

	// Without SyncTargets nothing is forgotten, even with an empty list.
	raw, _ = json.Marshal(Request{Action: ActionStatus})
	if _, err := h.Handle(context.Background(), ActionStatus, "", string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.targetDir(), "live.json")); err != nil {
		t.Fatalf("live target forgotten without a sync: %v", err)
	}
}
