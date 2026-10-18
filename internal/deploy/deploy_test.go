package deploy

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// localGuest runs "in the container" on this machine, without systemd, so
// the process-session path is what the tests exercise.
type localGuest struct{}

func (localGuest) Run(ctx context.Context, _ string, argv ...string) ([]byte, error) {
	if strings.Contains(argv[len(argv)-1], "systemd-run") {
		return []byte("session\n"), nil
	}
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}

func (localGuest) Stream(ctx context.Context, _ string, stdin io.Reader, out io.Writer, argv ...string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, out, out
	cmd.WaitDelay = 2 * time.Second
	return cmd.Run()
}

func newManager() *Manager { return &Manager{Guest: localGuest{}, KillGrace: 2 * time.Second} }

func spec(t *testing.T, id string, cmds ...Command) Spec {
	t.Helper()
	work := t.TempDir()
	return Spec{ID: id, MachineID: "ct-1", Dir: work, RunAs: "root", Commands: cmds, LogDir: filepath.Join(t.TempDir(), "logs")}
}

func wait(t *testing.T, m *Manager, s Spec) Status {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := m.Status(s.LogDir, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if st.Done() {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the deployment did not finish")
	return Status{}
}

func readLog(t *testing.T, m *Manager, s Spec) string {
	t.Helper()
	b, _, err := m.Log(s.LogDir, s.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunSucceedsInTheWorkingFolderWithEnv(t *testing.T) {
	m := newManager()
	s := spec(t, "aaaaaaaa-0001", Command{Run: "pwd; echo \"token=$SECRET_TOKEN\""}, Command{Run: "echo second >&2"})
	s.Env = map[string]string{"SECRET_TOKEN": "it's $ecret"}
	if _, err := m.Start(s); err != nil {
		t.Fatal(err)
	}
	st := wait(t, m, s)
	if st.State != StateSucceeded || st.ExitCode == nil || *st.ExitCode != 0 {
		t.Fatalf("state %+v", st)
	}
	for i, step := range st.Steps {
		if step.ExitCode == nil || *step.ExitCode != 0 || step.StartedAt == nil || step.FinishedAt == nil {
			t.Fatalf("step %d %+v", i+1, step)
		}
	}
	log := readLog(t, m, s)
	if !strings.Contains(log, s.Dir) || !strings.Contains(log, "token=it's $ecret") || !strings.Contains(log, "second") {
		t.Fatalf("log %q", log)
	}
	if strings.ContainsRune(log, markerByte) || strings.Contains(log, "NDL:") {
		t.Fatalf("markers leaked into the log: %q", log)
	}
	if st.LogBytes != int64(len(log)) {
		t.Fatalf("log bytes %d, read %d", st.LogBytes, len(log))
	}
}

func TestRunStopsAtTheFirstFailureUnlessTolerated(t *testing.T) {
	m := newManager()
	s := spec(t, "aaaaaaaa-0002",
		Command{Run: "exit 4", ContinueOnError: true},
		Command{Run: "echo building; exit 7"},
		Command{Run: "echo never"})
	if _, err := m.Start(s); err != nil {
		t.Fatal(err)
	}
	st := wait(t, m, s)
	if st.State != StateFailed || st.ExitCode == nil || *st.ExitCode != 7 || st.FailedStep != 2 {
		t.Fatalf("state %+v", st)
	}
	if st.Steps[2].StartedAt != nil {
		t.Fatal("a command after the failure ran")
	}
	log := readLog(t, m, s)
	if strings.Contains(log, "never") || !strings.Contains(log, "exit code 7") || !strings.Contains(log, "exit code 4, continuing") {
		t.Fatalf("log %q", log)
	}
}

func TestMissingFolderFailsWithReason(t *testing.T) {
	m := newManager()
	s := spec(t, "aaaaaaaa-0003", Command{Run: "true"})
	s.Dir = filepath.Join(s.Dir, "gone")
	if _, err := m.Start(s); err != nil {
		t.Fatal(err)
	}
	st := wait(t, m, s)
	if st.State != StateFailed || st.ExitCode == nil || *st.ExitCode != 2 {
		t.Fatalf("state %+v", st)
	}
}

func alive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// A zombie has exited; only its parent has not collected it yet.
	f := strings.Fields(string(raw)[strings.LastIndexByte(string(raw), ')')+1:])
	return len(f) > 0 && f[0] != "Z"
}

func readPIDs(t *testing.T, path string) []int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(path)
		var pids []int
		for _, f := range strings.Fields(string(raw)) {
			if n, err := strconv.Atoi(f); err == nil {
				pids = append(pids, n)
			}
		}
		if len(pids) >= 2 {
			return pids
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the build processes did not start")
	return nil
}

// Cancelling ends the commands and every process they started, including
// background children that ignore SIGTERM.
func TestCancelKillsTheWholeTree(t *testing.T) {
	m := newManager()
	pidFile := filepath.Join(t.TempDir(), "pids")
	s := spec(t, "aaaaaaaa-0004", Command{Run: "(trap '' TERM; sleep 300) & echo $! >> " + pidFile + "; sleep 300 & echo $! >> " + pidFile + "; wait"})
	if _, err := m.Start(s); err != nil {
		t.Fatal(err)
	}
	pids := readPIDs(t, pidFile)
	if _, err := m.Cancel(s.LogDir, s.ID); err != nil {
		t.Fatal(err)
	}
	st := wait(t, m, s)
	if st.State != StateCancelled {
		t.Fatalf("state %+v", st)
	}
	deadline := time.Now().Add(10 * time.Second)
	for _, pid := range pids {
		for alive(pid) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if alive(pid) {
			t.Fatalf("process %d survived the cancel", pid)
		}
	}
	if !strings.Contains(readLog(t, m, s), "cancelled") {
		t.Fatal("the log does not say it was cancelled")
	}
}

func TestTimeoutStopsTheRun(t *testing.T) {
	m := newManager()
	s := spec(t, "aaaaaaaa-0005", Command{Run: "sleep 300"})
	s.TimeoutSec = 1
	if _, err := m.Start(s); err != nil {
		t.Fatal(err)
	}
	st := wait(t, m, s)
	if st.State != StateTimedOut || !strings.Contains(st.Error, "time limit") {
		t.Fatalf("state %+v", st)
	}
}

// A run the agent lost (it restarted) is reported interrupted, not running.
func TestStatusAfterAgentRestartIsInterrupted(t *testing.T) {
	logs := filepath.Join(t.TempDir(), "logs")
	if _, err := LogDir(logs, true); err != nil {
		t.Fatal(err)
	}
	id := "aaaaaaaa-0006"
	raw, _ := json.Marshal(Status{ID: id, MachineID: "ct-1", State: StateRunning, Mode: ModeSession, StartedAt: time.Now()})
	_ = os.WriteFile(filepath.Join(logs, id+".json"), raw, 0o640)
	_ = os.WriteFile(filepath.Join(logs, id+".log"), []byte("partial output\n"), 0o640)
	m := newManager()
	st, err := m.Status(logs, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateInterrupted || st.FinishedAt == nil {
		t.Fatalf("state %+v", st)
	}
	again, _ := m.Status(logs, id)
	if again.State != StateInterrupted {
		t.Fatalf("not saved: %+v", again)
	}
}

func TestLogReadsByOffsetAndDeleteKeepsRunning(t *testing.T) {
	m := newManager()
	s := spec(t, "aaaaaaaa-0007", Command{Run: "seq 1 2000"})
	if _, err := m.Start(s); err != nil {
		t.Fatal(err)
	}
	wait(t, m, s)
	all, size, err := m.Log(s.LogDir, s.ID, 0, 0)
	if err != nil || int64(len(all)) != size {
		t.Fatalf("%d %d %v", len(all), size, err)
	}
	part, _, _ := m.Log(s.LogDir, s.ID, 10, 20)
	if string(part) != string(all[10:30]) {
		t.Fatalf("offset read %q", part)
	}
	if n, err := m.Delete(s.LogDir, []string{s.ID, "../../etc"}); err != nil || n != 1 {
		t.Fatalf("deleted %d %v", n, err)
	}
	if _, _, err := m.Log(s.LogDir, s.ID, 0, 0); err == nil {
		t.Fatal("the log is still there")
	}
}

func TestLogDirRefusesLinksAndSystemFolders(t *testing.T) {
	for _, bad := range []string{"", "relative", "/", "/etc/ndl", "/proc/x", "/var/lib/../etc"} {
		if _, err := LogDir(bad, false); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	root := t.TempDir()
	_ = os.Symlink("/etc", filepath.Join(root, "link"))
	if _, err := LogDir(filepath.Join(root, "link", "logs"), true); err == nil {
		t.Fatal("a log folder through a link was accepted")
	}
}

func TestSpecValidation(t *testing.T) {
	ok := Spec{ID: "aaaaaaaa-0008", MachineID: "ct-1", Dir: "/srv/app/", Commands: []Command{{Run: " git pull "}, {Run: ""}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok.Dir != "/srv/app" || len(ok.Commands) != 1 || ok.Commands[0].Run != "git pull" || ok.TimeoutSec != DefaultTimeoutSec {
		t.Fatalf("not cleaned: %+v", ok)
	}
	for name, s := range map[string]Spec{
		"host":     {ID: "aaaaaaaa-0009", MachineID: "host", Dir: "/srv", Commands: []Command{{Run: "x"}}},
		"root dir": {ID: "aaaaaaaa-0009", MachineID: "ct", Dir: "/", Commands: []Command{{Run: "x"}}},
		"no cmds":  {ID: "aaaaaaaa-0009", MachineID: "ct", Dir: "/srv"},
		"bad id":   {ID: "../x", MachineID: "ct", Dir: "/srv", Commands: []Command{{Run: "x"}}},
		"bad user": {ID: "aaaaaaaa-0009", MachineID: "ct", Dir: "/srv", RunAs: "a b", Commands: []Command{{Run: "x"}}},
		"bad env":  {ID: "aaaaaaaa-0009", MachineID: "ct", Dir: "/srv", Env: map[string]string{"PATH": "x"}, Commands: []Command{{Run: "x"}}},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSecretsStayOffTheCommandLine(t *testing.T) {
	m := &Manager{}
	r := &run{spec: Spec{ID: "aaaaaaaa-0010", TimeoutSec: 60, Env: map[string]string{"TOKEN": "s3cret"}}, st: Status{Mode: ModeSystemd, Unit: UnitName("aaaaaaaa-0010"), RunAs: "deploy"}}
	argv := strings.Join(m.argv(r), " ")
	if strings.Contains(argv, "s3cret") || !strings.Contains(argv, "User=deploy") || !strings.Contains(argv, "KillMode=control-group") {
		t.Fatalf("argv %s", argv)
	}
	if !strings.Contains(Script(r.spec), "export TOKEN='s3cret'") {
		t.Fatal("the script does not set the variable")
	}
}

func TestMarkerWriterSplitsAcrossWrites(t *testing.T) {
	var out strings.Builder
	var got []Marker
	w := &markerWriter{out: &out, on: func(m Marker) { got = append(got, m) }}
	for _, chunk := range []string{"abc\x1eNDL:st", "ep:2:end:9\nde", "f\x1eNDL:exit:9\n"} {
		_, _ = w.Write([]byte(chunk))
	}
	if out.String() != "abcdef" || len(got) != 2 || got[0].Step != 2 || got[0].Code != 9 || got[1].Kind != "exit" {
		t.Fatalf("%q %+v", out.String(), got)
	}
}
