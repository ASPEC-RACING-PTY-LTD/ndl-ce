package gameserver

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SQL for the database manager runs through the mariadb client inside the
// database's own container, as root, so nothing needs a network path to
// it and no driver is needed here. The client writes each result set as
// XML, which keeps NULL apart from the text 'NULL' and escapes every
// value. After each statement a marker query reports ROW_COUNT(), which
// is how a result, a count or an error is tied to its statement.

const (
	queryMarker      = "__ndl_affected"
	queryMarkerStmt  = "__ndl_stmt"
	defaultQueryRows = 500
	maxQueryRows     = 5000
	maxQueryOutput   = 64 << 20
	queryTimeout     = 2 * time.Minute
	// queryStatementSeconds stops a runaway SELECT on the server side too.
	queryStatementSeconds = 60
)

// DBResultSet is one result set a statement returned.
type DBResultSet struct {
	Columns []string    `json:"columns"`
	Rows    [][]*string `json:"rows"`
	// RowCount is every row the statement returned; Rows holds at most
	// the requested number, and Truncated says when there were more.
	RowCount  int  `json:"row_count"`
	Truncated bool `json:"truncated,omitempty"`
}

// DBStatementResult is one statement's outcome.
type DBStatementResult struct {
	SQL  string        `json:"sql"`
	Sets []DBResultSet `json:"sets"`
	// Affected is ROW_COUNT(): rows changed by INSERT, UPDATE, DELETE and
	// the like, -1 for a statement that returned rows.
	Affected int64 `json:"affected"`
	// Checked, in a dry run, says how the statement was tested:
	// "rolled_back" (it ran, then everything was undone) or "syntax" (it
	// was only checked, because MariaDB could not undo it).
	Checked string `json:"checked,omitempty"`
}

// DBQueryResult is a run of one or more statements. A failing statement
// stops the run: Error says why and ErrorIndex which statement.
type DBQueryResult struct {
	Statements []DBStatementResult `json:"statements"`
	Error      string              `json:"error,omitempty"`
	ErrorIndex int                 `json:"error_index"`
	// DryRun: nothing was kept (see DBStatementResult.Checked).
	DryRun    bool  `json:"dry_run,omitempty"`
	ElapsedMS int64 `json:"elapsed_ms"`
}

// SplitSQL splits a script into statements at semicolons outside quotes,
// backticks and comments. Comments are kept with their statement; empty
// statements are dropped. DELIMITER is not supported.
func SplitSQL(script string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" && !onlyComments(s) {
			out = append(out, s)
		}
		cur.Reset()
	}
	rs := []rune(script)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			cur.WriteRune(c)
			for i++; i < len(rs); i++ {
				cur.WriteRune(rs[i])
				if rs[i] == '\\' && c != '`' && i+1 < len(rs) {
					i++
					cur.WriteRune(rs[i])
					continue
				}
				if rs[i] == c {
					// A doubled quote is an escaped quote.
					if i+1 < len(rs) && rs[i+1] == c {
						i++
						cur.WriteRune(rs[i])
						continue
					}
					break
				}
			}
		case c == '-' && i+2 < len(rs) && rs[i+1] == '-' && (rs[i+2] == ' ' || rs[i+2] == '\t' || rs[i+2] == '\n'),
			c == '#':
			for ; i < len(rs) && rs[i] != '\n'; i++ {
				cur.WriteRune(rs[i])
			}
			if i < len(rs) {
				cur.WriteRune('\n')
			}
		case c == '/' && i+1 < len(rs) && rs[i+1] == '*':
			cur.WriteString("/*")
			for i += 2; i < len(rs); i++ {
				cur.WriteRune(rs[i])
				if rs[i] == '/' && rs[i-1] == '*' {
					break
				}
			}
		case c == ';':
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}

func onlyComments(s string) bool {
	s = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(s, "")
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") && !strings.HasPrefix(line, "#") {
			return false
		}
	}
	return true
}

var dbErrorLine = regexp.MustCompile(`(?m)^ERROR (\d+) \(([0-9A-Z]+)\)(?: at line \d+)?: (.*)$`)

