package lxc

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner executes a validated argv vector and returns combined output.
// Tests replace it. There is no shell string.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// StdinRunner is Runner plus an already-open stdin. Unprivileged extract
// uses it so mapped-root tar never opens the host cache pathname.
type StdinRunner func(ctx context.Context, name string, stdin *os.File, args ...string) ([]byte, error)

func allowedBin(name string) bool {
	switch name {
	case BinLXCStart, BinLXCStop, BinLXCInfo, BinLXCCopy, BinLXCAttach, BinLXCCgroup, BinSystemctl, BinTar, BinUsernsExec, BinCP, BinGPGV:
		return true
	default:
		return false
	}
}

func (e *Engine) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return e.runStdin(ctx, nil, name, args...)
}

func (e *Engine) runStdin(ctx context.Context, stdin *os.File, name string, args ...string) ([]byte, error) {
	if !allowedBin(name) {
		return nil, fmt.Errorf("refusing unlisted binary %s", name)
	}
	if e.RunStdin != nil {
		return e.RunStdin(ctx, name, stdin, args...)
	}
	if e.Run != nil {
		return e.Run(ctx, name, args...)
	}
	if e.SkipHostCmds {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, name, args...)
	// A process left inside a guest by lxc-attach can hold the output pipe
	// open after lxc-attach itself is killed. Do not wait on it forever.
	cmd.WaitDelay = 5 * time.Second
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) == 0 {
			return out, err
		}
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// StreamRunner runs a validated argv with stdin and copies its output to
// out as it arrives. Tests replace it.
type StreamRunner func(ctx context.Context, name string, stdin io.Reader, out io.Writer, args ...string) error

// GuestStream runs cmd in a running system container, feeding it stdin and
// copying its output to out as it comes. Deployments use it so their output
// is seen live and secrets travel on stdin, never on a command line.
func (e *Engine) GuestStream(ctx context.Context, id string, stdin io.Reader, out io.Writer, cmd ...string) error {
	args := e.attachArgs(id, cmd...)
	if e.Stream != nil {
		return e.Stream(ctx, BinLXCAttach, stdin, out, args...)
	}
	if e.SkipHostCmds {
		return fmt.Errorf("host commands are disabled")
	}
	c := exec.CommandContext(ctx, BinLXCAttach, args...)
	c.Stdin, c.Stdout, c.Stderr = stdin, out, out
	// See runStdin: a process left in the guest can hold the pipe open.
	c.WaitDelay = 5 * time.Second
	return c.Run()
}
