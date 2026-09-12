package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/backup/cdc"
)

// Config bounds the engine's local footprint and concurrency. Zero values fall
// back to safe defaults.
type Config struct {
	// PackTarget is the flush size for pack objects.
	PackTarget int
	// ChunkConfig sets the content-defined chunk-size bounds.
	ChunkConfig cdc.Config
	// MaxLocalBytes caps the local repository size. Zero means unbounded.
	MaxLocalBytes int64
	// MinHostFreeBytes is the host free-space floor the repository must never
	// cross. Zero means no explicit reserve.
	MinHostFreeBytes int64
}

func (c Config) withDefaults() Config {
	if c.PackTarget <= 0 {
		c.PackTarget = DefaultPackTarget
	}
	if c.ChunkConfig.Avg <= 0 {
		c.ChunkConfig = cdc.DefaultConfig()
	}
	return c
}

// Engine captures and restores backups against a repository.
type Engine struct {
	repo *Repository
	cfg  Config
}

// NewEngine builds an engine over a repository.
func NewEngine(repo *Repository, cfg Config) *Engine {
	return &Engine{repo: repo, cfg: cfg.withDefaults()}
}

// Repo exposes the underlying repository.
func (e *Engine) Repo() *Repository { return e.repo }

// CaptureOptions describes one workload capture.
type CaptureOptions struct {
	Source       string
	WorkloadID   string
	WorkloadName string
	Consistency  string
	Blueprint    Blueprint
	// Excludes are absolute paths (or path prefixes) under Source that are safe
	// ephemeral data and must be skipped.
	Excludes []string
}

