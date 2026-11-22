package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// A game server's own MariaDB (gameserver/database.go). The owner picks
// the database name; NDL generates the user and passwords and keeps
// them, readable here by whoever may configure the server. Installing
// runs as a background job, like a backup, since the first install
// downloads MariaDB.

type gameDBView struct {
	Supported   bool       `json:"supported"`
	State       string     `json:"state"`
	Database    string     `json:"database,omitempty"`
	User        string     `json:"user,omitempty"`
	Host        string     `json:"host,omitempty"`
	Port        int        `json:"port,omitempty"`
	Image       string     `json:"image,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
	Password    string     `json:"password,omitempty"`
	// Socket is where the game can connect through a Unix socket instead
	// of TCP, once the database runs with NDL's socket folder.
	Socket string         `json:"socket,omitempty"`
	Job    *gameBackupJob `json:"job"`
}

func (s *Server) gameDBView(row appdb.GameServer, tmpl gameserver.Template, reply gameserver.DBReply, withPassword bool) gameDBView {
	v := gameDBView{Supported: gameserver.DatabaseSupported(tmpl), State: reply.State, Job: s.gameJobs.job(row.ID)}
	if c := reply.Config; c != nil {
		v.Database, v.User, v.Host, v.Port, v.Image, v.InstalledAt = c.Database, c.User, c.Host, c.Port, c.Image, c.InstalledAt
		if c.InstalledAt != nil {
			v.Socket = gameserver.DBSocketPath
		}
		if withPassword {
			v.Password = c.Password
		}
	}
	if v.State == "" {
		v.State = "none"
	}
	return v
}

// loadGameDB loads the server and its template for a database request.
func (s *Server) loadGameDB(w http.ResponseWriter, r *http.Request) (*principal, *appdb.GameServer, gameserver.Template, bool) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConfig)
	if !ok {
		return nil, nil, gameserver.Template{}, false
	}
	row, ok := s.loadGame(w, r, p, "config")
	if !ok {
		return nil, nil, gameserver.Template{}, false
	}
	tmpl, err := s.resolveTemplate(r.Context(), p.User.ClusterID, row.TemplateID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, nil, gameserver.Template{}, false
	}
	if !gameserver.DatabaseSupported(tmpl) {
		writeErr(w, http.StatusUnprocessableEntity, "this game server has no database support")
		return nil, nil, gameserver.Template{}, false
	}
	return p, row, tmpl, true
}

func (s *Server) gameDBCall(ctx context.Context, id string, call gameserver.DBCall) (gameserver.DBReply, error) {
	return s.gameHost().Database(ctx, id, call)
}

func (s *Server) getGameDatabase(w http.ResponseWriter, r *http.Request) {
	_, row, tmpl, ok := s.loadGameDB(w, r)
	if !ok {
		return
	}
	reply, err := s.gameDBCall(r.Context(), row.ID, gameserver.DBCall{Op: "get"})
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, s.gameDBView(*row, tmpl, reply, false))
}

// getGameDatabaseCredentials returns the password too. Each read is
// recorded on the server's activity.
func (s *Server) getGameDatabaseCredentials(w http.ResponseWriter, r *http.Request) {
	p, row, tmpl, ok := s.loadGameDB(w, r)
	if !ok {
		return
	}
	reply, err := s.gameDBCall(r.Context(), row.ID, gameserver.DBCall{Op: "get"})
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	if reply.Config == nil {
		writeErr(w, http.StatusNotFound, "set the database name first")
		return
	}
	s.noteGame(r, p, row.ID, "database.credentials", "Database password viewed", "")
	writeJSON(w, http.StatusOK, s.gameDBView(*row, tmpl, reply, true))
}

func (s *Server) putGameDatabase(w http.ResponseWriter, r *http.Request) {
	p, row, tmpl, ok := s.loadGameDB(w, r)
	if !ok {
		return
	}
	var req struct {
		Database string `json:"database"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	name, err := gameserver.CleanDBName(req.Database)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	reply, err := s.gameDBCall(r.Context(), row.ID, gameserver.DBCall{Op: "configure", Database: name})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, gameserver.HumanError(err.Error()))
		return
	}
	s.noteGame(r, p, row.ID, "database.configure", "Database set to "+name, "")
	writeJSON(w, http.StatusOK, s.gameDBView(*row, tmpl, reply, false))
}

