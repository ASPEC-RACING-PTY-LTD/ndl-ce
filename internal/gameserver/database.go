package gameserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A game server can have its own MariaDB. It runs as a container of its
// own beside the game, on a private network only the two share: the game
// reaches it as host "mariadb", port 3306, and nothing else can reach it
// (it publishes no port). Its data and credentials live under
// <root>/game-db/<server>, outside the server folder, so the game cannot
// read or change them. Backups include a dump of the database, and a
// restore loads it back.

const (
	// DBImage is the MariaDB release new databases run (a long-term
	// support series).
	DBImage    = "mariadb:11.4"
	DBHost     = "mariadb"
	DBPort     = 3306
	dbMemoryMB = 1024
	// dbDumpEntry is where a backup keeps the database dump.
	dbDumpEntry = ".ndl-db/dump.sql"
	// dbSocketDir holds MariaDB's Unix socket, in the database's container
	// and, read-only, in the game's: the game may connect through the
	// socket instead of TCP.
	dbSocketDir = "/run/mysqld"
	// DBSocketPath is the socket the game connects to.
	DBSocketPath = dbSocketDir + "/mysqld.sock"
	dbSocketPath = DBSocketPath
)

// DBConfig is a server's database and the credentials NDL generated.
type DBConfig struct {
	Engine       string `json:"engine"`
	Image        string `json:"image"`
	Database     string `json:"database"`
	User         string `json:"user"`
	Password     string `json:"password"`
	RootPassword string `json:"root_password"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	MemoryMB     int    `json:"memory_mb"`
	// CPUs caps the cores it may use; 0 is no cap.
	CPUs float64 `json:"cpus,omitempty"`
	// BufferPoolMB is MariaDB's InnoDB cache; 0 sizes it from MemoryMB.
	BufferPoolMB int `json:"buffer_pool_mb,omitempty"`
	// FastCommit flushes InnoDB's log once a second instead of at every
	// commit (innodb_flush_log_at_trx_commit = 2): much faster writes, but
	// a crash of the whole host can lose the last second of commits. A
	// crash of MariaDB alone loses nothing.
	FastCommit  bool       `json:"fast_commit,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
}

// DBCall is one database operation: get, configure, install, start,
// stop or remove.
type DBCall struct {
	Op       string `json:"op"`
	Database string `json:"database,omitempty"`
	// DeleteData, on remove, also deletes the data and the credentials.
	// Without it the database can be installed again as it was.
	DeleteData bool `json:"delete_data,omitempty"`
	// SQL and MaxRows are for query; Database there picks the database to
	// run in (empty: the server's own).
	SQL     string `json:"sql,omitempty"`
	MaxRows int    `json:"max_rows,omitempty"`
	// Resources, for resources, changes the limits; absent, they are read.
	Resources *DBResources `json:"resources,omitempty"`
}

// DBReply is the database's settings and state.
type DBReply struct {
	Config *DBConfig `json:"config,omitempty"`
	// State is none, configured, running, stopped or missing (installed,
	// but its container is gone).
	State string `json:"state"`
	// Query is a query's result.
	Query *DBQueryResult `json:"query,omitempty"`
	// Usage is what the database uses now, for resources.
	Usage *DBUsage `json:"usage,omitempty"`
}

var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// CleanDBName checks a database name.
func CleanDBName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !dbNameRe.MatchString(name) {
		return "", errors.New("the database name may use letters, digits and _ only, up to 64 characters")
	}
	switch strings.ToLower(name) {
	case "mysql", "information_schema", "performance_schema", "sys":
		return "", fmt.Errorf("%s is a MariaDB system database; pick another name", name)
	}
	return name, nil
}

func dbContainer(id string) string { return "ndl-gsdb-" + id }
func dbNetwork(id string) string   { return "ndl-gsdb-" + id }

func (r *Runtime) dbDir(id string) string { return filepath.Join(r.Root, "game-db", id) }

