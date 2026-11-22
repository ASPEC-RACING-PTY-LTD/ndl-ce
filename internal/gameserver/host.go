package gameserver

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/no-dal/ndl-ce/internal/iojail"
)

// Host is every host-side game server operation: containers, the server
// folder, backups and content. The control plane is unprivileged, so in
// production it reaches a Host through the root agent; LocalHost is the
// implementation the agent runs (and tests use directly).
//
// Every method is scoped to one server ID. Paths are relative to that
// server's folder and are resolved beneath it, never from a caller path.
type Host interface {
	EnsureData(ctx context.Context, id string) (string, error)
	RemoveData(ctx context.Context, id string) error
	Install(ctx context.Context, srv Server, tmpl Template, progress ProgressFunc) error
	LogTail(ctx context.Context, id string, max int) string
	Start(ctx context.Context, spec RunSpec) (string, error)
	Stop(ctx context.Context, id string) error
	StopGraceful(ctx context.Context, id, stopCmd string, timeout time.Duration) error
	Kill(ctx context.Context, id string) error
	Running(ctx context.Context, id string) bool
	Send(ctx context.Context, id, line string) error
	Logs(ctx context.Context, id string, tail int) (string, error)
	ListDir(ctx context.Context, id, rel string) (DirListing, error)
	ReadFile(ctx context.Context, id, rel string, max int64) ([]byte, error)
	WriteFile(ctx context.Context, id, rel string, body []byte) error
	// WriteFileFrom streams r into rel, refusing more than max bytes. The
	// file appears only once it is complete.
	WriteFileFrom(ctx context.Context, id, rel string, r io.Reader, max int64) (int64, error)
	Mkdir(ctx context.Context, id, rel string) error
	Remove(ctx context.Context, id, rel string) error
	Rename(ctx context.Context, id, from, to string) error
	Extract(ctx context.Context, id, destRel string, raw []byte, name string) error
	Backup(ctx context.Context, id, backupID string) (BackupFile, error)
	RestoreBackup(ctx context.Context, id, backupID string) error
	DeleteBackup(ctx context.Context, id, backupID string) error
	// BackupScope and SetBackupScope read and change what the server's
	// backups take (backup_scope.go). An empty scope is the whole folder.
	BackupScope(ctx context.Context, id string) (BackupScope, error)
	SetBackupScope(ctx context.Context, id string, scope BackupScope) (BackupScope, error)
	// WorkshopSync runs one live Workshop sync operation.
	WorkshopSync(ctx context.Context, id string, call SyncCall) (SyncReply, error)
	// Database runs one operation on the server's own MariaDB.
	Database(ctx context.Context, id string, call DBCall) (DBReply, error)
}

// ProgressFunc receives install phase changes with the current log tail.
type ProgressFunc func(phase, message, logTail string)

