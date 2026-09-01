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
	"github.com/no-dal/ndl-ce/internal/ndltls"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/store"
	"github.com/no-dal/ndl-ce/migrations"
)

// Config is process configuration.
type Config struct {
	Listen     string
	TLSListen  string
	HTTPListen string
	CertDir    string
	DSN        string
	UIDir      string
	SetupHash  string
	AgentSock  string
}

// LoadConfig reads environment.
func LoadConfig() Config {
	c := Config{
		Listen:     getenv("NODAL_LISTEN", ":8080"),
		TLSListen:  getenv("NODAL_TLS_LISTEN", ":443"),
		HTTPListen: getenv("NODAL_HTTP_LISTEN", ":80"),
		CertDir:    getenv("NODAL_CERT_DIR", "/var/lib/ndl/certs"),
		DSN:        first(os.Getenv("NODAL_DSN"), os.Getenv("NODAL_DATABASE_URL")),
		UIDir:      getenv("NODAL_UI_DIR", "/usr/share/ndl/ui"),
		SetupHash:  os.Getenv("NODAL_SETUP_HASH"),
		AgentSock:  os.Getenv("NODAL_AGENT_SOCKET"),
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
	challenges := &ndltls.ChallengeMem{}
	srv := &httpapi.Server{
		Store:      st,
		Lockout:    auth.NewLockout(),
		Agent:      agent,
		Observer:   agent,
		Logs:       agent,
		Storage:    agent,
		Network:    agent,
		Workloads:  agent,
		IO:         agent,
		QEMU:       httpapi.AdaptQEMU(agent),
		VM:         httpapi.AdaptVM(agent),
		OCI:        httpapi.AdaptOCI(agent),
		Backup:     httpapi.AdaptBackup(agent),
		Object:     httpapi.AdaptObject(agent),
		Verify:     httpapi.AdaptVerify(agent),
		Update:     httpapi.AdaptUpdate(agent),
		GPU:        httpapi.AdaptGPU(agent),
		ZFS:        httpapi.AdaptZFS(agent),
		LVM:        httpapi.AdaptLVM(agent),
		Datastore:  httpapi.AdaptDatastore(agent),
		Hub:        hub,
		UI:         ui,
		SetupHash:  cfg.SetupHash,
		TLSListen:  cfg.TLSListen,
		HTTPListen: cfg.HTTPListen,
		CertDir:    ndltls.Dir{Root: cfg.CertDir},
		Challenges: challenges,
	}
	enabled, err := certificateEnabled(ctx, st)
	if err != nil {
		return fmt.Errorf("tls state is unreadable; refusing to start: %w", err)
	}
	if enabled {
		srv.TLSRequired = true
	}
	go observer{Store: st, Agent: agent, Hub: hub, Nightly: srv.TickNightlyBackups, Alerts: srv.TickAlerts}.run(context.Background())
	handler := srv.Handler()
	if srv.TLSRequired {
		mat, err := loadEnabledMaterial(cfg.CertDir)
		if err != nil {
			return fmt.Errorf("tls is enabled; refusing to start without last-good material: %w", err)
		}
		srv.TLSServing = true
		redir := redirectHandler("", challenges)
		go func() {
			log.Printf("ndl-control HTTP redirect on %s", cfg.Listen)
			_ = http.ListenAndServe(cfg.Listen, redir)
		}()
		if cfg.HTTPListen != "" && cfg.HTTPListen != cfg.Listen {
			go func() {
				log.Printf("ndl-control HTTP redirect on %s", cfg.HTTPListen)
				_ = http.ListenAndServe(cfg.HTTPListen, redir)
			}()
		}
		return serveTLS(cfg.TLSListen, mat.Certificate, handler)
	}
	log.Printf("ndl-control listening on %s", cfg.Listen)
	return http.ListenAndServe(cfg.Listen, handler)
}

func certificateEnabled(ctx context.Context, st appdb.Store) (bool, error) {
	c, err := st.GetCluster(ctx)
	if err != nil {
		return false, err
	}
	if c == nil {
		return false, nil
	}
	cert, err := st.GetCertificate(ctx, c.ID)
	if err != nil {
		return false, err
	}
	return cert != nil && cert.Enabled, nil
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
