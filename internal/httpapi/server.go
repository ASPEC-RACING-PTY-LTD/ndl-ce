package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/journald"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/ndltls"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

const (
	sessionCookie = "ndl_session"
	sessionTTL    = 12 * time.Hour
	edition       = "Community Edition"
)

// Agent is the local southbound client used after setup.
type Agent interface {
	Enroll(ctx context.Context, clusterID string) (nodeID string, hostPlatform json.RawMessage, err error)
}

// Observer reads cached agent observations. It does not scan the host here.
type Observer interface {
	GetMetrics(ctx context.Context, from, to time.Time) (metrics.QueryResult, error)
}

// LogsRPC reads typed journalctl output from the agent.
type LogsRPC interface {
	GetLogs(ctx context.Context, unit string, lines int, since time.Time) (journald.Result, error)
}

// Server is the northbound HTTP API plus static UI.
type Server struct {
	Store       appdb.Store
	Lockout     *auth.Lockout
	Agent       Agent
	Observer    Observer
	Logs        LogsRPC
	HTTPClient  *http.Client
	Storage     StorageRPC
	Network     NetworkRPC
	Workloads   WorkloadRPC
	IO          IORPC
	QEMU        QemuRPC
	VM          VMRPC
	OCI         OCIRPC
	Backup      BackupRPC
	Update      UpdateRPC
	GPU         GPURPC
	ZFS         ZFSRPC
	Hub         *EventHub
	UI          fs.FS
	Now         func() time.Time
	SetupHash   string
	AllowedUID  uint32
	TLSRequired bool
	TLSServing  bool // true when this process is listening with TLS
	CertDirty   bool // true when on-disk material changed since TLSServing
	TLSListen   string
	HTTPListen  string
	HTTPSURL    string
	CertDir     ndltls.Dir
	Challenges  *ndltls.ChallengeMem
	backupMu    sync.Mutex
	nightlyBusy atomic.Bool
	alertBusy   atomic.Bool
}

