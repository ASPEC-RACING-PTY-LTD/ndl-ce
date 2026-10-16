package liveupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Layout under Root (default /var/lib/ndl/live).
const (
	DefaultRoot    = "/var/lib/ndl/live"
	DefaultKeysDir = "/etc/ndl-live/keys"
	// EnabledMarker turns automatic application on. Without it bundles are
	// verified and listed but never applied.
	DefaultEnabledMarker = "/etc/ndl-live/enabled"
	inboxDir             = "inbox"
	releasesDir          = "releases"
	savedDir             = "saved"
	uiLink               = "ui"
	stateFile            = "state.json"
	// RollbackRequest asks the updater to undo the newest live update.
	RollbackRequest = "rollback.request"
	// BundleExt names bundles in the inbox.
	BundleExt    = ".ndlu"
	keepReleases = 5
)

// ErrBusy means a backup, restore or other change is running; the update
// stays queued and is tried again.
var ErrBusy = errors.New("waiting for running operations to finish")

// Entry is one line of update history.
type Entry struct {
	ID         string    `json:"id"`
	Version    string    `json:"version"`
	Action     string    `json:"action"` // apply, rollback, recover, refuse
	Result     string    `json:"result"` // applied, rolled-back, failed, refused, superseded
	Message    string    `json:"message,omitempty"`
	Components []string  `json:"components,omitempty"`
	At         time.Time `json:"at"`
	// PrevUI is the UI release that was live before this one ("" = packaged).
	PrevUI string `json:"prev_ui,omitempty"`
	// RolledBack marks an applied update that was undone.
	RolledBack bool `json:"rolled_back,omitempty"`
}

// Pending is an update being applied. It is written before every step, so
// an interrupted apply is found and undone at the next start.
type Pending struct {
	ID         string    `json:"id"`
	Version    string    `json:"version"`
	Phase      string    `json:"phase"`
	Components []string  `json:"components"`
	StartedAt  time.Time `json:"started_at"`
	PrevUI     string    `json:"prev_ui,omitempty"`
}

// State is state.json: what is live, what is happening and what happened.
type State struct {
	Current string   `json:"current,omitempty"`
	Pending *Pending `json:"pending,omitempty"`
	// Waiting says why a queued update has not started.
	Waiting string  `json:"waiting,omitempty"`
	History []Entry `json:"history"`
}

// System is what the updater needs from the host. Tests fake it.
type System interface {
	// Busy lists operations that must not be interrupted, if any.
	Busy(ctx context.Context) ([]string, error)
	// AppliedMigrations lists the database migrations already applied.
	AppliedMigrations(ctx context.Context) ([]string, error)
	// PackageVersion is the installed NDL-CE package version.
	PackageVersion() string
	// CheckBinary runs a staged binary's version command; it must report build.
	CheckBinary(ctx context.Context, path, build string) error
	// Restart restarts a component's service.
	Restart(ctx context.Context, component string) error
	// Healthy waits until the component serves again, as build when build
	// is not empty, or returns an error after its timeout.
	Healthy(ctx context.Context, component, build string) error
}

// Updater applies, rolls back and recovers live updates.
type Updater struct {
	Root   string
	BinDir string // where ndl-control and ndl-agent live, /usr/sbin
	Keys   Keys
	Sys    System
	Now    func() time.Time
	Logf   func(format string, args ...any)
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now().UTC()
	}
	return time.Now().UTC()
}