func (r *Runtime) dbConfigPath(id string) string {
	return filepath.Join(r.dbDir(id), "config.json")
}

// DBConfigFor returns the server's database settings, or nil.
func (r *Runtime) DBConfigFor(id string) (*DBConfig, error) {
	if !ValidHostID(id) {
		return nil, fmt.Errorf("game server id is invalid")
	}
	raw, err := os.ReadFile(r.dbConfigPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c DBConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("database settings are damaged: %w", err)
	}
	return &c, nil
}

func (r *Runtime) saveDBConfig(id string, c DBConfig) error {
	if err := os.MkdirAll(r.dbDir(id), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(c, "", "  ")
	tmp := r.dbConfigPath(id) + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.dbConfigPath(id))
}

func randomString(n int, alphabet string) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out), nil
}

const (
	lowerDigits  = "abcdefghijklmnopqrstuvwxyz0123456789"
	passwordAlph = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
)

// dbState reads the container's state for an installed database.
func (r *Runtime) dbState(ctx context.Context, id string, c *DBConfig) string {
	if c == nil {
		return "none"
	}
	if c.InstalledAt == nil {
		return "configured"
	}
	out, err := r.run(ctx, r.dockerBin(), "inspect", "-f", "{{.State.Running}}", dbContainer(id))
	if err != nil {
		return "missing"
	}
	if strings.TrimSpace(string(out)) == "true" {
		return "running"
	}
	return "stopped"
}

// Database runs one database operation.
func (r *Runtime) Database(ctx context.Context, id string, call DBCall) (DBReply, error) {
	if !ValidHostID(id) {
		return DBReply{}, fmt.Errorf("game server id is invalid")
	}
	c, err := r.DBConfigFor(id)
	if err != nil {
		return DBReply{}, err
	}
	switch call.Op {
	case "get", "":
	case "configure":
		c, err = r.dbConfigure(id, c, call.Database)
	case "install":
		c, err = r.dbInstall(ctx, id, c, true)
	case "restart":
		// Recreates the container from the settings, keeping the data:
		// limits and the cache size all take effect.
		if c == nil || c.InstalledAt == nil {
			return DBReply{}, errors.New("install the database first")
		}
		c, err = r.dbInstall(ctx, id, c, false)
	case "resources":
		return r.dbResources(ctx, id, c, call)
	case "start":
		if c == nil || c.InstalledAt == nil {
			return DBReply{}, errors.New("install the database first")
		}
		err = r.dbStart(ctx, id)
	case "stop":
		if c == nil || c.InstalledAt == nil {
			return DBReply{}, errors.New("the database is not installed")
		}
		_, err = r.dockerOut(ctx, "stop", "-t", "30", dbContainer(id))
	case "remove":
		c, err = r.dbRemove(ctx, id, c, call.DeleteData)
	case "query":
		q, qerr := r.dbQuery(ctx, id, call)
		if qerr != nil {
			return DBReply{}, qerr
		}
		return DBReply{Config: c, State: "running", Query: q}, nil
	default:
		return DBReply{}, fmt.Errorf("unknown database operation %q", call.Op)
	}
	if err != nil {
		return DBReply{}, err
	}
	return DBReply{Config: c, State: r.dbState(ctx, id, c)}, nil
}

func (r *Runtime) dbConfigure(id string, c *DBConfig, name string) (*DBConfig, error) {
	name, err := CleanDBName(name)
	if err != nil {
		return nil, err
	}
	if c != nil && c.InstalledAt != nil && c.Database != name {
		return nil, errors.New("the database name cannot change once it is installed")
	}
	if c == nil {
		c = &DBConfig{}
	}
	c.Engine, c.Host, c.Port = "mariadb", DBHost, DBPort
	if c.Image == "" {
		c.Image = DBImage
	}
	if c.MemoryMB == 0 {
		c.MemoryMB = dbMemoryMB
	}
	c.Database = name
	if c.User == "" {
		suffix, err := randomString(6, lowerDigits)
		if err != nil {
			return nil, err
		}
		c.User = "game_" + suffix
	}
	if c.Password == "" {
		if c.Password, err = randomString(32, passwordAlph); err != nil {
			return nil, err
		}
	}
	if c.RootPassword == "" {
		if c.RootPassword, err = randomString(40, passwordAlph); err != nil {
			return nil, err
		}
	}
	return c, r.saveDBConfig(id, *c)
}

