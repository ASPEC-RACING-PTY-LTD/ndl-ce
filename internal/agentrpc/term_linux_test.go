//go:build linux

package agentrpc

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/no-dal/ndl-ce/internal/iojail"
)

func TestRootLoginShellStaysOpenInHome(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("cd /root requires root")
	}
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("bash is required")
	}
	cmd := exec.Command("/bin/sh", "-c", ctRootShell)
	cmd.Env = []string{
		"TERM=linux",
		"LANG=C.UTF-8",
		"HOME=/root",
		"USER=root",
		"LOGNAME=root",
		"SHELL=/bin/bash",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = f.Close()
	}()
	_ = f.SetReadDeadline(time.Now().Add(3 * time.Second))
	var buf bytes.Buffer
	tmp := make([]byte, 4096)
	for buf.Len() < 8 {
		n, rerr := f.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if rerr != nil {
			break
		}
		if strings.Contains(buf.String(), "#") {
			break
		}
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		t.Fatalf("login shell exited: %s", buf.String())
	}
	_, _ = f.Write([]byte("pwd; echo HOME=$HOME\n"))
	_ = f.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 20; i++ {
		n, rerr := f.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if strings.Contains(buf.String(), "/root") && strings.Contains(buf.String(), "HOME=/root") {
			break
		}
		if rerr != nil {
			break
		}
	}
	got := buf.String()
	if !strings.Contains(got, "/root") {
		t.Fatalf("cwd is not /root: %q", got)
	}
	if !strings.Contains(got, "HOME=/root") {
		t.Fatalf("HOME is not /root: %q", got)
	}
	if strings.Contains(got, "login:") {
		t.Fatal("must not present a login prompt")
	}
}

func TestTermArgvRejectsLoginDashF(t *testing.T) {
	argv, err := termArgv(termRequest{TargetKind: iojail.TargetCT, TargetID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range argv {
		if a == "/bin/login" || a == "-f" {
			t.Fatalf("login -f must not be used: %#v", argv)
		}
	}
}
