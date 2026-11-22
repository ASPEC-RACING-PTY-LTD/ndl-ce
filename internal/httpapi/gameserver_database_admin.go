package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

// The database manager (MariaDB tab): browse tables, run SQL, and manage
// users and their privileges, in a server's own MariaDB. Everything runs
// as the database's root through the agent (gameserver/database_query.go).
// Requests that build SQL quote every name and value here; the SQL
// console runs what the person typed, which they may do: they can
// configure this server, and the database is the server's own.

var (
	dbUserRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,80}$`)
	dbHostRe = regexp.MustCompile(`^[A-Za-z0-9_.%:\-]{1,255}$`)
	// Statements that carry a password, so the activity log never does.
	dbSecretRe = regexp.MustCompile(`(?i)(IDENTIFIED\s+(?:BY\s+(?:PASSWORD\s+)?|VIA\s+\S+\s+USING\s+(?:PASSWORD\s*\(\s*)?)|PASSWORD\s*\(\s*)'(?:[^'\\]|\\.|'')*'`)
)

// dbPrivileges are the privileges a user may be given on one database.
var dbPrivileges = []string{
	"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "INDEX", "ALTER",
	"CREATE TEMPORARY TABLES", "LOCK TABLES", "EXECUTE", "CREATE VIEW", "SHOW VIEW",
	"CREATE ROUTINE", "ALTER ROUTINE", "EVENT", "TRIGGER", "REFERENCES",
}

// dbProtectedUsers are MariaDB's own accounts; the manager leaves them be.
var dbProtectedUsers = map[string]bool{"root": true, "mariadb.sys": true, "healthcheck": true, "PUBLIC": true}

func sqlIdent(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }

func sqlString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\x00", `\0`, "\n", `\n`, "\r", `\r`, "\x1a", `\Z`)
	return "'" + r.Replace(s) + "'"
}

func sqlAccount(user, host string) string { return sqlString(user) + "@" + sqlString(host) }

func redactSQL(sql string) string {
	return dbSecretRe.ReplaceAllString(sql, "${1}'***'")
}

// dbRun runs SQL in the server's database and returns the result. A
// failing statement comes back in the result, not as an error.
func (s *Server) dbRun(ctx context.Context, id, database, sql string, maxRows int) (*gameserver.DBQueryResult, error) {
	return s.dbRunMode(ctx, id, database, sql, maxRows, false)
}

// dbRunMode runs SQL, or with dryRun tests it and keeps nothing.
func (s *Server) dbRunMode(ctx context.Context, id, database, sql string, maxRows int, dryRun bool) (*gameserver.DBQueryResult, error) {
	reply, err := s.gameDBCall(ctx, id, gameserver.DBCall{Op: "query", Database: database, SQL: sql, MaxRows: maxRows, DryRun: dryRun})
	if err != nil {
		return nil, err
	}
	if reply.Query == nil {
		return nil, fmt.Errorf("the database did not answer")
	}
	return reply.Query, nil
}

// dbRows returns the rows of the first result set of a run that must
// succeed.
func dbRows(q *gameserver.DBQueryResult) ([][]*string, error) {
	if q.Error != "" {
		return nil, fmt.Errorf("%s", q.Error)
	}
	for _, st := range q.Statements {
		if len(st.Sets) > 0 {
			return st.Sets[0].Rows, nil
		}
	}
	return [][]*string{}, nil
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func dbWriteFail(w http.ResponseWriter, err error) {
	writeErr(w, http.StatusUnprocessableEntity, gameserver.HumanError(err.Error()))
}

// loadRunningDB loads the server for a manager request and its database
// settings; the database must be installed.
func (s *Server) loadRunningDB(w http.ResponseWriter, r *http.Request) (*principal, *appdb.GameServer, *gameserver.DBConfig, bool) {
	p, row, _, ok := s.loadGameDB(w, r)
	if !ok {
		return nil, nil, nil, false
	}
	reply, err := s.gameDBCall(r.Context(), row.ID, gameserver.DBCall{Op: "get"})
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return nil, nil, nil, false
	}
	if reply.Config == nil || reply.Config.InstalledAt == nil {
		writeErr(w, http.StatusUnprocessableEntity, "install the database first")
		return nil, nil, nil, false
	}
	if reply.State != "running" {
		writeErr(w, http.StatusUnprocessableEntity, "the database is "+reply.State+"; start it first")
		return nil, nil, nil, false
	}
	return p, row, reply.Config, true
}

