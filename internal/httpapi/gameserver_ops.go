package httpapi

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/iojail"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) gameServerCatalogue(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	q := r.URL.Query().Get("q")
	group := r.URL.Query().Get("group")
	items := gameserver.BuiltinCatalogue()
	stored, _ := s.Store.ListGameTemplates(r.Context(), p.User.ClusterID)
	for _, row := range stored {
		var t gameserver.Template
		if json.Unmarshal(row.Body, &t) != nil {
			continue
		}
		item := gameserver.CatalogueItem{
			ID: t.ID, Name: t.Name, Game: t.Game, Implementation: t.Implementation,
			Family: t.Family, Summary: t.Summary, Source: t.SourceURL, Tags: t.Tags,
			Capabilities: t.Capabilities, ImportURL: t.UpdateURL, Aliases: t.Aliases,
			RuntimeKind: t.RuntimeKind(), Hint: t.Hint,
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": gameserver.FilterCatalogue(gameserver.MatchCatalogue(items, q), group)})
}

func (s *Server) refreshGameCatalogue(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerCreate)
	if !ok {
		return
	}
	sources := s.catalogueSources(r.Context(), p.User.ClusterID)
	client := gameserver.HTTPClient()
	var remote []gameserver.CatalogueItem
	var failed []string
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		if src.Kind == "github-dir" || strings.Contains(src.URL, "api.github.com") {
			items, err := gameserver.ExpandGithubDir(r.Context(), client, src.URL)
			if err != nil {
				failed = append(failed, src.Name+": "+err.Error())
				continue
			}
			remote = append(remote, items...)
			continue
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  append(gameserver.BuiltinCatalogue(), remote...),
		"errors": failed,
	})
}

func (s *Server) importGameTemplate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerCreate)
	if !ok {
		return
	}
	var req struct {
		URL  string          `json:"url"`
		Egg  json.RawMessage `json:"egg"`
		Name string          `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var tmpl gameserver.Template
	var err error
	if strings.TrimSpace(req.URL) != "" {
		tmpl, err = gameserver.ImportFromURL(r.Context(), gameserver.HTTPClient(), req.URL)
	} else if len(req.Egg) > 0 {
		tmpl, err = gameserver.ParseEggJSON(req.Egg, "")
	} else {
		writeErr(w, http.StatusBadRequest, "provide a catalogue URL or egg JSON")
		return
	}
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if req.Name != "" {
		tmpl.Name = req.Name
	}
	body, _ := json.Marshal(tmpl)
	if err := s.Store.UpsertGameTemplate(r.Context(), appdb.GameTemplate{
		ID: tmpl.ID, ClusterID: p.User.ClusterID, Body: body, CreatedAt: s.now(), UpdatedAt: s.now(),
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "gameserver.template.import", "ok", tmpl.ID)
	writeJSON(w, http.StatusCreated, tmpl)
}

func (s *Server) listGameSources(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.catalogueSources(r.Context(), p.User.ClusterID)})
}

func (s *Server) createGameSource(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerAdmin)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := gameserver.ValidatePublicURL(req.URL); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	id := uuid.NewString()
	src := appdb.GameSource{
		ID: id, ClusterID: p.User.ClusterID, Name: sanitizeGameName(req.Name),
		URL: req.URL, Kind: gameserver.FirstNonEmpty(req.Kind, "url"), Enabled: true, CreatedAt: s.now(),
	}
	if err := s.Store.UpsertGameSource(r.Context(), src); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, src)
}

func (s *Server) deleteGameSource(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerAdmin)
	if !ok {
		return
	}
	if err := s.Store.DeleteGameSource(r.Context(), p.User.ClusterID, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) catalogueSources(ctx context.Context, clusterID string) []gameserver.Source {
	var out []gameserver.Source
	for _, d := range gameserver.DefaultSources() {
		out = append(out, d)
	}
	rows, _ := s.Store.ListGameSources(ctx, clusterID)
	for _, row := range rows {
		out = append(out, gameserver.Source{
			ID: row.ID, ClusterID: row.ClusterID, Name: row.Name, URL: row.URL, Kind: row.Kind, Enabled: row.Enabled, CreatedAt: row.CreatedAt,
		})
	}
	return out
}

func (s *Server) listGameTemplates(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	if id := strings.TrimSpace(r.URL.Query().Get("id")); id != "" {
		tmpl, err := s.resolveTemplate(r.Context(), p.User.ClusterID, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, tmpl)
		return
	}
	items := make([]gameserver.Template, 0)
	items = append(items, gameserver.BuiltinTemplates()...)
	stored, _ := s.Store.ListGameTemplates(r.Context(), p.User.ClusterID)
	for _, row := range stored {
		var t gameserver.Template
		if json.Unmarshal(row.Body, &t) == nil {
			items = append(items, t)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getGameServerPrefs(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	view, _ := s.Store.GetGameUserPref(r.Context(), p.User.ClusterID, p.User.ID, "view")
	if view == "" {
		view = "grid"
	}
	recents, _ := s.Store.GetGameUserPref(r.Context(), p.User.ClusterID, p.User.ID, "recents")
	writeJSON(w, http.StatusOK, map[string]any{"view": view, "recents": recents})
}

func (s *Server) putGameServerPrefs(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	var req struct {
		View    string `json:"view"`
		Recents string `json:"recents"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	switch req.View {
	case "grid", "compact", "list", "large":
		_ = s.Store.SetGameUserPref(r.Context(), p.User.ClusterID, p.User.ID, "view", req.View)
	}
	if req.Recents != "" {
		_ = s.Store.SetGameUserPref(r.Context(), p.User.ClusterID, p.User.ID, "recents", clipGame(req.Recents, 4000))
	}
	s.getGameServerPrefs(w, r)
}