func (r *Runtime) dockerOut(ctx context.Context, args ...string) (string, error) {
	out, err := r.run(ctx, r.dockerBin(), args...)
	if err != nil {
		return string(out), fmt.Errorf("%s", strings.TrimSpace(string(out)+" "+err.Error()))
	}
	return string(out), nil
}

func (r *Runtime) dbEnsureNetwork(ctx context.Context, id string) error {
	if _, err := r.run(ctx, r.dockerBin(), "network", "inspect", dbNetwork(id)); err == nil {
		return nil
	}
	// Internal: the network has no route out. The game reaches the
	// internet through its own network; the database has no need to.
	_, err := r.dockerOut(ctx, "network", "create", "--internal", "--label", "ndl.game-db="+id, dbNetwork(id))
	return err
}

func (r *Runtime) dbInstall(ctx context.Context, id string, c *DBConfig, pull bool) (*DBConfig, error) {
	if c == nil {
		return nil, errors.New("set the database name first")
	}
	if r.networkMode() == "host" {
		return nil, errors.New("game servers here use host networking, which cannot reach a private database network")
	}
	data := filepath.Join(r.dbDir(id), "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		return nil, err
	}
	if err := r.dbEnsureNetwork(ctx, id); err != nil {
		return nil, err
	}
	// A restart keeps the image it has; an install fetches the latest
	// release of the series.
	_, haveImage := r.run(ctx, r.dockerBin(), "image", "inspect", c.Image)
	if pull || haveImage != nil {
		if _, err := r.dockerOut(ctx, "pull", c.Image); err != nil {
			return nil, fmt.Errorf("could not download %s: %w", c.Image, err)
		}
	}
	confDir, err := r.dbWriteConf(id, c)
	if err != nil {
		return nil, err
	}
	sockDir, err := r.dbEnsureSocketDir(id)
	if err != nil {
		return nil, err
	}
	_, _ = r.run(ctx, r.dockerBin(), "rm", "-f", dbContainer(id))
	args := []string{
		"run", "-d", "--name", dbContainer(id),
		"--restart", "unless-stopped",
		"--network", dbNetwork(id), "--network-alias", DBHost,
		"--security-opt", "no-new-privileges",
		"--memory", strconv.Itoa(c.MemoryMB) + "m",
		"--memory-swap", strconv.Itoa(c.MemoryMB) + "m",
		"--label", "ndl.game-db=" + id,
		"-v", data + ":/var/lib/mysql",
		// NDL's settings (the cache size) live here, read on every start.
		"-v", confDir + ":/etc/mysql/conf.d:ro",
		"-v", sockDir + ":" + dbSocketDir,
		// Values come from this process's environment, so they are not
		// on the command line.
		"-e", "MARIADB_ROOT_PASSWORD", "-e", "MARIADB_DATABASE", "-e", "MARIADB_USER", "-e", "MARIADB_PASSWORD",
	}
	if c.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(c.CPUs, 'f', -1, 64))
	}
	args = append(args, c.Image)
	secret := []string{
		"MARIADB_ROOT_PASSWORD=" + c.RootPassword,
		"MARIADB_DATABASE=" + c.Database,
		"MARIADB_USER=" + c.User,
		"MARIADB_PASSWORD=" + c.Password,
	}
	if out, err := r.runWithEnv(ctx, secret, r.dockerBin(), args...); err != nil {
		return nil, fmt.Errorf("could not start MariaDB: %s", strings.TrimSpace(string(out)+" "+err.Error()))
	}
	if err := r.dbWaitReady(ctx, id); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	c.InstalledAt = &now
	if err := r.saveDBConfig(id, *c); err != nil {
		return nil, err
	}
	// A server already running joins the network now, without a restart.
	if r.Running(ctx, id) {
		_, _ = r.run(ctx, r.dockerBin(), "network", "connect", dbNetwork(id), containerName(id))
	}
	return c, nil
}

