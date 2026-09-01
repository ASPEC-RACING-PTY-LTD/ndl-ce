package control

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/httpapi"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/store"
	"github.com/no-dal/ndl-ce/migrations"
)

// Config is process configuration.
type Config struct {
	Listen    string
	DSN       string
	UIDir     string
	SetupHash string
	AgentSock string
}

// LoadConfig reads environment.
func LoadConfig() Config {
	c := Config{
		Listen:    getenv("NODAL_LISTEN", ":8080"),
		DSN:       first(os.Getenv("NODAL_DSN"), os.Getenv("NODAL_DATABASE_URL")),
		UIDir:     getenv("NODAL_UI_DIR", "/usr/share/ndl/ui"),
		SetupHash: os.Getenv("NODAL_SETUP_HASH"),
		AgentSock: os.Getenv("NODAL_AGENT_SOCKET"),
	}
	if c.DSN == "" {
		c.DSN = "postgresql:///nodal?host=/var/run/postgresql"
	}
	if c.SetupHash == "" {
		if h, err := httpapi.LoadSetupHash("/etc/ndl/setup.token.hash"); err == nil {
			c.SetupHash = h
		}
	}
	return c
}

// Run starts the control plane.
func Run(cfg Config) error {
	if err := RefuseRoot(); err != nil {
		return err
	}
	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	files, err := store.List(migrations.FS, ".")
	if err != nil {
		return err
	}
	if err := store.Apply(ctx, store.SQLDB{DB: db}, files); err != nil {
		return err
	}
	st := &appdb.Postgres{DB: db}
	if err := ensureReady(ctx, st, cfg.SetupHash); err != nil {
		return err
	}
	var ui fs.FS
	if cfg.UIDir != "" {
		if info, err := os.Stat(cfg.UIDir); err == nil && info.IsDir() {
			ui = os.DirFS(cfg.UIDir)
		}
	}
	hub := &httpapi.EventHub{}
	agent := agentrpc.Client{Socket: cfg.AgentSock}
	srv := &httpapi.Server{
		Store:     st,
		Lockout:   auth.NewLockout(),
		Agent:     agent,
		Observer:  agent,
		Storage:   agent,
		Network:   agent,
		Workloads: agent,
		IO:        agent,
		Hub:       hub,
		UI:        ui,
		SetupHash: cfg.SetupHash,
	}
	go observer{Store: st, Agent: agent, Hub: hub}.run(context.Background())
	log.Printf("ndl-control listening on %s", cfg.Listen)
	return http.ListenAndServe(cfg.Listen, srv.Handler())
}

func ensureReady(ctx context.Context, st appdb.Store, setupHash string) error {
	c, err := st.GetCluster(ctx)
	if err != nil {
		return err
	}
	if c == nil {
		// Cluster is created at first setup claim so replay has a durable row.
		return nil
	}
	return st.EnsureRoles(ctx, c.ID, rbac.SeedRoles())
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func first(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