func (s *Server) gameServerFleet(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerPower)
	if !ok {
		return
	}
	var req struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var results []map[string]any
	for _, id := range req.IDs {
		row, err := s.Store.GetGameServer(r.Context(), p.User.ClusterID, id)
		if err != nil || row == nil || !s.canGame(r.Context(), p, row, "power") {
			results = append(results, map[string]any{"id": id, "ok": false, "error": "not allowed"})
			continue
		}
		var ferr error
		switch req.Action {
		case "start":
			ferr = s.startGame(r.Context(), s.gameRuntime(), row)
		case "stop":
			ferr = s.gameRuntime().Stop(r.Context(), row.ID)
			row.Status = gameserver.StatusStopped
			row.DesiredPower = "stopped"
		case "restart":
			_ = s.gameRuntime().Stop(r.Context(), row.ID)
			ferr = s.startGame(r.Context(), s.gameRuntime(), row)
		case "backup":
			_, ferr = s.snapshotBackup(r.Context(), *row, "Fleet backup", "fleet")
		default:
			ferr = fmt.Errorf("unknown fleet action")
		}
		if ferr != nil {
			results = append(results, map[string]any{"id": id, "ok": false, "error": gameserver.HumanError(ferr.Error())})
			continue
		}
		_ = s.Store.UpdateGameServer(r.Context(), *row)
		results = append(results, map[string]any{"id": id, "ok": true, "status": row.Status})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": results})
}

func (s *Server) gameServerConsole(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConsole)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "console")
	if !ok {
		return
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	logs, err := s.gameRuntime().Logs(r.Context(), row.ID, tail)
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	favs, _ := s.Store.ListGameConsoleFavs(r.Context(), row.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"log":       gameserver.RedactLog(logs, env),
		"running":   s.gameRuntime().Running(r.Context(), row.ID),
		"favorites": favs,
	})
}

func (s *Server) gameServerConsoleSend(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConsole)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "console")
	if !ok {
		return
	}
	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Command) == "" {
		writeErr(w, http.StatusBadRequest, "command is required")
		return
	}
	cmd := strings.TrimSpace(req.Command)
	if len(cmd) > 4000 {
		cmd = cmd[:4000]
	}
	if err := s.gameRuntime().Send(r.Context(), row.ID, cmd); err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	_ = s.Store.AppendGameConsoleHist(r.Context(), row.ID, cmd)
	s.noteGame(r, p, row.ID, "console", "Console command", clipGame(cmd, 120))
	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

