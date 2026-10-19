package backuphost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// RulesPath is where a workload keeps its own backup rules, inside the
// guest. They travel with the workload and its backups, and every capture
// reads them first.
const RulesPath = "/.ndl-backup/rules.json"

// WorkloadRules are a SuperAdmin's per-workload overrides: paths never backed
// up, and paths always backed up whatever the policy's scope would skip.
// The most specific path wins, so a kept folder inside a skipped one is
// still captured, and the other way round.
type WorkloadRules struct {
	Version   int       `json:"version"`
	Skip      []string  `json:"skip,omitempty"`
	Keep      []string  `json:"keep,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
}

// cleanGuestPaths keeps absolute, clean guest paths, sorted and unique.
func cleanGuestPaths(in []string, allowRoot bool) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") || strings.Contains(p, "\x00") {
			return nil, fmt.Errorf("%q is not an absolute path in the workload", p)
		}
		p = path.Clean(p)
		if p == "/" && !allowRoot {
			return nil, errors.New("the whole workload cannot be skipped; leave it out of the policy instead")
		}
		if p == path.Dir(RulesPath) || strings.HasPrefix(p, path.Dir(RulesPath)+"/") {
			continue // the rules themselves are always kept
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Normalize validates the rules for storage.
func (r WorkloadRules) Normalize() (WorkloadRules, error) {
	var err error
	r.Version = 1
	if r.Skip, err = cleanGuestPaths(r.Skip, false); err != nil {
		return r, err
	}
	if r.Keep, err = cleanGuestPaths(r.Keep, true); err != nil {
		return r, err
	}
	keep := map[string]bool{}
	for _, p := range r.Keep {
		keep[p] = true
	}
	for _, p := range r.Skip {
		if keep[p] {
			return r, fmt.Errorf("%s is both skipped and kept", p)
		}
	}
	return r, nil
}

// rulesDir opens the rules folder inside rootfs without following a
// symlink the guest may have put there to reach the host.
func rulesDir(rootfs string, create bool) (string, error) {
	if rootfs == "" || !filepath.IsAbs(rootfs) {
		return "", errors.New("the workload root is required")
	}
	dir := filepath.Join(rootfs, strings.TrimPrefix(path.Dir(RulesPath), "/"))
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) && create {
		if err := os.Mkdir(dir, 0o755); err != nil {
			return "", err
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%s in the workload is not a plain folder", path.Dir(RulesPath))
	}
	return dir, nil
}

// LoadRules reads a workload's rules; a workload without any has none.
func LoadRules(rootfs string) (WorkloadRules, error) {
	var r WorkloadRules
	dir, err := rulesDir(rootfs, false)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	f, err := os.OpenFile(filepath.Join(dir, path.Base(RulesPath)), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return r, fmt.Errorf("reading %s: %w", RulesPath, err)
	}
	defer f.Close()
	dec := json.NewDecoder(&limitedReader{f: f, n: 1 << 20})
	if err := dec.Decode(&r); err != nil {
		return r, fmt.Errorf("%s is not valid: %w", RulesPath, err)
	}
	return r.Normalize()
}

type limitedReader struct {
	f interface{ Read([]byte) (int, error) }
	n int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errors.New("file is too large")
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.f.Read(p)
	l.n -= int64(n)
	return n, err
}

// SaveRules writes a workload's rules into it atomically.
func SaveRules(rootfs string, r WorkloadRules) (WorkloadRules, error) {
	r, err := r.Normalize()
	if err != nil {
		return r, err
	}
	dir, err := rulesDir(rootfs, true)
	if err != nil {
		return r, err
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now().UTC()
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return r, err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".rules-%d.tmp", time.Now().UnixNano()))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return r, err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return r, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return r, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, path.Base(RulesPath))); err != nil {
		_ = os.Remove(tmp)
		return r, err
	}
	return r, nil
}

// applyRules adds a workload's rules to a capture's include and exclude
// lists. An empty include list means "everything"; a kept path then needs
// the whole workload listed too, or it would become the only thing kept.
func applyRules(includes, excludes []string, r WorkloadRules) ([]string, []string) {
	excludes = append(excludes, r.Skip...)
	if len(r.Keep) > 0 {
		if len(includes) == 0 {
			includes = append(includes, "/")
		}
		includes = append(includes, r.Keep...)
	}
	return includes, excludes
}

// BrowseEntry is one item of a workload folder listing.
type BrowseEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Dir     bool   `json:"dir"`
	Symlink bool   `json:"symlink,omitempty"`
	Size    int64  `json:"size"`
	Files   int64  `json:"files,omitempty"`
	MTime   int64  `json:"mtime"`
	// Partial marks a folder whose size ran out of time; it is at least Size.
	Partial bool   `json:"partial,omitempty"`
	Label   string `json:"label"`
	// Rule is this path's own rule: skip, keep or "".
	Rule string `json:"rule,omitempty"`
	// Effective is the rule a parent or this path gives it.
	Effective string `json:"effective,omitempty"`
}

// Listing is a workload folder with its rules.
type Listing struct {
	Path    string        `json:"path"`
	Entries []BrowseEntry `json:"entries"`
	Rules   WorkloadRules `json:"rules"`
}

// skipped are kernel and runtime trees that are never backed up or sized.
var browseSkip = map[string]bool{"/proc": true, "/sys": true, "/dev": true, "/run": true}

// resolveGuestDir maps a guest folder to its host path, refusing symlinks
// anywhere on the way so a guest cannot point the listing at the host.
func resolveGuestDir(rootfs, guest string) (string, string, error) {
	guest = path.Clean("/" + strings.TrimSpace(guest))
	host := filepath.Clean(rootfs)
	for _, part := range strings.Split(strings.TrimPrefix(guest, "/"), "/") {
		if part == "" {
			continue
		}
		host = filepath.Join(host, part)
		info, err := os.Lstat(host)
		if err != nil {
			return "", "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", "", fmt.Errorf("%s is a link; open its target instead", guest)
		}
		if !info.IsDir() {
			return "", "", fmt.Errorf("%s is not a folder", guest)
		}
	}
	return guest, host, nil
}

// Browse lists one folder of a workload with sizes, a short label for each
// item and the rule that applies to it. Folder sizes share a time budget.
func Browse(ctx context.Context, rootfs, guest string, budget time.Duration) (Listing, error) {
	if budget <= 0 {
		budget = 8 * time.Second
	}
	guest, host, err := resolveGuestDir(rootfs, guest)
	if err != nil {
		return Listing{}, err
	}
	rules, err := LoadRules(rootfs)
	if err != nil {
		return Listing{}, err
	}
	entries, err := os.ReadDir(host)
	if err != nil {
		return Listing{}, err
	}
	deadline := time.Now().Add(budget)
	out := Listing{Path: guest, Rules: rules, Entries: make([]BrowseEntry, 0, len(entries))}
	for _, e := range entries {
		gp := path.Join(guest, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		be := BrowseEntry{
			Name: e.Name(), Path: gp, Dir: e.IsDir(), Symlink: info.Mode()&fs.ModeSymlink != 0,
			MTime: info.ModTime().Unix(), Label: Label(gp, e.IsDir()),
		}
		be.Rule, be.Effective = ruleFor(gp, rules)
		switch {
		case be.Symlink:
		case !be.Dir:
			be.Size, be.Files = info.Size(), 1
		case browseSkip[gp]:
		default:
			be.Size, be.Files, be.Partial = dirSize(ctx, filepath.Join(host, e.Name()), deadline)
		}
		out.Entries = append(out.Entries, be)
	}
	return out, nil
}

func ruleFor(p string, r WorkloadRules) (own, effective string) {
	best := -1
	check := func(list []string, rule string) {
		for _, q := range list {
			if q == p {
				own = rule
			}
			if (q == p || q == "/" || strings.HasPrefix(p, q+"/")) && len(q) > best {
				best, effective = len(q), rule
			}
		}
	}
	check(r.Keep, "keep")
	check(r.Skip, "skip")
	return own, effective
}

// dirSize adds up a folder on one filesystem until the deadline.
func dirSize(ctx context.Context, dir string, deadline time.Time) (size, files int64, partial bool) {
	var dev uint64
	if st, ok := statT(dir); ok {
		dev = st.Dev
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			partial = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			if st, ok := statT(p); ok && st.Dev != dev {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			size += info.Size()
			files++
		}
		return nil
	})
	return size, files, partial
}

func statT(p string) (*syscall.Stat_t, bool) {
	info, err := os.Lstat(p)
	if err != nil {
		return nil, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return st, ok
}
