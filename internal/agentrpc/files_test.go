package agentrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/iojail"
	"github.com/no-dal/ndl-ce/internal/lxc"
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
	if _, err := runFilesOp("/", "rename", "var/lib/ndl/host.key", "tmp/ndl-out", 0); err == nil {
		t.Fatal("rename of host.key must be denied")
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

func TestResolveJailVMGuestToken(t *testing.T) {
	h := &Handler{}
	id := "11111111-1111-4111-8111-111111111111"
	for _, jail := range []string{"guest:/", "guest:", "guest", "/", ""} {
		got, err := h.resolveJail("vm", id, jail)
		if err != nil || got != guestJailRoot {
			t.Fatalf("jail %q -> %q %v", jail, got, err)
		}
	}
	ok := filepath.Join("/var/lib/ndl/runtime/qemu", id, "serial.sock")
	if got, err := h.resolveJail("vm", id, ok); err != nil || got != ok {
		t.Fatalf("serial sock still required: %s %v", got, err)
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

func TestFilesOpChmodAndChownStayInJail(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runFilesOp(root, "chmod", "a.txt", "", 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	if _, err := runFilesOp(root, "chmod", "../a.txt", "", 0o777); err == nil {
		t.Fatal("chmod escape must fail")
	}
	if _, err := runFilesOp(root, "chown", "../a.txt", "0:0", 0); err == nil {
		t.Fatal("chown escape must fail")
	}
}

func TestFilesOpPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file mode bits")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked.txt")
	if err := os.WriteFile(locked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	if _, err := runFilesOp(root, "stat", "locked.txt", "", 0); err == nil {
		t.Fatal("stat of an unreadable file must fail")
	}
}

func TestFilesOpRecursiveDelete(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tree", "n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tree", "n", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runFilesOp(root, "delete", "tree", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tree")); !os.IsNotExist(err) {
		t.Fatal("tree must be gone")
	}
}

func TestJailRelCWD(t *testing.T) {
	jail := "/var/lib/ndl/storage/pool/volumes/ct"
	if got := jailRelCWD(jail, filepath.Join(jail, "root")); got != "/root" {
		t.Fatalf("got %s", got)
	}
	if got := jailRelCWD(jail, "/home/user"); got != "/home/user" {
		t.Fatalf("guest-style cwd %s", got)
	}
	if got := jailRelCWD("guest:/", "/etc"); got != "/etc" {
		t.Fatalf("guest jail %s", got)
	}
}

func writeCTLastApplied(t *testing.T, dataDir, id, rootfs string) {
	t.Helper()
	row := lxc.Applied{
		SchemaVersion: lxc.LastAppliedSchema,
		Spec:          lxc.Spec{WorkloadID: id, RootfsPath: rootfs},
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "workloads", id, "last-applied.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestResolveJailCTUsesLastAppliedNotClientRoot(t *testing.T) {
	dir := t.TempDir()
	id := uuid.NewString()
	rootfs := filepath.Join(dir, "storage", "ct")
	if err := os.MkdirAll(rootfs, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCTLastApplied(t, dir, id, rootfs)
	h := &Handler{Workloads: &lxc.Engine{DataDir: dir}}
	got, err := h.resolveJail(iojail.TargetCT, id, "/etc")
	if err != nil || got != filepath.Clean(rootfs) {
		t.Fatalf("client jail_root must not override last-applied: %q %v", got, err)
	}
}

func TestResolveJailCTFailsClosedWithoutLastApplied(t *testing.T) {
	h := &Handler{Workloads: &lxc.Engine{DataDir: t.TempDir()}}
	id := uuid.NewString()
	if _, err := h.resolveJail(iojail.TargetCT, id, "/etc"); err == nil {
		t.Fatal("missing last-applied must not trust client jail_root")
	}
	if _, err := h.resolveJail(iojail.TargetCT, "", "/etc"); err == nil {
		t.Fatal("empty target_id must fail closed")
	}
}

func TestResolveJailCTWithoutEngineUsesRequested(t *testing.T) {
	h := &Handler{}
	got, err := h.resolveJail(iojail.TargetCT, uuid.NewString(), "/tmp/jail")
	if err != nil || got != "/tmp/jail" {
		t.Fatalf("tests without an engine still use requested jail: %q %v", got, err)
	}
}