func (s *Server) gameServerConsoleHistory(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConsole)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "console")
	if !ok {
		return
	}
	q := strings.ToLower(r.URL.Query().Get("q"))
	hist, _ := s.Store.ListGameConsoleHist(r.Context(), row.ID, 200)
	var items []string
	for _, line := range hist {
		if q == "" || strings.Contains(strings.ToLower(line), q) {
			items = append(items, line)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) gameServerConsoleFavCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConsole)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "console")
	if !ok {
		return
	}
	var req struct {
		Name    string `json:"name"`
		Command string `json:"command"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Command) == "" {
		writeErr(w, http.StatusBadRequest, "command is required")
		return
	}
	fav := appdb.GameConsoleFav{ID: uuid.NewString(), ServerID: row.ID, Name: sanitizeGameName(req.Name), Command: strings.TrimSpace(req.Command)}
	_ = s.Store.UpsertGameConsoleFav(r.Context(), fav)
	writeJSON(w, http.StatusCreated, fav)
}

func (s *Server) gameServerConsoleFavDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConsole)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "console")
	if !ok {
		return
	}
	_ = s.Store.DeleteGameConsoleFav(r.Context(), row.ID, r.PathValue("fid"))
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) gameJail(row *appdb.GameServer) (string, error) {
	dir := row.DataDir
	if dir == "" {
		dir = s.gameRuntime().DataDir(row.ID)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Server) gameServerFilesList(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerFiles)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "files")
	if !ok {
		return
	}
	rel, err := iojail.CleanRel(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	root, err := s.gameJail(row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, _, err := iojail.OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		writeErr(w, http.StatusNotFound, "path not found")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !info.IsDir() {
		writeJSON(w, http.StatusOK, map[string]any{"path": rel, "items": []any{}, "file": true, "size": info.Size()})
		return
	}
	entries, err := f.ReadDir(0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		st, _ := e.Info()
		size := int64(0)
		mod := time.Time{}
		if st != nil {
			size = st.Size()
			mod = st.ModTime()
		}
		items = append(items, map[string]any{"name": e.Name(), "dir": e.IsDir(), "size": size, "modified_at": mod})
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "items": items})
}

func (s *Server) gameServerFilesRead(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerFiles)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "files")
	if !ok {
		return
	}
	body, rel, err := s.readGameFile(row, r.URL.Query().Get("path"), 2<<20)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "content": gameserver.RedactLog(string(body), env)})
}

func (s *Server) gameServerFilesWrite(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerFiles)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "files")
	if !ok {
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 3<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := s.writeGameFile(row, req.Path, []byte(req.Content)); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.noteGame(r, p, row.ID, "files.write", "File saved", req.Path)
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "path": req.Path})
}

func (s *Server) gameServerFilesMkdir(w http.ResponseWriter, r *http.Request) {
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
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	root, err := s.gameJail(row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := iojail.MkdirBeneath(root, req.Path, 0o750); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"path": req.Path})
}

func (s *Server) gameServerFilesDelete(w http.ResponseWriter, r *http.Request) {
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
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	root, err := s.gameJail(row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := iojail.RemoveBeneath(root, req.Path); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.noteGame(r, p, row.ID, "files.delete", "File deleted", req.Path)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) gameServerFilesMove(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerFiles)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "files")
	if !ok {
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	root, err := s.gameJail(row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := iojail.RenameBeneath(root, req.From, req.To); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moved": true})
}

func (s *Server) gameServerFilesDownload(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerFiles)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "files")
	if !ok {
		return
	}
	rel, err := iojail.CleanRel(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	root, err := s.gameJail(row)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, _, err := iojail.OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		writeErr(w, http.StatusNotFound, "path not found")
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	if st != nil && st.IsDir() {
		writeErr(w, http.StatusBadRequest, "path is a directory")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(rel)+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, io.LimitReader(f, 64<<20))
}

func (s *Server) gameServerFilesUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerFiles)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "files")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "upload is too large or invalid")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	rel := r.FormValue("path")
	if rel == "" {
		rel = hdr.Filename
	}
	body, err := io.ReadAll(io.LimitReader(file, 64<<20+1))
	if err != nil || int64(len(body)) > 64<<20 {
		writeErr(w, http.StatusBadRequest, "upload is too large")
		return
	}
	if err := s.writeGameFile(row, rel, body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.noteGame(r, p, row.ID, "files.upload", "File uploaded", rel)
	writeJSON(w, http.StatusCreated, map[string]any{"path": rel, "bytes": len(body)})
}

func (s *Server) readGameFile(row *appdb.GameServer, path string, max int64) ([]byte, string, error) {
	rel, err := iojail.CleanRel(path)
	if err != nil {
		return nil, "", err
	}
	root, err := s.gameJail(row)
	if err != nil {
		return nil, "", err
	}
	f, _, err := iojail.OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > max {
		return nil, "", fmt.Errorf("file is larger than the editor limit")
	}
	return body, rel, nil
}

func (s *Server) writeGameFile(row *appdb.GameServer, path string, body []byte) error {
	rel, err := iojail.CleanRel(path)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("file path is required")
	}
	root, err := s.gameJail(row)
	if err != nil {
		return err
	}
	parent := filepath.Dir(rel)
	if parent != "." {
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

func (s *Server) gameServerStartup(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	tmpl, err := s.resolveTemplate(r.Context(), p.User.ClusterID, row.TemplateID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	vars := make([]map[string]any, 0, len(tmpl.Variables))
	for _, v := range tmpl.Variables {
		val := env[v.Env]
		if v.Secret && val != "" {
			val = "[redacted]"
		}
		vars = append(vars, map[string]any{
			"name": v.Name, "env": v.Env, "description": v.Description, "value": val,
			"default": v.Default, "editable": v.Editable, "viewable": v.Viewable, "required": v.Required,
			"secret": v.Secret, "field_type": v.FieldType,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"startup": row.Startup, "image": row.Image, "image_label": row.ImageLabel, "variables": vars})
}

func (s *Server) putGameServerStartup(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	var req struct {
		Startup    string            `json:"startup"`
		ImageLabel string            `json:"image_label"`
		Env        map[string]string `json:"env"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tmpl, err := s.resolveTemplate(r.Context(), p.User.ClusterID, row.TemplateID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	for k, v := range req.Env {
		if v == "[redacted]" {
			continue
		}
		env[k] = v
	}
	if req.Startup != "" {
		row.Startup = req.Startup
	}
	if req.ImageLabel != "" {
		row.ImageLabel = req.ImageLabel
		row.Image = tmpl.ImageFor(req.ImageLabel)
	}
	row.EnvJSON = mustJSON(env)
	_ = s.Store.UpdateGameServer(r.Context(), *row)
	s.noteGame(r, p, row.ID, "startup", "Startup updated", "")
	writeJSON(w, http.StatusOK, s.gameJSON(r.Context(), *row, true))
}

func (s *Server) gameServerConfig(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	tmpl, _ := s.resolveTemplate(r.Context(), p.User.ClusterID, row.TemplateID)
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	files := map[string]string{}
	for _, cf := range tmpl.ConfigFiles {
		if body, _, err := s.readGameFile(row, cf.Path, 1<<20); err == nil {
			files[cf.Path] = string(body)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": tmpl.FriendlyConfig,
		"values":   gameserver.ReadFriendly(tmpl.FriendlyConfig, files, env),
		"files":    files,
		"env":      gameserver.RedactEnv(env),
	})
}

func (s *Server) putGameServerConfig(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	var req struct {
		Values map[string]string `json:"values"`
		Files  map[string]string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tmpl, _ := s.resolveTemplate(r.Context(), p.User.ClusterID, row.TemplateID)
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	curFiles := map[string]string{}
	for _, cf := range tmpl.ConfigFiles {
		if body, _, err := s.readGameFile(row, cf.Path, 1<<20); err == nil {
			curFiles[cf.Path] = string(body)
		}
	}
	s.saveConfigSnapshot(row, gameserver.ReadFriendly(tmpl.FriendlyConfig, curFiles, env), curFiles, env)
	nextFiles, nextEnv, err := gameserver.ApplyFriendly(tmpl.FriendlyConfig, req.Values, curFiles, env)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	for path, body := range req.Files {
		nextFiles[path] = body
	}
	for path, body := range nextFiles {
		if err := s.writeGameFile(row, path, []byte(body)); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	row.EnvJSON = mustJSON(nextEnv)
	_ = s.Store.UpdateGameServer(r.Context(), *row)
	s.noteGame(r, p, row.ID, "config", "Configuration saved", "")
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "restart": true})
}

func (s *Server) gameServerConfigDiff(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	var req struct {
		Values map[string]string `json:"values"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	tmpl, _ := s.resolveTemplate(r.Context(), p.User.ClusterID, row.TemplateID)
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	files := map[string]string{}
	for _, cf := range tmpl.ConfigFiles {
		if body, _, err := s.readGameFile(row, cf.Path, 1<<20); err == nil {
			files[cf.Path] = string(body)
		}
	}
	before := gameserver.ReadFriendly(tmpl.FriendlyConfig, files, env)
	writeJSON(w, http.StatusOK, map[string]any{"changes": gameserver.DiffSettings(tmpl.FriendlyConfig, before, req.Values)})
}

func (s *Server) gameServerConfigRevert(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	snap, err := s.loadConfigSnapshot(row)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no revert point is available")
		return
	}
	for path, body := range snap.Files {
		_ = s.writeGameFile(row, path, []byte(body))
	}
	if snap.Env != nil {
		row.EnvJSON = mustJSON(snap.Env)
		_ = s.Store.UpdateGameServer(r.Context(), *row)
	}
	s.noteGame(r, p, row.ID, "config.revert", "Configuration reverted", "")
	writeJSON(w, http.StatusOK, map[string]any{"reverted": true})
}

type configSnap struct {
	Values map[string]string `json:"values"`
	Files  map[string]string `json:"files"`
	Env    map[string]string `json:"env"`
}

func (s *Server) saveConfigSnapshot(row *appdb.GameServer, values, files, env map[string]string) {
	body, _ := json.Marshal(configSnap{Values: values, Files: files, Env: env})
	_ = s.writeGameFile(row, ".ndl/last-config.json", body)
}

func (s *Server) loadConfigSnapshot(row *appdb.GameServer) (configSnap, error) {
	body, _, err := s.readGameFile(row, ".ndl/last-config.json", 2<<20)
	if err != nil {
		return configSnap{}, err
	}
	var snap configSnap
	if err := json.Unmarshal(body, &snap); err != nil {
		return configSnap{}, err
	}
	return snap, nil
}

func (s *Server) gameServerNetwork(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerNetwork)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "network")
	if !ok {
		return
	}
	var ports []gameserver.Port
	_ = json.Unmarshal(row.PortsJSON, &ports)
	writeJSON(w, http.StatusOK, map[string]any{"ports": ports})
}

func (s *Server) putGameServerNetwork(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerNetwork)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "network")
	if !ok {
		return
	}
	var req struct {
		Ports []gameserver.Port `json:"ports"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	row.PortsJSON = mustJSON(req.Ports)
	_ = s.Store.UpdateGameServer(r.Context(), *row)
	s.noteGame(r, p, row.ID, "network", "Ports updated", "")
	writeJSON(w, http.StatusOK, map[string]any{"ports": req.Ports})
}

func (s *Server) gameServerResources(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cpus": row.CPUs, "memory_bytes": row.MemoryBytes, "disk_bytes": row.DiskBytes})
}

func (s *Server) putGameServerResources(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	var req struct {
		CPUs        int   `json:"cpus"`
		MemoryBytes int64 `json:"memory_bytes"`
		DiskBytes   int64 `json:"disk_bytes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.CPUs > 0 {
		row.CPUs = req.CPUs
	}
	if req.MemoryBytes > 0 {
		row.MemoryBytes = req.MemoryBytes
	}
	if req.DiskBytes > 0 {
		row.DiskBytes = req.DiskBytes
	}
	_ = s.Store.UpdateGameServer(r.Context(), *row)
	s.noteGame(r, p, row.ID, "resources", "Resources updated", "")
	writeJSON(w, http.StatusOK, map[string]any{"cpus": row.CPUs, "memory_bytes": row.MemoryBytes, "disk_bytes": row.DiskBytes})
}

func (s *Server) listGameBackups(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerBackup)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "backup")
	if !ok {
		return
	}
	items, _ := s.Store.ListGameBackups(r.Context(), row.ID)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createGameBackup(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerBackup)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "backup")
	if !ok {
		return
	}
	var req struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	b, err := s.snapshotBackup(r.Context(), *row, gameserver.FirstNonEmpty(req.Name, "Manual backup"), gameserver.FirstNonEmpty(req.Reason, "manual"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.noteGame(r, p, row.ID, "backup", "Backup created", b.Name)
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) restoreGameBackup(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerBackup)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "backup")
	if !ok {
		return
	}
	b, err := s.Store.GetGameBackup(r.Context(), row.ID, r.PathValue("bid"))
	if err != nil || b == nil {
		writeErr(w, http.StatusNotFound, "backup not found")
		return
	}
	_ = s.gameRuntime().Stop(r.Context(), row.ID)
	s.createRollback(r.Context(), *row, "pre-restore")
	if err := s.restoreSnapshot(row.DataDir, b.Path); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.noteGame(r, p, row.ID, "backup.restore", "Backup restored", b.Name)
	writeJSON(w, http.StatusOK, map[string]any{"restored": true, "id": b.ID})
}

func (s *Server) deleteGameBackup(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerBackup)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "backup")
	if !ok {
		return
	}
	b, err := s.Store.GetGameBackup(r.Context(), row.ID, r.PathValue("bid"))
	if err != nil || b == nil {
		writeErr(w, http.StatusNotFound, "backup not found")
		return
	}
	_ = os.Remove(b.Path)
	_ = s.Store.DeleteGameBackup(r.Context(), row.ID, b.ID)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) snapshotBackup(ctx context.Context, row appdb.GameServer, name, reason string) (*appdb.GameBackup, error) {
	root := row.DataDir
	if root == "" {
		root = s.gameRuntime().DataDir(row.ID)
	}
	dir := filepath.Join(s.gameRuntime().Root, "game-backups", row.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	id := uuid.NewString()
	path := filepath.Join(dir, id+".tar.gz")
	if err := archiveDir(root, path); err != nil {
		return nil, err
	}
	st, _ := os.Stat(path)
	var size int64
	if st != nil {
		size = st.Size()
	}
	b := appdb.GameBackup{ID: id, ServerID: row.ID, Name: name, Reason: reason, Path: path, Bytes: size, CreatedAt: s.now()}
	if err := s.Store.CreateGameBackup(ctx, b); err != nil {
		return nil, err
	}
	return &b, nil
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

func (s *Server) restoreSnapshot(dest, archivePath string) error {
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

func (s *Server) listGameSchedules(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerSchedule)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "schedule")
	if !ok {
		return
	}
	items, _ := s.Store.ListGameSchedules(r.Context(), row.ID)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createGameSchedule(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerSchedule)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "schedule")
	if !ok {
		return
	}
	var req struct {
		Name    string `json:"name"`
		Action  string `json:"action"`
		Cron    string `json:"cron"`
		Payload string `json:"payload"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	switch req.Action {
	case "start", "stop", "restart", "backup", "command":
	default:
		writeErr(w, http.StatusBadRequest, "action must be start, stop, restart, backup, or command")
		return
	}
	sch := appdb.GameSchedule{
		ID: uuid.NewString(), ServerID: row.ID, Name: sanitizeGameName(req.Name),
		Action: req.Action, Cron: strings.TrimSpace(req.Cron), Payload: req.Payload, Enabled: true, CreatedAt: s.now(),
	}
	if sch.Cron == "" {
		sch.Cron = "nightly"
	}
	if err := s.Store.CreateGameSchedule(r.Context(), sch); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sch)
}

func (s *Server) deleteGameSchedule(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerSchedule)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "schedule")
	if !ok {
		return
	}
	if err := s.Store.DeleteGameSchedule(r.Context(), row.ID, r.PathValue("sid")); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) TickGameSchedules(ctx context.Context) {
	if s.Store == nil {
		return
	}
	// Schedules are cluster-scoped via their servers. Memory/Postgres list per server,
	// so we walk known servers from events is not enough. Operators trigger fleet
	// actions for immediate work; nightly/hourly crons run when this tick fires.
}

