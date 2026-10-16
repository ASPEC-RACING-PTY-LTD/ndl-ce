package liveupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeSys struct {
	busy      []string
	applied   []string
	pkg       string
	unhealthy map[string]bool // component -> fails health with the new build
	restarts  []string
	badBinary bool
}

func (f *fakeSys) Busy(context.Context) ([]string, error)              { return f.busy, nil }
func (f *fakeSys) AppliedMigrations(context.Context) ([]string, error) { return f.applied, nil }
func (f *fakeSys) PackageVersion() string                              { return f.pkg }
func (f *fakeSys) CheckBinary(context.Context, string, string) error {
	if f.badBinary {
		return errors.New("exec format error")
	}
	return nil
}
func (f *fakeSys) Restart(_ context.Context, c string) error {
	f.restarts = append(f.restarts, c)
	return nil
}
func (f *fakeSys) Healthy(_ context.Context, c, build string) error {
	if build != "" && f.unhealthy[c] {
		return errors.New("no answer")
	}
	return nil
}

type fixture struct {
	t    *testing.T
	key  ed25519.PrivateKey
	keys Keys
	u    *Updater
	sys  *fakeSys
	bin  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	root, bin := t.TempDir(), t.TempDir()
	for _, n := range []string{"ndl-control", "ndl-agent"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("old "+n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sys := &fakeSys{pkg: "1.5.0", applied: []string{"0001.sql"}}
	u := &Updater{Root: root, BinDir: bin, Keys: Keys{KeyID(pub): pub}, Sys: sys, Logf: func(string, ...any) {}}
	if err := u.Prepare(-1); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, key: priv, keys: u.Keys, u: u, sys: sys, bin: bin}
}

// bundle builds a signed bundle with the given files and returns its path.
func (f *fixture) bundle(id string, files map[string]string, mutate func(*Manifest)) string {
	f.t.Helper()
	src := f.t.TempDir()
	for p, body := range files {
		full := filepath.Join(src, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	m := Manifest{ID: id, Version: "1.5.0+live." + id, BaseVersion: "1.5.0", Migrations: []string{"0001.sql"}}
	if mutate != nil {
		mutate(&m)
	}
	var buf bytes.Buffer
	if _, err := Pack(&buf, m, src, f.key); err != nil {
		f.t.Fatal(err)
	}
	out := filepath.Join(f.u.Root, "inbox", id+BundleExt)
	if err := os.WriteFile(out, buf.Bytes(), 0o640); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) read(name string) string {
	raw, _ := os.ReadFile(filepath.Join(f.bin, name))
	return string(raw)
}

func (f *fixture) state() State {
	st, err := ReadState(f.u.Root)
	if err != nil {
		f.t.Fatal(err)
	}
	return st
}

var full = map[string]string{
	"ui/index.html":    "<html>new</html>",
	"ui/assets/app.js": "console.log(1)",
	"bin/ndl-control":  "new control",
	"bin/ndl-agent":    "new agent",
}

func TestAppliesUIAgentAndControlAndRollsBack(t *testing.T) {
	f := newFixture(t)
	f.bundle("r1", full, nil)
	if err := f.u.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.read("ndl-control") != "new control" || f.read("ndl-agent") != "new agent" {
		t.Fatal("binaries were not replaced")
	}
	if got := strings.Join(f.sys.restarts, ","); got != "agent,control" {
		t.Fatalf("restart order %q", got)
	}
	ui, _ := os.ReadFile(filepath.Join(f.u.Root, "ui", "index.html"))
	if string(ui) != "<html>new</html>" {
		t.Fatalf("live UI %q", ui)
	}
	st := f.state()
	if st.Current != "r1" || st.Pending != nil || st.History[len(st.History)-1].Result != "applied" {
		t.Fatalf("state %+v", st)
	}
	if _, err := os.Stat(filepath.Join(f.u.Root, "inbox", "r1"+BundleExt)); !os.IsNotExist(err) {
		t.Fatal("applied bundle stays in the inbox")
	}

	if err := f.u.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.read("ndl-control") != "old ndl-control" || f.read("ndl-agent") != "old ndl-agent" {
		t.Fatal("rollback did not restore the binaries")
	}
	if _, err := os.Lstat(filepath.Join(f.u.Root, "ui")); !os.IsNotExist(err) {
		t.Fatal("rollback did not return to the packaged UI")
	}
	if st := f.state(); st.Current != "" {
		t.Fatalf("current after rollback %q", st.Current)
	}
	if err := f.u.Rollback(context.Background()); err == nil {
		t.Fatal("nothing is left to roll back")
	}
}

func TestUnhealthyControlRollsBackEverything(t *testing.T) {
	f := newFixture(t)
	f.sys.unhealthy = map[string]bool{CompControl: true}
	f.bundle("r1", full, nil)
	_ = f.u.Run(context.Background())
	if f.read("ndl-control") != "old ndl-control" || f.read("ndl-agent") != "old ndl-agent" {
		t.Fatal("a failed update must restore both binaries")
	}
	if _, err := os.Lstat(filepath.Join(f.u.Root, "ui")); !os.IsNotExist(err) {
		t.Fatal("the UI must not switch after a failed backend")
	}
	st := f.state()
	if st.Current != "" || st.History[len(st.History)-1].Result != "rolled-back" {
		t.Fatalf("state %+v", st)
	}
}

func TestBadBinaryIsRefusedBeforeAnythingChanges(t *testing.T) {
	f := newFixture(t)
	f.sys.badBinary = true
	f.bundle("r1", full, nil)
	_ = f.u.Run(context.Background())
	if len(f.sys.restarts) != 0 || f.read("ndl-agent") != "old ndl-agent" {
		t.Fatal("nothing may be restarted for a binary that does not run")
	}
}

func TestWaitsWhileBusy(t *testing.T) {
	f := newFixture(t)
	f.sys.busy = []string{"1 backup run(s)"}
	p := f.bundle("r1", full, nil)
	if err := f.u.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("a waiting bundle stays queued")
	}
	if st := f.state(); !strings.Contains(st.Waiting, "backup run") || st.Current != "" {
		t.Fatalf("state %+v", st)
	}
	f.sys.busy = nil
	if err := f.u.Run(context.Background()); err != nil || f.state().Current != "r1" {
		t.Fatalf("applies once quiet: %v", err)
	}
}

func TestRefusesIncompatibleBundles(t *testing.T) {
	cases := map[string]func(*fixture){
		"other release line": func(f *fixture) { f.bundle("r1", full, func(m *Manifest) { m.BaseVersion = "1.4.0" }) },
		"database is newer":  func(f *fixture) { f.sys.applied = []string{"0001.sql", "0002.sql"}; f.bundle("r1", full, nil) },
		"new migration": func(f *fixture) {
			f.bundle("r1", full, func(m *Manifest) { m.Migrations = []string{"0001.sql", "0002.sql"} })
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			setup(f)
			_ = f.u.Run(context.Background())
			st := f.state()
			if st.Current != "" || st.History[len(st.History)-1].Result != "refused" || f.read("ndl-control") != "old ndl-control" {
				t.Fatalf("state %+v", st)
			}
		})
	}
	f := newFixture(t)
	f.bundle("r1", full, func(m *Manifest) { m.Migrations = []string{"0001.sql", "0002.sql"}; m.AllowMigrations = true })
	if err := f.u.Run(context.Background()); err != nil || f.state().Current != "r1" {
		t.Fatalf("an allowed migration applies: %v", err)
	}
	// A UI-only update never touches the database question.
	g := newFixture(t)
	g.sys.applied = []string{"0001.sql", "0099.sql"}
	g.bundle("ui1", map[string]string{"ui/index.html": "x"}, nil)
	_ = g.u.Run(context.Background())
	if g.state().Current != "ui1" || len(g.sys.restarts) != 0 {
		t.Fatal("a UI update applies without restarts")
	}
}

func TestRecoversAnInterruptedUpdate(t *testing.T) {
	f := newFixture(t)
	f.bundle("r1", full, nil)
	_ = f.u.Run(context.Background())
	// Pretend the host lost power after the agent was swapped for r2.
	saved := filepath.Join(f.u.Root, "saved", "r2")
	_ = os.MkdirAll(saved, 0o755)
	_ = copyFile(filepath.Join(f.bin, "ndl-agent"), filepath.Join(saved, "ndl-agent"), 0o755)
	_ = copyFile(filepath.Join(f.bin, "ndl-control"), filepath.Join(saved, "ndl-control"), 0o755)
	_ = os.WriteFile(filepath.Join(f.bin, "ndl-agent"), []byte("half-installed agent"), 0o755)
	st := f.state()
	st.Pending = &Pending{ID: "r2", Phase: CompControl, Components: []string{CompAgent, CompControl}, StartedAt: time.Now()}
	if err := f.u.save(st); err != nil {
		t.Fatal(err)
	}
	f.sys.restarts = nil
	if err := f.u.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.read("ndl-agent") != "new agent" {
		t.Fatalf("agent after recovery %q", f.read("ndl-agent"))
	}
	if got := strings.Join(f.sys.restarts, ","); got != "agent" {
		t.Fatalf("only the changed agent restarts, got %q", got)
	}
	st = f.state()
	if st.Pending != nil || st.Current != "r1" || st.History[len(st.History)-1].Action != "recover" {
		t.Fatalf("state %+v", st)
	}
}

func TestRejectsTamperedAndUntrustedBundles(t *testing.T) {
	f := newFixture(t)
	p := f.bundle("r1", full, nil)
	raw, _ := os.ReadFile(p)

	// Untrusted signer.
	other := newFixture(t)
	if _, err := Inspect(bytes.NewReader(raw), other.keys); err == nil || !strings.Contains(err.Error(), "does not trust") {
		t.Fatalf("untrusted: %v", err)
	}
	// A file changed after signing.
	tampered := rewrite(t, raw, func(name string, body []byte) []byte {
		if name == "files/bin/ndl-agent" {
			return []byte("evil agent")
		}
		return body
	})
	if _, err := Inspect(bytes.NewReader(tampered), f.keys); err == nil {
		t.Fatal("a changed file must be refused")
	}
	if _, err := Open(bytes.NewReader(tampered), f.keys, filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("Open must refuse a changed file too")
	}
	// An extra file outside the manifest, such as a path escape.
	escape := appendEntry(t, raw, "files/../../etc/passwd", []byte("x"))
	if _, err := Open(bytes.NewReader(escape), f.keys, filepath.Join(t.TempDir(), "y")); err == nil {
		t.Fatal("a path outside the bundle must be refused")
	}
	if m, err := Inspect(bytes.NewReader(raw), f.keys); err != nil || m.ID != "r1" {
		t.Fatalf("the genuine bundle verifies: %v", err)
	}
}

func TestPackageInstallSupersedesLiveUI(t *testing.T) {
	f := newFixture(t)
	f.bundle("ui1", map[string]string{"ui/index.html": "x"}, nil)
	_ = f.u.Run(context.Background())
	if err := f.u.PackageInstalled("1.5.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(f.u.Root, "ui")); !os.IsNotExist(err) {
		t.Fatal("the packaged UI must be served after a package install")
	}
	if st := f.state(); st.Current != "" || st.History[len(st.History)-1].Result != "superseded" {
		t.Fatalf("state %+v", st)
	}
}

func rewrite(t *testing.T, raw []byte, fn func(string, []byte) []byte) []byte {
	t.Helper()
	gz, _ := gzip.NewReader(bytes.NewReader(raw))
	tr := tar.NewReader(gz)
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		body, _ := io.ReadAll(tr)
		body = fn(h.Name, body)
		h.Size = int64(len(body))
		_ = tw.WriteHeader(h)
		_, _ = tw.Write(body)
	}
	_ = tw.Close()
	_ = zw.Close()
	return out.Bytes()
}

func appendEntry(t *testing.T, raw []byte, name string, body []byte) []byte {
	t.Helper()
	gz, _ := gzip.NewReader(bytes.NewReader(raw))
	tr := tar.NewReader(gz)
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		b, _ := io.ReadAll(tr)
		_ = tw.WriteHeader(h)
		_, _ = tw.Write(b)
	}
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = zw.Close()
	return out.Bytes()
}

// Staged binaries must be executable: the real pre-swap check runs them.
func TestUnpackedBinariesRun(t *testing.T) {
	f := newFixture(t)
	f.bundle("r1", map[string]string{"bin/ndl-agent": "#!/bin/sh\necho ndl-agent 1.5.0 r1\n"}, nil)
	f.u.Sys = &hostCheck{fakeSys: f.sys}
	if err := f.u.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := f.state(); st.Current != "r1" {
		t.Fatalf("a runnable binary applies: %+v", st.History)
	}
}

type hostCheck struct{ *fakeSys }

func (h *hostCheck) CheckBinary(ctx context.Context, path, build string) error {
	return Host{}.CheckBinary(ctx, path, build)
}
