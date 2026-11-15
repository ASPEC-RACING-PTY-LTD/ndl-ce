package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Live Workshop sync: per server, ndl-ce checks the Workshop collection
// hourly or when the server asks (request.json), downloads what changed and
// tells the server with the cs_sync_ready console command. The engine is
// gameserver.WorkshopSync; this file schedules it and serves its settings.

type workshopSyncRunner struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
	busy  map[string]bool
	retry map[string]time.Time
	// Tests replace Steam and SteamCMD.
	api   gameserver.WorkshopAPI
	fetch gameserver.WorkshopFetcher
}

func (w *workshopSyncRunner) lock(id string) *sync.Mutex {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.locks == nil {
		w.locks = map[string]*sync.Mutex{}
	}
	if w.locks[id] == nil {
		w.locks[id] = &sync.Mutex{}
	}
	return w.locks[id]
}

func (w *workshopSyncRunner) start(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.busy == nil {
		w.busy = map[string]bool{}
	}
	if w.busy[id] {
		return false
	}
	w.busy[id] = true
	return true
}

func (w *workshopSyncRunner) done(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.busy, id)
}

func (w *workshopSyncRunner) checking(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.busy[id]
}

// syncRoot holds ndl-ce's own sync state, outside every server's folder.
func (s *Server) syncRoot() string {
	return filepath.Join(s.gameRuntime().Root, "workshop-sync")
}

// syncItemsDir holds a server's packs; the server sees it read-only.
func (s *Server) syncItemsDir(id string) string {
	return filepath.Join(s.syncRoot(), id, "items")
}

func (s *Server) syncStatePath(id string) string {
	return filepath.Join(s.syncRoot(), id, "state.json")
}

func (s *Server) loadSyncState(id string) gameserver.SyncState {
	var st gameserver.SyncState
	if raw, err := os.ReadFile(s.syncStatePath(id)); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

func (s *Server) saveSyncState(id string, st gameserver.SyncState) error {
	path := s.syncStatePath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(st, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Server) syncEngine(id string) gameserver.WorkshopSync {
	api := s.wsync.api
	if api == nil {
		api = gameserver.SteamWorkshopAPI{}
	}
	fetch := s.wsync.fetch
	if fetch == nil {
		fetch = s.gameRuntime().WorkshopDownload
	}
	return gameserver.WorkshopSync{API: api, Fetch: fetch, Now: s.now, Work: filepath.Join(s.syncRoot(), id, "work"), Items: s.syncItemsDir(id)}
}

// syncCollection is the collection the server runs: its Workshop collection
// setting, or one added as content.
func (s *Server) syncCollection(ctx context.Context, row appdb.GameServer, tmpl gameserver.Template) string {
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	if id := strings.TrimSpace(env["WORKSHOP_COLLECTION"]); id != "" {
		return id
	}
	return gameserver.WorkshopCollection(ctx, s.gameHost(), row.ID, tmpl.Content, gameserver.Workshop{})
}

// RunWorkshopSync looks at every server with live sync on every few seconds:
// a new request is noticed within that time; checks run when due.
func (s *Server) RunWorkshopSync(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		s.workshopSyncTick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) workshopSyncTick(ctx context.Context) {
	entries, err := os.ReadDir(s.syncRoot())
	if err != nil {
		return
	}
	cl, err := s.Store.GetCluster(ctx)
	if err != nil || cl == nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !s.loadSyncState(e.Name()).Enabled {
			continue
		}
		row, err := s.Store.GetGameServer(ctx, cl.ID, e.Name())
		if err != nil || row == nil {
			continue
		}
		if !s.wsync.start(row.ID) {
			continue
		}
		go func(row appdb.GameServer) {
			defer s.wsync.done(row.ID)
			s.workshopSyncServer(context.WithoutCancel(ctx), row)
		}(*row)
	}
}

// workshopSyncServer does what one server needs now: a new baseline after a
// restart, picking up a request, and a check when one is due.
func (s *Server) workshopSyncServer(ctx context.Context, row appdb.GameServer) {
	mu := s.wsync.lock(row.ID)
	mu.Lock()
	defer mu.Unlock()
	st := s.loadSyncState(row.ID)
	if !st.Enabled || row.Status != gameserver.StatusRunning || row.ContainerID == "" {
		return
	}
	now := s.now()
	s.wsync.mu.Lock()
	wait := s.wsync.retry[row.ID]
	s.wsync.mu.Unlock()
	if now.Before(wait) {
		return
	}
	fail := func(err error) {
		st.LastError = err.Error()
		_ = s.saveSyncState(row.ID, st)
		s.wsync.mu.Lock()
		if s.wsync.retry == nil {
			s.wsync.retry = map[string]time.Time{}
		}
		s.wsync.retry[row.ID] = now.Add(time.Minute)
		s.wsync.mu.Unlock()
	}
	tmpl, err := s.resolveTemplate(ctx, row.ClusterID, row.TemplateID)
	if err != nil || !gameserver.WorkshopSyncSupported(tmpl) {
		fail(errors.New("this server's game does not support live Workshop sync"))
		return
	}
	collection := s.syncCollection(ctx, row, tmpl)
	if collection == "" {
		fail(errors.New("the server has no Workshop collection set"))
		return
	}
	if !s.gameRuntime().HasReadOnlyMount(ctx, row.ID, tmpl.WorkingDir, gameserver.SyncItemsRel) {
		fail(errors.New("restart the server once: it was started before ndl-ce could attach its read-only packs folder"))
		return
	}
	dir, err := gameserver.OpenSyncDir(row.DataDir, gameserver.SyncDirRel)
	if err != nil {
		fail(fmt.Errorf("opening %s: %w", gameserver.SyncDirRel, err))
		return
	}
	defer dir.Close()
	engine := s.syncEngine(row.ID)
	cctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()

	if st.ContainerID != row.ContainerID {
		// The server (re)started: what it loaded is the collection as it is now.
		if err := engine.Baseline(cctx, &st, collection); err != nil {
			fail(fmt.Errorf("reading the collection: %w", err))
			return
		}
		st.ContainerID = row.ContainerID
		st.LastError = ""
		_ = s.saveSyncState(row.ID, st)
	}
	if req, _, err := gameserver.ReadSyncRequest(dir); err == nil && req.Valid() && !st.Seen(req.ID) {
		st.Pending = req.ID
		_ = s.saveSyncState(row.ID, st)
	}
	last := time.Unix(st.LastCheck, 0)
	due := (st.Pending != "" && now.Sub(last) >= gameserver.SyncMinInterval) || now.Sub(last) >= gameserver.SyncCheckEvery
	if !due {
		return
	}
	reqID := st.Pending
	m, err := engine.Check(cctx, &st, dir, collection, reqID)
	if err != nil {
		fail(err)
		return
	}
	if reqID != "" {
		st.Handle(reqID)
	}
	_ = s.saveSyncState(row.ID, st)
	if err := s.gameHost().Send(ctx, row.ID, fmt.Sprintf("%s %d", gameserver.SyncReadyCommand, m.Seq)); err != nil {
		log.Printf("workshop sync: %s: telling the server about manifest %d: %v", row.ID, m.Seq, err)
	}
	if len(m.Items) > 0 || len(m.Errors) > 0 {
		log.Printf("workshop sync: %s: manifest %d, %d item(s) ready, %d error(s)", row.ID, m.Seq, len(m.Items), len(m.Errors))
	}
}

func (s *Server) workshopSyncJSON(ctx context.Context, row appdb.GameServer) map[string]any {
	tmpl, _ := s.resolveTemplate(ctx, row.ClusterID, row.TemplateID)
	st := s.loadSyncState(row.ID)
	items := []map[string]any{}
	errs := st.LastErrors
	if errs == nil {
		errs = []gameserver.SyncError{}
	}
	for id, it := range st.Synced {
		items = append(items, map[string]any{
			"wsid": id, "title": it.Title, "state": it.State, "kind": it.Kind,
			"time_updated": it.TimeUpdated, "size": it.Size,
		})
	}
	out := map[string]any{
		"supported": gameserver.WorkshopSyncSupported(tmpl), "enabled": st.Enabled,
		"collection": s.syncCollection(ctx, row, tmpl), "running": row.Status == gameserver.StatusRunning,
		"checking": s.wsync.checking(row.ID), "seq": st.Seq, "pending_request": st.Pending,
		"last_result": st.LastResult, "last_error": st.LastError, "errors": errs, "items": items,
	}
	if st.LastCheck > 0 {
		out["last_check"] = time.Unix(st.LastCheck, 0).UTC().Format(time.RFC3339)
		next := time.Unix(st.LastCheck, 0).Add(gameserver.SyncCheckEvery)
		if st.Pending != "" {
			next = time.Unix(st.LastCheck, 0).Add(gameserver.SyncMinInterval)
		}
		out["next_check"] = next.UTC().Format(time.RFC3339)
	}
	return out
}

func (s *Server) getWorkshopSync(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.workshopSyncJSON(r.Context(), *row))
}