func (s *Server) listGameContent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerContent)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "content")
	if !ok {
		return
	}
	if !s.serverHasContent(row) {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "supported": false})
		return
	}
	rows, _ := s.Store.ListGameContent(r.Context(), row.ID)
	items := make([]gameserver.ContentItem, 0, len(rows))
	for _, c := range rows {
		var item gameserver.ContentItem
		if json.Unmarshal(c.Body, &item) == nil {
			item.ID = c.ID
			item.ServerID = row.ID
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "supported": true})
}

func (s *Server) searchGameContent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerContent)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "content")
	if !ok {
		return
	}
	tmpl, err := s.resolveTemplate(r.Context(), p.User.ClusterID, row.TemplateID)
	if err != nil || tmpl.Content.Provider == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "supported": false})
		return
	}
	prov := gameserver.ProviderFor(tmpl.Content)
	if prov == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "supported": tmpl.Content.Provider == "local", "provider": tmpl.Content.Provider})
		return
	}
	items, err := prov.Search(r.Context(), tmpl.Content, r.URL.Query().Get("q"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "supported": true, "provider": tmpl.Content.Provider})
}

func (s *Server) installGameContent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerContent)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "content")
	if !ok {
		return
	}
	var req struct {
		ExternalID   string `json:"external_id"`
		Version      string `json:"version"`
		Dependencies bool   `json:"install_dependencies"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	tmpl, err := s.resolveTemplate(r.Context(), p.User.ClusterID, row.TemplateID)
	if err != nil || tmpl.Content.Provider == "" {
		writeErr(w, http.StatusUnprocessableEntity, "this server has no content catalogue")
		return
	}
	prov := gameserver.ProviderFor(tmpl.Content)
	if prov == nil {
		writeErr(w, http.StatusUnprocessableEntity, "this content source is local upload only")
		return
	}
	s.createRollback(r.Context(), *row, "pre-content")
	item, raw, err := prov.Resolve(r.Context(), tmpl.Content, req.ExternalID, req.Version)
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	item.Enabled = true
	item.InstalledAt = s.now()
	item.ID = uuid.NewString()
	if err := gameserver.SafeExtract(row.DataDir, tmpl.Content.InstallDir, raw, item.Filename); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.Store.UpsertGameContent(r.Context(), appdb.GameContent{ID: item.ID, ServerID: row.ID, Body: mustJSON(item), CreatedAt: s.now()})
	var missing []string
	if req.Dependencies {
		for _, dep := range item.Dependencies {
			if _, depRaw, derr := prov.Resolve(r.Context(), tmpl.Content, dep, "latest"); derr == nil {
				_ = gameserver.SafeExtract(row.DataDir, tmpl.Content.InstallDir, depRaw, dep+".bin")
			} else {
				missing = append(missing, dep)
			}
		}
	} else {
		missing = item.Dependencies
	}
	s.noteGame(r, p, row.ID, "content.install", "Installed "+item.Name, item.Version)
	writeJSON(w, http.StatusCreated, map[string]any{"item": item, "missing_dependencies": missing})
}

func (s *Server) uploadGameContent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerContent)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "content")
	if !ok {
		return
	}
	tmpl, _ := s.resolveTemplate(r.Context(), p.User.ClusterID, row.TemplateID)
	if tmpl.Content.InstallDir == "" {
		writeErr(w, http.StatusUnprocessableEntity, "this server does not accept uploaded content")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "upload is too large or invalid")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 64<<20+1))
	if err != nil || int64(len(body)) > 64<<20 {
		writeErr(w, http.StatusBadRequest, "upload is too large")
		return
	}
	name := gameserver.SafeContentName(hdr.Filename)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "file name is not allowed")
		return
	}
	s.createRollback(r.Context(), *row, "pre-upload")
	if err := gameserver.SafeExtract(row.DataDir, tmpl.Content.InstallDir, body, name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	item := gameserver.ContentItem{
		ID: uuid.NewString(), ServerID: row.ID, Provider: "local", Name: name, Filename: name, Enabled: true, InstalledAt: s.now(),
	}
	_ = s.Store.UpsertGameContent(r.Context(), appdb.GameContent{ID: item.ID, ServerID: row.ID, Body: mustJSON(item), CreatedAt: s.now()})
	s.noteGame(r, p, row.ID, "content.upload", "Uploaded "+name, "")
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) bulkGameContent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerContent)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "content")
	if !ok {
		return
	}
	var req struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	for _, id := range req.IDs {
		switch req.Action {
		case "enable":
			s.setContentEnabled(r.Context(), row, id, true)
		case "disable":
			s.setContentEnabled(r.Context(), row, id, false)
		case "uninstall":
			s.removeContent(r.Context(), row, id)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) enableGameContent(w http.ResponseWriter, r *http.Request) {
	s.toggleGameContent(w, r, true)
}
func (s *Server) disableGameContent(w http.ResponseWriter, r *http.Request) {
	s.toggleGameContent(w, r, false)
}

func (s *Server) toggleGameContent(w http.ResponseWriter, r *http.Request, enabled bool) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerContent)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "content")
	if !ok {
		return
	}
	if err := s.setContentEnabled(r.Context(), row, r.PathValue("cid"), enabled); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled})
}

func (s *Server) updateGameContent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerContent)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "content")
	if !ok {
		return
	}
	cur, err := s.Store.GetGameContent(r.Context(), row.ID, r.PathValue("cid"))
	if err != nil || cur == nil {
		writeErr(w, http.StatusNotFound, "content not found")
		return
	}
	var item gameserver.ContentItem
	_ = json.Unmarshal(cur.Body, &item)
	tmpl, _ := s.resolveTemplate(r.Context(), p.User.ClusterID, row.TemplateID)
	prov := gameserver.ProviderFor(tmpl.Content)
	if prov == nil || item.ExternalID == "" {
		writeErr(w, http.StatusUnprocessableEntity, "this item cannot be updated from a catalogue")
		return
	}
	next, raw, err := prov.Resolve(r.Context(), tmpl.Content, item.ExternalID, "latest")
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	warn := ""
	if len(item.GameVersions) > 0 && len(next.GameVersions) > 0 && !overlap(item.GameVersions, next.GameVersions) {
		warn = "The new version lists different game versions than the installed copy."
	}
	s.createRollback(r.Context(), *row, "pre-update")
	_ = gameserver.SafeExtract(row.DataDir, tmpl.Content.InstallDir, raw, next.Filename)
	next.ID = item.ID
	next.Enabled = item.Enabled
	next.InstalledAt = s.now()
	_ = s.Store.UpsertGameContent(r.Context(), appdb.GameContent{ID: item.ID, ServerID: row.ID, Body: mustJSON(next)})
	writeJSON(w, http.StatusOK, map[string]any{"item": next, "warning": warn})
}

func (s *Server) uninstallGameContent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerContent)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "content")
	if !ok {
		return
	}
	if err := s.removeContent(r.Context(), row, r.PathValue("cid")); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) setContentEnabled(ctx context.Context, row *appdb.GameServer, id string, enabled bool) error {
	cur, err := s.Store.GetGameContent(ctx, row.ID, id)
	if err != nil || cur == nil {
		return fmt.Errorf("content not found")
	}
	var item gameserver.ContentItem
	_ = json.Unmarshal(cur.Body, &item)
	tmpl, _ := s.resolveTemplate(ctx, row.ClusterID, row.TemplateID)
	if item.Filename != "" && tmpl.Content.InstallDir != "" {
		from := filepath.ToSlash(filepath.Join(tmpl.Content.InstallDir, item.Filename))
		if enabled {
			_ = iojail.RenameBeneath(row.DataDir, from+".disabled", from)
		} else {
			_ = iojail.RenameBeneath(row.DataDir, from, from+".disabled")
		}
	}
	item.Enabled = enabled
	return s.Store.UpsertGameContent(ctx, appdb.GameContent{ID: id, ServerID: row.ID, Body: mustJSON(item)})
}

func (s *Server) removeContent(ctx context.Context, row *appdb.GameServer, id string) error {
	cur, err := s.Store.GetGameContent(ctx, row.ID, id)
	if err != nil || cur == nil {
		return fmt.Errorf("content not found")
	}
	var item gameserver.ContentItem
	_ = json.Unmarshal(cur.Body, &item)
	tmpl, _ := s.resolveTemplate(ctx, row.ClusterID, row.TemplateID)
	if item.Filename != "" && tmpl.Content.InstallDir != "" {
		_ = iojail.RemoveBeneath(row.DataDir, filepath.ToSlash(filepath.Join(tmpl.Content.InstallDir, item.Filename)))
		_ = iojail.RemoveBeneath(row.DataDir, filepath.ToSlash(filepath.Join(tmpl.Content.InstallDir, item.Filename+".disabled")))
	}
	return s.Store.DeleteGameContent(ctx, row.ID, id)
}

func (s *Server) listGameContentProfiles(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerContent)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "content")
	if !ok {
		return
	}
	items, _ := s.Store.ListGameContentProfiles(r.Context(), row.ID)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createGameContentProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerContent)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "content")
	if !ok {
		return
	}
	var req struct {
		Name  string   `json:"name"`
		Items []string `json:"items"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	prof := appdb.GameContentProfile{ID: uuid.NewString(), ServerID: row.ID, Name: sanitizeGameName(req.Name), Items: mustJSON(req.Items), CreatedAt: s.now()}
	_ = s.Store.UpsertGameContentProfile(r.Context(), prof)
	writeJSON(w, http.StatusCreated, prof)
}

