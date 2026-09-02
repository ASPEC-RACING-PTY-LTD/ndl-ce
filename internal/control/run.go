package control

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/cluster"
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
	CADir      string
	HolderID   string
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
		CADir:      getenv("NODAL_CLUSTER_CA_DIR", "/var/lib/ndl/secrets/cluster-ca"),
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

func shutdownSignals() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func newLeaseHolderID() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), uuid.NewString())
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
	holder := cfg.HolderID
	if holder == "" {
		holder = newLeaseHolderID()
	}
	ca := cluster.CA{Dir: cfg.CADir}
	runCtx, stop := shutdownSignals()
	defer stop()
	lease := newWriterLease(st, holder, ca)
	if err := lease.acquire(ctx); err != nil {
		return fmt.Errorf("another control plane holds the writer lease: %w", err)
	}
	if lease.getClusterID() != "" {
		_ = ca.Ensure(time.Now().UTC())
	}
	lease.startRenewal()
	defer func() {
		relinquishCtx, relinquishCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer relinquishCancel()
		if err := lease.relinquish(relinquishCtx); err != nil {
			log.Printf("writer lease release: %v", err)
		}
	}()
	hub := &httpapi.EventHub{}
	agent := agentrpc.Client{Socket: cfg.AgentSock}
	challenges := &ndltls.ChallengeMem{}
	srv := &httpapi.Server{
		Store:       st,
		Lockout:     auth.NewLockout(),
		Agent:       agent,
		Observer:    agent,
		Logs:        agent,
		Storage:     agent,
		Network:     agent,
		Workloads:   agent,
		IO:          agent,
		QEMU:        httpapi.AdaptQEMU(agent),
		VM:          httpapi.AdaptVM(agent),
		Migrate:     httpapi.AdaptMigrate(agent),
		OCI:         httpapi.AdaptOCI(agent),
		Backup:      httpapi.AdaptBackup(agent),
		Object:      httpapi.AdaptObject(agent),
		Verify:      httpapi.AdaptVerify(agent),
		Update:      httpapi.AdaptUpdate(agent),
		GPU:         httpapi.AdaptGPU(agent),
		ZFS:         httpapi.AdaptZFS(agent),
		LVM:         httpapi.AdaptLVM(agent),
		Datastore:   httpapi.AdaptDatastore(agent),
		Hub:         hub,
		UI:          ui,
		SetupHash:   cfg.SetupHash,
		TLSListen:   cfg.TLSListen,
		HTTPListen:  cfg.HTTPListen,
		CertDir:     ndltls.Dir{Root: cfg.CertDir},
		ClusterCA:   ca,
		LeaseHolder: holder,
		Challenges:  challenges,
	}
	enabled, err := certificateEnabled(ctx, st)
	if err != nil {
		return fmt.Errorf("tls state is unreadable; refusing to start: %w", err)
	}
	if enabled {
		srv.TLSRequired = true
	}
	go observer{Store: st, Agent: agent, Hub: hub, Nightly: srv.TickNightlyBackups, Alerts: srv.TickAlerts}.run(runCtx)
	handler := srv.Handler()
	instances, err := controlHTTPInstances(cfg, srv, handler, challenges)
	if err != nil {
		return err
	}
	return serveHTTPServers(runCtx, instances)
}

func controlHTTPInstances(cfg Config, srv *httpapi.Server, handler http.Handler, challenges *ndltls.ChallengeMem) ([]*httpInstance, error) {
	if !srv.TLSRequired {
		return []*httpInstance{newHTTPInstance("http", cfg.Listen, handler)}, nil
	}
	mat, err := loadEnabledMaterial(cfg.CertDir)
	if err != nil {
		return nil, fmt.Errorf("tls is enabled; refusing to start without last-good material: %w", err)
	}
	srv.TLSServing = true
	tlsInst, err := newTLSInstance("https", cfg.TLSListen, mat.Certificate, handler)
	if err != nil {
		return nil, fmt.Errorf("tls listen %s: %w", cfg.TLSListen, err)
	}
	redir := redirectHandler("", challenges)
	instances := []*httpInstance{
		newHTTPInstance("http-redirect", cfg.Listen, redir),
		tlsInst,
	}
	if cfg.HTTPListen != "" && cfg.HTTPListen != cfg.Listen {
		instances = append(instances, newHTTPInstance("http-redirect", cfg.HTTPListen, redir))
	}
	return instances, nil
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
