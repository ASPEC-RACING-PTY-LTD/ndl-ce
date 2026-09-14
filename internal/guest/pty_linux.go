//go:build linux

package guest

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

type linuxPTY struct {
	file *os.File
	cmd  *exec.Cmd
}

func openGuestPTY(ctx context.Context, cwd string) (ptySession, error) {
	bin := "/bin/sh"
	if _, err := os.Stat("/bin/bash"); err == nil {
		bin = "/bin/bash"
	}
	cmd := exec.CommandContext(ctx, bin, "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if cwd != "" && cwd != "/" {
		cmd.Dir = cwd
	}
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("open guest pty: %w", err)
	}
	return &linuxPTY{file: f, cmd: cmd}, nil
}

func (s *linuxPTY) Read(p []byte) (int, error) {
	return s.file.Read(p)
}

func (s *linuxPTY) Write(p []byte) (int, error) {
	return s.file.Write(p)
}

func (s *linuxPTY) Resize(rows, cols uint16) error {
	return pty.Setsize(s.file, &pty.Winsize{Rows: rows, Cols: cols})
}

func (s *linuxPTY) Close() error {
	if s.file != nil {
		_ = s.file.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGHUP)
	}
	return nil
}

var _ = io.Discard

func guestShutdown(mode string) error {
	args := []string{"-h", "now"}
	if mode == "reboot" {
		args = []string{"-r", "now"}
	}
	cmd := exec.Command("/sbin/shutdown", args...)
	return cmd.Start()
}
