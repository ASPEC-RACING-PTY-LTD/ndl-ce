package lxc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
