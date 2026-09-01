package agentrpc

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/iojail"
)

type termRequest struct {
	TargetKind string
	TargetID   string
	JailRoot   string
	CWD        string
	LXCPath    string
}

type termSession interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(rows, cols uint16) error
	CWD() (string, bool)
	Pong() error
	Done() <-chan struct{}
	Close()
}

func startTermSession(ctx context.Context, req termRequest) (termSession, error) {
	if req.TargetKind == "vm" {
		return startVMConsole(ctx, req)
	}
	return startPTYSession(ctx, req)
}

func hostShellArgv() []string {
	if _, err := os.Stat("/bin/bash"); err == nil {
		return []string{"/bin/bash", "-l"}
	}
	return []string{"/bin/sh", "-l"}
}

func termArgv(req termRequest) ([]string, error) {
	kind := strings.TrimSpace(req.TargetKind)
	mode := strings.TrimSpace(req.CWD)
	switch kind {
	case "", iojail.TargetHost, "node":
		return hostShellArgv(), nil
	case iojail.TargetCT, "workload", "system-container-console":
		if strings.TrimSpace(req.TargetID) == "" {
			return nil, fmt.Errorf("target_id is required")
		}
		lxcPath := req.LXCPath
		if lxcPath == "" {
			lxcPath = "/var/lib/ndl/runtime/lxc"
		}
		if kind == "system-container-console" || mode == "console" {
			return []string{"/usr/bin/lxc-console", "-P", lxcPath, "-n", req.TargetID}, nil
		}
		return []string{"/usr/bin/lxc-attach", "-P", lxcPath, "-n", req.TargetID, "--", "/bin/sh", "-l"}, nil
	default:
		return nil, fmt.Errorf("unsupported terminal target %q", kind)
	}
}

func allowlisted(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty argv")
	}
	switch argv[0] {
	case "/bin/bash", "/bin/sh", "/usr/bin/lxc-attach", "/usr/bin/lxc-console":
		return nil
	default:
		return fmt.Errorf("binary is not on the terminal allowlist")
	}
}

func hostStartDir(root, cwd string) (string, error) {
	if cwd == "" || cwd == "/" || cwd == "console" {
		if root == "/" {
			return "/", nil
		}
		return root, nil
	}
	f, abs, err := iojail.OpenBeneath(root, cwd, os.O_RDONLY, 0)
	if err != nil {
		if root == "/" {
			return "/", nil
		}
		return root, nil
	}
	info, err := f.Stat()
	_ = f.Close()
	if err != nil || !info.IsDir() {
		return root, nil
	}
	return abs, nil
}

func cwdTick() <-chan time.Time {
	return time.After(2 * time.Second)
}

type closedTerm struct {
	err error
	ch  chan struct{}
}

func newClosedTerm(err error) *closedTerm {
	ch := make(chan struct{})
	close(ch)
	return &closedTerm{err: err, ch: ch}
}

func (c *closedTerm) Read([]byte) (int, error)  { return 0, c.err }
func (c *closedTerm) Write([]byte) (int, error) { return 0, c.err }
func (c *closedTerm) Resize(uint16, uint16) error {
	return c.err
}
func (c *closedTerm) CWD() (string, bool) { return "", false }
func (c *closedTerm) Pong() error         { return c.err }
func (c *closedTerm) Done() <-chan struct{} {
	return c.ch
}
func (c *closedTerm) Close() {}