// pickDB is the database a request names, or the server's own. Names are
// checked against the databases that exist.
func (s *Server) pickDB(ctx context.Context, id, want string, cfg *gameserver.DBConfig) (string, error) {
	want = strings.TrimSpace(want)
	if want == "" || want == cfg.Database {
		return cfg.Database, nil
	}
	dbs, err := s.dbDatabases(ctx, id)
	if err != nil {
		return "", err
	}
	for _, d := range dbs {
		if d == want {
			return want, nil
		}
	}
	return "", fmt.Errorf("there is no database %s", want)
}

func (s *Server) dbDatabases(ctx context.Context, id string) ([]string, error) {
	q, err := s.dbRun(ctx, id, "", "SHOW DATABASES", 1000)
	if err != nil {
		return nil, err
	}
	rows, err := dbRows(q)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, r := range rows {
		if len(r) > 0 {
			out = append(out, str(r[0]))
		}
	}
	return out, nil
}

// POST /game-servers/{id}/database/query {sql, database, max_rows}
func (s *Server) queryGameDatabase(w http.ResponseWriter, r *http.Request) {
	p, row, cfg, ok := s.loadRunningDB(w, r)
	if !ok {
		return
	}
	var req struct {
		SQL      string `json:"sql"`
		Database string `json:"database"`
		MaxRows  int    `json:"max_rows"`
		DryRun   bool   `json:"dry_run"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON (1 MB at most)")
		return
	}
	db, err := s.pickDB(r.Context(), row.ID, req.Database, cfg)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	q, err := s.dbRunMode(r.Context(), row.ID, db, req.SQL, req.MaxRows, req.DryRun)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	if req.DryRun {
		// A test changes nothing; it is recorded so the activity still
		// shows what was tried.
		s.noteGame(r, p, row.ID, "database.query", "SQL tested in "+db+" (nothing kept)", clipGame(redactSQL(strings.TrimSpace(req.SQL)), 600))
		writeJSON(w, http.StatusOK, q)
		return
	}
	s.noteDBQuery(r, p, row.ID, db, req.SQL, q)
	writeJSON(w, http.StatusOK, q)
}

// noteDBQuery records a run on the server's activity and the audit log,
// with passwords taken out.
func (s *Server) noteDBQuery(r *http.Request, p *principal, serverID, db, sql string, q *gameserver.DBQueryResult) {
	result := "ok"
	if q.Error != "" {
		result = "failed"
	}
	detail := clipGame(redactSQL(strings.TrimSpace(sql)), 600)
	s.noteGame(r, p, serverID, "database.query", "SQL run in "+db+" ("+result+")", detail)
	s.audit(r, p.User.ClusterID, p.User.ID, "gameserver.database.query", result, serverID+" "+db+": "+clipGame(detail, 300))
}

type dbTable struct {
	Name       string `json:"name"`
	Engine     string `json:"engine"`
	Rows       int64  `json:"rows"`
	DataBytes  int64  `json:"data_bytes"`
	IndexBytes int64  `json:"index_bytes"`
	Comment    string `json:"comment,omitempty"`
	View       bool   `json:"view,omitempty"`
}

// GET /game-servers/{id}/database/schema?database=
func (s *Server) gameDatabaseSchema(w http.ResponseWriter, r *http.Request) {
	_, row, cfg, ok := s.loadRunningDB(w, r)
	if !ok {
		return
	}
	db, err := s.pickDB(r.Context(), row.ID, r.URL.Query().Get("database"), cfg)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	dbs, err := s.dbDatabases(r.Context(), row.ID)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	q, err := s.dbRun(r.Context(), row.ID, "", "SELECT TABLE_NAME, IFNULL(ENGINE,''), IFNULL(TABLE_ROWS,0), IFNULL(DATA_LENGTH,0), IFNULL(INDEX_LENGTH,0), TABLE_COMMENT, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = "+sqlString(db)+" ORDER BY TABLE_NAME", 5000)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	rows, err := dbRows(q)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	tables := []dbTable{}
	for _, t := range rows {
		n := func(i int) int64 { v, _ := strconv.ParseInt(str(t[i]), 10, 64); return v }
		tables = append(tables, dbTable{Name: str(t[0]), Engine: str(t[1]), Rows: n(2), DataBytes: n(3), IndexBytes: n(4), Comment: str(t[5]), View: str(t[6]) == "VIEW"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"database": db, "server_database": cfg.Database, "databases": dbs, "tables": tables})
}

type dbColumn struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Nullable bool    `json:"nullable"`
	Key      string  `json:"key,omitempty"`
	Default  *string `json:"default"`
	Extra    string  `json:"extra,omitempty"`
}

// GET /game-servers/{id}/database/table?database=&table=&offset=&limit=&order=&dir=
func (s *Server) gameDatabaseTable(w http.ResponseWriter, r *http.Request) {
	_, row, cfg, ok := s.loadRunningDB(w, r)
	if !ok {
		return
	}
	qs := r.URL.Query()
	db, err := s.pickDB(r.Context(), row.ID, qs.Get("database"), cfg)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	table := qs.Get("table")
	q, err := s.dbRun(r.Context(), row.ID, "", "SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COLUMN_DEFAULT, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = "+sqlString(db)+" AND TABLE_NAME = "+sqlString(table)+" ORDER BY ORDINAL_POSITION", 5000)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	crows, err := dbRows(q)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	if len(crows) == 0 {
		writeErr(w, http.StatusNotFound, "there is no table "+table+" in "+db)
		return
	}
	cols := []dbColumn{}
	names := map[string]bool{}
	for _, c := range crows {
		cols = append(cols, dbColumn{Name: str(c[0]), Type: str(c[1]), Nullable: str(c[2]) == "YES", Key: str(c[3]), Default: c[4], Extra: str(c[5])})
		names[str(c[0])] = true
	}
	limit, _ := strconv.Atoi(qs.Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	offset, _ := strconv.Atoi(qs.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	order := ""
	if o := qs.Get("order"); names[o] {
		dir := "ASC"
		if strings.EqualFold(qs.Get("dir"), "desc") {
			dir = "DESC"
		}
		order = " ORDER BY " + sqlIdent(o) + " " + dir
	}
	from := sqlIdent(db) + "." + sqlIdent(table)
	sql := "SELECT COUNT(*) FROM " + from + ";\nSELECT * FROM " + from + order + fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	q, err = s.dbRun(r.Context(), row.ID, "", sql, limit)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	if q.Error != "" {
		dbWriteFail(w, fmt.Errorf("%s", q.Error))
		return
	}
	total := int64(0)
	rows := [][]*string{}
	if len(q.Statements) == 2 {
		if sets := q.Statements[0].Sets; len(sets) > 0 && len(sets[0].Rows) > 0 {
			total, _ = strconv.ParseInt(str(sets[0].Rows[0][0]), 10, 64)
		}
		if sets := q.Statements[1].Sets; len(sets) > 0 {
			rows = sets[0].Rows
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"database": db, "table": table, "columns": cols, "rows": rows, "total": total, "offset": offset, "limit": limit})
}

type dbUser struct {
	User      string   `json:"user"`
	Host      string   `json:"host"`
	Grants    []string `json:"grants"`
	Protected bool     `json:"protected,omitempty"`
	// Server marks the user NDL made for the game.
	Server bool `json:"server,omitempty"`
	// Privileges are the privileges on each database, for the editor.
	Privileges map[string][]string `json:"privileges"`
}

var grantOnDB = regexp.MustCompile("^GRANT (.+) ON `((?:[^`]|``)+)`\\.\\* TO ")

// GET /game-servers/{id}/database/users
func (s *Server) gameDatabaseUsers(w http.ResponseWriter, r *http.Request) {
	_, row, cfg, ok := s.loadRunningDB(w, r)
	if !ok {
		return
	}
	users, err := s.dbListUsers(r.Context(), row.ID, cfg)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	dbs, _ := s.dbDatabases(r.Context(), row.ID)
	writeJSON(w, http.StatusOK, map[string]any{"users": users, "privileges": dbPrivileges, "databases": dbs, "server_database": cfg.Database})
}

func (s *Server) dbListUsers(ctx context.Context, id string, cfg *gameserver.DBConfig) ([]dbUser, error) {
	q, err := s.dbRun(ctx, id, "", "SELECT User, Host FROM mysql.user ORDER BY User, Host", 5000)
	if err != nil {
		return nil, err
	}
	rows, err := dbRows(q)
	if err != nil {
		return nil, err
	}
	users := []dbUser{}
	var shows []string
	for _, u := range rows {
		user, host := str(u[0]), str(u[1])
		users = append(users, dbUser{User: user, Host: host, Grants: []string{}, Privileges: map[string][]string{},
			Protected: dbProtectedUsers[user], Server: user == cfg.User})
		shows = append(shows, "SHOW GRANTS FOR "+sqlAccount(user, host))
	}
	if len(shows) == 0 {
		return users, nil
	}
	q, err = s.dbRun(ctx, id, "", strings.Join(shows, ";\n"), 500)
	if err != nil {
		return nil, err
	}
	for i, st := range q.Statements {
		if i >= len(users) || len(st.Sets) == 0 {
			continue
		}
		for _, g := range st.Sets[0].Rows {
			line := str(g[0])
			users[i].Grants = append(users[i].Grants, redactSQL(line))
			if m := grantOnDB.FindStringSubmatch(line); m != nil {
				db := strings.ReplaceAll(m[2], "``", "`")
				privs := []string{}
				for _, p := range strings.Split(m[1], ",") {
					p = strings.TrimSpace(p)
					if p == "ALL PRIVILEGES" {
						privs = append([]string{}, dbPrivileges...)
						break
					}
					privs = append(privs, p)
				}
				users[i].Privileges[db] = privs
			}
		}
	}
	return users, nil
}

func checkAccount(user, host string) error {
	if !dbUserRe.MatchString(user) {
		return fmt.Errorf("a username may use letters, digits, _ . and - only, up to 80 characters")
	}
	if !dbHostRe.MatchString(host) {
		return fmt.Errorf("the host may be %% (anywhere), an address or a name")
	}
	return nil
}

func cleanPrivileges(in []string) ([]string, error) {
	allowed := map[string]bool{}
	for _, p := range dbPrivileges {
		allowed[p] = true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, p := range in {
		p = strings.ToUpper(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		if !allowed[p] {
			return nil, fmt.Errorf("%s is not a privilege that can be given here", p)
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func newDBPassword() (string, error) {
	return gameserver.RandomPassword(28)
}

// POST /game-servers/{id}/database/users {user, host, password, database, privileges}
func (s *Server) createGameDatabaseUser(w http.ResponseWriter, r *http.Request) {
	p, row, cfg, ok := s.loadRunningDB(w, r)
	if !ok {
		return
	}
	var req struct {
		User       string   `json:"user"`
		Host       string   `json:"host"`
		Password   string   `json:"password"`
		Database   string   `json:"database"`
		Privileges []string `json:"privileges"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Host = gameserver.FirstNonEmpty(strings.TrimSpace(req.Host), "%")
	if err := checkAccount(req.User, req.Host); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if dbProtectedUsers[req.User] {
		writeErr(w, http.StatusBadRequest, req.User+" is one of MariaDB's own accounts")
		return
	}
	privs, err := cleanPrivileges(req.Privileges)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	db, err := s.pickDB(r.Context(), row.ID, req.Database, cfg)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	pw := req.Password
	if pw == "" {
		if pw, err = newDBPassword(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	sql := "CREATE USER " + sqlAccount(req.User, req.Host) + " IDENTIFIED BY " + sqlString(pw)
	if len(privs) > 0 {
		sql += ";\nGRANT " + strings.Join(privs, ", ") + " ON " + sqlIdent(db) + ".* TO " + sqlAccount(req.User, req.Host)
	}
	q, err := s.dbRun(r.Context(), row.ID, "", sql, 10)
	if err == nil && q.Error != "" {
		err = fmt.Errorf("%s", q.Error)
	}
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	s.noteGame(r, p, row.ID, "database.user", "Database user "+req.User+"@"+req.Host+" created", strings.Join(privs, ", ")+" on "+db)
	s.audit(r, p.User.ClusterID, p.User.ID, "gameserver.database.user.create", "ok", row.ID+" "+req.User+"@"+req.Host)
	// The password is shown once, here.
	writeJSON(w, http.StatusOK, map[string]any{"user": req.User, "host": req.Host, "password": pw})
}

func (s *Server) dbAccountRequest(w http.ResponseWriter, r *http.Request, cfg *gameserver.DBConfig, allowServer bool) (user, host, password string, ok bool) {
	var req struct {
		User     string `json:"user"`
		Host     string `json:"host"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return "", "", "", false
	}
	if err := checkAccount(req.User, req.Host); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return "", "", "", false
	}
	if dbProtectedUsers[req.User] {
		writeErr(w, http.StatusBadRequest, req.User+" is one of MariaDB's own accounts and is left alone")
		return "", "", "", false
	}
	if req.User == cfg.User && !allowServer {
		writeErr(w, http.StatusBadRequest, req.User+" is the server's own user, which NDL keeps the password for; it cannot be changed or removed here")
		return "", "", "", false
	}
	return req.User, req.Host, req.Password, true
}

// POST /game-servers/{id}/database/users/drop {user, host}
func (s *Server) dropGameDatabaseUser(w http.ResponseWriter, r *http.Request) {
	p, row, cfg, ok := s.loadRunningDB(w, r)
	if !ok {
		return
	}
	user, host, _, ok := s.dbAccountRequest(w, r, cfg, false)
	if !ok {
		return
	}
	q, err := s.dbRun(r.Context(), row.ID, "", "DROP USER "+sqlAccount(user, host), 10)
	if err == nil && q.Error != "" {
		err = fmt.Errorf("%s", q.Error)
	}
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	s.noteGame(r, p, row.ID, "database.user", "Database user "+user+"@"+host+" removed", "")
	s.audit(r, p.User.ClusterID, p.User.ID, "gameserver.database.user.drop", "ok", row.ID+" "+user+"@"+host)
	writeJSON(w, http.StatusOK, map[string]any{"dropped": true})
}

// POST /game-servers/{id}/database/users/password {user, host, password}
func (s *Server) passwordGameDatabaseUser(w http.ResponseWriter, r *http.Request) {
	p, row, cfg, ok := s.loadRunningDB(w, r)
	if !ok {
		return
	}
	user, host, pw, ok := s.dbAccountRequest(w, r, cfg, false)
	if !ok {
		return
	}
	var err error
	if pw == "" {
		if pw, err = newDBPassword(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	q, err := s.dbRun(r.Context(), row.ID, "", "ALTER USER "+sqlAccount(user, host)+" IDENTIFIED BY "+sqlString(pw), 10)
	if err == nil && q.Error != "" {
		err = fmt.Errorf("%s", q.Error)
	}
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	s.noteGame(r, p, row.ID, "database.user", "Password changed for "+user+"@"+host, "")
	s.audit(r, p.User.ClusterID, p.User.ID, "gameserver.database.user.password", "ok", row.ID+" "+user+"@"+host)
	writeJSON(w, http.StatusOK, map[string]any{"user": user, "host": host, "password": pw})
}

// PUT /game-servers/{id}/database/grants {user, host, database, privileges}
// sets exactly these privileges for the user on the database.
func (s *Server) putGameDatabaseGrants(w http.ResponseWriter, r *http.Request) {
	p, row, cfg, ok := s.loadRunningDB(w, r)
	if !ok {
		return
	}
	var req struct {
		User       string   `json:"user"`
		Host       string   `json:"host"`
		Database   string   `json:"database"`
		Privileges []string `json:"privileges"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := checkAccount(req.User, req.Host); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if dbProtectedUsers[req.User] {
		writeErr(w, http.StatusBadRequest, req.User+" is one of MariaDB's own accounts and is left alone")
		return
	}
	privs, err := cleanPrivileges(req.Privileges)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	db, err := s.pickDB(r.Context(), row.ID, req.Database, cfg)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	acct := sqlAccount(req.User, req.Host)
	// Revoking fails when there is nothing to revoke (error 1141); that
	// is fine. It runs on its own so a failure there stops nothing.
	if _, err := s.dbRun(r.Context(), row.ID, "", "REVOKE ALL PRIVILEGES ON "+sqlIdent(db)+".* FROM "+acct, 10); err != nil {
		dbWriteFail(w, err)
		return
	}
	if len(privs) > 0 {
		q, err := s.dbRun(r.Context(), row.ID, "", "GRANT "+strings.Join(privs, ", ")+" ON "+sqlIdent(db)+".* TO "+acct, 10)
		if err == nil && q.Error != "" {
			err = fmt.Errorf("%s", q.Error)
		}
		if err != nil {
			dbWriteFail(w, err)
			return
		}
	}
	summary := "none"
	if len(privs) > 0 {
		summary = strings.Join(privs, ", ")
	}
	s.noteGame(r, p, row.ID, "database.grants", "Privileges for "+req.User+"@"+req.Host+" on "+db+" set", summary)
	s.audit(r, p.User.ClusterID, p.User.ID, "gameserver.database.grants", "ok", row.ID+" "+req.User+"@"+req.Host+" "+db+": "+summary)
	writeJSON(w, http.StatusOK, map[string]any{"user": req.User, "host": req.Host, "database": db, "privileges": privs})
}

type dbIndex struct {
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	Unique    bool     `json:"unique"`
	Type      string   `json:"type"`
	Primary   bool     `json:"primary,omitempty"`
	Comment   string   `json:"comment,omitempty"`
	Cardinal  int64    `json:"cardinality"`
	Nullable  bool     `json:"nullable,omitempty"`
	Invisible bool     `json:"invisible,omitempty"`
}

var dbIndexNameRe = regexp.MustCompile(`^[A-Za-z0-9_$]{1,64}$`)

// dbTableColumns returns a table's columns, which also proves it exists.
func (s *Server) dbTableColumns(ctx context.Context, id, db, table string) ([]string, error) {
	q, err := s.dbRun(ctx, id, "", "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = "+sqlString(db)+" AND TABLE_NAME = "+sqlString(table)+" ORDER BY ORDINAL_POSITION", 5000)
	if err != nil {
		return nil, err
	}
	rows, err := dbRows(q)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("there is no table %s in %s", table, db)
	}
	cols := make([]string, 0, len(rows))
	for _, r := range rows {
		cols = append(cols, str(r[0]))
	}
	return cols, nil
}

// GET /game-servers/{id}/database/indexes?database=&table=
func (s *Server) gameDatabaseIndexes(w http.ResponseWriter, r *http.Request) {
	_, row, cfg, ok := s.loadRunningDB(w, r)
	if !ok {
		return
	}
	db, err := s.pickDB(r.Context(), row.ID, r.URL.Query().Get("database"), cfg)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	table := r.URL.Query().Get("table")
	cols, err := s.dbTableColumns(r.Context(), row.ID, db, table)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	q, err := s.dbRun(r.Context(), row.ID, "", "SELECT INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME, INDEX_TYPE, IFNULL(CARDINALITY,0), NULLABLE, INDEX_COMMENT, IFNULL(SUB_PART,'') FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = "+sqlString(db)+" AND TABLE_NAME = "+sqlString(table)+" ORDER BY INDEX_NAME = 'PRIMARY' DESC, INDEX_NAME, SEQ_IN_INDEX", 5000)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	rows, err := dbRows(q)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	idx := []*dbIndex{}
	byName := map[string]*dbIndex{}
	for _, x := range rows {
		name := str(x[0])
		ix := byName[name]
		if ix == nil {
			ix = &dbIndex{Name: name, Columns: []string{}, Unique: str(x[1]) == "0", Type: str(x[4]), Primary: name == "PRIMARY", Comment: str(x[7])}
			byName[name] = ix
			idx = append(idx, ix)
		}
		col := str(x[3])
		if sub := str(x[8]); sub != "" {
			col += "(" + sub + ")"
		}
		ix.Columns = append(ix.Columns, col)
		if c, _ := strconv.ParseInt(str(x[5]), 10, 64); c > ix.Cardinal {
			ix.Cardinal = c
		}
		if str(x[6]) == "YES" {
			ix.Nullable = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"database": db, "table": table, "columns": cols, "indexes": idx})
}

// POST /game-servers/{id}/database/indexes {database, table, name,
// columns, kind: index|unique|fulltext}
func (s *Server) createGameDatabaseIndex(w http.ResponseWriter, r *http.Request) {
	p, row, cfg, ok := s.loadRunningDB(w, r)
	if !ok {
		return
	}
	var req struct {
		Database string   `json:"database"`
		Table    string   `json:"table"`
		Name     string   `json:"name"`
		Columns  []string `json:"columns"`
		Kind     string   `json:"kind"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	kind := map[string]string{"": "INDEX", "index": "INDEX", "unique": "UNIQUE INDEX", "fulltext": "FULLTEXT INDEX"}[strings.ToLower(req.Kind)]
	if kind == "" {
		writeErr(w, http.StatusBadRequest, "kind is index, unique or fulltext")
		return
	}
	if !dbIndexNameRe.MatchString(req.Name) || strings.EqualFold(req.Name, "PRIMARY") {
		writeErr(w, http.StatusBadRequest, "an index name may use letters, digits, _ and $ only, up to 64 characters, and cannot be PRIMARY")
		return
	}
	if len(req.Columns) == 0 || len(req.Columns) > 16 {
		writeErr(w, http.StatusBadRequest, "pick 1 to 16 columns")
		return
	}
	db, err := s.pickDB(r.Context(), row.ID, req.Database, cfg)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	cols, err := s.dbTableColumns(r.Context(), row.ID, db, req.Table)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	known := map[string]bool{}
	for _, c := range cols {
		known[c] = true
	}
	parts := []string{}
	seen := map[string]bool{}
	for _, c := range req.Columns {
		if !known[c] {
			writeErr(w, http.StatusBadRequest, "there is no column "+c+" in "+req.Table)
			return
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		parts = append(parts, sqlIdent(c))
	}
	sql := "ALTER TABLE " + sqlIdent(db) + "." + sqlIdent(req.Table) + " ADD " + kind + " " + sqlIdent(req.Name) + " (" + strings.Join(parts, ", ") + ")"
	q, err := s.dbRun(r.Context(), row.ID, "", sql, 10)
	if err == nil && q.Error != "" {
		err = fmt.Errorf("%s", q.Error)
	}
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	s.noteGame(r, p, row.ID, "database.index", "Index "+req.Name+" added to "+req.Table, sql)
	s.audit(r, p.User.ClusterID, p.User.ID, "gameserver.database.index.create", "ok", row.ID+" "+db+"."+req.Table+" "+req.Name)
	writeJSON(w, http.StatusOK, map[string]any{"created": true, "sql": sql})
}

// POST /game-servers/{id}/database/indexes/drop {database, table, name}
func (s *Server) dropGameDatabaseIndex(w http.ResponseWriter, r *http.Request) {
	p, row, cfg, ok := s.loadRunningDB(w, r)
	if !ok {
		return
	}
	var req struct {
		Database string `json:"database"`
		Table    string `json:"table"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if !dbIndexNameRe.MatchString(req.Name) {
		writeErr(w, http.StatusBadRequest, "that is not an index name")
		return
	}
	db, err := s.pickDB(r.Context(), row.ID, req.Database, cfg)
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	if _, err := s.dbTableColumns(r.Context(), row.ID, db, req.Table); err != nil {
		dbWriteFail(w, err)
		return
	}
	drop := "DROP INDEX " + sqlIdent(req.Name)
	if strings.EqualFold(req.Name, "PRIMARY") {
		drop = "DROP PRIMARY KEY"
	}
	sql := "ALTER TABLE " + sqlIdent(db) + "." + sqlIdent(req.Table) + " " + drop
	q, err := s.dbRun(r.Context(), row.ID, "", sql, 10)
	if err == nil && q.Error != "" {
		err = fmt.Errorf("%s", q.Error)
	}
	if err != nil {
		dbWriteFail(w, err)
		return
	}
	s.noteGame(r, p, row.ID, "database.index", "Index "+req.Name+" removed from "+req.Table, sql)
	s.audit(r, p.User.ClusterID, p.User.ID, "gameserver.database.index.drop", "ok", row.ID+" "+db+"."+req.Table+" "+req.Name)
	writeJSON(w, http.StatusOK, map[string]any{"dropped": true})
}
