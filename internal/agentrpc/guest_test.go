package agentrpc

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/guest"
)

func TestGuestChannelPTYProofAndFilesMux(t *testing.T) {
	root := t.TempDir()
	sock := filepath.Join(t.TempDir(), "guest.sock")
	host := &guest.Host{Root: root, FakePTY: true, OS: "linux"}
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
			go func(conn net.Conn) { _ = host.ServeConn(ctx, conn) }(c)
		}
	}()
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	h := &Handler{GuestSocketFn: func(string) string { return sock }}
	list, err := h.FilesOp(ctx, connect.NewRequest(&agentv1.FilesOpRequest{
		TargetKind: "vm-guest", TargetId: id, Action: "mkdir", Path: "home",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !list.Msg.GetOk() {
		t.Fatal(list.Msg.GetMessage())
	}
	raw, err := h.guestFilesPut(ctx, id, "home/note.txt", 0o644, []byte("from-agent"))
	if err != nil || !bytes.Contains(raw, []byte("from-agent")) && !bytes.Contains(raw, []byte("note.txt")) {
		t.Fatalf("%s %v", raw, err)
	}
	got, _, err := h.guestFilesGet(ctx, id, "home/note.txt")
	if err != nil || string(got) != "from-agent" {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := h.FilesOp(ctx, connect.NewRequest(&agentv1.FilesOpRequest{
		TargetKind: "vm-guest", TargetId: id, Action: "stat", Path: "../etc/passwd",
	})); err == nil {
		t.Fatal("guest files must not escape the guest jail")
	}

	sess, err := startGuestPTY(ctx, h, termRequest{TargetKind: "vm-guest", TargetID: id, CWD: "/"})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	deadline := time.Now().Add(2 * time.Second)
	var banner []byte
	buf := make([]byte, 64)
	for time.Now().Before(deadline) {
		n, rerr := sess.Read(buf)
		if n > 0 {
			banner = append(banner, buf[:n]...)
			if bytes.Contains(banner, []byte("nodal-guest-pty")) {
				break
			}
		}
		if rerr != nil {
			t.Fatal(rerr)
		}
	}
	if !bytes.Contains(banner, []byte("nodal-guest-pty")) {
		t.Fatalf("linux guest PTY proof missing banner: %q", banner)
	}

	st := h.guestStatus(ctx, id)
	if st.NodalGA.State != guest.StateOK {
		t.Fatalf("%+v", st)
	}
}

func TestGuestStatusMissingIsNotInstalled(t *testing.T) {
	h := &Handler{GuestSocketFn: func(string) string { return filepath.Join(t.TempDir(), "gone.sock") }}
	st := h.guestStatus(context.Background(), "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	if st.NodalGA.State != guest.StateUnavailable && st.NodalGA.State != guest.StateNotInstalled {
		t.Fatalf("%+v", st)
	}
	if st.QEMUGA.State == guest.StateOK {
		t.Fatal("must not fake qemu-ga")
	}
}
