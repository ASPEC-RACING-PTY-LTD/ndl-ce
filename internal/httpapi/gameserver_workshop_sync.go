package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Live Workshop sync: the agent does the work next to the server folders
// (gameserver.LocalHost.WorkshopSync). The control plane only tells it what
// the database knows, every few seconds, and serves the panel.

// syncCall fills in what the agent needs to know about a server.
func (s *Server) syncCall(ctx context.Context, op string, row appdb.GameServer, tmpl gameserver.Template) gameserver.SyncCall {
	return gameserver.SyncCall{
		Op: op, Running: row.Status == gameserver.StatusRunning, ContainerID: row.ContainerID,
		WorkDir: tmpl.WorkingDir, Collection: s.syncCollection(ctx, row, tmpl),
	}
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

// RunWorkshopSync asks the agent every few seconds to do what is due for
// each running Garry's Mod server: a new request is noticed within that
// time, and checks run in the agent when due.
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
	cl, err := s.Store.GetCluster(ctx)
	if err != nil || cl == nil {
		return
	}
	rows, err := s.Store.ListGameServers(ctx, cl.ID)
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.Status != gameserver.StatusRunning || row.ContainerID == "" {
			continue
		}
		s.workshopSyncServer(ctx, row)
	}
}

// workshopSyncServer hands one server's due work to the agent.
func (s *Server) workshopSyncServer(ctx context.Context, row appdb.GameServer) {
	tmpl, err := s.resolveTemplate(ctx, row.ClusterID, row.TemplateID)
	if err != nil || !gameserver.WorkshopSyncSupported(tmpl) {
		return
	}
	if _, err := s.gameHost().WorkshopSync(ctx, row.ID, s.syncCall(ctx, gameserver.SyncOpTick, row, tmpl)); err != nil {
		log.Printf("workshop sync: %s: %v", row.ID, err)
	}
}

func (s *Server) workshopSyncJSON(ctx context.Context, row appdb.GameServer, reply gameserver.SyncReply) map[string]any {
	tmpl, _ := s.resolveTemplate(ctx, row.ClusterID, row.TemplateID)
	st := reply.State
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
		"checking": reply.Checking, "seq": st.Seq, "pending_request": st.Pending,
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
	reply, err := s.gameHost().WorkshopSync(r.Context(), row.ID, gameserver.SyncCall{Op: gameserver.SyncOpGet})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "could not read the sync state: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.workshopSyncJSON(r.Context(), *row, reply))
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
	reply, err := s.gameHost().WorkshopSync(r.Context(), row.ID, gameserver.SyncCall{Op: gameserver.SyncOpSet, Enabled: *req.Enabled})
	if err != nil {
		writeErr(w, http.StatusConflict, "could not save the setting: "+err.Error())
		return
	}
	st := reply.State
	state := "off"
	if st.Enabled {
		state = "on"
	}
	s.noteGame(r, p, row.ID, "workshop-sync", "Live Workshop sync turned "+state, "")
	writeJSON(w, http.StatusOK, s.workshopSyncJSON(r.Context(), *row, reply))
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
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	reqID := fmt.Sprintf("panel-%d-%s", s.now().Unix(), hex.EncodeToString(b))
	reply, err := s.gameHost().WorkshopSync(r.Context(), row.ID, gameserver.SyncCall{Op: gameserver.SyncOpCheck, RequestID: reqID})
	if err != nil {
		code := http.StatusConflict
		if !reply.State.Enabled {
			code = http.StatusUnprocessableEntity
		}
		writeErr(w, code, err.Error())
		return
	}
	s.noteGame(r, p, row.ID, "workshop-sync", "Workshop check requested", "")
	writeJSON(w, http.StatusAccepted, s.workshopSyncJSON(r.Context(), *row, reply))
}

// removeWorkshopSync forgets a deleted server's sync state.
func (s *Server) removeWorkshopSync(ctx context.Context, id string) {
	if _, err := s.gameHost().WorkshopSync(ctx, id, gameserver.SyncCall{Op: gameserver.SyncOpRemove}); err != nil {
		log.Printf("workshop sync: %s: forgetting the deleted server: %v", id, err)
	}
}
