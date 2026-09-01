package agentrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/iojail"
)

func TestFilesOpJailAndCRUD(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &Handler{}
	list, err := h.FilesOp(context.Background(), connect.NewRequest(&agentv1.FilesOpRequest{
		TargetKind: iojail.TargetHost, JailRoot: root, Action: "list", Path: ".",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Entries []fileEntry `json:"entries"`
	}
	if err := json.Unmarshal(list.Msg.GetResultJson(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Entries) == 0 {
		t.Fatal("expected entries")
	}
	if _, err := h.FilesOp(context.Background(), connect.NewRequest(&agentv1.FilesOpRequest{
		TargetKind: iojail.TargetHost, JailRoot: root, Action: "mkdir", Path: "sub",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := runFilesOp(root, "copy", "ok.txt", "sub/copy.txt", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := runFilesOp(root, "rename", "sub/copy.txt", "sub/renamed.txt", 0); err != nil {
		t.Fatal(err)
	}
	st, err := runFilesOp(root, "stat", "sub/renamed.txt", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(st, []byte("renamed.txt")) {
		t.Fatalf("%s", st)
	}
	if _, err := runFilesOp(root, "delete", "sub/renamed.txt", "", 0); err != nil {
		t.Fatal(err)
	}
}

func TestFilesOpRejectsEscape(t *testing.T) {
	root := t.TempDir()
	if _, err := runFilesOp(root, "list", "../etc", "", 0); err == nil {
		t.Fatal("dot-dot must fail")
	}
	if _, err := runFilesOp(root, "stat", "foo/../../etc/passwd", "", 0); err == nil {
		t.Fatal("nested escape must fail")
	}
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	if _, err := runFilesOp(root, "stat", "link", "", 0); err == nil {
		t.Fatal("symlink escape must fail")
	}
	if _, err := runFilesOp(root, "copy", "ok.txt", "../outside.txt", 0); err == nil {
		t.Fatal("copy dest escape must fail")
	}
}

func TestFilesOpDeleteDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(secret, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "inner.txt"), []byte("in"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "sub", "link")); err != nil {
		t.Skip(err)
	}
	if _, err := runFilesOp(root, "delete", "sub", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("outside file must survive directory delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub")); !os.IsNotExist(err) {
		t.Fatal("sub must be deleted")
	}
	if _, err := runFilesOp(root, "delete", ".", "", 0); err == nil {
		t.Fatal("jail root delete must fail")
	}
}

func TestFilesOpHostDeny(t *testing.T) {
	if _, err := runFilesOp("/", "stat", "var/lib/ndl/host.key", "", 0); err == nil {
		t.Fatal("host.key must be denied")
	}
	if _, err := runFilesOp("/", "list", "etc/ndl", "", 0); err == nil {
		t.Fatal("/etc/ndl must be denied")
	}
	if _, err := runFilesOp("/", "stat", "var/lib/ndl/setup.token", "", 0); err == nil {
		t.Fatal("setup.token must be denied")
	}
}

func TestWritePartThenRenameSHA(t *testing.T) {
	root := t.TempDir()
	raw, err := writePartThenRename(root, "a.txt", 0o644, bytes.NewReader([]byte("payload")), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("sha256")) {
		t.Fatalf("%s", raw)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil || string(got) != "payload" {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt.part")); !os.IsNotExist(err) {
		t.Fatal("part file must be renamed away")
	}
}

func TestResolveJailVMConsoleRejectsTraversal(t *testing.T) {
	h := &Handler{}
	id := "11111111-1111-4111-8111-111111111111"
	ok := filepath.Join("/var/lib/ndl/runtime/qemu", id, "serial.sock")
	if got, err := h.resolveJail("vm", id, ok); err != nil || got != ok {
		t.Fatalf("serial sock %s %v", got, err)
	}
	if _, err := h.resolveJail("vm", id, filepath.Join("/var/lib/ndl/runtime/qemu", id, "qmp.sock")); err == nil {
		t.Fatal("qmp must not be a console jail")
	}
	if _, err := h.resolveJail("vm", id, "/etc/passwd"); err == nil {
		t.Fatal("etc passwd")
	}
	if _, err := h.resolveJail("vm", id, filepath.Join("/var/lib/ndl/runtime/qemu", id, "..", "..", "serial.sock")); err == nil {
		t.Fatal("dot-dot")
	}
}
