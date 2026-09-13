package backuphost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backupscope"
)

func TestHostSmartCaptureSkipsReproducibleTree(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "var/lib/postgresql/data"), []byte("pg"))
	mustWrite(t, filepath.Join(src, "usr/bin/ls"), []byte("os-bin"))
	mustWrite(t, filepath.Join(src, "var/cache/apt/x"), []byte("cache"))

	h, err := Open(Options{Root: root, Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(Request{
		Action: ActionCapture, WorkloadID: "wl-1", WorkloadName: "SoundDock",
		CaptureMode: backup.CaptureModeSmart,
	})
	res, err := h.Handle(context.Background(), ActionCapture, src, string(req))
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != backup.Format {
		t.Fatalf("format %s", res.Format)
	}
	var out Result
	if err := json.Unmarshal([]byte(res.Extra), &out); err != nil {
		t.Fatal(err)
	}
	if !out.LocalComplete || out.BackupID == "" {
		t.Fatalf("result %+v", out)
	}
	if out.CaptureMode != backup.CaptureModeSmart {
		t.Fatalf("mode %s", out.CaptureMode)
	}
	dst := t.TempDir()
	_, err = h.Handle(context.Background(), ActionRestore, dst, mustJSON(Request{
		Namespace: out.Namespace, BackupID: out.BackupID, Dest: dst,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "var/lib/postgresql/data")); err != nil {
		t.Fatal("postgres data must restore")
	}
	if _, err := os.Stat(filepath.Join(dst, "usr/bin/ls")); !os.IsNotExist(err) {
		t.Fatal("smart mode must not restore OS files")
	}
}

func TestHostFullCaptureKeepsOSFiles(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "usr/bin/ls"), []byte("os-bin"))
	mustWrite(t, filepath.Join(src, "var/lib/app/data"), []byte("app"))
	h, err := Open(Options{Root: root, Settings: Settings{MaxLocalBytes: 1 << 30, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Handle(context.Background(), ActionCapture, src, mustJSON(Request{
		WorkloadID: "wl-2", WorkloadName: "FullBox", CaptureMode: backup.CaptureModeFull,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var out Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	dst := t.TempDir()
	if _, err := h.Handle(context.Background(), ActionRestore, dst, mustJSON(Request{
		Namespace: out.Namespace, BackupID: out.BackupID,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "usr/bin/ls")); err != nil {
		t.Fatal("full machine must restore OS files")
	}
}

func TestHostPreviewDoesNotCapture(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "var/lib/postgresql/x"), []byte("pg"))
	h, err := Open(Options{Root: t.TempDir(), Settings: Settings{MaxLocalBytes: 1 << 20, MinHostFreeBytes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Handle(context.Background(), ActionPreview, src, mustJSON(Request{
		WorkloadID: "wl", WorkloadName: "db", CaptureMode: backupscope.ModeSmart,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var out Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	if out.Preview.ProtectedBytes == 0 {
		t.Fatalf("preview %+v", out.Preview)
	}
	states, _ := h.repo.ListStates()
	if len(states) != 0 {
		t.Fatal("preview must not create a restore point")
	}
}

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
