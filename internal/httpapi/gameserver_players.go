package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// The Players tab: who is online, read from the game's own query protocol,
// and the console commands that act on one of them where the game has
// well-known ones. Game servers run on the control node, so the query goes
// to the published port on loopback.

// gameQueryHost is where the control plane reaches published game ports.
var gameQueryHost = "127.0.0.1"

// playerCommands are console commands per game, keyed by action. %s is
// the player's name, already quoted where the game needs quotes.
func playerCommands(row *appdb.GameServer) map[string]string {
	switch {
	case strings.EqualFold(row.Family, "minecraft"):
		return map[string]string{"kick": "kick %s", "ban": "ban %s", "op": "op %s"}
	case strings.EqualFold(row.Family, "source"):
		return map[string]string{"kick": `kick "%s"`}
	case strings.EqualFold(row.Game, "rust"):
		return map[string]string{"kick": `kick "%s"`, "ban": `ban "%s"`}
	}
	return nil
}

func (s *Server) gameServerPlayers(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "view")
	if !ok {
		return
	}
	actions := []string{}
	if s.canGame(r.Context(), p, row, "console") {
		for a := range playerCommands(row) {
			actions = append(actions, a)
		}
		slices.Sort(actions)
	}
	var ports []gameserver.Port
	_ = json.Unmarshal(row.PortsJSON, &ports)
	target, supported := gameserver.QueryTargetFor(row.Family, ports)
	resp := map[string]any{"supported": supported, "running": false, "actions": actions}
	if !supported {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if !s.gameHost().Running(r.Context(), row.ID) {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp["running"] = true
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	list, err := gameserver.QueryPlayers(ctx, gameQueryHost, target)
	if err != nil {
		resp["error"] = "The server did not answer the player query on port " + itoa(target.Port) + ". It may still be starting."
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp["list"] = list
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) gameServerPlayerAction(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConsole)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "console")
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tmpl, ok := playerCommands(row)[req.Action]
	if !ok {
		writeErr(w, http.StatusBadRequest, "this game has no "+req.Action+" command")
		return
	}
	name := strings.TrimSpace(req.Name)
	// Names go into a console line, so nothing that could end the line,
	// close the quotes or start a second command.
	if name == "" || len(name) > 64 || strings.ContainsAny(name, "\"\\;\r\n\x00") {
		writeErr(w, http.StatusBadRequest, "this player name cannot be used in a console command; use the console")
		return
	}
	if strings.EqualFold(row.Family, "minecraft") && strings.ContainsAny(name, " \t") {
		writeErr(w, http.StatusBadRequest, "Minecraft names have no spaces")
		return
	}
	cmd := strings.Replace(tmpl, "%s", name, 1)
	if err := s.gameHost().Send(r.Context(), row.ID, cmd); err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	_ = s.Store.AppendGameConsoleHist(r.Context(), row.ID, cmd)
	s.noteGame(r, p, row.ID, "players."+req.Action, "Player "+req.Action, clipGame(name, 64))
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "command": cmd})
}
