package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ManifestSummary contains the fields needed for status views without
// retaining the manifest's potentially large file and chunk lists.
type ManifestSummary struct {
	CaptureMode     string
	Consistency     string
	LogicalBytes    int64
	PhysicalNewData int64
}

type manifestSummaryCacheEntry struct {
	size    int64
	modTime int64
	summary ManifestSummary
}

// LoadManifestSummary reads and authenticates the status fields of a manifest.
// An unchanged manifest is read and parsed once per repository process.
func (r *Repository) LoadManifestSummary(namespace, backupID string) (ManifestSummary, error) {
	path := filepath.Join(r.root, "snapshots", namespace, backupID+".snap")
	info, err := os.Stat(path)
	if err != nil {
		return ManifestSummary{}, err
	}

	r.manifestMu.Lock()
	defer r.manifestMu.Unlock()
	if cached, ok := r.manifestCache[path]; ok && cached.size == info.Size() && cached.modTime == info.ModTime().UnixNano() {
		return cached.summary, nil
	}

	readFile := r.manifestRead
	if readFile == nil {
		readFile = os.ReadFile
	}
	raw, err := readFile(path)
	if err != nil {
		return ManifestSummary{}, err
	}
	plain, err := r.keys.OpenBytes("manifest:"+namespace, raw)
	if err != nil {
		return ManifestSummary{}, fmt.Errorf("manifest failed authentication: %w", err)
	}
	var decoded struct {
		RepoVersion    int    `json:"repo_version"`
		ChunkAlgorithm string `json:"chunk_algorithm"`
		Consistency    string `json:"consistency"`
		Blueprint      struct {
			CaptureMode string `json:"capture_mode"`
		} `json:"blueprint"`
		Stats struct {
			LogicalBytes    int64 `json:"logical_bytes"`
			PhysicalNewData int64 `json:"physical_new_data"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(plain, &decoded); err != nil {
		return ManifestSummary{}, err
	}
	if decoded.RepoVersion != RepoVersion {
		return ManifestSummary{}, fmt.Errorf("unsupported repository version %d", decoded.RepoVersion)
	}
	if decoded.ChunkAlgorithm != ChunkAlgorithm {
		return ManifestSummary{}, fmt.Errorf("unsupported chunk algorithm %q", decoded.ChunkAlgorithm)
	}
	summary := ManifestSummary{
		CaptureMode:     decoded.Blueprint.CaptureMode,
		Consistency:     decoded.Consistency,
		LogicalBytes:    decoded.Stats.LogicalBytes,
		PhysicalNewData: decoded.Stats.PhysicalNewData,
	}
	r.manifestCache[path] = manifestSummaryCacheEntry{
		size: info.Size(), modTime: info.ModTime().UnixNano(), summary: summary,
	}
	return summary, nil
}

// LoadManifest reads and authenticates one restore point's manifest.
func (r *Repository) LoadManifest(namespace, backupID string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(r.root, "snapshots", namespace, backupID+".snap"))
	if err != nil {
		return nil, err
	}
	plain, err := r.keys.OpenBytes("manifest:"+namespace, raw)
	if err != nil {
		return nil, fmt.Errorf("manifest failed authentication: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, err
	}
	if m.RepoVersion != RepoVersion {
		return nil, fmt.Errorf("unsupported repository version %d", m.RepoVersion)
	}
	if m.ChunkAlgorithm != ChunkAlgorithm {
		return nil, fmt.Errorf("unsupported chunk algorithm %q", m.ChunkAlgorithm)
	}
	return &m, nil
}

// ListStates returns every restore point's state sidecar, newest first.
func (r *Repository) ListStates() ([]PointState, error) {
	base := filepath.Join(r.root, "snapshots")
	nsDirs, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	var out []PointState
	for _, ns := range nsDirs {
		if !ns.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(base, ns.Name()))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) != ".state" {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(base, ns.Name(), f.Name()))
			if err != nil {
				return nil, err
			}
			var s PointState
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, err
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAtNS > out[j].CreatedAtNS })
	return out, nil
}

// LoadState reads one restore point's state sidecar.
func (r *Repository) LoadState(namespace, backupID string) (*PointState, error) {
	raw, err := os.ReadFile(filepath.Join(r.root, "snapshots", namespace, backupID+".state"))
	if err != nil {
		return nil, err
	}
	var s PointState
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Verify checks that every chunk referenced by a manifest is present in the
// repository, without decrypting each chunk. It uses the in-memory index rather
// than a per-chunk remote HEAD, which was part of the old performance problem.
func (r *Repository) Verify(m *Manifest) error {
	missing := 0
	for _, f := range m.Files {
		for _, id := range f.Chunks {
			if !r.Has(id) {
				missing++
			}
		}
	}
	if missing > 0 {
		return fmt.Errorf("%d referenced chunk(s) missing from repository", missing)
	}
	return nil
}

// RestoreOptions controls a restore.
type RestoreOptions struct {
	// Dest is the target directory. Restore-as-new simply points this at a new
	// path; replacing an existing workload is the caller's explicit decision.
	Dest string
	// Chown attempts to restore uid/gid. It is best-effort and skipped on
	// permission errors so unprivileged restores still reconstruct content.
	Chown bool
}

// Restore reconstructs the workload filesystem described by m into opts.Dest,
// preserving mode, mtime, symlinks, and (best-effort) ownership. It streams one
// chunk at a time and never materializes the whole workload in memory.
func (r *Repository) Restore(ctx context.Context, m *Manifest, opts RestoreOptions) error {
	if opts.Dest == "" {
		return fmt.Errorf("restore destination is required")
	}
	if err := r.Verify(m); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.Dest, 0o750); err != nil {
		return err
	}
	entries := append([]FileEntry(nil), m.Files...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	var links []FileEntry
	for _, e := range entries {
		if e.Type == EntryFile && e.Hardlink != "" {
			links = append(links, e)
			continue
		}
		if err := r.restoreEntry(ctx, opts.Dest, e, opts.Chown); err != nil {
			return err
		}
	}
	for _, e := range links {
		if err := r.restoreEntry(ctx, opts.Dest, e, opts.Chown); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) restoreEntry(ctx context.Context, dest string, e FileEntry, chown bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target := filepath.Join(dest, filepath.FromSlash(e.Path))
	switch e.Type {
	case EntryDir:
		if err := os.MkdirAll(target, os.FileMode(e.Mode).Perm()); err != nil {
			return err
		}
	case EntrySymlink:
		_ = os.Remove(target)
		if err := os.Symlink(e.Linkname, target); err != nil {
			return err
		}
	case EntryFile:
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		if e.Hardlink != "" {
			src := filepath.Join(dest, filepath.FromSlash(e.Hardlink))
			_ = os.Remove(target)
			if err := os.Link(src, target); err != nil {
				return err
			}
			break
		}
		if err := r.restoreFile(target, e); err != nil {
			return err
		}
	case EntryFIFO, EntrySocket, EntryBlock, EntryChar:
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		_ = os.Remove(target)
		if err := makeSpecial(target, e.Type, e.Mode, e.Rdev); err != nil {
			return fmt.Errorf("restore special %s: %w", e.Path, err)
		}
	}
	r.applyMetadata(target, e, chown)
	return nil
}

func (r *Repository) restoreFile(target string, e FileEntry) error {
	perm := os.FileMode(e.Mode).Perm()
	if perm == 0 {
		perm = 0o600
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	if e.Size > 0 {
		_ = f.Truncate(e.Size)
	}
	for _, id := range e.Chunks {
		plain, err := r.GetChunk(id)
		if err != nil {
			return err
		}
		data, err := decompressChunk(plain)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
	}
	if len(e.Holes) > 0 {
		punchHoles(f, e.Holes)
	}
	return nil
}

func (r *Repository) applyMetadata(target string, e FileEntry, chown bool) {
	if e.Type != EntrySymlink {
		perm := os.FileMode(e.Mode).Perm()
		if perm != 0 {
			_ = os.Chmod(target, perm)
		}
		if e.MTimeNS != 0 {
			mt := time.Unix(0, e.MTimeNS)
			_ = os.Chtimes(target, mt, mt)
		}
	}
	if chown {
		// Best-effort: ignore permission errors so unprivileged restores work.
		_ = os.Lchown(target, e.UID, e.GID)
	}
	_ = applyXattrs(target, e.Xattrs)
}