// Capture performs a live, crash-consistent backup of opts.Source into the
// repository. It never freezes, pauses, stops, or otherwise interrupts a
// workload: it only reads the directory tree. It returns the sealed manifest
// and the local restore-point state (remote upload is a separate stage).
func (e *Engine) Capture(ctx context.Context, opts CaptureOptions) (*Manifest, *PointState, error) {
	if opts.Source == "" {
		return nil, nil, fmt.Errorf("capture source is required")
	}
	if err := e.checkWorkspace(); err != nil {
		return nil, nil, err
	}
	start := time.Now()
	namespace := Namespace(firstNonEmpty(opts.WorkloadName, opts.WorkloadID), opts.WorkloadID)
	chunker := cdc.New(e.cfg.ChunkConfig)
	cache, err := e.repo.loadCache(namespace)
	if err != nil {
		return nil, nil, err
	}
	pw := e.repo.newPackWriter(e.cfg.PackTarget)
	pw.guard = e.checkWorkspace

	excl := make(map[string]struct{}, len(opts.Excludes))
	for _, p := range opts.Excludes {
		excl[filepath.Clean(p)] = struct{}{}
	}

	var (
		files []FileEntry
		stats CaptureStats
		seen  = map[string]struct{}{}
	)

	walkErr := filepath.WalkDir(opts.Source, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, skip := excl[filepath.Clean(path)]; skip {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(opts.Source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		uid, gid, inode, ctimeNS := statMeta(info)
		seen[rel] = struct{}{}
		switch {
		case d.IsDir():
			files = append(files, FileEntry{
				Path: rel, Type: EntryDir, Mode: uint32(info.Mode().Perm()),
				UID: uid, GID: gid, MTimeNS: info.ModTime().UnixNano(),
			})
			return nil
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			files = append(files, FileEntry{
				Path: rel, Type: EntrySymlink, Mode: uint32(info.Mode().Perm()),
				UID: uid, GID: gid, MTimeNS: info.ModTime().UnixNano(), Linkname: target,
			})
			return nil
		case info.Mode().IsRegular():
			meta := cacheEntry{
				Size: info.Size(), MTimeNS: info.ModTime().UnixNano(), CTimeNS: ctimeNS,
				Inode: inode, Mode: uint32(info.Mode().Perm()), UID: uid, GID: gid,
			}
			stats.FilesScanned++
			stats.LogicalBytes += info.Size()
			chunks, reused, err := e.captureFile(ctx, rel, path, meta, cache, pw, &stats)
			if err != nil {
				return err
			}
			if reused {
				stats.FilesUnchanged++
			} else {
				stats.FilesChanged++
			}
			cache.update(rel, meta, chunks)
			files = append(files, FileEntry{
				Path: rel, Type: EntryFile, Mode: meta.Mode, UID: uid, GID: gid,
				Size: info.Size(), MTimeNS: meta.MTimeNS, Chunks: chunks,
			})
			return nil
		default:
			// Sockets, devices, and pipes are recorded as metadata-only dirs of
			// their parent; their contents are not meaningful to copy.
			return nil
		}
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	if err := pw.flush(); err != nil {
		return nil, nil, err
	}
	stats.PacksCommitted = len(pw.packs)
	stats.DurationNanos = time.Since(start).Nanoseconds()

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	backupID := uuid.NewString()
	man := &Manifest{
		Kind:           ManifestKind,
		RepoVersion:    RepoVersion,
		ChunkAlgorithm: ChunkAlgorithm,
		ChunkConfig:    chunker.Config(),
		BackupID:       backupID,
		WorkloadID:     opts.WorkloadID,
		WorkloadName:   opts.WorkloadName,
		Namespace:      namespace,
		CreatedAtNS:    time.Now().UnixNano(),
		Consistency:    firstNonEmpty(opts.Consistency, ConsistencyCrash),
		Blueprint:      normalizeBlueprint(opts.Blueprint, opts),
		Files:          files,
		Stats:          stats,
	}
	if err := e.repo.writeManifest(man); err != nil {
		return nil, nil, err
	}
	cache.prune(seen)
	if err := cache.save(); err != nil {
		return nil, nil, err
	}
	state := &PointState{
		BackupID: backupID, Namespace: namespace, WorkloadID: opts.WorkloadID,
		WorkloadName: opts.WorkloadName, CreatedAtNS: man.CreatedAtNS,
		LocalComplete: true, Remote: RemoteNone, Packs: append([]string(nil), pw.packs...),
	}
	if err := e.repo.writeState(state); err != nil {
		return nil, nil, err
	}
	return man, state, nil
}

// captureFile returns the ordered chunk ids for one file, reusing cached chunk
// references when the file is provably unchanged and all referenced chunks are
// still present in the repository.
func (e *Engine) captureFile(ctx context.Context, relPath, absPath string, meta cacheEntry, cache *metaCache, pw *packWriter, stats *CaptureStats) ([]KeyID, bool, error) {
	if cached, ok := cache.lookup(relPath, meta); ok && e.allPresent(cached) {
		stats.ChunksTotal += len(cached)
		stats.ChunksReused += len(cached)
		return cached, true, nil
	}
	f, err := os.Open(absPath)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	var chunks []KeyID
	err = cdc.New(e.cfg.ChunkConfig).Split(f, func(chunk []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := e.repo.keys.ID(chunk)
		stats.ChunksTotal++
		stats.BytesRead += int64(len(chunk))
		chunks = append(chunks, id)
		if e.repo.Has(id) {
			stats.ChunksReused++
			return nil
		}
		frame, err := compressChunk(chunk)
		if err != nil {
			return err
		}
		sealed, err := e.repo.keys.Seal(id, frame)
		if err != nil {
			return err
		}
		isNew, err := pw.add(id, sealed)
		if err != nil {
			return err
		}
		if isNew {
			stats.ChunksNew++
			stats.PhysicalNewData += int64(len(sealed))
		} else {
			stats.ChunksReused++
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return chunks, false, nil
}

func (e *Engine) allPresent(ids []KeyID) bool {
	for _, id := range ids {
		if !e.repo.Has(id) {
			return false
		}
	}
	return true
}

func normalizeBlueprint(bp Blueprint, opts CaptureOptions) Blueprint {
	bp.Kind = BlueprintKind
	if bp.Version == 0 {
		bp.Version = 1
	}
	if bp.WorkloadID == "" {
		bp.WorkloadID = opts.WorkloadID
	}
	if bp.Name == "" {
		bp.Name = opts.WorkloadName
	}
	return bp
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// writeManifest seals and stores a manifest under the workload namespace.
func (r *Repository) writeManifest(m *Manifest) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	sealed, err := r.keys.SealBytes("manifest:"+m.Namespace, raw)
	if err != nil {
		return err
	}
	dir := filepath.Join(r.root, "snapshots", m.Namespace)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, m.BackupID+".snap"), sealed)
}

func (r *Repository) writeState(s *PointState) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	dir := filepath.Join(r.root, "snapshots", s.Namespace)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, s.BackupID+".state"), raw)
}
