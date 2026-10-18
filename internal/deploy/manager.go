package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Run states.
const (
	StateRunning     = "running"
	StateSucceeded   = "succeeded"
	StateFailed      = "failed"
	StateCancelled   = "cancelled"
	StateTimedOut    = "timed-out"
	StateInterrupted = "interrupted"
)

// Execution modes in the container.
const (
	ModeSystemd = "systemd" // a transient unit: stopping it ends the whole cgroup
	ModeSession = "session" // no systemd: the process tree is walked and signalled
)

// Step is one command's progress.
type Step struct {
	Command    string     `json:"command"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Tolerated  bool       `json:"continue_on_error,omitempty"`
}

// Status is a run as the agent sees it. It is kept next to the log as
// <id>.json so it outlives the agent process.
type Status struct {
	ID         string     `json:"id"`
	MachineID  string     `json:"machine_id"`
	Dir        string     `json:"dir"`
	RunAs      string     `json:"run_as,omitempty"`
	Mode       string     `json:"mode,omitempty"`
	Unit       string     `json:"unit,omitempty"`
	PID        int        `json:"pid,omitempty"`
	State      string     `json:"state"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	FailedStep int        `json:"failed_step,omitempty"`
	Error      string     `json:"error,omitempty"`
	Steps      []Step     `json:"steps"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	LogBytes   int64      `json:"log_bytes"`
}

// Done reports whether the run has ended.
func (s Status) Done() bool { return s.State != StateRunning }

// Guest runs commands inside a system container.
type Guest interface {
	// Run runs argv and returns its combined output.
	Run(ctx context.Context, machineID string, argv ...string) ([]byte, error)
	// Stream runs argv with stdin and copies its output to out.
	Stream(ctx context.Context, machineID string, stdin io.Reader, out io.Writer, argv ...string) error
}

// Manager runs deployments. One per agent.
type Manager struct {
	Guest Guest
	Now   func() time.Time
	// KillGrace is how long a stopped run gets before it is killed.
	KillGrace time.Duration

	mu   sync.Mutex
	runs map[string]*run
}

type run struct {
	spec   Spec
	mu     sync.Mutex
	st     Status
	log    *os.File
	dir    string
	cancel context.CancelFunc
	reason string // why the run is being stopped: cancelled or timed-out
	done   chan struct{}
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func (m *Manager) grace() time.Duration {
	if m.KillGrace > 0 {
		return m.KillGrace
	}
	return 15 * time.Second
}

// forbiddenLogRoots are host folders a log folder may not be in.
var forbiddenLogRoots = []string{"/proc", "/sys", "/dev", "/run", "/boot", "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32"}

// LogDir validates and, when create is set, makes the log folder. Every
// part of it must be a real folder, never a link.
func LogDir(dir string, create bool) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
		return "", fmt.Errorf("the deployment log folder must be a clean absolute path")
	}
	for _, f := range forbiddenLogRoots {
		if dir == f || strings.HasPrefix(dir, f+"/") {
			return "", fmt.Errorf("deployment logs cannot be kept under %s", f)
		}
	}
	cur := "/"
	for _, part := range strings.Split(strings.TrimPrefix(dir, "/"), "/") {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) && create {
			if err := os.Mkdir(cur, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
				return "", err
			}
			info, err = os.Lstat(cur)
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("%s is not a plain folder", cur)
		}
	}
	return dir, nil
}

func logPath(dir, id string) string    { return filepath.Join(dir, id+".log") }
func statusPath(dir, id string) string { return filepath.Join(dir, id+".json") }

// Start begins a run and returns at once.
func (m *Manager) Start(spec Spec) (Status, error) {
	if m.Guest == nil {
		return Status{}, errors.New("system containers are unavailable on this node")
	}
	if err := spec.Validate(); err != nil {
		return Status{}, err
	}
	dir, err := LogDir(spec.LogDir, true)
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runs == nil {
		m.runs = map[string]*run{}
	}
	if _, ok := m.runs[spec.ID]; ok {
		return Status{}, fmt.Errorf("deployment %s is already running", spec.ID)
	}
	f, err := os.OpenFile(logPath(dir, spec.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollow, 0o640)
	if err != nil {
		return Status{}, fmt.Errorf("deployment %s already has a log: %w", spec.ID, err)
	}
	st := Status{ID: spec.ID, MachineID: spec.MachineID, Dir: spec.Dir, RunAs: spec.RunAs, State: StateRunning, StartedAt: m.now()}
	for _, c := range spec.Commands {
		st.Steps = append(st.Steps, Step{Command: c.Run, Tolerated: c.ContinueOnError})
	}
	r := &run{spec: spec, st: st, log: f, dir: dir, done: make(chan struct{})}
	m.runs[spec.ID] = r
	r.save()
	go m.execute(r)
	return r.snapshot(), nil
}

func (r *run) snapshot() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.st
	st.Steps = append([]Step(nil), r.st.Steps...)
	if info, err := r.log.Stat(); err == nil {
		st.LogBytes = info.Size()
	}
	return st
}

// save writes the status file. Callers may hold r.mu.
func (r *run) save() {
	st := r.st
	if info, err := r.log.Stat(); err == nil {
		st.LogBytes = info.Size()
	}
	raw, _ := json.MarshalIndent(st, "", "  ")
	tmp := statusPath(r.dir, r.st.ID) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o640); err == nil {
		_ = os.Rename(tmp, statusPath(r.dir, r.st.ID))
	}
}

func (r *run) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(r.log, format, args...)
}

func (m *Manager) execute(r *run) {
	defer close(r.done)
	spec := r.spec
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()
	defer cancel()

	timer := time.AfterFunc(time.Duration(spec.TimeoutSec)*time.Second, func() { m.stop(r, StateTimedOut) })
	defer timer.Stop()

	exit := -1
	err := m.prepare(ctx, r)
	if err == nil {
		mw := &markerWriter{out: r.log, on: func(mk Marker) { m.onMarker(r, mk, &exit) }}
		err = m.Guest.Stream(ctx, spec.MachineID, strings.NewReader(Script(spec)), mw, m.argv(r)...)
	}
	m.finish(r, exit, err)
}

// prepare finds how to run in the container and as whom.
func (m *Manager) prepare(ctx context.Context, r *run) error {
	spec := r.spec
	pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	mode := ModeSession
	if out, err := m.Guest.Run(pctx, spec.MachineID, "/bin/sh", "-c",
		"if [ -d /run/systemd/system ] && command -v systemd-run >/dev/null 2>&1; then echo systemd; else echo session; fi"); err == nil &&
		strings.TrimSpace(string(out)) == ModeSystemd {
		mode = ModeSystemd
	}
	user := spec.RunAs
	if user == "" {
		out, err := m.Guest.Run(pctx, spec.MachineID, "stat", "-c", "%U", "--", spec.Dir)
		if err != nil {
			return fmt.Errorf("the working folder %s is not in the container: %s", spec.Dir, strings.TrimSpace(string(out)))
		}
		user = strings.TrimSpace(string(out))
		if user == "UNKNOWN" || !ValidUser(user) {
			user = "root"
		}
	}
	r.mu.Lock()
	r.st.Mode, r.st.RunAs = mode, user
	if mode == ModeSystemd {
		r.st.Unit = UnitName(spec.ID)
	}
	r.save()
	r.mu.Unlock()
	how := "a process session"
	if mode == ModeSystemd {
		how = "systemd unit " + r.st.Unit
	}
	r.logf("\x1b[2m# %s in %s as %s (%s)\x1b[0m\n", m.now().Format(time.RFC3339), spec.Dir, user, how)
	return nil
}

func (m *Manager) argv(r *run) []string {
	r.mu.Lock()
	mode, unit, user := r.st.Mode, r.st.Unit, r.st.RunAs
	r.mu.Unlock()
	if mode == ModeSystemd {
		argv := []string{"systemd-run", "--quiet", "--pipe", "--wait", "--collect",
			"--unit", unit, "--service-type=exec",
			"-p", "KillMode=control-group",
			"-p", "TimeoutStopSec=" + strconv.Itoa(int(m.grace()/time.Second)),
			"-p", "RuntimeMaxSec=" + strconv.Itoa(r.spec.TimeoutSec+60),
		}
		if user != "root" {
			argv = append(argv, "-p", "User="+user)
		}
		return append(append(argv, "--"), Wrapper...)
	}
	if user != "root" {
		return []string{"su", "-s", "/bin/bash", "-c", Wrapper[2], "--", user}
	}
	return Wrapper
}

func (m *Manager) onMarker(r *run, mk Marker, exit *int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := m.now()
	switch mk.Kind {
	case "pid":
		r.st.PID = mk.Code
	case "exit":
		*exit = mk.Code
	case "step":
		i := mk.Step - 1
		if i < 0 || i >= len(r.st.Steps) {
			return
		}
		s := &r.st.Steps[i]
		if mk.Phase == "start" {
			s.StartedAt = &now
			r.logf("\n\x1b[1m$ %s\x1b[0m\n", s.Command)
		} else {
			code := mk.Code
			s.FinishedAt, s.ExitCode = &now, &code
			if code != 0 {
				note := ""
				if s.Tolerated {
					note = ", continuing"
				}
				r.logf("\x1b[31m# exit code %d%s\x1b[0m\n", code, note)
			}
		}
	}
	r.save()
}

func (m *Manager) finish(r *run, exit int, err error) {
	r.mu.Lock()
	now := m.now()
	r.st.FinishedAt = &now
	switch {
	case r.reason != "":
		r.st.State = r.reason
		if r.reason == StateTimedOut {
			r.st.Error = fmt.Sprintf("stopped after the %s time limit", time.Duration(r.spec.TimeoutSec)*time.Second)
		} else {
			r.st.Error = "cancelled"
		}
	case exit == 0:
		r.st.State = StateSucceeded
	case exit > 0:
		r.st.State = StateFailed
	default:
		r.st.State = StateFailed
		if err != nil {
			r.st.Error = err.Error()
		} else {
			r.st.Error = "the commands ended without reporting a result"
		}
	}
	if exit >= 0 {
		code := exit
		r.st.ExitCode = &code
	}
	if r.st.State != StateSucceeded {
		for i, s := range r.st.Steps {
			if s.ExitCode != nil && *s.ExitCode != 0 && !s.Tolerated {
				r.st.FailedStep = i + 1
				break
			}
			if s.StartedAt != nil && s.FinishedAt == nil {
				r.st.FailedStep = i + 1
				break
			}
		}
	}
	switch r.st.State {
	case StateSucceeded:
		r.logf("\n\x1b[32m# succeeded\x1b[0m\n")
	default:
		msg := r.st.State
		if r.st.Error != "" {
			msg += ": " + r.st.Error
		}
		r.logf("\n\x1b[31m# %s\x1b[0m\n", msg)
	}
	r.save()
	r.mu.Unlock()
	_ = r.log.Close()
	m.mu.Lock()
	delete(m.runs, r.spec.ID)
	m.mu.Unlock()
}

// stop ends a run's whole process tree in the container.
func (m *Manager) stop(r *run, reason string) {
	r.mu.Lock()
	if r.reason != "" || r.st.Done() {
		r.mu.Unlock()
		return
	}
	r.reason = reason
	mode, unit, pid, machine := r.st.Mode, r.st.Unit, r.st.PID, r.spec.MachineID
	cancel := r.cancel
	r.mu.Unlock()
	r.logf("\n\x1b[33m# %s: stopping every process of this deployment\x1b[0m\n", map[string]string{StateTimedOut: "time limit reached", StateCancelled: "cancelled"}[reason])
	go func() {
		ctx, done := context.WithTimeout(context.Background(), m.grace()+30*time.Second)
		defer done()
		KillTree(ctx, m.Guest, machine, mode, unit, pid, m.grace())
		// The tree is gone; end lxc-attach if it is still waiting.
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			if cancel != nil {
				cancel()
			}
		}
	}()
}

// KillTree stops everything a run started. With systemd the unit's cgroup
// is stopped (TERM, then KILL after the grace); without it the process tree
// under pid is frozen, sent TERM, and whatever is left after the grace is
// killed.
func KillTree(ctx context.Context, g Guest, machine, mode, unit string, pid int, grace time.Duration) {
	if mode == ModeSystemd && unit != "" {
		// stop waits for TimeoutStopSec and then sends SIGKILL to the cgroup.
		_, _ = g.Run(ctx, machine, "systemctl", "stop", unit)
		_, _ = g.Run(ctx, machine, "systemctl", "kill", "--signal=SIGKILL", unit)
		return
	}
	if pid <= 1 {
		return
	}
	secs := int(grace / time.Second)
	if secs < 1 {
		secs = 1
	}
	_, _ = g.Run(ctx, machine, "/bin/sh", "-c", treeKill, "ndl-kill", strconv.Itoa(pid), strconv.Itoa(secs))
}

// treeKill ends a process and all its descendants. Each process is stopped
// before its children are listed, so none can fork past the walk. The list
// is kept, so a child whose parent dies on SIGTERM is still killed later.
const treeKill = `
ndl_tree() {
	local p="$1" f l r
	kill -STOP "$p" 2>/dev/null || return 0
	for f in /proc/[0-9]*/stat; do
		l=$(cat "$f" 2>/dev/null) || continue
		r=${l##*) }
		set -- $r
		if [ "$2" = "$p" ]; then f=${f#/proc/}; ndl_tree "${f%/stat}"; fi
	done
	echo "$p"
}
ndl_alive() {
	local p
	for p in $all; do
		case "$(cat /proc/$p/stat 2>/dev/null)" in
		"") ;;
		*") Z "*) ;;
		*) return 0 ;;
		esac
	done
	return 1
}
all=$(ndl_tree "$1")
for p in $all; do kill -TERM "$p" 2>/dev/null; kill -CONT "$p" 2>/dev/null; done
i=0
while [ "$i" -lt "$2" ] && ndl_alive; do sleep 1; i=$((i+1)); done
ndl_alive || exit 0
more=
for p in $all; do more="$more $(ndl_tree "$p")"; done
for p in $all $more; do kill -KILL "$p" 2>/dev/null; done
exit 0
`

// Cancel stops a running deployment.
func (m *Manager) Cancel(logDir, id string) (Status, error) {
	m.mu.Lock()
	r := m.runs[id]
	m.mu.Unlock()
	if r != nil {
		m.stop(r, StateCancelled)
		return r.snapshot(), nil
	}
	st, err := m.Status(logDir, id)
	if err != nil {
		return st, err
	}
	return st, nil
}

// Status reports a run. A run the agent no longer holds but whose file
// says it is running was interrupted (the agent restarted): its unit is
// stopped and the file says so.
func (m *Manager) Status(logDir, id string) (Status, error) {
	if !ValidID(id) {
		return Status{}, errors.New("the deployment id is invalid")
	}
	m.mu.Lock()
	r := m.runs[id]
	m.mu.Unlock()
	if r != nil {
		return r.snapshot(), nil
	}
	dir, err := LogDir(logDir, false)
	if err != nil {
		return Status{}, err
	}
	raw, err := readNoFollow(statusPath(dir, id), 1<<20)
	if err != nil {
		return Status{}, fmt.Errorf("deployment %s is unknown on this node", id)
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return Status{}, err
	}
	if info, err := os.Lstat(logPath(dir, id)); err == nil {
		st.LogBytes = info.Size()
	}
	if st.State == StateRunning && m.Guest != nil {
		ctx, cancel := context.WithTimeout(context.Background(), m.grace()+30*time.Second)
		KillTree(ctx, m.Guest, st.MachineID, st.Mode, st.Unit, st.PID, m.grace())
		cancel()
		now := m.now()
		st.State, st.FinishedAt = StateInterrupted, &now
		st.Error = "the node agent restarted while this deployment ran; it was stopped"
		raw, _ := json.MarshalIndent(st, "", "  ")
		_ = os.WriteFile(statusPath(dir, id), raw, 0o640)
		if f, err := os.OpenFile(logPath(dir, id), os.O_WRONLY|os.O_APPEND|noFollow, 0); err == nil {
			_, _ = fmt.Fprintf(f, "\n\x1b[31m# interrupted: %s\x1b[0m\n", st.Error)
			_ = f.Close()
		}
	}
	return st, nil
}

// Running lists the runs this agent holds.
func (m *Manager) Running() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.runs))
	for id := range m.runs {
		out = append(out, id)
	}
	return out
}

// Log returns up to max bytes of a run's log from offset, and its size.
func (m *Manager) Log(logDir, id string, offset int64, max int) ([]byte, int64, error) {
	if !ValidID(id) {
		return nil, 0, errors.New("the deployment id is invalid")
	}
	dir, err := LogDir(logDir, false)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.OpenFile(logPath(dir, id), os.O_RDONLY|noFollow, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("the log of deployment %s is gone", id)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := info.Size()
	if offset < 0 || offset > size {
		offset = size
	}
	if max <= 0 || max > 4<<20 {
		max = 1 << 20
	}
	n := size - offset
	if n > int64(max) {
		n = int64(max)
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, offset); err != nil && !errors.Is(err, io.EOF) {
		return nil, size, err
	}
	return buf, size, nil
}

// Delete removes finished runs' logs. Running ones are kept.
func (m *Manager) Delete(logDir string, ids []string) (int, error) {
	dir, err := LogDir(logDir, false)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, id := range ids {
		if !ValidID(id) || m.runs[id] != nil {
			continue
		}
		removed := false
		for _, p := range []string{logPath(dir, id), statusPath(dir, id)} {
			if err := os.Remove(p); err == nil {
				removed = true
			}
		}
		if removed {
			n++
		}
	}
	return n, nil
}

// ProbeResult is a short command's output.
type ProbeResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

// Probe runs a short script in a project's folder and waits for it. It is
// used to read Git state; it is bounded and is not a deployment.
func (m *Manager) Probe(ctx context.Context, machineID, dir, runAs, script string, env map[string]string, timeoutSec int) (ProbeResult, error) {
	if m.Guest == nil {
		return ProbeResult{}, errors.New("system containers are unavailable on this node")
	}
	spec := Spec{ID: "00000000-0000", MachineID: machineID, Dir: dir, RunAs: runAs, Env: env, Commands: []Command{{Run: script}}}
	if err := spec.Validate(); err != nil {
		return ProbeResult{}, err
	}
	if timeoutSec <= 0 || timeoutSec > ProbeTimeoutSec {
		timeoutSec = ProbeTimeoutSec
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	if spec.RunAs == "" {
		out, err := m.Guest.Run(ctx, machineID, "stat", "-c", "%U", "--", spec.Dir)
		if err != nil {
			return ProbeResult{}, fmt.Errorf("the working folder %s is not in the container", spec.Dir)
		}
		spec.RunAs = strings.TrimSpace(string(out))
		if !ValidUser(spec.RunAs) {
			spec.RunAs = "root"
		}
	}
	argv := Wrapper
	if spec.RunAs != "root" {
		argv = []string{"su", "-s", "/bin/bash", "-c", Wrapper[2], "--", spec.RunAs}
	}
	var out strings.Builder
	exit := -1
	mw := &markerWriter{out: &limitWriter{w: &out, n: 256 << 10}, on: func(mk Marker) {
		if mk.Kind == "exit" {
			exit = mk.Code
		}
	}}
	err := m.Guest.Stream(ctx, machineID, strings.NewReader(Script(spec)), mw, argv...)
	if exit < 0 {
		if err == nil {
			err = errors.New("the check ended without a result")
		}
		return ProbeResult{Output: out.String()}, err
	}
	return ProbeResult{Output: out.String(), ExitCode: exit}, nil
}

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	n := len(p)
	if l.n <= 0 {
		return n, nil
	}
	if len(p) > l.n {
		p = p[:l.n]
	}
	l.n -= len(p)
	_, err := l.w.Write(p)
	return n, err
}

func readNoFollow(p string, max int64) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|noFollow, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max))
}