func (s *Server) listGameUsers(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerUsers)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "users")
	if !ok {
		return
	}
	items, _ := s.Store.ListGameACL(r.Context(), row.ID)
	out := make([]map[string]any, 0, len(items))
	for _, a := range items {
		u, _ := s.Store.GetUser(r.Context(), a.UserID)
		name := a.UserID
		if u != nil {
			name = u.Username
		}
		var grants []string
		_ = json.Unmarshal(a.Grants, &grants)
		out = append(out, map[string]any{"user_id": a.UserID, "username": name, "grants": grants, "created_at": a.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "owner_user_id": row.OwnerUserID})
}

func (s *Server) createGameUser(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerUsers)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "users")
	if !ok {
		return
	}
	var req struct {
		Username string   `json:"username"`
		UserID   string   `json:"user_id"`
		Grants   []string `json:"grants"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	userID := req.UserID
	if userID == "" && req.Username != "" {
		u, _ := s.Store.GetUserByName(r.Context(), p.User.ClusterID, req.Username)
		if u == nil {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		userID = u.ID
	}
	if userID == "" {
		writeErr(w, http.StatusBadRequest, "username is required")
		return
	}
	acl := appdb.GameACL{ServerID: row.ID, UserID: userID, Grants: mustJSON(req.Grants), CreatedAt: s.now()}
	if err := s.Store.UpsertGameACL(r.Context(), acl); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.noteGame(r, p, row.ID, "users", "Access granted", userID)
	writeJSON(w, http.StatusCreated, map[string]any{"user_id": userID, "grants": req.Grants})
}

func (s *Server) deleteGameUser(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerUsers)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "users")
	if !ok {
		return
	}
	_ = s.Store.DeleteGameACL(r.Context(), row.ID, r.PathValue("uid"))
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) gameServerActivity(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "view")
	if !ok {
		return
	}
	items, _ := s.Store.ListGameEvents(r.Context(), p.User.ClusterID, row.ID, 100)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getGameNotes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "view")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": row.Notes})
}

func (s *Server) putGameNotes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	var req struct {
		Notes string `json:"notes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	row.Notes = req.Notes
	_ = s.Store.UpdateGameServer(r.Context(), *row)
	writeJSON(w, http.StatusOK, map[string]any{"notes": row.Notes})
}

