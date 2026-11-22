package gameserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// A server's database has CPU and memory limits of its own, apart from
// the game's. They change live: docker update sets the container's
// limits, and MariaDB resizes its cache (the InnoDB buffer pool) while it
// runs. The cache size is also written to a config file the container
// reads on every start, so it holds after a restart. A database created
// before that file existed keeps its old cache size on restart until it
// is restarted once from NDL (RestartNeeded).

const (
	minDBMemoryMB  = 256
	maxDBMemoryMB  = 65536
	maxDBCPUs      = 64
	minBufferMB    = 128
	bufferChunkMB  = 128
	confFileName   = "ndl.cnf"
	resizeWaitTime = 30 * time.Second
)

// DBResources are a database's limits.
type DBResources struct {
	MemoryMB int     `json:"memory_mb"`
	CPUs     float64 `json:"cpus"`
	// BufferPoolMB 0 sizes the cache from MemoryMB.
	BufferPoolMB int `json:"buffer_pool_mb"`
	// FastCommit: see DBConfig.FastCommit.
	FastCommit bool `json:"fast_commit"`
}

// DBUsage is what the database uses now, and whether its settings are
// fully in effect.
type DBUsage struct {
	MemoryBytes     int64   `json:"memory_bytes"`
	MemoryLimit     int64   `json:"memory_limit_bytes"`
	CPUPercent      float64 `json:"cpu_percent"`
	BufferPoolBytes int64   `json:"buffer_pool_bytes"`
	// EffectiveBufferPoolMB is the cache size the settings work out to.
	EffectiveBufferPoolMB int `json:"effective_buffer_pool_mb"`
	// FlushLogAtCommit is innodb_flush_log_at_trx_commit as it is now.
	FlushLogAtCommit int `json:"flush_log_at_commit"`
	// RestartNeeded: the container predates NDL's config file and socket
	// folder, so it needs one restart from NDL to pick them up.
	RestartNeeded bool `json:"restart_needed"`
	Running       bool `json:"running"`
}

// bufferPoolFor is the cache size for a database: as set, or half its
// memory, in whole 128 MB chunks, at least 128 MB.
func bufferPoolFor(c *DBConfig) int {
	mb := c.BufferPoolMB
	if mb <= 0 {
		mb = c.MemoryMB / 2
	}
	mb = (mb / bufferChunkMB) * bufferChunkMB
	if mb < minBufferMB {
		mb = minBufferMB
	}
	return mb
}

func (r *Runtime) dbConfDir(id string) string { return filepath.Join(r.dbDir(id), "conf.d") }

// dbWriteConf writes the settings MariaDB reads at start.
func (r *Runtime) dbWriteConf(id string, c *DBConfig) (string, error) {
	dir := r.dbConfDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	flush := 1
	if c.FastCommit {
		flush = 2
	}
	// skip-name-resolve: everything connects by address or socket, and a
	// reverse lookup would only slow each connection.
	body := fmt.Sprintf("# Written by NDL from the database's settings. Changes here are replaced.\n[mariadb]\ninnodb_buffer_pool_size = %dM\ninnodb_flush_log_at_trx_commit = %d\nskip-name-resolve\nsocket = %s\n", bufferPoolFor(c), flush, dbSocketPath)
	tmp := filepath.Join(dir, confFileName+".tmp")
	// The server runs as the mysql user, which must read it.
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return "", err
	}
	return dir, os.Rename(tmp, filepath.Join(dir, confFileName))
}

// checkResources validates a change.
func checkResources(res DBResources) error {
	if res.MemoryMB < minDBMemoryMB || res.MemoryMB > maxDBMemoryMB {
		return fmt.Errorf("memory must be between %d MB and %d MB", minDBMemoryMB, maxDBMemoryMB)
	}
	if res.CPUs < 0 || res.CPUs > maxDBCPUs {
		return fmt.Errorf("CPUs must be between 0 (no cap) and %d", maxDBCPUs)
	}
	if res.BufferPoolMB != 0 && (res.BufferPoolMB < minBufferMB || res.BufferPoolMB > res.MemoryMB*4/5) {
		return fmt.Errorf("the cache must be between %d MB and 80%% of the memory (%d MB), or automatic", minBufferMB, res.MemoryMB*4/5)
	}
	return nil
}

func (r *Runtime) dbResources(ctx context.Context, id string, c *DBConfig, call DBCall) (DBReply, error) {
	if c == nil {
		return DBReply{}, errors.New("set the database name first")
	}
	if call.Resources != nil {
		if err := r.dbSetResources(ctx, id, c, *call.Resources); err != nil {
			return DBReply{}, err
		}
	}
	usage := r.dbUsage(ctx, id, c)
	return DBReply{Config: c, State: r.dbState(ctx, id, c), Usage: &usage}, nil
}

