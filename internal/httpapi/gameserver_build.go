package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// A server's build (32-bit or 64-bit, gameserver/builds.go) is the
// launcher its startup command runs. Changing it rewrites the command
// and the Steam branch; the files arrive the next time the server
// starts, through SteamCMD without validate, so edited files are kept.

// errGameBuildMissing is returned by startGame when the build the server
// is set to has not been downloaded yet.
type errGameBuildMissing struct{ build gameserver.BuildOption }

func (e errGameBuildMissing) Error() string {
	return fmt.Sprintf("the %s files are not installed yet", e.build.Label)
}

// gameStartup is the command a server starts with: its own, as saved on
// the Startup tab, or else its template's.
func gameStartup(row appdb.GameServer, tmpl gameserver.Template) string {
	if strings.TrimSpace(row.Startup) != "" {
		return row.Startup
	}
	return tmpl.Startup
}

func (s *Server) gameBuildInstalled(ctx context.Context, id string, b gameserver.BuildOption) bool {
	listing, err := s.gameHost().ListDir(ctx, id, b.LauncherFile())
	return err == nil && listing.File
}

// checkGameBuild is called before a start: it fails when the server's
// build has no files yet.
func (s *Server) checkGameBuild(ctx context.Context, row appdb.GameServer, tmpl gameserver.Template) error {
	if len(tmpl.Builds) == 0 {
		return nil
	}
	b, ok := tmpl.BuildFor(gameStartup(row, tmpl))
	// The default build is what every install puts down; only a server
	// switched to another build may lack its files.
	if !ok || b.ID == tmpl.Builds[0].ID || s.gameBuildInstalled(ctx, row.ID, b) {
		return nil
	}
	return errGameBuildMissing{build: b}
}

// prepareGameBuild downloads the server's build in the background and
// starts the server when it is in place. The row is saved as installing.
func (s *Server) prepareGameBuild(ctx context.Context, row *appdb.GameServer, b gameserver.BuildOption) error {
	tmpl, err := s.resolveTemplate(ctx, row.ClusterID, row.TemplateID)
	if err != nil {
		return err
	}
	row.Status = gameserver.StatusPending
	row.InstallPhase = "downloading"
	row.DesiredPower = "running"
	row.ErrorHuman, row.ErrorRaw = "", ""
	if err := s.Store.UpdateGameServer(ctx, *row); err != nil {
		return err
	}
	_ = s.Store.InsertGameEvent(ctx, appdb.GameEvent{ID: uuid.NewString(), ServerID: row.ID, ClusterID: row.ClusterID, Kind: "build", Summary: "Downloading the " + b.Label + " files", CreatedAt: s.now()})
	srv := *row
	go func() {
		ctx := context.Background()
		s.createRollback(ctx, srv, "pre-build")
		env := map[string]string{}
		_ = json.Unmarshal(srv.EnvJSON, &env)
		env["STEAM_BETA"] = b.Beta
		env["NDL_STEAM_VALIDATE"] = "0"
		inst := srv
		inst.EnvJSON = mustJSON(env)
		s.installGame(inst, tmpl)
		cur, err := s.Store.GetGameServer(ctx, srv.ClusterID, srv.ID)
		if err != nil || cur == nil || cur.Status != gameserver.StatusReady {
			return
		}
		fail := func(human string) {
			s.saveInstallState(ctx, *cur, func(g *appdb.GameServer) {
				g.Status = gameserver.StatusFailed
				g.ErrorHuman, g.ErrorRaw = human, human
			})
		}
		if !s.gameBuildInstalled(ctx, cur.ID, b) {
			fail("SteamCMD finished but the " + b.Label + " launcher (" + b.Run + ") is still missing.")
			return
		}
		if err := s.startGame(ctx, s.gameHost(), cur); err != nil {
			fail(gameserver.HumanError(err.Error()))
			return
		}
		cur.ErrorHuman, cur.ErrorRaw = "", ""
		_ = s.Store.UpdateGameServer(ctx, *cur)
		_ = s.Store.InsertGameEvent(ctx, appdb.GameEvent{ID: uuid.NewString(), ServerID: cur.ID, ClusterID: cur.ClusterID, Kind: "build", Summary: "Started on the " + b.Label + " build", CreatedAt: s.now()})
	}()
	return nil
}

type gameBuildState struct {
	Options   []gameBuildOption `json:"options"`
	Current   string            `json:"current"`
	Running   bool              `json:"running"`
	Supported bool              `json:"supported"`
	// Custom is set when the startup command starts with none of the
	// known launchers, so the build cannot be changed here.
	Custom bool `json:"custom,omitempty"`
}

type gameBuildOption struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Installed bool   `json:"installed"`
}

func (s *Server) gameBuildState(ctx context.Context, row appdb.GameServer, tmpl gameserver.Template) gameBuildState {
	st := gameBuildState{Options: []gameBuildOption{}, Supported: len(tmpl.Builds) > 0}
	switch row.Status {
	case gameserver.StatusPending, gameserver.StatusInstalling, gameserver.StatusStarting, gameserver.StatusRunning, gameserver.StatusStopping:
		st.Running = true
	}
	for _, b := range tmpl.Builds {
		st.Options = append(st.Options, gameBuildOption{ID: b.ID, Label: b.Label, Installed: s.gameBuildInstalled(ctx, row.ID, b)})
	}
	if cur, ok := tmpl.BuildFor(gameStartup(row, tmpl)); ok {
		st.Current = cur.ID
	} else if st.Supported {
		st.Custom = true
	}
	return st
}

func (s *Server) getGameBuild(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, s.gameBuildState(r.Context(), *row, tmpl))
}

func (s *Server) putGameBuild(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Build string `json:"build"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	to, ok := tmpl.BuildByID(req.Build)
	if !ok {
		writeErr(w, http.StatusBadRequest, "this server has no such build")
		return
	}
	if st := s.gameBuildState(r.Context(), *row, tmpl); st.Running {
		writeErr(w, http.StatusConflict, "stop the server before changing its build")
		return
	}
	startup, err := tmpl.WithBuild(gameStartup(*row, tmpl), to)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	env := map[string]string{}
	_ = json.Unmarshal(row.EnvJSON, &env)
	// Updates and reinstalls follow the build's Steam branch.
	if to.Beta != "" {
		env["STEAM_BETA"] = to.Beta
	} else {
		delete(env, "STEAM_BETA")
	}
	row.Startup = startup
	row.EnvJSON = mustJSON(env)
	if err := s.Store.UpdateGameServer(r.Context(), *row); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.noteGame(r, p, row.ID, "build", "Build set to "+to.Label, "")
	writeJSON(w, http.StatusOK, s.gameBuildState(r.Context(), *row, tmpl))
}

// startOrPrepare starts the server, or begins downloading its build
// first when the build's files are missing. preparing reports the second.
func (s *Server) startOrPrepare(ctx context.Context, rt gameserver.Host, row *appdb.GameServer) (preparing bool, err error) {
	err = s.startGame(ctx, rt, row)
	var missing errGameBuildMissing
	if errors.As(err, &missing) {
		return true, s.prepareGameBuild(ctx, row, missing.build)
	}
	return false, err
}