func (u *Updater) logf(format string, args ...any) {
	if u.Logf != nil {
		u.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func (u *Updater) path(parts ...string) string {
	return filepath.Join(append([]string{u.Root}, parts...)...)
}

func binName(component string) string {
	if component == CompControl {
		return "ndl-control"
	}
	return "ndl-agent"
}

// ReadState loads state.json; a missing file is an empty state.
func ReadState(root string) (State, error) {
	var st State
	raw, err := os.ReadFile(filepath.Join(root, stateFile))
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	err = json.Unmarshal(raw, &st)
	return st, err
}

func (u *Updater) save(st State) error {
	if len(st.History) > 100 {
		st.History = st.History[len(st.History)-100:]
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(u.path(stateFile), raw, 0o644)
}

func writeAtomic(p string, data []byte, mode os.FileMode) error {
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Prepare creates the directory layout. The inbox is writable by group
// so the control plane can queue bundles and rollback requests.
func (u *Updater) Prepare(gid int) error {
	for _, d := range []string{u.Root, u.path(releasesDir), u.path(savedDir)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	inbox := u.path(inboxDir)
	if err := os.MkdirAll(inbox, 0o770); err != nil {
		return err
	}
	if gid >= 0 {
		if err := os.Chown(inbox, 0, gid); err != nil {
			return err
		}
	}
	return os.Chmod(inbox, 0o770)
}

// Run is one pass of the updater service: undo an interrupted update, act
// on a rollback request, then apply queued bundles oldest first.
func (u *Updater) Run(ctx context.Context) error {
	if err := u.Recover(ctx); err != nil {
		return err
	}
	if _, err := os.Stat(u.path(inboxDir, RollbackRequest)); err == nil {
		_ = os.Remove(u.path(inboxDir, RollbackRequest))
		if err := u.Rollback(ctx); err != nil {
			u.logf("live update: rollback: %v", err)
		}
	}
	entries, err := os.ReadDir(u.path(inboxDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	type queued struct {
		name string
		at   time.Time
	}
	var list []queued
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != BundleExt {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		list = append(list, queued{e.Name(), info.ModTime()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].at.Before(list[j].at) })
	for _, q := range list {
		p := u.path(inboxDir, q.name)
		err := u.Install(ctx, p)
		if errors.Is(err, ErrBusy) {
			return nil // try again on the next pass
		}
		_ = os.Remove(p)
		if err != nil {
			u.logf("live update %s: %v", q.name, err)
		}
	}
	return nil
}

// Install verifies and applies one bundle.
func (u *Updater) Install(ctx context.Context, bundlePath string) error {
	st, err := ReadState(u.Root)
	if err != nil {
		return err
	}
	f, err := os.Open(bundlePath)
	if err != nil {
		return err
	}
	defer f.Close()
	m, rel, err := u.unpack(f)
	if err != nil {
		u.record(&st, Entry{ID: strings.TrimSuffix(filepath.Base(bundlePath), BundleExt), Action: "refuse", Result: "refused", Message: err.Error()})
		return err
	}
	if err := u.compatible(ctx, m); err != nil {
		_ = os.RemoveAll(rel)
		u.record(&st, Entry{ID: m.ID, Version: m.Version, Action: "refuse", Result: "refused", Message: err.Error(), Components: m.Components()})
		return err
	}
	busy, err := u.Sys.Busy(ctx)
	if err != nil {
		busy = []string{"could not check running operations: " + err.Error()}
	}
	if len(busy) > 0 {
		st.Waiting = fmt.Sprintf("%s %s waits for: %s", m.ID, m.Version, strings.Join(busy, "; "))
		_ = u.save(st)
		return ErrBusy
	}
	st.Waiting = ""
	return u.apply(ctx, &st, m, rel)
}

// unpack verifies a bundle into releases/<id>, reusing an earlier copy.
func (u *Updater) unpack(r io.Reader) (Manifest, string, error) {
	tmp := u.path(releasesDir, fmt.Sprintf(".incoming-%d", time.Now().UnixNano()))
	m, err := Open(r, u.Keys, tmp)
	if err != nil {
		return m, "", err
	}
	rel := u.path(releasesDir, m.ID)
	if _, err := os.Stat(rel); err == nil {
		_ = os.RemoveAll(tmp)
		return m, rel, nil
	}
	if err := os.Rename(tmp, rel); err != nil {
		_ = os.RemoveAll(tmp)
		return m, "", err
	}
	return m, rel, nil
}

func majorMinor(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}

func (u *Updater) compatible(ctx context.Context, m Manifest) error {
	if pkg := u.Sys.PackageVersion(); pkg != "" && majorMinor(pkg) != majorMinor(m.BaseVersion) {
		return fmt.Errorf("built for NDL-CE %s, this host runs %s; install the package release instead", m.BaseVersion, pkg)
	}
	hasControl := false
	for _, c := range m.Components() {
		hasControl = hasControl || c == CompControl
	}
	if !hasControl {
		return nil
	}
	applied, err := u.Sys.AppliedMigrations(ctx)
	if err != nil {
		return fmt.Errorf("could not read the database migrations: %w", err)
	}
	carried := map[string]bool{}
	for _, name := range m.Migrations {
		carried[name] = true
	}
	for _, name := range applied {
		if !carried[name] {
			return fmt.Errorf("the database has migration %s, which this build does not know; it is older than the running version", name)
		}
	}
	isApplied := map[string]bool{}
	for _, name := range applied {
		isApplied[name] = true
	}
	var fresh []string
	for _, name := range m.Migrations {
		if !isApplied[name] {
			fresh = append(fresh, name)
		}
	}
	if len(fresh) > 0 && !m.AllowMigrations {
		names := fresh
		if len(names) > 3 {
			names = append(names[:3:3], fmt.Sprintf("%d more", len(fresh)-3))
		}
		return fmt.Errorf("this build changes the database (%s); install it as a package release, or publish it with migrations allowed", strings.Join(names, ", "))
	}
	return nil
}

func (u *Updater) uiTarget() string {
	t, err := os.Readlink(u.path(uiLink))
	if err != nil {
		return ""
	}
	return t
}

func (u *Updater) setUI(target string) error {
	link := u.path(uiLink)
	if target == "" {
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".ndl-live"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	// rename replaces the binary in one step; the running process keeps
	// the old inode until it restarts.
	return os.Rename(tmp, dst)
}

func (u *Updater) apply(ctx context.Context, st *State, m Manifest, rel string) error {
	comps := m.Components()
	p := &Pending{ID: m.ID, Version: m.Version, Components: comps, StartedAt: u.now(), PrevUI: u.uiTarget(), Phase: "saving"}
	st.Pending = p
	if err := u.save(*st); err != nil {
		return err
	}
	saved := u.path(savedDir, m.ID)
	_ = os.RemoveAll(saved)
	if err := os.MkdirAll(saved, 0o755); err != nil {
		return err
	}
	for _, c := range comps {
		if c == CompUI {
			continue
		}
		staged := filepath.Join(rel, "files", "bin", binName(c))
		if err := u.Sys.CheckBinary(ctx, staged, m.ID); err != nil {
			return u.fail(ctx, st, m, fmt.Errorf("%s does not start on this host: %w", binName(c), err), nil)
		}
		if err := copyFile(filepath.Join(u.BinDir, binName(c)), filepath.Join(saved, binName(c)), 0o755); err != nil {
			return u.fail(ctx, st, m, fmt.Errorf("could not keep the running %s for rollback: %w", binName(c), err), nil)
		}
	}
	var done []string
	for _, c := range comps {
		p.Phase = c
		if err := u.save(*st); err != nil {
			return err
		}
		if c == CompUI {
			if err := u.setUI(filepath.Join(rel, "files", "ui")); err != nil {
				return u.fail(ctx, st, m, fmt.Errorf("could not switch the web UI: %w", err), done)
			}
			done = append(done, c)
			continue
		}
		u.logf("live update %s: installing %s", m.ID, binName(c))
		if err := copyFile(filepath.Join(rel, "files", "bin", binName(c)), filepath.Join(u.BinDir, binName(c)), 0o755); err != nil {
			return u.fail(ctx, st, m, err, done)
		}
		done = append(done, c)
		if err := u.Sys.Restart(ctx, c); err != nil {
			return u.fail(ctx, st, m, fmt.Errorf("restart %s: %w", binName(c), err), done)
		}
		if err := u.Sys.Healthy(ctx, c, m.ID); err != nil {
			return u.fail(ctx, st, m, fmt.Errorf("%s did not come up healthy: %w", binName(c), err), done)
		}
	}
	prevUI := p.PrevUI
	st.Pending = nil
	st.Current = m.ID
	u.record(st, Entry{ID: m.ID, Version: m.Version, Action: "apply", Result: "applied", Components: comps, PrevUI: prevUI})
	u.prune(*st)
	u.logf("live update %s (%s) applied: %s", m.ID, m.Version, strings.Join(comps, ", "))
	return nil
}

// fail restores what an apply already changed and records why.
func (u *Updater) fail(ctx context.Context, st *State, m Manifest, cause error, done []string) error {
	u.logf("live update %s failed, rolling back: %v", m.ID, cause)
	prevUI := ""
	if st.Pending != nil {
		prevUI = st.Pending.PrevUI
	}
	msg := cause.Error()
	if err := u.restore(ctx, m.ID, done, prevUI); err != nil {
		msg += "; rollback also failed: " + err.Error()
	}
	st.Pending = nil
	u.record(st, Entry{ID: m.ID, Version: m.Version, Action: "apply", Result: "rolled-back", Message: msg, Components: m.Components()})
	return cause
}

// restore puts back the binaries saved before update id and the UI that
// was live, then restarts and checks what changed.
func (u *Updater) restore(ctx context.Context, id string, comps []string, prevUI string) error {
	var errs []string
	for i := len(comps) - 1; i >= 0; i-- {
		c := comps[i]
		if c == CompUI {
			if err := u.setUI(prevUI); err != nil {
				errs = append(errs, err.Error())
			}
			continue
		}
		saved := filepath.Join(u.path(savedDir, id), binName(c))
		want, _, err := hashFile(saved)
		if err != nil {
			errs = append(errs, "no saved "+binName(c))
			continue
		}
		if have, _, _ := hashFile(filepath.Join(u.BinDir, binName(c))); have == want {
			continue // never replaced; nothing to restart
		}
		if err := copyFile(saved, filepath.Join(u.BinDir, binName(c)), 0o755); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if err := u.Sys.Restart(ctx, c); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if err := u.Sys.Healthy(ctx, c, ""); err != nil {
			errs = append(errs, binName(c)+" after rollback: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Rollback undoes the newest applied live update.
func (u *Updater) Rollback(ctx context.Context) error {
	st, err := ReadState(u.Root)
	if err != nil {
		return err
	}
	if st.Pending != nil {
		return errors.New("an update is being applied")
	}
	idx := -1
	for i := len(st.History) - 1; i >= 0; i-- {
		e := st.History[i]
		if e.Action == "apply" && e.Result == "applied" && !e.RolledBack {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("there is no live update to roll back")
	}
	e := st.History[idx]
	if busy, _ := u.Sys.Busy(ctx); len(busy) > 0 {
		return fmt.Errorf("%w: %s", ErrBusy, strings.Join(busy, "; "))
	}
	err = u.restore(ctx, e.ID, e.Components, e.PrevUI)
	st.History[idx].RolledBack = true
	st.Current = ""
	for i := idx - 1; i >= 0; i-- {
		if p := st.History[i]; p.Action == "apply" && p.Result == "applied" && !p.RolledBack {
			st.Current = p.ID
			break
		}
	}
	res, msg := "rolled-back", ""
	if err != nil {
		res, msg = "failed", err.Error()
	}
	u.record(&st, Entry{ID: e.ID, Version: e.Version, Action: "rollback", Result: res, Message: msg, Components: e.Components})
	return err
}

// Recover undoes an apply that was interrupted, for example by a power
// cut, and leaves the host as it was before that update.
func (u *Updater) Recover(ctx context.Context) error {
	st, err := ReadState(u.Root)
	if err != nil {
		return err
	}
	if st.Pending == nil {
		return nil
	}
	p := *st.Pending
	u.logf("live update %s was interrupted during %s; restoring the previous version", p.ID, p.Phase)
	var touched []string
	for _, c := range p.Components {
		if c == CompUI || fileExists(filepath.Join(u.path(savedDir, p.ID), binName(c))) {
			touched = append(touched, c)
		}
		if c == p.Phase {
			break
		}
	}
	err = u.restore(ctx, p.ID, touched, p.PrevUI)
	st.Pending = nil
	res, msg := "rolled-back", "interrupted during "+p.Phase
	if err != nil {
		res, msg = "failed", msg+"; "+err.Error()
	}
	u.record(&st, Entry{ID: p.ID, Version: p.Version, Action: "recover", Result: res, Message: msg, Components: p.Components})
	return nil
}

// PackageInstalled records that a package install replaced the binaries
// and UI: the packaged UI is served again and live history stays.
func (u *Updater) PackageInstalled(version string) error {
	st, err := ReadState(u.Root)
	if err != nil {
		return err
	}
	if err := u.setUI(""); err != nil {
		return err
	}
	if st.Current == "" && st.Pending == nil {
		return nil
	}
	for i := range st.History {
		if st.History[i].Action == "apply" && st.History[i].Result == "applied" {
			st.History[i].RolledBack = true
		}
	}
	st.Current, st.Pending, st.Waiting = "", nil, ""
	u.record(&st, Entry{ID: "package-" + version, Version: version, Action: "apply", Result: "superseded", Message: "a package install replaced the live updates"})
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (u *Updater) record(st *State, e Entry) {
	if e.At.IsZero() {
		e.At = u.now()
	}
	st.History = append(st.History, e)
	if err := u.save(*st); err != nil {
		u.logf("live update: saving state: %v", err)
	}
}

// prune keeps the newest releases and their saved binaries, and always the
// live one and those a rollback could return to.
func (u *Updater) prune(st State) {
	keep := map[string]bool{st.Current: true}
	n := 0
	for i := len(st.History) - 1; i >= 0 && n < keepReleases; i-- {
		if e := st.History[i]; e.Action == "apply" && e.Result == "applied" {
			keep[e.ID] = true
			n++
		}
	}
	if t := u.uiTarget(); t != "" {
		keep[filepath.Base(filepath.Dir(filepath.Dir(t)))] = true
	}
	for _, dir := range []string{releasesDir, savedDir} {
		entries, _ := os.ReadDir(u.path(dir))
		for _, e := range entries {
			if e.IsDir() && !keep[e.Name()] && !strings.HasPrefix(e.Name(), ".") {
				_ = os.RemoveAll(u.path(dir, e.Name()))
			}
		}
	}
}
