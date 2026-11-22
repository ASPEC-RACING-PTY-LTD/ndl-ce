package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// The global database manager (Administration > Infrastructure >
// Databases): every game server's MariaDB in one list, with bulk start,
// stop and SQL. Managing one database opens the same manager as its
// server's MariaDB tab.

type globalDB struct {
	ServerID    string     `json:"server_id"`
	ServerName  string     `json:"server_name"`
	Game        string     `json:"game"`
	Database    string     `json:"database"`
	User        string     `json:"user"`
	State       string     `json:"state"`
	Image       string     `json:"image,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
	SizeBytes   int64      `json:"size_bytes"`
	Tables      int64      `json:"tables"`
	MemoryMB    int        `json:"memory_mb"`
	CPUs        float64    `json:"cpus"`
}

// GET /api/v1/databases
func (s *Server) listDatabases(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.DatabasesManage)
	if err != nil {
		return
	}
	rows, err := s.Store.ListGameServers(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []globalDB{}
	for _, row := range rows {
		tmpl, err := s.resolveTemplate(r.Context(), row.ClusterID, row.TemplateID)
		if err != nil || !gameserver.DatabaseSupported(tmpl) {
			continue
		}
		reply, err := s.gameDBCall(r.Context(), row.ID, gameserver.DBCall{Op: "get"})
		if err != nil || reply.Config == nil {
			continue
		}
		c := reply.Config
		d := globalDB{ServerID: row.ID, ServerName: row.Name, Game: row.Game, Database: c.Database, User: c.User, State: reply.State, Image: c.Image, InstalledAt: c.InstalledAt, MemoryMB: c.MemoryMB, CPUs: c.CPUs}
		if reply.State == "running" {
			d.SizeBytes, d.Tables = s.dbSize(r.Context(), row.ID)
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) dbSize(ctx context.Context, id string) (int64, int64) {
	q, err := s.dbRun(ctx, id, "", "SELECT IFNULL(SUM(DATA_LENGTH+INDEX_LENGTH),0), COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')", 1)
	if err != nil {
		return 0, 0
	}
	rows, err := dbRows(q)
	if err != nil || len(rows) == 0 {
		return 0, 0
	}
	size, _ := strconv.ParseInt(str(rows[0][0]), 10, 64)
	n, _ := strconv.ParseInt(str(rows[0][1]), 10, 64)
	return size, n
}

type bulkDBResult struct {
	ServerID   string                    `json:"server_id"`
	ServerName string                    `json:"server_name"`
	OK         bool                      `json:"ok"`
	Error      string                    `json:"error,omitempty"`
	State      string                    `json:"state,omitempty"`
	Query      *gameserver.DBQueryResult `json:"query,omitempty"`
}

// POST /api/v1/databases/bulk {server_ids, action: start|stop|query, sql}
func (s *Server) bulkDatabases(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.DatabasesManage)
	if err != nil {
		return
	}
	var req struct {
		ServerIDs []string `json:"server_ids"`
		Action    string   `json:"action"`
		SQL       string   `json:"sql"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	switch req.Action {
	case "start", "stop", "restart":
	case "query":
		if strings.TrimSpace(req.SQL) == "" {
			writeErr(w, http.StatusBadRequest, "there is no SQL to run")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "action is start, stop, restart or query")
		return
	}
	if len(req.ServerIDs) == 0 || len(req.ServerIDs) > 200 {
		writeErr(w, http.StatusBadRequest, "pick 1 to 200 databases")
		return
	}
	results := []bulkDBResult{}
	for _, id := range req.ServerIDs {
		row, err := s.Store.GetGameServer(r.Context(), p.User.ClusterID, id)
		if err != nil || row == nil {
			results = append(results, bulkDBResult{ServerID: id, Error: "no such game server"})
			continue
		}
		res := bulkDBResult{ServerID: id, ServerName: row.Name}
		if j := s.gameJobs.job(id); j != nil && j.Running {
			res.Error = errGameJobBusy.Error()
			results = append(results, res)
			continue
		}
		if req.Action == "query" {
			q, err := s.dbRun(r.Context(), id, "", req.SQL, 200)
			if err != nil {
				res.Error = gameserver.HumanError(err.Error())
			} else {
				res.Query, res.OK = q, q.Error == ""
				if q.Error != "" {
					res.Error = q.Error
				}
				s.noteDBQuery(r, p, id, "(bulk)", req.SQL, q)
			}
		} else {
			reply, err := s.gameDBCall(r.Context(), id, gameserver.DBCall{Op: req.Action})
			if err != nil {
				res.Error = gameserver.HumanError(err.Error())
			} else {
				res.OK, res.State = true, reply.State
				s.noteGame(r, p, id, "database."+req.Action, "Database "+req.Action+" (bulk)", "")
			}
		}
		results = append(results, res)
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "databases.bulk."+req.Action, "ok", strconv.Itoa(len(req.ServerIDs))+" database(s)")
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