// DirEntry is one file manager row.
type DirEntry struct {
	Name       string    `json:"name"`
	Dir        bool      `json:"dir"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

// DirListing is a directory, or a single file when File is true.
type DirListing struct {
	Path  string     `json:"path"`
	File  bool       `json:"file"`
	Size  int64      `json:"size"`
	Items []DirEntry `json:"items"`
}

// BackupFile is a written backup archive.
type BackupFile struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

var hostIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)

// ValidHostID reports whether id is safe to use as a folder name.
func ValidHostID(id string) bool {
	return hostIDPattern.MatchString(id)
}

// LocalHost runs game servers on this machine with the local Docker engine.
type LocalHost struct {
	RT *Runtime

	// Tests replace Steam, SteamCMD and the clock, and run sync inline.
	SyncAPI    WorkshopAPI
	SyncFetch  WorkshopFetcher
	SyncNow    func() time.Time
	SyncInline bool

	mu       sync.Mutex
	progress map[string]ProgressFunc
	hooked   bool
	sync     syncRunner
}

// Ready reports whether game servers can run on this host.
func (h *LocalHost) Ready(ctx context.Context) error { return h.RT.Ready(ctx) }

// NewLocalHost wraps a runtime rooted at root.
func NewLocalHost(root string) *LocalHost {
	return &LocalHost{RT: NewRuntime(root)}
}

func (h *LocalHost) serverDir(id string) (string, error) {
	if !ValidHostID(id) {
		return "", fmt.Errorf("game server id is invalid")
	}
	return h.RT.DataDir(id), nil
}

func (h *LocalHost) jail(id string) (string, error) {
	dir, err := h.serverDir(id)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

func (h *LocalHost) backupPath(id, backupID string) (string, error) {
	if !ValidHostID(id) || !ValidHostID(backupID) {
		return "", fmt.Errorf("backup id is invalid")
	}
	return filepath.Join(h.RT.Root, "game-backups", id, backupID+".tar.gz"), nil
}

func (h *LocalHost) EnsureData(_ context.Context, id string) (string, error) {
	if !ValidHostID(id) {
		return "", fmt.Errorf("game server id is invalid")
	}
	return h.RT.EnsureData(id)
}

func (h *LocalHost) RemoveData(ctx context.Context, id string) error {
	dir, err := h.serverDir(id)
	if err != nil {
		return err
	}
	// A deleted server takes its database with it.
	if c, _ := h.RT.DBConfigFor(id); c != nil {
		if _, err := h.RT.dbRemove(ctx, id, c, true); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}

// Install routes the runtime's progress callbacks to the caller of this
// install, so concurrent installs each see only their own progress.
func (h *LocalHost) Install(ctx context.Context, srv Server, tmpl Template, progress ProgressFunc) error {
	if !ValidHostID(srv.ID) {
		return fmt.Errorf("game server id is invalid")
	}
	h.mu.Lock()
	if h.progress == nil {
		h.progress = map[string]ProgressFunc{}
	}
	if !h.hooked {
		prev := h.RT.OnProgress
		h.RT.OnProgress = func(id, phase, message string) {
			if prev != nil {
				prev(id, phase, message)
			}
			h.mu.Lock()
			fn := h.progress[id]
			h.mu.Unlock()
			if fn != nil {
				fn(phase, message, h.RT.LogTail(id, 200))
			}
		}
		h.hooked = true
	}
	if progress != nil {
		h.progress[srv.ID] = progress
	}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.progress, srv.ID)
		h.mu.Unlock()
	}()
	return h.RT.Install(ctx, srv, tmpl)
}

func (h *LocalHost) LogTail(_ context.Context, id string, max int) string {
	return h.RT.LogTail(id, max)
}

func (h *LocalHost) Start(ctx context.Context, spec RunSpec) (string, error) {
	if !ValidHostID(spec.ID) {
		return "", fmt.Errorf("game server id is invalid")
	}
	// Host folders are never taken from the caller: the only one is the
	// agent's own Workshop sync packs folder, shown read-only.
	spec.ReadOnly = nil
	if spec.WorkshopSync {
		items := h.SyncItemsDir(spec.ID)
		if err := os.MkdirAll(items, 0o755); err != nil {
			log.Printf("game server %s: starting without the Workshop sync folder: %v", spec.ID, err)
		} else {
			spec.ReadOnly = []ReadOnlyMount{{Host: items, Rel: SyncItemsRel}}
		}
	}
	spec.Networks = h.RT.dbNetworksFor(ctx, spec.ID)
	return h.RT.Start(ctx, spec)
}

// Database runs one operation on the server's own MariaDB (database.go).
func (h *LocalHost) Database(ctx context.Context, id string, call DBCall) (DBReply, error) {
	return h.RT.Database(ctx, id, call)
}

func (h *LocalHost) Stop(ctx context.Context, id string) error { return h.RT.Stop(ctx, id) }

func (h *LocalHost) StopGraceful(ctx context.Context, id, stopCmd string, timeout time.Duration) error {
	return h.RT.StopGraceful(ctx, id, stopCmd, timeout)
}

func (h *LocalHost) Kill(ctx context.Context, id string) error { return h.RT.Kill(ctx, id) }

func (h *LocalHost) Running(ctx context.Context, id string) bool { return h.RT.Running(ctx, id) }

func (h *LocalHost) Send(ctx context.Context, id, line string) error { return h.RT.Send(ctx, id, line) }

func (h *LocalHost) Logs(ctx context.Context, id string, tail int) (string, error) {
	return h.RT.Logs(ctx, id, tail)
}

func (h *LocalHost) ListDir(_ context.Context, id, rel string) (DirListing, error) {
	rel, err := iojail.CleanRel(rel)
	if err != nil {
		return DirListing{}, err
	}
	root, err := h.jail(id)
	if err != nil {
		return DirListing{}, err
	}
	f, _, err := iojail.OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		return DirListing{}, fs.ErrNotExist
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return DirListing{}, err
	}
	out := DirListing{Path: rel, Items: []DirEntry{}}
	if !info.IsDir() {
		out.File = true
		out.Size = info.Size()
		return out, nil
	}
	entries, err := f.ReadDir(0)
	if err != nil {
		return DirListing{}, err
	}
	for _, e := range entries {
		row := DirEntry{Name: e.Name(), Dir: e.IsDir()}
		if st, _ := e.Info(); st != nil {
			row.Size = st.Size()
			row.ModifiedAt = st.ModTime()
		}
		out.Items = append(out.Items, row)
	}
	return out, nil
}

func (h *LocalHost) ReadFile(_ context.Context, id, rel string, max int64) ([]byte, error) {
	rel, err := iojail.CleanRel(rel)
	if err != nil {
		return nil, err
	}
	root, err := h.jail(id)
	if err != nil {
		return nil, err
	}
	f, _, err := iojail.OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.IsDir() {
		return nil, fmt.Errorf("path is a directory")
	}
	body, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("file is larger than the editor limit")
	}
	return body, nil
}

func (h *LocalHost) WriteFile(_ context.Context, id, rel string, body []byte) error {
	rel, err := iojail.CleanRel(rel)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("file path is required")
	}
	root, err := h.jail(id)
	if err != nil {
		return err
	}
	if parent := filepath.Dir(rel); parent != "." {
		_ = iojail.MkdirBeneath(root, parent, 0o750)
	}
	f, _, err := iojail.OpenBeneath(root, rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(body)
	return err
}

func (h *LocalHost) WriteFileFrom(_ context.Context, id, rel string, r io.Reader, max int64) (int64, error) {
	rel, err := iojail.CleanRel(rel)
	if err != nil {
		return 0, err
	}
	if rel == "." {
		return 0, fmt.Errorf("file path is required")
	}
	root, err := h.jail(id)
	if err != nil {
		return 0, err
	}
	if err := mkdirAllBeneath(root, filepath.Dir(rel)); err != nil {
		return 0, err
	}
	part := rel + ".part"
	f, _, err := iojail.OpenBeneath(root, part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(r, max+1))
	if err == nil && n > max {
		err = fmt.Errorf("upload is larger than %d MiB", max>>20)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = iojail.RenameBeneath(root, part, rel)
	}
	if err != nil {
		_ = iojail.RemoveBeneath(root, part)
		return 0, err
	}
	return n, nil
}

// mkdirAllBeneath creates rel and its parents under root.
func mkdirAllBeneath(root, rel string) error {
	if rel == "." || rel == "" {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		cur = filepath.Join(cur, part)
		if err := iojail.MkdirBeneath(root, cur, 0o750); err != nil {
			return err
		}
	}
	return nil
}

// Jail is the server's folder, created if missing.
func (h *LocalHost) Jail(id string) (string, error) { return h.jail(id) }

func (h *LocalHost) Mkdir(_ context.Context, id, rel string) error {
	root, err := h.jail(id)
	if err != nil {
		return err
	}
	return iojail.MkdirBeneath(root, rel, 0o750)
}

func (h *LocalHost) Remove(_ context.Context, id, rel string) error {
	root, err := h.jail(id)
	if err != nil {
		return err
	}
	return iojail.RemoveBeneath(root, rel)
}

func (h *LocalHost) Rename(_ context.Context, id, from, to string) error {
	root, err := h.jail(id)
	if err != nil {
		return err
	}
	return iojail.RenameBeneath(root, from, to)
}

func (h *LocalHost) Extract(_ context.Context, id, destRel string, raw []byte, name string) error {
	root, err := h.jail(id)
	if err != nil {
		return err
	}
	return SafeExtract(root, destRel, raw, name)
}

func (h *LocalHost) Backup(ctx context.Context, id, backupID string) (BackupFile, error) {
	root, err := h.jail(id)
	if err != nil {
		return BackupFile{}, err
	}
	path, err := h.backupPath(id, backupID)
	if err != nil {
		return BackupFile{}, err
	}
	scope, err := h.BackupScope(ctx, id)
	if err != nil {
		return BackupFile{}, err
	}
	// The server's own database goes in as a dump (database.go).
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return BackupFile{}, err
	}
	dump := path + ".db.sql"
	hasDB, err := h.RT.dbDump(ctx, id, dump)
	if err != nil {
		return BackupFile{}, err
	}
	if !hasDB {
		dump = ""
	} else {
		defer os.Remove(dump)
	}
	if err := archiveDir(ctx, root, path, scope, dump); err != nil {
		// Never leave a half-written archive: nothing records it, so
		// nothing would ever delete it.
		_ = os.Remove(path)
		return BackupFile{}, err
	}
	out := BackupFile{Path: path}
	if st, err := os.Stat(path); err == nil {
		out.Bytes = st.Size()
	}
	return out, nil
}

func (h *LocalHost) RestoreBackup(ctx context.Context, id, backupID string) error {
	root, err := h.jail(id)
	if err != nil {
		return err
	}
	path, err := h.backupPath(id, backupID)
	if err != nil {
		return err
	}
	dump := path + ".restore.sql"
	defer os.Remove(dump)
	return restoreArchive(root, path, &restoreDB{
		DumpPath: dump,
		Check:    func() error { return h.RT.dbCanRestore(ctx, id) },
		Load:     func(p string) error { return h.RT.dbRestore(ctx, id, p) },
	})
}

func (h *LocalHost) DeleteBackup(_ context.Context, id, backupID string) error {
	path, err := h.backupPath(id, backupID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// archiveDir writes the backup archive. dbDump, when set, is a database
// dump to include.
func archiveDir(ctx context.Context, src, dest string, scope BackupScope, dbDump string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	var plan scopePlan
	if scope.Scoped() {
		var err error
		if plan, err = planScope(src, scope); err != nil {
			return err
		}
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if scope.Scoped() || dbDump != "" {
		// The manifest goes first so a restore knows what it owns, then
		// the database dump, so a restore can check the database is
		// ready before it touches a file.
		raw, _ := json.Marshal(backupManifest{Version: 1, Scope: scope, Database: dbDump != ""})
		if err := tw.WriteHeader(&tar.Header{Name: backupManifestName, Mode: 0o600, Size: int64(len(raw)), ModTime: time.Now(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(raw); err != nil {
			return err
		}
	}
	if dbDump != "" {
		if err := addArchiveFile(tw, dbDump, dbDumpEntry); err != nil {
			return err
		}
	}
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("backup path escapes the jail")
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if scope.Scoped() {
			if rel == "." {
				return nil
			}
			switch plan.decide(filepath.ToSlash(rel), d.IsDir()) {
			case scopeSkip:
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			case scopeDescend:
				return nil
			}
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		readFrom := path
		if info.Mode().IsRegular() && isSQLite(path) {
			// A live database is copied through SQLite (sqlite_snapshot.go).
			snap := dest + ".sqlite-snapshot"
			if err := snapshotSQLite(ctx, path, snap); err != nil {
				return fmt.Errorf("%s: %w", hdr.Name, err)
			}
			defer os.Remove(snap)
			st, err := os.Stat(snap)
			if err != nil {
				return err
			}
			hdr.Size = st.Size()
			readFrom = snap
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(readFrom)
		if err != nil {
			return err
		}
		// Copy exactly the header size. Running servers can grow files
		// (Minecraft cache downloads) which would otherwise trip
		// archive/tar: write too long.
		n, copyErr := io.Copy(tw, io.LimitReader(in, hdr.Size))
		_ = in.Close()
		if copyErr != nil {
			return copyErr
		}
		if n < hdr.Size {
			_, copyErr = io.CopyN(tw, zeroReader{}, hdr.Size-n)
		}
		return copyErr
	})
	// Close in order and keep the first error: a failed flush (a full
	// disk) means the archive is truncated and must not be recorded.
	for _, closeErr := range []error{tw.Close(), gz.Close(), f.Sync()} {
		if err == nil {
			err = closeErr
		}
	}
	return err
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// restoreArchive puts a backup back. A whole-folder backup replaces
// the folder. A scoped backup (one that starts with a manifest) replaces
// only what its scope covers and leaves the rest, such as the game
// install, untouched.
// restoreDB loads a backup's database dump: Check runs before any file
// is touched, Load after the files are back.
type restoreDB struct {
	DumpPath string
	Check    func() error
	Load     func(path string) error
}

func restoreArchive(dest, archivePath string, db *restoreDB) error {
	if dest == "" {
		return fmt.Errorf("data directory is missing")
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	first, err := tr.Next()
	if err == io.EOF {
		return fmt.Errorf("backup is empty")
	}
	if err != nil {
		return err
	}
	var manifest *backupManifest
	if first.Name == backupManifestName {
		var m backupManifest
		if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&m); err != nil {
			return fmt.Errorf("backup manifest is damaged: %w", err)
		}
		if m.Version != 1 {
			return fmt.Errorf("backup manifest is not understood")
		}
		manifest = &m
		first = nil
	}
	loadDB := false
	if manifest != nil && manifest.Database {
		// The dump comes right after the manifest. Check the database
		// can take it before any file changes.
		hdr, err := tr.Next()
		if err != nil || hdr.Name != dbDumpEntry {
			return fmt.Errorf("backup says it has a database dump, but it is missing")
		}
		if db == nil {
			return fmt.Errorf("this backup includes a database, which cannot be restored here")
		}
		if err := db.Check(); err != nil {
			return err
		}
		out, err := os.OpenFile(db.DumpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(out, tr, hdr.Size)
		if cerr := out.Close(); copyErr == nil {
			copyErr = cerr
		}
		if copyErr != nil {
			return copyErr
		}
		loadDB = true
	}
	scoped := manifest != nil && manifest.Scope.Scoped()
	if !scoped {
		entries, _ := os.ReadDir(dest)
		for _, e := range entries {
			if e.Name() == ".ndl" {
				continue
			}
			_ = os.RemoveAll(filepath.Join(dest, e.Name()))
		}
	}
	restored := map[string]bool{}
	hdr := first
	for {
		if hdr == nil {
			hdr, err = tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
		}
		cur := hdr
		hdr = nil
		rel, err := iojail.CleanRel(cur.Name)
		if err != nil || rel == "." || rel == backupManifestName || rel == dbDumpEntry {
			continue
		}
		abs := filepath.Join(dest, filepath.FromSlash(rel))
		if relOut, rerr := filepath.Rel(dest, abs); rerr != nil || strings.HasPrefix(relOut, "..") {
			return fmt.Errorf("restore path escapes the jail")
		}
		restored[filepath.ToSlash(rel)] = true
		mode := os.FileMode(cur.Mode) & 0o777
		if cur.FileInfo().IsDir() {
			_ = os.MkdirAll(abs, 0o750)
			continue
		}
		if cur.Typeflag != tar.TypeReg {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			return err
		}
		// Never follow a link the game may have left in its place.
		if st, err := os.Lstat(abs); err == nil && !st.Mode().IsRegular() {
			if err := os.RemoveAll(abs); err != nil {
				return err
			}
		}
		if mode == 0 {
			mode = 0o640
		}
		out, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(out, tr, cur.Size)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		_ = os.Chmod(abs, mode)
		_ = os.Chtimes(abs, cur.ModTime, cur.ModTime)
	}
	if loadDB {
		if err := db.Load(db.DumpPath); err != nil {
			return err
		}
	}
	if !scoped {
		return nil
	}
	// Remove files the scope owns that the backup did not have (a file
	// added after it was taken), so the restored code matches exactly.
	return filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(dest, p)
		if rerr != nil || rel == "." {
			return nil
		}
		r := filepath.ToSlash(rel)
		if d.IsDir() {
			if anyCovered(manifest.Scope.Exclude, r) {
				return filepath.SkipDir
			}
			if !manifest.Scope.owned(r) && !anyAncestor(manifest.Scope.Include, r) {
				return filepath.SkipDir
			}
			return nil
		}
		if manifest.Scope.owned(r) && !restored[r] {
			_ = os.Remove(p)
		}
		return nil
	})
}

// addArchiveFile adds one file from disk under name.
func addArchiveFile(tw *tar.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.CopyN(tw, f, st.Size())
	return err
}
