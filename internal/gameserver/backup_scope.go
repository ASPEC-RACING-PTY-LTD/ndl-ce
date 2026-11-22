package gameserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// BackupScope narrows a server's backups to the folders and files that
// cannot be rebuilt (its code, data and config), instead of the whole
// server folder with its game install and download caches.
//
// Every entry is a path relative to the server folder, using "/".
// A segment may use * ? and [...] wildcards (path.Match). An entry that
// names a folder covers everything inside it.
//
// An empty scope (no Include) means the whole server folder.
type BackupScope struct {
	// Include lists what a backup takes.
	Include []string `json:"include"`
	// Exclude removes paths from Include.
	Exclude []string `json:"exclude"`
	// Newest takes only the most recently changed file matching each
	// entry, even inside an excluded folder (the latest of a rotating
	// set of copies).
	Newest []string `json:"newest"`
}

// Scoped reports whether the scope narrows the backup at all.
func (s BackupScope) Scoped() bool { return len(s.Include) > 0 }

const maxScopeEntries = 100

// newestSettle is how long a Newest file must be unchanged to count as
// finished.
const newestSettle = time.Minute

// CleanBackupScope validates a scope and returns it in canonical form.
func CleanBackupScope(in BackupScope) (BackupScope, error) {
	var out BackupScope
	clean := func(field string, list []string) ([]string, error) {
		if len(list) > maxScopeEntries {
			return nil, fmt.Errorf("%s has more than %d entries", field, maxScopeEntries)
		}
		seen := map[string]bool{}
		res := []string{}
		for _, raw := range list {
			p := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
			p = strings.Trim(p, "/")
			if p == "" {
				continue
			}
			if strings.ContainsRune(p, 0) || len(p) > 512 {
				return nil, fmt.Errorf("%s entry %q is not allowed", field, raw)
			}
			for _, seg := range strings.Split(p, "/") {
				if seg == "" || seg == "." || seg == ".." {
					return nil, fmt.Errorf("%s entry %q must be a plain path inside the server folder", field, raw)
				}
				if _, err := path.Match(seg, ""); err != nil {
					return nil, fmt.Errorf("%s entry %q has a broken wildcard", field, raw)
				}
			}
			if !seen[p] {
				seen[p] = true
				res = append(res, p)
			}
		}
		return res, nil
	}
	var err error
	if out.Include, err = clean("include", in.Include); err != nil {
		return BackupScope{}, err
	}
	if out.Exclude, err = clean("exclude", in.Exclude); err != nil {
		return BackupScope{}, err
	}
	if out.Newest, err = clean("newest", in.Newest); err != nil {
		return BackupScope{}, err
	}
	if len(out.Include) == 0 && (len(out.Exclude) > 0 || len(out.Newest) > 0) {
		return BackupScope{}, errors.New("list at least one path to include, or clear the scope to back up the whole server folder")
	}
	return out, nil
}

// segMatch reports how pattern relates to rel:
// covered when rel is the pattern or inside it, ancestor when rel is a
// folder on the way to it.
func segMatch(pattern, rel string) (covered, ancestor bool) {
	ps := strings.Split(pattern, "/")
	rs := strings.Split(rel, "/")
	n := len(ps)
	if len(rs) < n {
		n = len(rs)
	}
	for i := 0; i < n; i++ {
		if ok, _ := path.Match(ps[i], rs[i]); !ok {
			return false, false
		}
	}
	if len(rs) >= len(ps) {
		return true, false
	}
	return false, true
}

func anyCovered(patterns []string, rel string) bool {
	for _, p := range patterns {
		if c, _ := segMatch(p, rel); c {
			return true
		}
	}
	return false
}

func anyAncestor(patterns []string, rel string) bool {
	for _, p := range patterns {
		if _, a := segMatch(p, rel); a {
			return true
		}
	}
	return false
}

// scopePlan is a scope resolved against one server folder.
type scopePlan struct {
	scope  BackupScope
	forced map[string]bool // Newest picks, relative paths
}