type principal struct {
	User    appdb.User
	Roles   []string
	Grants  []string
	SessID  string
	AAL     int
	TokenID string
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// Handler returns the mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/setup/status", s.setupStatus)
	mux.HandleFunc("POST /api/v1/setup/claim", s.setupClaim)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("GET /api/v1/me", s.me)
	mux.HandleFunc("PATCH /api/v1/me", s.patchMe)
	mux.HandleFunc("POST /api/v1/tokens", s.createToken)
	mux.HandleFunc("POST /api/v1/tokens/revoke", s.revokeToken)
	mux.HandleFunc("GET /api/v1/nodes", s.listNodes)
	mux.HandleFunc("GET /api/v1/nodes/{id}", s.getNode)
	mux.HandleFunc("GET /api/v1/nodes/{id}/hardware", s.nodeHardware)
	mux.HandleFunc("GET /api/v1/nodes/{id}/usb", s.listNodeUSB)
	mux.HandleFunc("GET /api/v1/nodes/{id}/pci", s.listNodePCI)
	mux.HandleFunc("GET /api/v1/nodes/{id}/capabilities", s.nodeCapabilities)
	mux.HandleFunc("GET /api/v1/nodes/{id}/metrics", s.nodeMetrics)
	mux.HandleFunc("GET /api/v1/nodes/{id}/logs", s.nodeLogs)
	mux.HandleFunc("GET /api/v1/nodes/{id}/smart", s.nodeSMART)
	mux.HandleFunc("GET /api/v1/nodes/{id}/capacity", s.nodeCapacity)
	mux.HandleFunc("GET /api/v1/workloads/{id}/logs", s.workloadLogs)
	mux.HandleFunc("GET /api/v1/timeline", s.timeline)
	mux.HandleFunc("GET /api/v1/alerts", s.listAlerts)
	mux.HandleFunc("POST /api/v1/alerts", s.createAlert)
	mux.HandleFunc("GET /api/v1/alerts/channels", s.listChannels)
	mux.HandleFunc("POST /api/v1/alerts/channels", s.createChannel)
	mux.HandleFunc("GET /api/v1/tasks", s.listTasks)
	mux.HandleFunc("GET /api/v1/events", s.listEvents)
	mux.HandleFunc("GET /api/v1/events/stream", s.streamEvents)
	mux.HandleFunc("GET /api/v1/storage/pools", s.listPools)
	mux.HandleFunc("POST /api/v1/storage/pools", s.createPool)
	mux.HandleFunc("GET /api/v1/storage/pools/{id}", s.getPool)
	mux.HandleFunc("GET /api/v1/storage/volumes", s.listVolumes)
	mux.HandleFunc("POST /api/v1/storage/volumes", s.createVolume)
	mux.HandleFunc("GET /api/v1/storage/volumes/{id}", s.getVolume)
	mux.HandleFunc("GET /api/v1/storage/images", s.listImages)
	mux.HandleFunc("POST /api/v1/storage/images", s.uploadImage)
	mux.HandleFunc("GET /api/v1/storage/images/{id}", s.getImage)
	mux.HandleFunc("GET /api/v1/networks", s.listNetworks)
	mux.HandleFunc("POST /api/v1/networks", s.createNetwork)
	mux.HandleFunc("GET /api/v1/networks/{id}", s.getNetwork)
	mux.HandleFunc("POST /api/v1/networks/{id}/apply", s.applyNetwork)
	mux.HandleFunc("GET /api/v1/networks/{id}/reservations", s.listReservations)
	mux.HandleFunc("POST /api/v1/networks/{id}/reservations", s.createReservation)
	mux.HandleFunc("GET /api/v1/workloads", s.listWorkloads)
	mux.HandleFunc("POST /api/v1/workloads", s.createWorkload)
	mux.HandleFunc("POST /api/v1/workloads/import", s.importVM)
	mux.HandleFunc("GET /api/v1/workloads/{id}", s.getWorkload)
	mux.HandleFunc("GET /api/v1/workloads/{id}/guest", s.getWorkloadGuest)
	mux.HandleFunc("PATCH /api/v1/workloads/{id}", s.patchWorkload)
	mux.HandleFunc("POST /api/v1/workloads/{id}", s.patchWorkload)
	mux.HandleFunc("POST /api/v1/workloads/{id}/start", s.lifecycleWorkload("start"))
	mux.HandleFunc("POST /api/v1/workloads/{id}/stop", s.lifecycleWorkload("stop"))
	mux.HandleFunc("POST /api/v1/workloads/{id}/restart", s.lifecycleWorkload("restart"))
	mux.HandleFunc("POST /api/v1/workloads/{id}/force-stop", s.lifecycleWorkload("force-stop"))
	mux.HandleFunc("POST /api/v1/workloads/{id}/delete", s.lifecycleWorkload("delete"))
	mux.HandleFunc("POST /api/v1/workloads/{id}/clone", s.lifecycleWorkload("clone"))
	mux.HandleFunc("POST /api/v1/workloads/{id}/export", s.exportVM)
	mux.HandleFunc("POST /api/v1/workloads/{id}/usb", s.attachUSB)
	mux.HandleFunc("POST /api/v1/workloads/{id}/pci", s.attachPCI)
	mux.HandleFunc("GET /api/v1/templates", s.listTemplates)
	mux.HandleFunc("POST /api/v1/templates", s.createTemplate)
	mux.HandleFunc("POST /api/v1/templates/{id}/deploy", s.deployTemplate)
	mux.HandleFunc("POST /api/v1/workloads/{id}/console/sessions", s.createVMConsole)
	mux.HandleFunc("POST /api/v1/nodes/{id}/terminal/sessions", s.createNodeTerminal)
	mux.HandleFunc("POST /api/v1/workloads/{id}/terminal/sessions", s.createWorkloadTerminal)
	mux.HandleFunc("GET /api/v1/io/sessions/{id}", s.getIOSession)
	mux.HandleFunc("GET /api/v1/io/sessions/{id}/ws", s.ioSessionWS)
	mux.HandleFunc("GET /api/v1/nodes/{id}/files", s.nodeFilesList)
	mux.HandleFunc("GET /api/v1/nodes/{id}/files/stat", s.nodeFilesStat)
	mux.HandleFunc("GET /api/v1/nodes/{id}/files/download", s.nodeFilesDownload)
	mux.HandleFunc("POST /api/v1/nodes/{id}/files/upload", s.nodeFilesUpload)
	mux.HandleFunc("POST /api/v1/nodes/{id}/files/mkdir", s.nodeFilesMkdir)
	mux.HandleFunc("POST /api/v1/nodes/{id}/files/delete", s.nodeFilesDelete)
	mux.HandleFunc("POST /api/v1/nodes/{id}/files/move", s.nodeFilesMove)
	mux.HandleFunc("GET /api/v1/workloads/{id}/files", s.workloadFilesList)
	mux.HandleFunc("GET /api/v1/workloads/{id}/files/stat", s.workloadFilesStat)
	mux.HandleFunc("GET /api/v1/workloads/{id}/files/download", s.workloadFilesDownload)
	mux.HandleFunc("POST /api/v1/workloads/{id}/files/upload", s.workloadFilesUpload)
	mux.HandleFunc("POST /api/v1/workloads/{id}/files/mkdir", s.workloadFilesMkdir)
	mux.HandleFunc("POST /api/v1/workloads/{id}/files/delete", s.workloadFilesDelete)
	mux.HandleFunc("POST /api/v1/workloads/{id}/files/move", s.workloadFilesMove)
	mux.HandleFunc("POST /api/v1/lab/qemu-proto", s.labQemuProtoStart)
	mux.HandleFunc("GET /api/v1/lab/qemu-proto", s.labQemuProtoStatus)
	mux.HandleFunc("POST /api/v1/lab/qemu-proto/stop", s.labQemuProtoStop)
	mux.HandleFunc("POST /api/v1/lab/qemu-proto/kill", s.labQemuProtoKill)
	mux.HandleFunc("GET /api/v1/workloads/{id}/snapshots", s.listSnapshots)
	mux.HandleFunc("POST /api/v1/workloads/{id}/snapshots", s.createSnapshot)
	mux.HandleFunc("POST /api/v1/workloads/{id}/snapshots/flatten", s.flattenSnapshots)
	mux.HandleFunc("POST /api/v1/snapshots/{id}/rollback", s.rollbackSnapshot)
	mux.HandleFunc("GET /api/v1/backups/targets", s.listBackupTargets)
	mux.HandleFunc("POST /api/v1/backups/targets", s.createBackupTarget)
	mux.HandleFunc("GET /api/v1/backups/policies", s.listBackupPolicies)
	mux.HandleFunc("POST /api/v1/backups/policies", s.createBackupPolicy)
	mux.HandleFunc("GET /api/v1/backups/runs", s.listBackupRuns)
	mux.HandleFunc("GET /api/v1/backups/artifacts", s.listBackupArtifacts)
	mux.HandleFunc("POST /api/v1/backups/run", s.runBackup)
	mux.HandleFunc("POST /api/v1/backups/artifacts/{id}/restore", s.restoreBackup)
	mux.HandleFunc("GET /api/v1/certs", s.getCerts)
	mux.HandleFunc("POST /api/v1/certs/generate", s.generateCert)
	mux.HandleFunc("POST /api/v1/certs/import", s.importCert)
	mux.HandleFunc("POST /api/v1/certs/acme", s.acmeCert)
	mux.HandleFunc("GET /api/v1/updates", s.getUpdates)
	mux.HandleFunc("POST /api/v1/updates/check", s.checkUpdates)
	mux.HandleFunc("POST /api/v1/updates/preflight", s.preflightUpdates)
	mux.HandleFunc("POST /api/v1/updates/checkpoint", s.checkpointUpdates)
	mux.HandleFunc("POST /api/v1/updates/apply", s.applyUpdates)
	mux.HandleFunc("POST /api/v1/updates/rollback", s.rollbackUpdates)
	mux.HandleFunc("POST /api/v1/auth/mfa/verify", s.verifyMFA)
	mux.HandleFunc("GET /api/v1/mfa", s.getMFA)
	mux.HandleFunc("POST /api/v1/mfa/enroll", s.enrollMFA)
	mux.HandleFunc("POST /api/v1/mfa/confirm", s.confirmMFA)
	mux.HandleFunc("GET /api/v1/audit", s.listAudit)
	mux.HandleFunc("GET /api/v1/groups", s.listGroups)
	mux.HandleFunc("POST /api/v1/groups", s.createGroup)
	mux.HandleFunc("POST /api/v1/groups/{id}/members", s.addGroupMember)
	mux.HandleFunc("POST /api/v1/groups/{id}/roles", s.bindGroupRole)
	mux.HandleFunc("GET /api/v1/service-principals", s.listServicePrincipals)
	mux.HandleFunc("POST /api/v1/service-principals", s.createServicePrincipal)
	mux.HandleFunc("POST /api/v1/secrets/reveal", s.revealSecret)
	mux.HandleFunc("POST /api/v1/cluster/destroy", s.destroyCluster)
	mux.HandleFunc("POST /api/v1/storage/volumes/{id}/unlock", s.unlockVolume)
	mux.HandleFunc("GET /api/v1/gpus", s.listGPUs)
	mux.HandleFunc("GET /api/v1/gpus/runtime", s.gpuRuntime)
	mux.HandleFunc("POST /api/v1/gpus/runtime/install", s.installGPURuntime)
	mux.HandleFunc("POST /api/v1/gpus/assign", s.assignGPU)
	mux.HandleFunc("POST /api/v1/gpus/unassign", s.unassignGPU)
	mux.HandleFunc("GET /api/v1/workloads/{id}/gpus", s.workloadGPUs)
	mux.HandleFunc("GET /api/v1/registries", s.listRegistries)
	mux.HandleFunc("POST /api/v1/registries", s.createRegistry)
	mux.HandleFunc("GET /api/v1/stacks", s.listStacks)
	mux.HandleFunc("POST /api/v1/stacks", s.createStack)
	mux.HandleFunc("POST /api/v1/stacks/import", s.importStackCompose)
	mux.HandleFunc("GET /api/v1/stacks/{id}", s.getStack)
	mux.HandleFunc("PATCH /api/v1/stacks/{id}", s.patchStack)
	mux.HandleFunc("DELETE /api/v1/stacks/{id}", s.deleteStack)
	mux.HandleFunc("POST /api/v1/stacks/{id}/apply", s.applyStack)
	mux.HandleFunc("PATCH /api/v1/stacks/{id}/members/{memberId}", s.patchStackMember)
	mux.HandleFunc("GET /api/v1/storage/zfs", s.zfsRuntime)
	mux.HandleFunc("POST /api/v1/storage/zfs/import", s.importZFS)
	mux.HandleFunc("POST /api/v1/storage/zfs/create", s.createZFS)
	if s.UI != nil {
		mux.Handle("/", s.spa())
	}
	return mux
}

