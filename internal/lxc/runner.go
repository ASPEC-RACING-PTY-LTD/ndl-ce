package lxc

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes a validated argv vector and returns combined output.
// Tests replace it. There is no shell string.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func allowedBin(name string) bool {
	switch name {
	case BinLXCStart, BinLXCStop, BinLXCInfo, BinLXCCopy, BinSystemctl, BinTar, BinCP, BinGPGV:
		return true
	default:
		return false
	}
}

func (e *Engine) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if !allowedBin(name) {
		return nil, fmt.Errorf("refusing unlisted binary %s", name)
	}
	if e.Run != nil {
		return e.Run(ctx, name, args...)
	}
	if e.SkipHostCmds {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) == 0 {
			return out, err
		}
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