func (s *Server) installGameDatabase(w http.ResponseWriter, r *http.Request) {
	p, row, tmpl, ok := s.loadGameDB(w, r)
	if !ok {
		return
	}
	reply, err := s.gameDBCall(r.Context(), row.ID, gameserver.DBCall{Op: "get"})
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	if reply.Config == nil {
		writeErr(w, http.StatusUnprocessableEntity, "set the database name first")
		return
	}
	id := row.ID
	_, err = s.startGameJob(id, "database", reply.Config.Database, func(ctx context.Context) error {
		if _, err := s.gameDBCall(ctx, id, gameserver.DBCall{Op: "install"}); err != nil {
			return err
		}
		s.noteGameCtx(ctx, p, id, "database.install", "MariaDB installed", "")
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	reply.State = "installing"
	writeJSON(w, http.StatusAccepted, s.gameDBView(*row, tmpl, reply, false))
}

func (s *Server) gameDatabasePower(w http.ResponseWriter, r *http.Request, op string) {
	p, row, tmpl, ok := s.loadGameDB(w, r)
	if !ok {
		return
	}
	if j := s.gameJobs.job(row.ID); j != nil && j.Running {
		writeErr(w, http.StatusConflict, errGameJobBusy.Error())
		return
	}
	reply, err := s.gameDBCall(r.Context(), row.ID, gameserver.DBCall{Op: op})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, gameserver.HumanError(err.Error()))
		return
	}
	s.noteGame(r, p, row.ID, "database."+op, "Database "+op, "")
	writeJSON(w, http.StatusOK, s.gameDBView(*row, tmpl, reply, false))
}

func (s *Server) startGameDatabase(w http.ResponseWriter, r *http.Request) {
	s.gameDatabasePower(w, r, "start")
}

func (s *Server) stopGameDatabase(w http.ResponseWriter, r *http.Request) {
	s.gameDatabasePower(w, r, "stop")
}

// removeGameDatabase uninstalls the database. Its data and credentials
// stay unless delete_data is set, which needs the database name typed
// back as confirmation.
func (s *Server) removeGameDatabase(w http.ResponseWriter, r *http.Request) {
	p, row, tmpl, ok := s.loadGameDB(w, r)
	if !ok {
		return
	}
	if j := s.gameJobs.job(row.ID); j != nil && j.Running {
		writeErr(w, http.StatusConflict, errGameJobBusy.Error())
		return
	}
	var req struct {
		DeleteData bool   `json:"delete_data"`
		Confirm    string `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	cur, err := s.gameDBCall(r.Context(), row.ID, gameserver.DBCall{Op: "get"})
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	if cur.Config == nil {
		writeErr(w, http.StatusNotFound, "this server has no database")
		return
	}
	if req.DeleteData && strings.TrimSpace(req.Confirm) != cur.Config.Database {
		writeErr(w, http.StatusConflict, "type the database name to confirm deleting its data")
		return
	}
	reply, err := s.gameDBCall(r.Context(), row.ID, gameserver.DBCall{Op: "remove", DeleteData: req.DeleteData})
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	summary := "Database uninstalled (data kept)"
	if req.DeleteData {
		summary = "Database and its data deleted"
	}
	s.noteGame(r, p, row.ID, "database.remove", summary, cur.Config.Database)
	writeJSON(w, http.StatusOK, s.gameDBView(*row, tmpl, reply, false))
}

// GET /game-servers/{id}/database/resources: the limits and what the
// database uses now.
func (s *Server) getGameDatabaseResources(w http.ResponseWriter, r *http.Request) {
	_, row, _, ok := s.loadGameDB(w, r)
	if !ok {
		return
	}
	// ?usage=0 answers at once without measuring use (about 0.25 s).
	quick := r.URL.Query().Get("usage") == "0"
	reply, err := s.gameDBCall(r.Context(), row.ID, gameserver.DBCall{Op: "resources", Quick: quick})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, gameserver.HumanError(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, gameDBResourcesView(reply))
}

func gameDBResourcesView(reply gameserver.DBReply) map[string]any {
	out := map[string]any{"state": reply.State, "usage": reply.Usage}
	if c := reply.Config; c != nil {
		out["memory_mb"], out["cpus"], out["buffer_pool_mb"], out["fast_commit"] = c.MemoryMB, c.CPUs, c.BufferPoolMB, c.FastCommit
	}
	return out
}

// PUT /game-servers/{id}/database/resources {memory_mb, cpus,
// buffer_pool_mb}: applied live when the database runs.
func (s *Server) putGameDatabaseResources(w http.ResponseWriter, r *http.Request) {
	p, row, _, ok := s.loadGameDB(w, r)
	if !ok {
		return
	}
	if j := s.gameJobs.job(row.ID); j != nil && j.Running {
		writeErr(w, http.StatusConflict, errGameJobBusy.Error())
		return
	}
	var req gameserver.DBResources
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	reply, err := s.gameDBCall(r.Context(), row.ID, gameserver.DBCall{Op: "resources", Resources: &req})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, gameserver.HumanError(err.Error()))
		return
	}
	cpus := "no CPU cap"
	if req.CPUs > 0 {
		cpus = strconv.FormatFloat(req.CPUs, 'f', -1, 64) + " CPUs"
	}
	detail := strconv.Itoa(req.MemoryMB) + " MB, " + cpus
	if req.FastCommit {
		detail += ", faster writes"
	}
	s.noteGame(r, p, row.ID, "database.resources", "Database limits set", detail)
	s.audit(r, p.User.ClusterID, p.User.ID, "gameserver.database.resources", "ok", row.ID+" "+detail)
	writeJSON(w, http.StatusOK, gameDBResourcesView(reply))
}

// POST /game-servers/{id}/database/restart recreates the database's
// container from its settings, keeping the data.
func (s *Server) restartGameDatabase(w http.ResponseWriter, r *http.Request) {
	s.gameDatabasePower(w, r, "restart")
}
