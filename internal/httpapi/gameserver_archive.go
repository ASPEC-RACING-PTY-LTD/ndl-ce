package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/iojail"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Extract and zip in a game server's file manager. Both go through the
// server's Host, so they work on any node: extract reads the archive and
// unpacks it with the jailed extractor (zip, tar, tar.gz); zip lists and
// reads the chosen files and folders and writes one deflated archive.

// gameServerFilesExtract unpacks an archive into a folder.
func (s *Server) gameServerFilesExtract(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerFiles)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "files")
	if !ok {
		return
	}
	var req struct {
		Path string `json:"path"`
		Dest string `json:"dest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rel, err := iojail.CleanRel(req.Path)
	if err != nil || rel == "." {
		writeErr(w, http.StatusBadRequest, "path is not a file")
		return
	}
	if !gameserver.IsArchive(rel) {
		writeErr(w, http.StatusBadRequest, "only .zip, .tar, .tar.gz and .tgz files can be extracted")
		return
	}
	dest := strings.TrimSpace(req.Dest)
	if dest == "" {
		dest = path.Dir(rel)
	}
	if dest, err = iojail.CleanRel(dest); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, err := s.gameHost().ReadFile(r.Context(), row.ID, rel, gameserver.MaxArchiveBytes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "archive could not be read: "+err.Error())
		return
	}
	if err := s.gameHost().Extract(r.Context(), row.ID, dest, raw, rel); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.noteGame(r, p, row.ID, "files.extract", "Archive extracted", rel+" to /"+strings.TrimPrefix(dest, "."))
	writeJSON(w, http.StatusOK, map[string]any{"extracted": true, "path": rel, "dest": dest})
}

// gameServerFilesCompress zips files and folders into one archive.
func (s *Server) gameServerFilesCompress(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerFiles)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "files")
	if !ok {
		return
	}
	var req struct {
		Paths []string `json:"paths"`
		Dest  string   `json:"dest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(req.Paths) == 0 {
		writeErr(w, http.StatusBadRequest, "choose at least one file or folder")
		return
	}
	var rels []string
	for _, p := range req.Paths {
		rel, err := iojail.CleanRel(p)
		if err != nil || rel == "." {
			writeErr(w, http.StatusBadRequest, "paths must be files or folders inside the server")
			return
		}
		rels = append(rels, rel)
	}
	dest := strings.TrimSpace(req.Dest)
	if dest == "" {
		dest = rels[0] + ".zip"
		if len(rels) > 1 {
			dest = path.Join(path.Dir(rels[0]), "archive.zip")
		}
	}
	dest, err := iojail.CleanRel(dest)
	if err != nil || dest == "." {
		writeErr(w, http.StatusBadRequest, "archive name is not valid")
		return
	}
	if !strings.HasSuffix(strings.ToLower(dest), ".zip") {
		dest += ".zip"
	}
	if _, err := s.gameHost().ListDir(r.Context(), row.ID, dest); err == nil {
		writeErr(w, http.StatusConflict, "/"+dest+" already exists; choose another name")
		return
	}
	z := zipCollector{ctx: r.Context(), host: s.gameHost(), id: row.ID}
	for _, rel := range rels {
		if err := z.add(rel, path.Base(rel)); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	raw, err := gameserver.BuildZip(z.files)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.writeGameFile(row, dest, raw); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.noteGame(r, p, row.ID, "files.compress", "Archive created", dest)
	writeJSON(w, http.StatusCreated, map[string]any{"path": dest, "bytes": len(raw), "files": z.count})
}

// zipCollector walks files and folders through a game Host.
type zipCollector struct {
	ctx   context.Context
	host  gameserver.Host
	id    string
	files []gameserver.ZipFile
	count int
	bytes int64
}

// add puts rel into the archive as name, recursing into folders.
func (z *zipCollector) add(rel, name string) error {
	listing, err := z.host.ListDir(z.ctx, z.id, rel)
	if err != nil {
		return fmt.Errorf("/%s was not found", rel)
	}
	if listing.File {
		return z.addFile(rel, name, gameserver.DirEntry{Size: listing.Size})
	}
	z.files = append(z.files, gameserver.ZipFile{Name: name, Dir: true})
	for _, e := range listing.Items {
		childRel, childName := path.Join(rel, e.Name), path.Join(name, e.Name)
		if e.Dir {
			if err := z.add(childRel, childName); err != nil {
				return err
			}
			continue
		}
		if err := z.addFile(childRel, childName, e); err != nil {
			return err
		}
	}
	return nil
}

func (z *zipCollector) addFile(rel, name string, e gameserver.DirEntry) error {
	z.count++
	if z.count > gameserver.MaxArchiveFiles {
		return fmt.Errorf("more than %d files; zip a smaller folder", gameserver.MaxArchiveFiles)
	}
	if e.Size > gameserver.MaxOneFileBytes {
		return fmt.Errorf("/%s is larger than %d MiB", rel, gameserver.MaxOneFileBytes>>20)
	}
	z.bytes += e.Size
	if z.bytes > gameserver.MaxArchiveBytes {
		return fmt.Errorf("more than %d MiB; zip a smaller folder", gameserver.MaxArchiveBytes>>20)
	}
	body, err := z.host.ReadFile(z.ctx, z.id, rel, gameserver.MaxOneFileBytes)
	if err != nil {
		// Sockets, devices and links that point outside the server are left out.
		if errors.Is(err, context.Canceled) {
			return err
		}
		z.count--
		return nil
	}
	z.files = append(z.files, gameserver.ZipFile{Name: name, Body: body, Modified: e.ModifiedAt})
	return nil
}