func (r *Runtime) dbSetResources(ctx context.Context, id string, c *DBConfig, res DBResources) error {
	if err := checkResources(res); err != nil {
		return err
	}
	old := *c
	c.MemoryMB, c.CPUs, c.BufferPoolMB, c.FastCommit = res.MemoryMB, res.CPUs, res.BufferPoolMB, res.FastCommit
	if _, err := r.dbWriteConf(id, c); err != nil {
		return err
	}
	if c.InstalledAt == nil || r.dbState(ctx, id, c) != "running" {
		// Not running: the settings apply when it next starts. A stopped
		// container still takes new limits now.
		if c.InstalledAt != nil {
			_, _ = r.run(ctx, r.dockerBin(), r.dbUpdateArgs(id, c)...)
		}
		return r.saveDBConfig(id, *c)
	}
	newPool := bufferPoolFor(c)
	shrinking := c.MemoryMB < old.MemoryMB
	// Shrinking: make the cache smaller first, so MariaDB fits under the
	// new limit before it applies. Growing: raise the limit first.
	if shrinking {
		if err := r.dbSetBufferPool(ctx, id, newPool); err != nil {
			return err
		}
		if used := r.dbUsage(ctx, id, c).MemoryBytes; used > int64(c.MemoryMB)<<20*9/10 {
			*c = old
			_, _ = r.dbWriteConf(id, c)
			return fmt.Errorf("MariaDB is using %d MB now, too close to %d MB; lower the limit less, or restart the database first", used>>20, res.MemoryMB)
		}
	}
	if _, err := r.dockerOut(ctx, r.dbUpdateArgs(id, c)...); err != nil {
		*c = old
		_, _ = r.dbWriteConf(id, c)
		return fmt.Errorf("could not change the limits: %w", err)
	}
	if !shrinking {
		if err := r.dbSetBufferPool(ctx, id, newPool); err != nil {
			return err
		}
	}
	flush := 1
	if c.FastCommit {
		flush = 2
	}
	if err := r.dbExec(ctx, id, fmt.Sprintf(`MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb -uroot -e "SET GLOBAL innodb_flush_log_at_trx_commit = %d"`, flush)); err != nil {
		return fmt.Errorf("could not change how often the log is flushed: %w", err)
	}
	return r.saveDBConfig(id, *c)
}

func (r *Runtime) dbUpdateArgs(id string, c *DBConfig) []string {
	mem := strconv.Itoa(c.MemoryMB) + "m"
	// docker update ignores --cpus 0 rather than lifting the cap, so no
	// cap is every core this host has.
	cpus := c.CPUs
	if cpus <= 0 {
		cpus = float64(runtime.NumCPU())
	}
	return []string{"update", "--memory", mem, "--memory-swap", mem, "--cpus", strconv.FormatFloat(cpus, 'f', -1, 64), dbContainer(id)}
}

// dbSetBufferPool resizes the cache live and waits for MariaDB to finish.
func (r *Runtime) dbSetBufferPool(ctx context.Context, id string, mb int) error {
	set := fmt.Sprintf(`MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb -uroot -e "SET GLOBAL innodb_buffer_pool_size = %d"`, int64(mb)<<20)
	if err := r.dbExec(ctx, id, set); err != nil {
		return fmt.Errorf("could not resize the cache: %w", err)
	}
	deadline := time.Now().Add(resizeWaitTime)
	for time.Now().Before(deadline) {
		out, err := r.run(ctx, r.dockerBin(), "exec", dbContainer(id), "sh", "-c",
			`MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb -uroot -N -e "SHOW STATUS LIKE 'Innodb_buffer_pool_resize_status'"`)
		if err != nil || !strings.Contains(strings.ToLower(string(out)), "resiz") || strings.Contains(strings.ToLower(string(out)), "completed") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return nil
}

var sizeRe = regexp.MustCompile(`^([0-9.]+)\s*([KMGT]?i?B)$`)

func parseDockerSize(s string) int64 {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	mult := map[string]float64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40, "kB": 1e3, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12}[m[2]]
	return int64(v * mult)
}

// dbUsage reads what the database uses now.
func (r *Runtime) dbUsage(ctx context.Context, id string, c *DBConfig) DBUsage {
	u := DBUsage{EffectiveBufferPoolMB: bufferPoolFor(c)}
	if c.InstalledAt == nil {
		return u
	}
	if out, err := r.run(ctx, r.dockerBin(), "inspect", "-f", "{{range .Mounts}}{{.Destination}} {{end}}", dbContainer(id)); err == nil {
		u.RestartNeeded = !strings.Contains(string(out), "/etc/mysql/conf.d") || !strings.Contains(string(out), dbSocketDir)
	}
	if r.dbState(ctx, id, c) != "running" {
		return u
	}
	u.Running = true
	if out, err := r.run(ctx, r.dockerBin(), "stats", "--no-stream", "--format", "{{.CPUPerc}}|{{.MemUsage}}", dbContainer(id)); err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), "|")
		if len(parts) == 2 {
			u.CPUPercent, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(parts[0]), "%"), 64)
			if mem := strings.Split(parts[1], "/"); len(mem) == 2 {
				u.MemoryBytes = parseDockerSize(mem[0])
				u.MemoryLimit = parseDockerSize(mem[1])
			}
		}
	}
	if out, err := r.run(ctx, r.dockerBin(), "exec", dbContainer(id), "sh", "-c",
		`MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb -uroot -N -e "SELECT @@innodb_buffer_pool_size, @@innodb_flush_log_at_trx_commit"`); err == nil {
		if f := strings.Fields(string(out)); len(f) == 2 {
			u.BufferPoolBytes, _ = strconv.ParseInt(f[0], 10, 64)
			u.FlushLogAtCommit, _ = strconv.Atoi(f[1])
		}
	}
	return u
}