// Commands run inside the database container as root. The password
// comes from the container's own environment, through MYSQL_PWD, so it
// is never on a command line. The database name is checked by
// CleanDBName (letters, digits and _), so quoting it is safe.
const (
	dbPing  = `MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb-admin ping --silent -uroot`
	dbProbe = `MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb -uroot -e "SELECT 1" "$MARIADB_DATABASE"`
	dbDumpC = `MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb-dump -uroot --single-transaction --quick --routines --triggers --events --hex-blob "$MARIADB_DATABASE"`
	// dbLoadC empties the database, then loads the dump from stdin.
	dbLoadC = "export MYSQL_PWD=\"$MARIADB_ROOT_PASSWORD\"; " +
		"mariadb -uroot -e \"DROP DATABASE IF EXISTS \\`$MARIADB_DATABASE\\`; CREATE DATABASE \\`$MARIADB_DATABASE\\`; " +
		"GRANT ALL ON \\`$MARIADB_DATABASE\\`.* TO '$MARIADB_USER'@'%';\" && exec mariadb -uroot \"$MARIADB_DATABASE\""
)

func (r *Runtime) dbExec(ctx context.Context, id, cmd string) error {
	_, err := r.run(ctx, r.dockerBin(), "exec", dbContainer(id), "sh", "-c", cmd)
	return err
}