func planScope(root string, scope BackupScope) (scopePlan, error) {
	plan := scopePlan{scope: scope, forced: map[string]bool{}}
	for _, pat := range scope.Newest {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pat)))
		if err != nil {
			return scopePlan{}, err
		}
		// Prefer the newest file that has not changed for a minute: the
		// very newest may still be half written.
		var best, fresh string
		var bestAt, freshAt time.Time
		settled := time.Now().Add(-newestSettle)
		for _, m := range matches {
			st, err := os.Lstat(m)
			if err != nil || !st.Mode().IsRegular() {
				continue
			}
			at := st.ModTime()
			if at.After(settled) {
				if fresh == "" || at.After(freshAt) {
					fresh, freshAt = m, at
				}
				continue
			}
			if best == "" || at.After(bestAt) {
				best, bestAt = m, at
			}
		}
		if best == "" {
			best = fresh
		}
		if best == "" {
			continue
		}
		rel, err := filepath.Rel(root, best)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		plan.forced[filepath.ToSlash(rel)] = true
	}
	return plan, nil
}

type scopeDecision int

const (
	scopeSkip    scopeDecision = iota // leave out (SkipDir for a folder)
	scopeDescend                      // a folder on the way to something kept
	scopeTake                         // keep it
)

// decide says what to do with rel (a slash path, never ".").
func (p scopePlan) decide(rel string, isDir bool) scopeDecision {
	if p.forced[rel] {
		return scopeTake
	}
	forcedBelow := false
	if isDir {
		prefix := rel + "/"
		for f := range p.forced {
			if strings.HasPrefix(f, prefix) {
				forcedBelow = true
				break
			}
		}
	}
	if anyCovered(p.scope.Exclude, rel) {
		if forcedBelow {
			return scopeDescend
		}
		return scopeSkip
	}
	if anyCovered(p.scope.Include, rel) {
		return scopeTake
	}
	if isDir && (forcedBelow || anyAncestor(p.scope.Include, rel)) {
		return scopeDescend
	}
	return scopeSkip
}

// owned reports whether a restore of this scope is responsible for rel:
// files it covers are replaced, and ones the backup lacks are removed.
func (s BackupScope) owned(rel string) bool {
	return anyCovered(s.Include, rel) && !anyCovered(s.Exclude, rel)
}

// backupManifestName is the first entry of a scoped archive. Restore
// reads it to replace only what the backup covers.
const backupManifestName = ".ndl-backup.json"

type backupManifest struct {
	Version int         `json:"version"`
	Scope   BackupScope `json:"scope"`
}

func (h *LocalHost) backupScopePath(id string) (string, error) {
	if !ValidHostID(id) {
		return "", fmt.Errorf("game server id is invalid")
	}
	// Kept beside the backups, outside the server folder, so the game
	// itself cannot change what its backups take.
	return filepath.Join(h.RT.Root, "game-backups", id, "scope.json"), nil
}

// BackupScope returns the server's scope (empty: the whole folder).
func (h *LocalHost) BackupScope(_ context.Context, id string) (BackupScope, error) {
	p, err := h.backupScopePath(id)
	if err != nil {
		return BackupScope{}, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return BackupScope{Include: []string{}, Exclude: []string{}, Newest: []string{}}, nil
	}
	if err != nil {
		return BackupScope{}, err
	}
	var s BackupScope
	if err := json.Unmarshal(raw, &s); err != nil {
		return BackupScope{}, fmt.Errorf("backup scope file is damaged: %w", err)
	}
	return CleanBackupScope(s)
}

// SetBackupScope stores the scope; an empty one removes it.
func (h *LocalHost) SetBackupScope(_ context.Context, id string, scope BackupScope) (BackupScope, error) {
	clean, err := CleanBackupScope(scope)
	if err != nil {
		return BackupScope{}, err
	}
	p, err := h.backupScopePath(id)
	if err != nil {
		return BackupScope{}, err
	}
	if !clean.Scoped() {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return BackupScope{}, err
		}
		return clean, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return BackupScope{}, err
	}
	raw, _ := json.MarshalIndent(clean, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return BackupScope{}, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return BackupScope{}, err
	}
	return clean, nil
}