func (s *Server) spa() http.Handler {
	index, err := fs.ReadFile(s.UI, "index.html")
	if err != nil {
		index = []byte("<!doctype html><title>No-dal</title>")
	}
	fileServer := http.FileServer(http.FS(s.UI))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(s.UI, strings.TrimPrefix(r.URL.Path, "/")); err == nil && r.URL.Path != "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	open, _ := s.setupOpen(r.Context())
	status := "ok"
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      status,
		"service":     "ndl-control",
		"setup_open":  open,
		"tls_enabled": s.TLSRequired,
	})
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	open, err := s.setupOpen(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"open": open})
}

func (s *Server) setupOpen(ctx context.Context) (bool, error) {
	c, err := s.Store.GetCluster(ctx)
	if err != nil {
		return false, err
	}
	if c != nil && c.SetupCompletedAt != nil {
		return false, nil
	}
	st, err := s.Store.GetSetup(ctx)
	if err != nil {
		return false, err
	}
	if st != nil && st.ConsumedAt != nil {
		return false, nil
	}
	return true, nil
}

func (s *Server) setupClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Token == "" || req.Username == "" {
		writeErr(w, http.StatusBadRequest, "token and username are required")
		return
	}
	open, err := s.setupOpen(r.Context())
	if err != nil || !open {
		s.audit(r, "", "", "setup.claim", "denied", "replay")
		writeErr(w, http.StatusConflict, "setup is closed")
		return
	}
	if err := s.ensureCluster(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cluster, err := s.Store.GetCluster(r.Context())
	if err != nil || cluster == nil {
		writeErr(w, http.StatusInternalServerError, "cluster missing")
		return
	}
	st, err := s.Store.GetSetup(r.Context())
	if err != nil || st == nil {
		writeErr(w, http.StatusInternalServerError, "setup token missing")
		return
	}
	if !secutil.EqualHash(st.TokenHash, secutil.HashSHA256(req.Token)) {
		s.audit(r, cluster.ID, "", "setup.claim", "denied", "bad token")
		writeErr(w, http.StatusUnauthorized, "invalid setup token")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.EnsureRoles(r.Context(), cluster.ID, rbac.SeedRoles()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	user := appdb.User{
		ID:           uuid.NewString(),
		ClusterID:    cluster.ID,
		Username:     req.Username,
		PasswordHash: hash,
	}
	admins, err := s.Store.CountAdmins(r.Context(), cluster.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if admins > 0 {
		s.audit(r, cluster.ID, "", "setup.claim", "denied", "replay")
		writeErr(w, http.StatusConflict, "setup is closed")
		return
	}
	if err := s.Store.ConsumeSetup(r.Context(), cluster.ID); err != nil {
		s.audit(r, cluster.ID, "", "setup.claim", "denied", err.Error())
		writeErr(w, http.StatusConflict, "setup is closed")
		return
	}
	if err := s.Store.CreateUser(r.Context(), user); err != nil {
		writeErr(w, http.StatusConflict, "could not create user")
		return
	}
	if err := s.Store.BindRole(r.Context(), cluster.ID, user.ID, rbac.Admin); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Store.CompleteSetup(r.Context(), cluster.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.Agent != nil {
		nodeID, plat, err := s.Agent.Enroll(r.Context(), cluster.ID)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		_ = s.Store.UpsertNode(r.Context(), appdb.Node{
			ID:           nodeID,
			ClusterID:    cluster.ID,
			Name:         "local",
			HostPlatform: plat,
		})
	}
	if err := s.issueSession(w, r, user, 1); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, cluster.ID, user.ID, "setup.claim", "ok", "")
	s.writeMe(w, r, user, 1)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	key := host + "|" + strings.ToLower(strings.TrimSpace(req.Username))
	if err := s.lock().Check(key, s.now()); err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	cluster, err := s.Store.GetCluster(r.Context())
	if err != nil || cluster == nil {
		s.lock().Fail(key, s.now())
		s.audit(r, "", "", "auth.login", "denied", "no cluster")
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	user, err := s.Store.GetUserByName(r.Context(), cluster.ID, strings.TrimSpace(req.Username))
	if err != nil || user == nil || !auth.VerifyPassword(req.Password, user.PasswordHash) {
		s.lock().Fail(key, s.now())
		s.audit(r, cluster.ID, "", "auth.login", "denied", "invalid credentials")
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if user.Kind == appdb.UserKindService {
		s.lock().Fail(key, s.now())
		s.audit(r, cluster.ID, user.ID, "auth.login", "denied", "service principal")
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	method, _, _, err := s.Store.GetMFAMethod(r.Context(), user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "mfa state is unavailable")
		return
	}
	if method != nil && method.Enabled {
		s.writeMFAChallenge(w, r, *user)
		return
	}
	s.lock().Success(key)
	if err := s.issueSession(w, r, *user, 1); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, cluster.ID, user.ID, "auth.login", "ok", "")
	s.writeMe(w, r, *user, 1)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	p, err := s.principal(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	_ = s.Store.RevokeSession(r.Context(), p.SessID)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cookieSecure(r), SameSite: http.SameSiteLaxMode})
	s.audit(r, p.User.ClusterID, p.User.ID, "auth.logout", "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p, err := s.principal(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if !rbac.Authorize(p.Grants, rbac.IdentityRead) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	s.writeMe(w, r, p.User, p.AAL)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityTokenCreate)
	if err != nil {
		return
	}
	var req struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	for _, perm := range req.Permissions {
		if !rbac.Authorize(p.Grants, perm) {
			writeErr(w, http.StatusForbidden, "token permissions cannot exceed the creator")
			return
		}
	}
	raw, err := secutil.RandomHex(24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	plain := "ndl_" + raw
	tok := appdb.APIToken{
		ID:          uuid.NewString(),
		ClusterID:   p.User.ClusterID,
		UserID:      p.User.ID,
		Name:        strings.TrimSpace(req.Name),
		TokenHash:   secutil.HashSHA256(plain),
		Prefix:      plain[:8],
		Permissions: req.Permissions,
	}
	if err := s.Store.CreateToken(r.Context(), tok); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "token.create", "ok", tok.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     tok.ID,
		"prefix": tok.Prefix,
		"token":  plain,
	})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityTokenRevoke)
	if err != nil {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil || req.ID == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := s.Store.RevokeToken(r.Context(), req.ID, p.User.ID); err != nil {
		writeErr(w, http.StatusNotFound, "token not found")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "token.revoke", "ok", req.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) require(w http.ResponseWriter, r *http.Request, perm string) (*principal, error) {
	p, err := s.principal(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return nil, err
	}
	if !rbac.Authorize(p.Grants, perm) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return nil, errors.New("forbidden")
	}
	return p, nil
}

func (s *Server) principal(r *http.Request) (*principal, error) {
	if tok := bearer(r); tok != "" {
		row, err := s.Store.GetTokenByHash(r.Context(), secutil.HashSHA256(tok))
		if err != nil || row == nil || row.RevokedAt != nil {
			return nil, errors.New("invalid token")
		}
		u, err := s.Store.GetUser(r.Context(), row.UserID)
		if err != nil || u == nil {
			return nil, errors.New("invalid token")
		}
		return s.asPrincipal(r.Context(), *u, "", 1, row.ID, row.Permissions)
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, errors.New("no session")
	}
	sess, err := s.Store.GetSessionByHash(r.Context(), secutil.HashSHA256(c.Value))
	if err != nil || sess == nil || sess.RevokedAt != nil || s.now().After(sess.ExpiresAt) {
		return nil, errors.New("invalid session")
	}
	u, err := s.Store.GetUser(r.Context(), sess.UserID)
	if err != nil || u == nil {
		return nil, errors.New("invalid session")
	}
	return s.asPrincipal(r.Context(), *u, sess.ID, sess.AAL, "", nil)
}

func (s *Server) asPrincipal(ctx context.Context, u appdb.User, sessID string, aal int, tokenID string, tokenPerms []string) (*principal, error) {
	roles, err := s.Store.UserRoles(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	var grants []string
	cat := rbac.New()
	for _, role := range roles {
		grants = append(grants, cat.PermissionsForRole(role)...)
	}
	if len(tokenPerms) > 0 {
		filtered := make([]string, 0, len(tokenPerms))
		for _, perm := range tokenPerms {
			if rbac.Authorize(grants, perm) {
				filtered = append(filtered, perm)
			}
		}
		grants = filtered
	}
	if aal <= 0 {
		aal = 1
	}
	return &principal{User: u, Roles: roles, Grants: grants, SessID: sessID, AAL: aal, TokenID: tokenID}, nil
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, user appdb.User, aal int) error {
	raw, err := randomCookie()
	if err != nil {
		return err
	}
	if aal <= 0 {
		aal = 1
	}
	sess := appdb.Session{
		ID:        uuid.NewString(),
		ClusterID: user.ClusterID,
		UserID:    user.ID,
		TokenHash: secutil.HashSHA256(raw),
		ExpiresAt: s.now().Add(sessionTTL),
		AAL:       aal,
	}
	if err := s.Store.CreateSession(r.Context(), sess); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.ExpiresAt,
	})
	return nil
}

func (s *Server) writeMe(w http.ResponseWriter, r *http.Request, user appdb.User, aal int) {
	roles, _ := s.Store.UserRoles(r.Context(), user.ID)
	mfaEnabled := false
	if method, _, _, err := s.Store.GetMFAMethod(r.Context(), user.ID); err == nil && method != nil && method.Enabled {
		mfaEnabled = true
	}
	if aal <= 0 {
		aal = 1
	}
	prefs, _ := s.Store.GetUserPrefs(r.Context(), user.ID)
	level, ack, ackAt := prefsJSON(prefs)
	out := map[string]any{
		"user_id":     user.ID,
		"username":    user.Username,
		"roles":       roles,
		"edition":     edition,
		"cluster_id":  user.ClusterID,
		"aal":         aal,
		"mfa_enabled": mfaEnabled,
		"kind":        firstNonEmpty(user.Kind, appdb.UserKindPerson),
		"ux_level":    level,
		"expert_ack":  ack,
	}
	if ackAt != "" {
		out["expert_ack_at"] = ackAt
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ensureCluster(ctx context.Context) error {
	c, err := s.Store.GetCluster(ctx)
	if err != nil {
		return err
	}
	if c != nil {
		return nil
	}
	id := uuid.NewString()
	if err := s.Store.CreateCluster(ctx, appdb.Cluster{ID: id, Name: "local"}); err != nil {
		if existing, _ := s.Store.GetCluster(ctx); existing != nil {
			return nil
		}
		return err
	}
	hash := s.SetupHash
	if hash == "" {
		return errors.New("setup hash not configured")
	}
	return s.Store.PutSetup(ctx, id, hash)
}

func (s *Server) audit(r *http.Request, clusterID, actor, action, result, detail string) {
	body := json.RawMessage(`{}`)
	if detail != "" {
		b, _ := json.Marshal(map[string]string{"detail": detail})
		body = b
	}
	_ = s.Store.InsertAudit(r.Context(), appdb.AuditEvent{
		ID:          uuid.NewString(),
		ClusterID:   clusterID,
		ActorUserID: actor,
		Action:      action,
		Result:      result,
		RemoteAddr:  r.RemoteAddr,
		Detail:      body,
		CreatedAt:   s.now(),
	})
}

func (s *Server) lock() *auth.Lockout {
	if s.Lockout == nil {
		s.Lockout = auth.NewLockout()
	}
	return s.Lockout
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func randomCookie() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// LoadSetupHash reads a hex SHA-256 hash file.
func LoadSetupHash(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