func (r *Runtime) dbWaitReady(ctx context.Context, id string) error {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		// The first start creates the user and database, then restarts
		// the server; the probe answers once the database exists.
		if r.dbExec(ctx, id, dbPing) == nil && r.dbExec(ctx, id, dbProbe) == nil {
			return nil
		}
		if time.Now().After(deadline) {
			logs, _ := r.run(ctx, r.dockerBin(), "logs", "--tail", "20", dbContainer(id))
			return fmt.Errorf("MariaDB did not become ready: %s", strings.TrimSpace(string(logs)))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (r *Runtime) dbStart(ctx context.Context, id string) error {
	if err := r.dbEnsureNetwork(ctx, id); err != nil {
		return err
	}
	if _, err := r.dockerOut(ctx, "start", dbContainer(id)); err != nil {
		return err
	}
	return r.dbWaitReady(ctx, id)
}

func (r *Runtime) dbRemove(ctx context.Context, id string, c *DBConfig, deleteData bool) (*DBConfig, error) {
	_, _ = r.run(ctx, r.dockerBin(), "rm", "-f", dbContainer(id))
	_, _ = r.run(ctx, r.dockerBin(), "network", "disconnect", "-f", dbNetwork(id), containerName(id))
	_, _ = r.run(ctx, r.dockerBin(), "network", "rm", dbNetwork(id))
	if deleteData {
		if err := os.RemoveAll(r.dbDir(id)); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if c == nil {
		return nil, nil
	}
	// Keep the data and credentials: installing again brings it back.
	c.InstalledAt = nil
	return c, r.saveDBConfig(id, *c)
}

func (r *Runtime) dbSocketHostDir(id string) string { return filepath.Join(r.dbDir(id), "run") }

// dbEnsureSocketDir makes the folder the socket lives in. Like the
// image's own /run/mysqld it is open to every user with the sticky bit,
// so MariaDB (uid 999) can create the socket.
func (r *Runtime) dbEnsureSocketDir(id string) (string, error) {
	dir := r.dbSocketHostDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, os.Chmod(dir, os.ModeSticky|0o777)
}

// dbMountsFor is the socket folder a starting game server sees,
// read-only, when its database was installed with one.
func (r *Runtime) dbMountsFor(id string) []BindMount {
	c, err := r.DBConfigFor(id)
	if err != nil || c == nil || c.InstalledAt == nil {
		return nil
	}
	dir := r.dbSocketHostDir(id)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil
	}
	return []BindMount{{Host: dir, Target: dbSocketDir, ReadOnly: true}}
}

// dbNetworksFor is the extra network a starting game server joins.
func (r *Runtime) dbNetworksFor(ctx context.Context, id string) []string {
	c, err := r.DBConfigFor(id)
	if err != nil || c == nil || c.InstalledAt == nil {
		return nil
	}
	// The game expects its database up when it starts.
	if r.dbState(ctx, id, c) == "stopped" {
		_ = r.dbStart(ctx, id)
	}
	if err := r.dbEnsureNetwork(ctx, id); err != nil {
		return nil
	}
	return []string{dbNetwork(id)}
}

// dbDump writes a consistent dump of the server's database to path. It
// reports false when the server has no installed database.
func (r *Runtime) dbDump(ctx context.Context, id, path string) (bool, error) {
	c, err := r.DBConfigFor(id)
	if err != nil {
		return false, err
	}
	if c == nil || c.InstalledAt == nil {
		return false, nil
	}
	if st := r.dbState(ctx, id, c); st != "running" {
		return true, fmt.Errorf("the database is %s; start it so the backup can include it", st)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return true, err
	}
	// --single-transaction reads one consistent snapshot without locking
	// the game's tables.
	err = r.runStream(ctx, nil, f, "exec", dbContainer(id), "sh", "-c", dbDumpC)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return true, fmt.Errorf("database dump failed: %w", err)
	}
	return true, nil
}

// dbCanRestore reports whether a dump can be loaded now.
func (r *Runtime) dbCanRestore(ctx context.Context, id string) error {
	c, err := r.DBConfigFor(id)
	if err != nil {
		return err
	}
	if c == nil || c.InstalledAt == nil {
		return errors.New("this backup includes a database, but the server has none installed; install it, then restore again")
	}
	if st := r.dbState(ctx, id, c); st != "running" {
		return fmt.Errorf("this backup includes a database, but the database is %s; start it, then restore again", st)
	}
	return nil
}

// dbRestore replaces the database's contents with a dump.
func (r *Runtime) dbRestore(ctx context.Context, id, dumpPath string) error {
	f, err := os.Open(dumpPath)
	if err != nil {
		return err
	}
	defer f.Close()
	err = r.runStream(ctx, f, io.Discard, "exec", "-i", dbContainer(id), "sh", "-c", dbLoadC)
	if err != nil {
		return fmt.Errorf("database restore failed: %w", err)
	}
	return nil
}

// runStream runs docker with stdin and stdout attached to files, for
// dumps and loads too large to hold in memory.
func (r *Runtime) runStream(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	if r.Run != nil {
		out, err := r.Run(ctx, r.dockerBin(), args...)
		if err == nil {
			_, err = stdout.Write(out)
		}
		return err
	}
	cmd := exec.CommandContext(ctx, r.dockerBin(), args...)
	if host := strings.TrimSpace(r.DockerHost); host != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+host)
	}
	var stderr strings.Builder
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %w", strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

// DatabaseSupported reports games with common MySQL or MariaDB plugins
// or addons, which can use a database of their own.
func DatabaseSupported(t Template) bool {
	switch t.Game {
	case "gmod", "fivem", "redm", "rust", "arma3", "tf2", "l4d2", "cs2":
		return true
	case "minecraft":
		return hasCap(t.Capabilities, CapPlugins)
	}
	return false
}

// RandomPassword is a password of n letters and digits, without the ones
// easily mistaken for each other.
func RandomPassword(n int) (string, error) { return randomString(n, passwordAlph) }
