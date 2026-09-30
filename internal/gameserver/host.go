package gameserver

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
	Mkdir(ctx context.Context, id, rel string) error
	Remove(ctx context.Context, id, rel string) error
	Rename(ctx context.Context, id, from, to string) error
	Extract(ctx context.Context, id, destRel string, raw []byte, name string) error
	Backup(ctx context.Context, id, backupID string) (BackupFile, error)
	RestoreBackup(ctx context.Context, id, backupID string) error
	DeleteBackup(ctx context.Context, id, backupID string) error
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

	mu       sync.Mutex
	progress map[string]ProgressFunc
	hooked   bool
}

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

func (h *LocalHost) RemoveData(_ context.Context, id string) error {
	dir, err := h.serverDir(id)
	if err != nil {
		return err
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
	return h.RT.Start(ctx, spec)
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

func (h *LocalHost) Backup(_ context.Context, id, backupID string) (BackupFile, error) {
	root, err := h.jail(id)
	if err != nil {
		return BackupFile{}, err
	}
	path, err := h.backupPath(id, backupID)
	if err != nil {
		return BackupFile{}, err
	}
	if err := archiveDir(root, path); err != nil {
		return BackupFile{}, err
	}
	out := BackupFile{Path: path}
	if st, err := os.Stat(path); err == nil {
		out.Bytes = st.Size()
	}
	return out, nil
}

func (h *LocalHost) RestoreBackup(_ context.Context, id, backupID string) error {
	root, err := h.jail(id)
	if err != nil {
		return err
	}
	path, err := h.backupPath(id, backupID)
	if err != nil {
		return err
	}
	return restoreArchive(root, path)
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

func archiveDir(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("backup path escapes the jail")
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
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
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
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
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func restoreArchive(dest, archivePath string) error {
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
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		if e.Name() == ".ndl" {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dest, e.Name()))
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := iojail.CleanRel(hdr.Name)
		if err != nil || rel == "." {
			continue
		}
		abs := filepath.Join(dest, filepath.FromSlash(rel))
		if relOut, rerr := filepath.Rel(dest, abs); rerr != nil || strings.HasPrefix(relOut, "..") {
			return fmt.Errorf("restore path escapes the jail")
		}
		if hdr.FileInfo().IsDir() {
			_ = os.MkdirAll(abs, 0o750)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			return err
		}
		out, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(tr, 128<<20))
		_ = out.Close()
		if copyErr != nil {
			return copyErr
		}
	}
}
