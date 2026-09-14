//go:build linux

package agentrpc

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/no-dal/ndl-ce/internal/iojail"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

type ptySession struct {
	file *os.File
	cmd  *exec.Cmd
	done chan struct{}
	mu   sync.Mutex
	pong chan struct{}
}

func startPTYSession(ctx context.Context, req termRequest) (termSession, error) {
	argv, err := termArgv(req)
	if err != nil {
		return nil, err
	}
	if argv[0] == "/usr/bin/lxc-attach" {
		argv[0] = lxc.BinLXCAttach
	}
	if argv[0] == "/usr/bin/lxc-console" {
		argv[0] = lxc.BinLXCConsole
	}
	if err := allowlisted(argv); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if req.TargetKind == "" || req.TargetKind == iojail.TargetHost || req.TargetKind == "node" {
		dir, err := hostStartDir(req.JailRoot, req.CWD)
		if err == nil {
			cmd.Dir = dir
		}
	}
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("open pty: %w", err)
	}
	s := &ptySession{file: f, cmd: cmd, done: make(chan struct{}), pong: make(chan struct{}, 1)}
	go func() {
		_ = cmd.Wait()
		select {
		case <-s.done:
		default:
			close(s.done)
		}
	}()
	return s, nil
}

func (s *ptySession) Read(p []byte) (int, error) {
	return s.file.Read(p)
}

func (s *ptySession) Write(p []byte) (int, error) {
	return s.file.Write(p)
}

func (s *ptySession) Resize(rows, cols uint16) error {
	return pty.Setsize(s.file, &pty.Winsize{Rows: rows, Cols: cols})
}

func (s *ptySession) CWD() (string, bool) {
	if s.cmd == nil || s.cmd.Process == nil {
		return "", false
	}
	target, err := os.Readlink("/proc/" + strconv.Itoa(s.cmd.Process.Pid) + "/cwd")
	if err != nil {
		return "", false
	}
	return target, true
}

func (s *ptySession) Pong() error {
	select {
	case s.pong <- struct{}{}:
	default:
	}
	return nil
}

func (s *ptySession) Done() <-chan struct{} {
	return s.done
}

func (s *ptySession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		_ = s.file.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGHUP)
	}
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

var _ = io.Discard
