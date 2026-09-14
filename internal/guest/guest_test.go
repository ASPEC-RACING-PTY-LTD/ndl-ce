package guest

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLinuxGuestPTYAndFiles(t *testing.T) {
	root := t.TempDir()
	sock := filepath.Join(t.TempDir(), "guest.sock")
	h := &Host{Root: root, FakePTY: true, OS: "linux", Arch: "amd64"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) { _ = h.ServeConn(ctx, conn) }(c)
		}
	}()

	dialCtx, dialCancel := context.WithTimeout(ctx, 2*time.Second)
	defer dialCancel()
	cli, err := Dial(dialCtx, sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	info, err := cli.Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.OS != "linux" || info.Version != Version {
		t.Fatalf("%+v", info)
	}
	if _, err := cli.FilesOp("mkdir", "etc", "", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.FilesPut("etc/motd", 0o644, []byte("hello-guest")); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.FilesOp("chmod", "etc/motd", "", 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.FilesOp("chmod", "etc/motd", "", 0); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(root, "etc/motd"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0 {
		t.Fatalf("guest chmod 0 %o", st.Mode().Perm())
	}
	if err := os.Chmod(filepath.Join(root, "etc/motd"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, err := cli.FilesGet("etc/motd")
	if err != nil || string(got) != "hello-guest" {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := cli.FilesOp("list", "..", "", 0); err == nil {
		t.Fatal("path traversal must fail")
	}

	sess, err := cli.OpenPTY("/", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := cli.PTYRead(sess)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("nodal-guest-pty")) {
		t.Fatalf("linux PTY proof missing banner: %q", data)
	}
	if err := cli.PTYWrite(sess, []byte("echo hi\n")); err != nil {
		t.Fatal(err)
	}
	echo, _, err := cli.PTYRead(sess)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(echo, []byte("echo hi")) {
		t.Fatalf("pty echo %q", echo)
	}
	if err := cli.PTYClose(sess); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsGuestSubset(t *testing.T) {
	root := t.TempDir()
	sock := filepath.Join(t.TempDir(), "guest.sock")
	h := &Host{Root: root, OS: "windows", Arch: "amd64", Features: []string{"files", "network", "shutdown"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = h.ServeConn(ctx, c)
	}()
	cli, err := Dial(ctx, sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	info, err := cli.Info()
	if err != nil || info.OS != "windows" {
		t.Fatalf("%+v %v", info, err)
	}
	if _, err := cli.FilesPut("readme.txt", 0o644, []byte("win")); err != nil {
		t.Fatal(err)
	}
	got, _, err := cli.FilesGet("readme.txt")
	if err != nil || string(got) != "win" {
		t.Fatalf("%s %v", got, err)
	}
	raw, err := cli.Call(MethodShutdown, ShutdownParams{Mode: "powerdown"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"accepted":true`)) {
		t.Fatalf("%s", raw)
	}
	if _, err := cli.OpenPTY("/", 80, 24); err == nil {
		t.Fatal("windows PTY must stay unimplemented")
	}
}

func TestProbeMissingIsNotInstalled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	st := Probe(ctx, filepath.Join(t.TempDir(), "missing.sock"))
	if st.State != StateUnavailable {
		t.Fatalf("%+v", st)
	}
	qga := ProbeQGA(ctx, filepath.Join(t.TempDir(), "qga.sock"))
	if qga.State != StateUnavailable {
		t.Fatalf("%+v", qga)
	}
}

func TestDialDoesNotStickProbeDeadline(t *testing.T) {
	root := t.TempDir()
	sock := filepath.Join(t.TempDir(), "guest.sock")
	h := &Host{Root: root, FakePTY: true, OS: "linux"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = h.ServeConn(ctx, c)
	}()
	cli, err := Dial(ctx, sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	time.Sleep(900 * time.Millisecond)
	if _, err := cli.Call(MethodPing, nil); err != nil {
		t.Fatalf("live guest session must outlive probe timeout: %v", err)
	}
}

func TestProbeHangIsNotInstalled(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "hang.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		time.Sleep(3 * time.Second)
	}()
	start := time.Now()
	st := Probe(context.Background(), sock)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("probe hung %s", time.Since(start))
	}
	if st.State != StateNotInstalled {
		t.Fatalf("%+v", st)
	}
}

func TestNoShellMethod(t *testing.T) {
	raw, _ := json.Marshal(Request{ID: "1", Method: "guest.shell", Params: json.RawMessage(`{"cmd":"id"}`)})
	if strings.Contains(string(raw), "Host.Exec") {
		t.Fatal("must not mention Host.Exec")
	}
	h := &Host{Root: t.TempDir(), FakePTY: true}
	res := h.handle(context.Background(), Request{ID: "1", Method: "guest.shell"})
	if res.OK || !strings.Contains(res.Error, "unknown guest method") {
		t.Fatalf("%+v", res)
	}
}

func TestServeRejectsHostEscape(t *testing.T) {
	root := t.TempDir()
	h := &Host{Root: root, FakePTY: true}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() { _ = h.ServeConn(context.Background(), a) }()
	cli := NewConn(b)
	if _, err := cli.FilesOp("stat", "../etc/passwd", "", 0); err == nil {
		t.Fatal("escape")
	}
	_ = os.WriteFile(filepath.Join(root, "ok.txt"), []byte("x"), 0o644)
	if _, err := cli.FilesOp("stat", "ok.txt", "", 0); err != nil {
		t.Fatal(err)
	}
}