// dbQuery runs statements in the server's database as root.
func (r *Runtime) dbQuery(ctx context.Context, id string, call DBCall) (*DBQueryResult, error) {
	c, err := r.DBConfigFor(id)
	if err != nil {
		return nil, err
	}
	if c == nil || c.InstalledAt == nil {
		return nil, errors.New("install the database first")
	}
	if st := r.dbState(ctx, id, c); st != "running" {
		return nil, fmt.Errorf("the database is %s; start it first", st)
	}
	db := c.Database
	if strings.TrimSpace(call.Database) != "" {
		if db, err = CleanDBName(call.Database); err != nil {
			if !isSystemDB(call.Database) {
				return nil, err
			}
			db = call.Database
		}
	}
	stmts := SplitSQL(call.SQL)
	if len(stmts) == 0 {
		return nil, errors.New("there is no SQL to run")
	}
	maxRows := call.MaxRows
	if maxRows <= 0 {
		maxRows = defaultQueryRows
	}
	if maxRows > maxQueryRows {
		maxRows = maxQueryRows
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	var modes []string
	if call.DryRun {
		modes = r.dryRunModes(ctx, id, db, stmts)
	}
	var script strings.Builder
	fmt.Fprintf(&script, "SET SESSION max_statement_time=%d;\n", queryStatementSeconds)
	if call.DryRun {
		script.WriteString("START TRANSACTION;\n")
	}
	for i, s := range stmts {
		if call.DryRun && modes[i] == "syntax" {
			// PREPARE checks a statement without running it.
			fmt.Fprintf(&script, "PREPARE __ndl_dry FROM %s;\nDEALLOCATE PREPARE __ndl_dry", sqlQuote(s))
		} else {
			script.WriteString(s)
		}
		fmt.Fprintf(&script, ";\nSELECT ROW_COUNT() AS %s, %d AS %s;\n", queryMarker, i, queryMarkerStmt)
	}
	if call.DryRun {
		// Also undone if the run stops early: the connection closes with
		// the transaction open.
		script.WriteString("ROLLBACK;\n")
	}
	start := time.Now()
	stdout, stderr, runErr := r.runQuery(ctx, id, db, script.String())
	res := &DBQueryResult{Statements: []DBStatementResult{}, ErrorIndex: -1}
	parseErr := parseQueryXML(stdout, stmts, maxRows, res)
	if call.DryRun {
		for i := range res.Statements {
			if i < len(modes) {
				res.Statements[i].Checked = modes[i]
				if modes[i] == "syntax" {
					res.Statements[i].Affected = 0
				}
			}
		}
		res.DryRun = true
	}
	res.ElapsedMS = time.Since(start).Milliseconds()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.Error = fmt.Sprintf("stopped after %s", queryTimeout)
	case len(stdout) >= maxQueryOutput:
		res.Error = "the result is too large to show; add a LIMIT"
	case runErr != nil:
		res.Error = strings.TrimSpace(stderr)
		if m := dbErrorLine.FindStringSubmatch(stderr); m != nil {
			res.Error = fmt.Sprintf("%s (error %s)", m[3], m[1])
		}
		if res.Error == "" {
			res.Error = runErr.Error()
		}
	case parseErr != nil:
		res.Error = "could not read the result: " + parseErr.Error()
	}
	if res.Error != "" {
		res.ErrorIndex = len(res.Statements)
		if res.ErrorIndex >= len(stmts) {
			res.ErrorIndex = len(stmts) - 1
		}
	}
	return res, nil
}

func isSystemDB(name string) bool {
	switch strings.ToLower(name) {
	case "mysql", "information_schema", "performance_schema", "sys":
		return true
	}
	return false
}

// runQuery feeds the script to the client and returns its output, at
// most maxQueryOutput bytes of it.
func (r *Runtime) runQuery(ctx context.Context, id, db, script string) ([]byte, string, error) {
	args := []string{"exec", "-i", "-e", "NDL_DB=" + db, dbContainer(id), "sh", "-c",
		`MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb -uroot --xml --default-character-set=utf8mb4 "$NDL_DB"`}
	if r.Run != nil {
		out, err := r.Run(ctx, r.dockerBin(), append(args, "<<", script)...)
		if err != nil {
			return out, string(out), err
		}
		return out, "", nil
	}
	cmd := exec.CommandContext(ctx, r.dockerBin(), args...)
	if host := strings.TrimSpace(r.DockerHost); host != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+host)
	}
	var stdout limitedBuffer
	stdout.max = maxQueryOutput
	var stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stdout.full {
		return stdout.Bytes(), stderr.String(), nil
	}
	return stdout.Bytes(), stderr.String(), err
}