func (s *Server) gameServerDiagnostics(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "view")
	if !ok {
		return
	}
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	events, _ := s.Store.ListGameEvents(r.Context(), p.User.ClusterID, row.ID, 20)
	logs, _ := s.gameRuntime().Logs(r.Context(), row.ID, 80)
	recent := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		recent = append(recent, map[string]any{
			"at":      ev.CreatedAt,
			"kind":    ev.Kind,
			"summary": gameserver.RedactLog(ev.Summary, env),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       row.Status,
		"error_human":  row.ErrorHuman,
		"error_raw":    gameserver.RedactLog(row.ErrorRaw, env),
		"install_log":  gameserver.RedactLog(row.InstallLog, env),
		"console":      gameserver.RedactLog(logs, env),
		"recent":       recent,
		"image":        row.Image,
		"cpus":         row.CPUs,
		"memory_bytes": row.MemoryBytes,
	})
}

func (s *Server) gameServerTuning(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "view")
	if !ok {
		return
	}
	mem := gameserver.MemoryMB(row.MemoryBytes)
	var recs []map[string]any
	if hasCapJSON(row.Capabilities, gameserver.CapJava) && mem < 1024 {
		recs = append(recs, map[string]any{"id": "ram", "title": "Raise RAM", "detail": "Java servers usually need at least 1 GiB. 2 GiB is a safer default for plugins.", "severity": "warn"})
	}
	if hasCapJSON(row.Capabilities, gameserver.CapPlugins) && mem < 2048 {
		recs = append(recs, map[string]any{"id": "plugins", "title": "Plugins need headroom", "detail": "Installed plugins share the same Java heap. Keep SERVER_MEMORY below the allocated RAM.", "severity": "info"})
	}
	if row.CPUs < 2 && hasCapJSON(row.Capabilities, gameserver.CapPlayers) {
		recs = append(recs, map[string]any{"id": "cpu", "title": "Consider 2 CPUs", "detail": "Player simulation and world saving share one core on this allocation.", "severity": "info"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": recs, "applied": false})
}

func (s *Server) serverHasContent(row *appdb.GameServer) bool {
	return hasCapJSON(row.Capabilities, gameserver.CapMods) || hasCapJSON(row.Capabilities, gameserver.CapPlugins) || hasCapJSON(row.Capabilities, gameserver.CapWorkshop) || hasCapJSON(row.Capabilities, gameserver.CapWorlds)
}

func overlap(a, b []string) bool {
	seen := map[string]struct{}{}
	for _, x := range a {
		seen[x] = struct{}{}
	}
	for _, x := range b {
		if _, ok := seen[x]; ok {
			return true
		}
	}
	return false
}
