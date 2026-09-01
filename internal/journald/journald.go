package journald

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	StatusAvailable   = "available"
	StatusUnavailable = "unavailable"
	StatusUnsupported = "unsupported"
)

const (
	UnitAgent   = "ndl-agent.service"
	UnitControl = "ndl-control.service"
)

// Query is a typed journalctl request. Unit is allowlisted; there is no shell filter.
type Query struct {
	Unit  string
	Lines int
	Since time.Time
}

// Result is honest journal output. Empty lines are not invented.
type Result struct {
	Status  string   `json:"status"`
	Unit    string   `json:"unit"`
	Lines   []string `json:"lines"`
	Message string   `json:"message,omitempty"`
}

// Engine runs typed journalctl argv.
type Engine struct {
	SkipHostCmds bool
	Journalctl   string
	Output       func(argv []string) ([]byte, error)
}

// AllowUnit accepts only No-dal units. Workload instances require a UUID.
func AllowUnit(unit string) (string, error) {
	unit = strings.TrimSpace(unit)
	switch unit {
	case UnitAgent, UnitControl:
		return unit, nil
	}
	if strings.HasPrefix(unit, "nodal-vm@") && strings.HasSuffix(unit, ".service") {
		id := strings.TrimSuffix(strings.TrimPrefix(unit, "nodal-vm@"), ".service")
		if _, err := uuid.Parse(id); err == nil {
			return unit, nil
		}
	}
	if strings.HasPrefix(unit, "nodal-ct@") && strings.HasSuffix(unit, ".service") {
		id := strings.TrimSuffix(strings.TrimPrefix(unit, "nodal-ct@"), ".service")
		if _, err := uuid.Parse(id); err == nil {
			return unit, nil
		}
	}
	return "", errors.New("journal unit is not allowlisted")
}

// Argv is the typed journalctl argv. It never includes a shell or user filter expression.
func Argv(q Query) ([]string, error) {
	unit, err := AllowUnit(q.Unit)
	if err != nil {
		return nil, err
	}
	lines := q.Lines
	if lines <= 0 {
		lines = 200
	}
	if lines > 1000 {
		lines = 1000
	}
	bin := "/usr/bin/journalctl"
	argv := []string{bin, "--no-pager", "-o", "short-iso", "-n", strconv.Itoa(lines), "-u", unit}
	if !q.Since.IsZero() {
		argv = append(argv, "--since", q.Since.UTC().Format("2006-01-02 15:04:05"))
	}
	return argv, nil
}

// Read runs typed journalctl. SkipHostCmds returns unavailable, not fake lines.
func (e *Engine) Read(ctx context.Context, q Query) (Result, error) {
	argv, err := Argv(q)
	if err != nil {
		return Result{Status: StatusUnavailable, Lines: []string{}}, err
	}
	unit := argv[len(argv)-1]
	for i, a := range argv {
		if a == "-u" && i+1 < len(argv) {
			unit = argv[i+1]
			break
		}
	}
	out := Result{Status: StatusUnavailable, Unit: unit, Lines: []string{}}
	if e != nil && e.SkipHostCmds && e.Output == nil {
		out.Message = "journalctl skipped"
		return out, nil
	}
	raw, err := e.run(ctx, argv)
	if err != nil {
		out.Message = "journalctl unavailable"
		return out, nil
	}
	lines := splitLines(raw)
	if len(lines) == 0 {
		out.Status = StatusAvailable
		out.Message = "No log lines in this window"
		return out, nil
	}
	out.Status = StatusAvailable
	out.Lines = lines
	return out, nil
}

func (e *Engine) run(ctx context.Context, argv []string) ([]byte, error) {
	if e != nil && e.Output != nil {
		return e.Output(argv)
	}
	if len(argv) == 0 {
		return nil, errors.New("journalctl argv empty")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("journalctl: %w", err)
	}
	return stdout.Bytes(), nil
}

func splitLines(raw []byte) []string {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	parts := strings.Split(text, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimRight(p, "\r")
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