// limitedBuffer keeps the first max bytes and drops the rest.
type limitedBuffer struct {
	bytes.Buffer
	max  int
	full bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room < len(p) {
		b.full = true
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// parseQueryXML turns the client's XML into results. Result sets before a
// marker belong to the statement the marker names.
func parseQueryXML(raw []byte, stmts []string, maxRows int, res *DBQueryResult) error {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = false
	var pending []DBResultSet
	var set *DBResultSet
	var row []*string
	var cols []string
	var field *string
	var inField, isNil bool
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// A cut-off result (too large, or stopped) ends mid-document.
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "resultset":
				set = &DBResultSet{Columns: []string{}, Rows: [][]*string{}}
				cols = nil
			case "row":
				row = []*string{}
			case "field":
				inField, isNil = true, false
				text.Reset()
				name := ""
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "name":
						name = a.Value
					case "nil":
						isNil = a.Value == "true"
					}
				}
				if set != nil && set.RowCount == 0 {
					cols = append(cols, name)
				}
			}
		case xml.CharData:
			if inField {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "field":
				inField = false
				if isNil {
					field = nil
				} else {
					v := text.String()
					field = &v
				}
				row = append(row, field)
			case "row":
				if set == nil {
					continue
				}
				if set.RowCount == 0 {
					set.Columns = cols
				}
				set.RowCount++
				if len(set.Rows) < maxRows {
					set.Rows = append(set.Rows, row)
				} else {
					set.Truncated = true
				}
			case "resultset":
				if set == nil {
					continue
				}
				if len(set.Columns) >= 2 && set.Columns[0] == queryMarker && set.Columns[1] == queryMarkerStmt && len(set.Rows) == 1 {
					idx, _ := strconv.Atoi(deref(set.Rows[0][1]))
					affected, _ := strconv.ParseInt(deref(set.Rows[0][0]), 10, 64)
					sql := ""
					if idx >= 0 && idx < len(stmts) {
						sql = stmts[idx]
					}
					if pending == nil {
						pending = []DBResultSet{}
					}
					res.Statements = append(res.Statements, DBStatementResult{SQL: sql, Sets: pending, Affected: affected})
					pending = nil
				} else {
					pending = append(pending, *set)
				}
				set = nil
			}
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// dryRunData are statements a dry run may really run, inside a
// transaction it rolls back. Anything else (CREATE, ALTER, DROP, GRANT
// and the like commit at once in MariaDB) is only checked.
var dryRunData = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true,
	"WITH": true, "SHOW": true, "DESCRIBE": true, "DESC": true, "EXPLAIN": true, "VALUES": true, "TABLE": true,
}

var leadingNoise = regexp.MustCompile(`^(?:\s+|--[^\n]*\n?|#[^\n]*\n?|/\*.*?\*/|\()+`)

// firstKeyword is a statement's first word, upper-cased, past comments.
func firstKeyword(stmt string) string {
	rest := leadingNoise.ReplaceAllString(stmt, "")
	end := strings.IndexFunc(rest, func(r rune) bool { return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '_') })
	if end < 0 {
		end = len(rest)
	}
	return strings.ToUpper(rest[:end])
}

// dryRunModes says how a dry run tests each statement. Data statements
// run and are rolled back, unless a table that cannot roll back (MyISAM,
// Aria and the like) exists: then they are only checked too.
func (r *Runtime) dryRunModes(ctx context.Context, id, db string, stmts []string) []string {
	canRun := true
	out, _, err := r.runQuery(ctx, id, db, "SELECT COUNT(*) AS n FROM information_schema.TABLES WHERE TABLE_TYPE = 'BASE TABLE' AND ENGINE <> 'InnoDB' AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys');\nSELECT ROW_COUNT() AS "+queryMarker+", 0 AS "+queryMarkerStmt+";\n")
	check := &DBQueryResult{}
	if err != nil || parseQueryXML(out, []string{""}, 1, check) != nil || len(check.Statements) == 0 || len(check.Statements[0].Sets) == 0 ||
		len(check.Statements[0].Sets[0].Rows) == 0 || deref(check.Statements[0].Sets[0].Rows[0][0]) != "0" {
		canRun = false
	}
	modes := make([]string, len(stmts))
	for i, s := range stmts {
		if canRun && dryRunData[firstKeyword(s)] {
			modes[i] = "rolled_back"
		} else {
			modes[i] = "syntax"
		}
	}
	return modes
}

// sqlQuote is a SQL string literal.
func sqlQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\x00", `\0`, "\n", `\n`, "\r", `\r`, "\x1a", `\Z`).Replace(s) + "'"
}