func (s *Server) putWorkshopSync(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "send enabled: true or false")
		return
	}
	tmpl, _ := s.resolveTemplate(r.Context(), row.ClusterID, row.TemplateID)
	if *req.Enabled && !gameserver.WorkshopSyncSupported(tmpl) {
		writeErr(w, http.StatusUnprocessableEntity, "live Workshop sync is for Garry's Mod servers")
		return
	}
	mu := s.wsync.lock(row.ID)
	if !mu.TryLock() {
		writeErr(w, http.StatusConflict, "a check is running; try again when it finishes")
		return
	}
	st := s.loadSyncState(row.ID)
	if *req.Enabled && !st.Enabled {
		// What the server runs now is the starting point.
		st.ContainerID = ""
	}
	st.Enabled = *req.Enabled
	err := s.saveSyncState(row.ID, st)
	mu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save the setting")
		return
	}
	state := "off"
	if st.Enabled {
		state = "on"
	}
	s.noteGame(r, p, row.ID, "workshop-sync", "Live Workshop sync turned "+state, "")
	writeJSON(w, http.StatusOK, s.workshopSyncJSON(r.Context(), *row))
}

// checkWorkshopSync is "Check now" from the panel. It queues a request like
// one from the server, so the same rate limit applies.
func (s *Server) checkWorkshopSync(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return
	}
	mu := s.wsync.lock(row.ID)
	if !mu.TryLock() {
		writeErr(w, http.StatusConflict, "a check is running now")
		return
	}
	st := s.loadSyncState(row.ID)
	if !st.Enabled {
		mu.Unlock()
		writeErr(w, http.StatusUnprocessableEntity, "turn live Workshop sync on first")
		return
	}
	if st.Pending == "" {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		st.Pending = fmt.Sprintf("panel-%d-%s", s.now().Unix(), hex.EncodeToString(b))
	}
	err := s.saveSyncState(row.ID, st)
	mu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not queue the check")
		return
	}
	s.noteGame(r, p, row.ID, "workshop-sync", "Workshop check requested", "")
	writeJSON(w, http.StatusAccepted, s.workshopSyncJSON(r.Context(), *row))
}

// removeWorkshopSync forgets a deleted server's sync state.
func (s *Server) removeWorkshopSync(id string) {
	if id == "" || strings.ContainsAny(id, "/.") {
		return
	}
	_ = os.RemoveAll(filepath.Join(s.syncRoot(), id))
}
