package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/no-dal/ndl-ce/internal/buildinfo"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/ai"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/cluster"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/journald"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/migrate"
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

// LicenseProbe talks to a licensing API only when a key is present.
type LicenseProbe interface {
	Check(ctx context.Context, key string) error
}

// LogsRPC reads typed journalctl output from the agent.
type LogsRPC interface {
	GetLogs(ctx context.Context, unit string, lines int, since time.Time) (journald.Result, error)
}

// Server is the northbound HTTP API plus static UI.
type Server struct {
	// tokenUseSeen throttles API token usage writes (token|ip -> last write).
	tokenUseSeen sync.Map
	// trusted caches the trusted proxies used to read client addresses.
	trusted trustedProxyCache
	// ipRecovery holds pending IP allowlist recoveries (challenge id -> ip).
	ipRecovery sync.Map
	// ipRecoveryLimit rate-limits IP allowlist recovery attempts.
	ipRecoveryLimit recoveryLimiter
	Store           appdb.Store
	Lockout         *auth.Lockout
	Agent           Agent
	Observer        Observer
	Logs            LogsRPC
	HTTPClient      *http.Client
	Storage         StorageRPC
	Network         NetworkRPC
	Workloads       WorkloadRPC
	IO              IORPC
	QEMU            QemuRPC
	VM              VMRPC
	OCI             OCIRPC
	Docker          DockerRPC
	Backup          BackupRPC
	Object          ObjectRPC
	Verify          VerifyRPC
	Update          UpdateRPC
	GPU             GPURPC
	ZFS             ZFSRPC
	LVM             LVMRPC
	Datastore       DatastoreRPC
	Distributed     DistributedRPC
	K8sProcs        func() []string
	OSDProcs        func() []string
	AICompleter     ai.Completer
	LicenseProbe    LicenseProbe
	Hub             *EventHub
	Migrate         migrate.Runtime
	UI              fs.FS
	// LiveUpdates locates live update state, inbox and trusted keys.
	LiveUpdates LiveUpdatePaths
	// Deployer runs project deployments; nil uses the local agent.
	Deployer        DeployRPC
	Now             func() time.Time
	SetupHash       string
	AllowedUID      uint32
	TLSRequired     bool
	TLSServing      bool // true when this process is listening with TLS
	CertDirty       bool // true when on-disk material changed since TLSServing
	TLSListen       string
	HTTPListen      string
	HTTPSURL        string
	CertDir         ndltls.Dir
	ClusterCA       cluster.CA
	LeaseHolder     string
	Challenges      *ndltls.ChallengeMem
	backupMu        sync.Mutex
	backupWait      *sync.Cond
	backupInflight  map[string]struct{}
	policyMu        sync.Mutex
	policyActive    string
	nightlyBusy     atomic.Bool
	storageMu       sync.Mutex
	storageEvents   []storageEvent
	lastMaintenance time.Time
	lastVerify      time.Time
	alertBusy       atomic.Bool
	updateCheckBusy atomic.Bool
	impact          impactCache
	search          searchState
	updateMu        sync.Mutex
	hold            backupHold
	approvalMu      sync.Mutex
	ddos            *ddosState
	ddosOnce        sync.Once
	ddosMu          sync.Mutex
	edge            *edgeState
	edgeOnce        sync.Once
	edgeMu          sync.Mutex
	repoMove        repoMove
	tunnels         tunnelCache
	opsJobs         opsJobs
	uptimeMu        sync.Mutex
	// DSN is the control database, for the platform backup's pg_dump.
	DSN string
	// Platform overrides parts of the platform backup (tests).
	Platform PlatformHooks
	platform platformRuntime
	// Recovery overrides parts of recovery (tests).
	Recovery RecoveryHooks
	recovery recoveryRuntime
	// CFBaseURL and CFKeyPath override the Cloudflare API root and the token
	// key file (tests). CFPublicCheck replaces public site checks in tests.
	CFBaseURL     string
	CFKeyPath     string
	CFPublicCheck func(ctx context.Context, url string) uptimeResult
	// CFDocsURL overrides Cloudflare's documentation root (tests).
	CFDocsURL string
	cf        cfState
	ch        chState
	// CHKeyPath overrides the key that seals the CodeHold credential, and
	// CodeholdHTTP the client used to reach CodeHold (tests).
	CHKeyPath    string
	CodeholdHTTP *http.Client
	internalOnce sync.Once
	internalMux  http.Handler
	proxies      proxyCache
	extrasMu     sync.Mutex
	extrasBusy   map[string]bool
	// chCLIRun is the latest CodeHold CLI rollout (codehold_cli.go).
	chCLIMu        sync.Mutex
	chCLIRun       *cliRollout
	applyPreparing atomic.Bool
	updateStage    updateStage
	updateStatus   updateStatusCache
	docker         *dockerCache
	dockerOnce     sync.Once
	deployState    deployRuntime
	auto           automationState
	destOverride   *destAgentOverride
	Game           *gameserver.Runtime
	// GameHost runs game server containers and owns their folders. In
	// production it is the root agent; when nil, a LocalHost over Game is
	// used (tests and development).
	// Disk is host disk protection on the agent.
	Disk HostDiskRPC
	// Shield is DDoS protection on the agent; nil turns the loop off.
	Shield ShieldRPC
	// EdgeAgent is edge forwarding on the control node's agent; nil (and
	// no NodeAgents) turns the loop off.
	EdgeAgent EdgeRPC
	// NodeAgents replaces every node's agent by node ID (tests).
	NodeAgents map[string]NodeAgent
	// WorkerUpdaters replaces workers' agents for updates by node ID (tests).
	WorkerUpdaters map[string]HostUpdater
	// Scan measures and cleans up storage in the agent.
	Scan      StorageScanRPC
	GameHost  gameserver.Host
	gameLocal *gameserver.LocalHost
	gameMu    sync.Mutex
	PhysFS    inventory.FS
	// refresh shares live host observations between concurrent reads.
	refresh refreshGate
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
	mux.HandleFunc("GET /api/v1/setup/recover", s.recoveryStatus)
	mux.HandleFunc("POST /api/v1/setup/recover/connect", s.recoverConnect)
	mux.HandleFunc("POST /api/v1/setup/recover/unlock", s.recoverUnlock)
	mux.HandleFunc("POST /api/v1/setup/recover/restore", s.recoverRestore)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("GET /api/v1/me", s.me)
	mux.HandleFunc("GET /api/v1/me/appearance", s.getMyAppearance)
	mux.HandleFunc("GET /api/v1/settings/appearance", s.getAppearance)
	mux.HandleFunc("PUT /api/v1/settings/appearance", s.putAppearance)
	mux.HandleFunc("GET /api/v1/users/{id}/profile", s.getUserPublicProfile)
	mux.HandleFunc("GET /api/v1/search", s.searchQuery)
	mux.HandleFunc("GET /api/v1/search/settings", s.getSearchSettings)
	mux.HandleFunc("PUT /api/v1/search/settings", s.putSearchSettings)
	mux.HandleFunc("GET /api/v1/instance-templates", s.listInstanceTemplates)
	mux.HandleFunc("POST /api/v1/instance-templates", s.createInstanceTemplate)
	mux.HandleFunc("GET /api/v1/instance-templates/{id}", s.getInstanceTemplate)
	mux.HandleFunc("DELETE /api/v1/instance-templates/{id}", s.deleteInstanceTemplate)
	mux.HandleFunc("POST /api/v1/search/index", s.startSearchIndex)
	mux.HandleFunc("POST /api/v1/search/index/cancel", s.cancelSearchIndex)
	mux.HandleFunc("DELETE /api/v1/search/index/deep", s.clearDeepIndex)
	mux.HandleFunc("PATCH /api/v1/me", s.patchMe)
	mux.HandleFunc("GET /api/v1/me/profile", s.getMyProfile)
	mux.HandleFunc("PATCH /api/v1/me/profile", s.patchMyProfile)
	mux.HandleFunc("POST /api/v1/me/password", s.changeMyPassword)
	mux.HandleFunc("POST /api/v1/me/sessions/revoke-others", s.signOutOtherSessions)
	mux.HandleFunc("PUT /api/v1/me/avatar", s.putMyAvatar)
	mux.HandleFunc("DELETE /api/v1/me/avatar", s.deleteMyAvatar)
	mux.HandleFunc("POST /api/v1/me/mfa/disable", s.disableMyMFA)
	mux.HandleFunc("GET /api/v1/me/ip", s.getMyIP)
	mux.HandleFunc("GET /api/v1/me/ip-allowlist", s.getMyAllowlist)
	mux.HandleFunc("PUT /api/v1/me/ip-allowlist", s.putMyAllowlist)
	mux.HandleFunc("GET /api/v1/users/{id}/avatar", s.getUserAvatar)
	mux.HandleFunc("GET /api/v1/users/{id}/ip-allowlist", s.getUserAllowlist)
	mux.HandleFunc("PUT /api/v1/users/{id}/ip-allowlist", s.putUserAllowlist)
	mux.HandleFunc("POST /api/v1/users/{id}/unlock", s.unlockUser)
	mux.HandleFunc("POST /api/v1/users/{id}/mfa/reset", s.resetUserMFA)
	mux.HandleFunc("POST /api/v1/console/ip-recovery", s.consoleIPRecovery)
	mux.HandleFunc("POST /api/v1/console/set-root", s.consoleSetRoot)
	mux.HandleFunc("GET /api/v1/settings/trusted-proxies", s.getTrustedProxies)
	mux.HandleFunc("PUT /api/v1/settings/trusted-proxies", s.putTrustedProxies)
	mux.HandleFunc("GET /api/v1/users", s.listUsers)
	mux.HandleFunc("POST /api/v1/users", s.createUser)
	mux.HandleFunc("GET /api/v1/users/{id}", s.getUser)
	mux.HandleFunc("PATCH /api/v1/users/{id}", s.patchUser)
	mux.HandleFunc("DELETE /api/v1/users/{id}", s.deleteUser)
	mux.HandleFunc("POST /api/v1/users/{id}/password", s.resetUserPassword)
	mux.HandleFunc("POST /api/v1/users/{id}/sessions/revoke", s.revokeUserSessions)
	mux.HandleFunc("POST /api/v1/users/{id}/tokens/revoke", s.revokeUserTokens)
	mux.HandleFunc("GET /api/v1/roles", s.listRoles)
	mux.HandleFunc("GET /api/v1/settings/security", s.getSecuritySettings)
	mux.HandleFunc("PATCH /api/v1/settings/security", s.patchSecuritySettings)
	mux.HandleFunc("GET /api/v1/tokens", s.listTokens)
	mux.HandleFunc("POST /api/v1/tokens", s.createToken)
	mux.HandleFunc("POST /api/v1/tokens/revoke", s.revokeToken)
	mux.HandleFunc("PATCH /api/v1/tokens/{id}", s.updateToken)
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.deleteTokenByID)
	mux.HandleFunc("POST /api/v1/tokens/{id}/roll", s.rollToken)
	mux.HandleFunc("GET /api/v1/tokens/{id}/usage", s.tokenUsage)
	mux.HandleFunc("GET /api/v1/iam/access", s.iamAccess)
	mux.HandleFunc("GET /api/v1/iam/settings", s.getIAMSettings)
	mux.HandleFunc("PUT /api/v1/iam/settings/automation-creators", s.putAutomationCreators)
	mux.HandleFunc("PUT /api/v1/iam/settings/approvals-access", s.putApprovalsAccess)
	mux.HandleFunc("PUT /api/v1/iam/roles/order", s.iamRoleOrder)
	mux.HandleFunc("PUT /api/v1/iam/roles/{name}", s.iamSaveRole)
	mux.HandleFunc("DELETE /api/v1/iam/roles/{name}", s.iamDeleteRole)
	mux.HandleFunc("PUT /api/v1/iam/groups/{id}", s.iamSaveGroup)
	mux.HandleFunc("GET /api/v1/nodes", s.listNodes)
	mux.HandleFunc("POST /api/v1/placement/preview", s.previewPlacement)
	mux.HandleFunc("GET /api/v1/node-groups", s.listNodeGroups)
	mux.HandleFunc("POST /api/v1/node-groups", s.createNodeGroup)
	mux.HandleFunc("POST /api/v1/nodes/{id}/maintain", s.maintainNode)
	mux.HandleFunc("POST /api/v1/nodes/{id}/maintain/exit", s.exitMaintenance)
	mux.HandleFunc("POST /api/v1/nodes/{id}/dest-listen", s.setDestListen)
	mux.HandleFunc("POST /api/v1/nodes/{id}/check", s.checkNode)
	mux.HandleFunc("POST /api/v1/nodes/{id}/update", s.updateNode)
	mux.HandleFunc("GET /api/v1/nodes/{id}/update", s.nodeUpdateStatus)
	mux.HandleFunc("GET /api/v1/nodes/{id}", s.getNode)
	mux.HandleFunc("DELETE /api/v1/nodes/{id}", s.deleteNode)
	mux.HandleFunc("GET /api/v1/nodes/{id}/hardware", s.nodeHardware)
	mux.HandleFunc("GET /api/v1/nodes/{id}/usb", s.listNodeUSB)
	mux.HandleFunc("GET /api/v1/nodes/{id}/pci", s.listNodePCI)
	mux.HandleFunc("GET /api/v1/nodes/{id}/capabilities", s.nodeCapabilities)
	mux.HandleFunc("GET /api/v1/nodes/{id}/metrics", s.nodeMetrics)
	mux.HandleFunc("GET /api/v1/nodes/{id}/logs", s.nodeLogs)
	mux.HandleFunc("GET /api/v1/nodes/{id}/smart", s.nodeSMART)
	mux.HandleFunc("GET /api/v1/nodes/{id}/capacity", s.nodeCapacity)
	mux.HandleFunc("GET /api/v1/nodes/{id}/physical-disks", s.listPhysicalDisks)
	mux.HandleFunc("GET /api/v1/workloads/{id}/metrics", s.workloadMetrics)
	mux.HandleFunc("GET /api/v1/workloads/{id}/logs", s.workloadLogs)
	mux.HandleFunc("GET /api/v1/physical-disks", s.listPhysicalDisks)
	mux.HandleFunc("POST /api/v1/physical-disks/assign", s.assignPhysicalDiskAPI)
	mux.HandleFunc("POST /api/v1/physical-disks/unassign", s.unassignPhysicalDiskAPI)
	mux.HandleFunc("GET /api/v1/workloads/{id}/physical-disks", s.workloadPhysicalDisks)
	mux.HandleFunc("POST /api/v1/workloads/{id}/physical-disks", s.assignWorkloadPhysicalDisk)
	mux.HandleFunc("POST /api/v1/workloads/{id}/physical-disks/{device_id}/remove", s.removeWorkloadPhysicalDisk)
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
	mux.HandleFunc("DELETE /api/v1/networks/{id}", s.deleteNetwork)
	mux.HandleFunc("POST /api/v1/networks/{id}/apply", s.applyNetwork)
	mux.HandleFunc("GET /api/v1/networks/{id}/reservations", s.listReservations)
	mux.HandleFunc("POST /api/v1/networks/{id}/reservations", s.createReservation)
	mux.HandleFunc("POST /api/v1/networks/vlans", s.createVLAN)
	mux.HandleFunc("POST /api/v1/networks/bonds", s.createBond)
	mux.HandleFunc("POST /api/v1/networks/policies", s.createPolicy)
	mux.HandleFunc("POST /api/v1/networks/policies/{id}/apply", s.applyPolicy)
	mux.HandleFunc("POST /api/v1/networks/overlays", s.createOverlay)
	mux.HandleFunc("GET /api/v1/cluster/wg", s.listWG)
	mux.HandleFunc("POST /api/v1/cluster/wg/peers", s.createWGPeer)
	mux.HandleFunc("POST /api/v1/cluster/sessions", s.openClusterSession)
	mux.HandleFunc("GET /api/v1/cluster", s.getCluster)
	mux.HandleFunc("GET /api/v1/cluster/ha", s.getClusterHA)
	mux.HandleFunc("POST /api/v1/cluster/ha/replica", s.configureHAReplica)
	mux.HandleFunc("POST /api/v1/cluster/ha/fence", s.fenceClusterHA)
	mux.HandleFunc("POST /api/v1/cluster/ha/promote", s.promoteClusterHA)
	mux.HandleFunc("GET /api/v1/cluster/update", s.getClusterUpdate)
	mux.HandleFunc("POST /api/v1/cluster/update", s.runClusterUpdate)
	mux.HandleFunc("GET /api/v1/features", s.listFeatures)
	mux.HandleFunc("POST /api/v1/features/{id}/enable", s.enableFeature)
	mux.HandleFunc("POST /api/v1/features/{id}/disable", s.disableFeature)
	mux.HandleFunc("GET /api/v1/game-servers", s.listGameServers)
	mux.HandleFunc("POST /api/v1/game-servers", s.createGameServer)
	mux.HandleFunc("GET /api/v1/game-servers/catalogue", s.gameServerCatalogue)
	mux.HandleFunc("GET /api/v1/game-servers/runtime", s.gameServerRuntime)
	mux.HandleFunc("GET /api/v1/provisioning/capacity", s.provisioningCapacity)
	mux.HandleFunc("GET /api/v1/provisioning/services", s.listProvisionedServices)
	mux.HandleFunc("PUT /api/v1/provisioning/services/{external_id}", s.putProvisionedService)
	mux.HandleFunc("GET /api/v1/provisioning/services/{external_id}", s.getProvisionedService)
	mux.HandleFunc("DELETE /api/v1/provisioning/services/{external_id}", s.deleteProvisionedService)
	mux.HandleFunc("POST /api/v1/provisioning/services/{external_id}/suspend", s.suspendProvisionedService)
	mux.HandleFunc("POST /api/v1/provisioning/services/{external_id}/resume", s.resumeProvisionedService)
	mux.HandleFunc("POST /api/v1/provisioning/services/{external_id}/replace", s.replaceProvisionedService)
	mux.HandleFunc("POST /api/v1/game-servers/catalogue/refresh", s.refreshGameCatalogue)
	mux.HandleFunc("POST /api/v1/game-servers/catalogue/import", s.importGameTemplate)
	mux.HandleFunc("GET /api/v1/game-servers/catalogue/sources", s.listGameSources)
	mux.HandleFunc("POST /api/v1/game-servers/catalogue/sources", s.createGameSource)
	mux.HandleFunc("DELETE /api/v1/game-servers/catalogue/sources/{id}", s.deleteGameSource)
	mux.HandleFunc("GET /api/v1/game-servers/templates", s.listGameTemplates)
	mux.HandleFunc("POST /api/v1/game-servers/preflight", s.preflightGameServer)
	mux.HandleFunc("GET /api/v1/game-servers/search", s.searchGameServers)
	mux.HandleFunc("POST /api/v1/game-servers/fleet", s.gameServerFleet)
	mux.HandleFunc("GET /api/v1/game-servers/settings", s.getGameServerSettings)
	mux.HandleFunc("PUT /api/v1/game-servers/settings", s.putGameServerSettings)
	mux.HandleFunc("GET /api/v1/game-servers/modpacks", s.searchModpacks)
	mux.HandleFunc("GET /api/v1/game-servers/modpacks/{provider}/{id}/versions", s.modpackVersions)
	mux.HandleFunc("GET /api/v1/game-servers/prefs", s.getGameServerPrefs)
	mux.HandleFunc("PUT /api/v1/game-servers/prefs", s.putGameServerPrefs)
	mux.HandleFunc("GET /api/v1/game-servers/{id}", s.getGameServer)
	mux.HandleFunc("PATCH /api/v1/game-servers/{id}", s.patchGameServer)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/start", s.gameServerStart)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/stop", s.gameServerStop)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/restart", s.gameServerRestart)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/kill", s.gameServerKill)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/reinstall", s.gameServerReinstall)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/delete", s.gameServerDelete)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/favorite", s.gameServerFavorite)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/console", s.gameServerConsole)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/console", s.gameServerConsoleSend)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/console/sessions", s.gameServerConsoleSession)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/console/history", s.gameServerConsoleHistory)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/console/favorites", s.gameServerConsoleFavCreate)
	mux.HandleFunc("DELETE /api/v1/game-servers/{id}/console/favorites/{fid}", s.gameServerConsoleFavDelete)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/files", s.gameServerFilesList)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/files/content", s.gameServerFilesRead)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/files/content", s.gameServerFilesWrite)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/files/mkdir", s.gameServerFilesMkdir)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/files/delete", s.gameServerFilesDelete)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/files/move", s.gameServerFilesMove)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/files/download", s.gameServerFilesDownload)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/files/upload", s.gameServerFilesUpload)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/files/extract", s.gameServerFilesExtract)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/files/compress", s.gameServerFilesCompress)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/startup", s.gameServerStartup)
	mux.HandleFunc("PUT /api/v1/game-servers/{id}/startup", s.putGameServerStartup)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/config", s.gameServerConfig)
	mux.HandleFunc("PUT /api/v1/game-servers/{id}/config", s.putGameServerConfig)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/config/diff", s.gameServerConfigDiff)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/config/revert", s.gameServerConfigRevert)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/network", s.gameServerNetwork)
	mux.HandleFunc("PUT /api/v1/game-servers/{id}/network", s.putGameServerNetwork)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/resources", s.gameServerResources)
	mux.HandleFunc("PUT /api/v1/game-servers/{id}/resources", s.putGameServerResources)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/backups", s.listGameBackups)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/backups", s.createGameBackup)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/backups/{bid}/restore", s.restoreGameBackup)
	mux.HandleFunc("DELETE /api/v1/game-servers/{id}/backups/{bid}", s.deleteGameBackup)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/schedules", s.listGameSchedules)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/schedules", s.createGameSchedule)
	mux.HandleFunc("DELETE /api/v1/game-servers/{id}/schedules/{sid}", s.deleteGameSchedule)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/content", s.listGameContent)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/content/search", s.searchGameContent)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/content/install", s.installGameContent)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/content/upload", s.uploadGameContent)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/content/bulk", s.bulkGameContent)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/content/{cid}/enable", s.enableGameContent)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/content/{cid}/disable", s.disableGameContent)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/content/{cid}/update", s.updateGameContent)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/content/{cid}/uninstall", s.uninstallGameContent)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/content/profiles", s.listGameContentProfiles)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/content/profiles", s.createGameContentProfile)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/users", s.listGameUsers)
	mux.HandleFunc("POST /api/v1/game-servers/{id}/users", s.createGameUser)
	mux.HandleFunc("DELETE /api/v1/game-servers/{id}/users/{uid}", s.deleteGameUser)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/activity", s.gameServerActivity)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/timeline", s.gameServerActivity)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/notes", s.getGameNotes)
	mux.HandleFunc("PUT /api/v1/game-servers/{id}/notes", s.putGameNotes)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/diagnostics", s.gameServerDiagnostics)
	mux.HandleFunc("GET /api/v1/game-servers/{id}/tuning", s.gameServerTuning)
	mux.HandleFunc("GET /api/v1/docker", s.getDocker)
	mux.HandleFunc("GET /api/v1/docker/deploy/settings", s.getDeploySettings)
	mux.HandleFunc("PUT /api/v1/docker/deploy/settings", s.putDeploySettings)
	mux.HandleFunc("GET /api/v1/docker/deploy/projects", s.listDeployProjects)
	mux.HandleFunc("POST /api/v1/docker/deploy/projects", s.createDeployProject)
	mux.HandleFunc("GET /api/v1/docker/deploy/projects/{id}", s.getDeployProject)
	mux.HandleFunc("PUT /api/v1/docker/deploy/projects/{id}", s.putDeployProject)
	mux.HandleFunc("DELETE /api/v1/docker/deploy/projects/{id}", s.deleteDeployProject)
	mux.HandleFunc("POST /api/v1/docker/deploy/projects/{id}/deploy", s.deployProjectNow)
	mux.HandleFunc("GET /api/v1/docker/deploy/projects/{id}/deployments", s.listDeployments)
	mux.HandleFunc("POST /api/v1/docker/deploy/projects/{id}/check", s.checkDeployProject)
	mux.HandleFunc("POST /api/v1/docker/deploy/check", s.checkAllDeployProjects)
	mux.HandleFunc("POST /api/v1/docker/deploy/bulk", s.bulkDeploy)
	mux.HandleFunc("POST /api/v1/docker/deploy/projects/{id}/webhook", s.rotateDeployWebhook)
	mux.HandleFunc("POST /api/v1/hooks/deploy/{id}", s.deployWebhook)
	mux.HandleFunc("GET /api/v1/docker/deployments", s.listDeployments)
	mux.HandleFunc("GET /api/v1/docker/deployments/{id}", s.getDeployment)
	mux.HandleFunc("GET /api/v1/docker/deployments/{id}/log", s.getDeploymentLog)
	mux.HandleFunc("GET /api/v1/docker/deployments/{id}/log/download", s.downloadDeploymentLog)
	mux.HandleFunc("POST /api/v1/docker/deployments/{id}/cancel", s.cancelDeployment)
	mux.HandleFunc("POST /api/v1/docker/deployments/{id}/force", s.forceDeployment)
	mux.HandleFunc("GET /api/v1/docker/prefs", s.listDockerPrefs)
	mux.HandleFunc("GET /api/v1/docker/tunnels", s.getDockerTunnels)
	mux.HandleFunc("POST /api/v1/docker/tunnels/action", s.dockerTunnelAction)
	mux.HandleFunc("POST /api/v1/docker/bulk", s.dockerBulk)
	mux.HandleFunc("POST /api/v1/docker/script", s.runDockerScript)
	mux.HandleFunc("GET /api/v1/docker/prune", s.getDockerPrune)
	mux.HandleFunc("PUT /api/v1/docker/prune/settings", s.putDockerPruneSettings)
	mux.HandleFunc("POST /api/v1/docker/prune", s.postDockerPrune)
	mux.HandleFunc("GET /api/v1/ops/jobs", s.listOpsJobs)
	mux.HandleFunc("GET /api/v1/ops/jobs/{id}", s.getOpsJob)
	mux.HandleFunc("GET /api/v1/scripts", s.listSavedScripts)
	mux.HandleFunc("POST /api/v1/scripts", s.saveScript)
	mux.HandleFunc("PUT /api/v1/scripts/{id}", s.saveScript)
	mux.HandleFunc("DELETE /api/v1/scripts/{id}", s.deleteSavedScript)
	mux.HandleFunc("GET /api/v1/uptime/monitors", s.listUptimeMonitors)
	mux.HandleFunc("POST /api/v1/uptime/monitors", s.createUptimeMonitor)
	mux.HandleFunc("PUT /api/v1/uptime/monitors/{id}", s.updateUptimeMonitor)
	mux.HandleFunc("DELETE /api/v1/uptime/monitors/{id}", s.deleteUptimeMonitor)
	mux.HandleFunc("POST /api/v1/uptime/monitors/{id}/check", s.checkUptimeMonitor)
	mux.HandleFunc("POST /api/v1/uptime/check", s.checkAllUptime)
	mux.HandleFunc("POST /api/v1/uptime/import", s.importUptime)
	mux.HandleFunc("PUT /api/v1/docker/prefs", s.putDockerPref)
	mux.HandleFunc("GET /api/v1/docker/machines/{machine_id}/containers/{container_id}", s.getDockerContainer)
	mux.HandleFunc("GET /api/v1/docker/machines/{machine_id}/containers/{container_id}/logs", s.dockerContainerLogs)
	mux.HandleFunc("POST /api/v1/docker/machines/{machine_id}/containers/{container_id}/actions", s.dockerContainerAction)
	mux.HandleFunc("POST /api/v1/docker/machines/{machine_id}/containers/{container_id}/terminal/sessions", s.createDockerTerminal)
	mux.HandleFunc("GET /api/v1/kubernetes", s.getKubernetes)
	mux.HandleFunc("POST /api/v1/kubernetes/start", s.startKubernetes)
	mux.HandleFunc("POST /api/v1/kubernetes/stop", s.stopKubernetes)
	mux.HandleFunc("GET /api/v1/store/apps", s.listStoreApps)
	mux.HandleFunc("GET /api/v1/store/apps/{id}", s.getStoreApp)
	mux.HandleFunc("POST /api/v1/store/apps/import", s.importStoreApp)
	mux.HandleFunc("POST /api/v1/store/apps/{id}/install", s.installStoreApp)
	mux.HandleFunc("POST /api/v1/store/apps/{id}/sign", s.signStoreApp)
	mux.HandleFunc("POST /api/v1/store/apps/{id}/verify", s.verifyStoreApp)
	mux.HandleFunc("GET /api/v1/store/apps/{id}/scans", s.getStoreAppScans)
	mux.HandleFunc("GET /api/v1/store/installations", s.listStoreInstalls)
	mux.HandleFunc("GET /api/v1/store/keys", s.listStoreKeys)
	mux.HandleFunc("POST /api/v1/store/keys", s.createStoreKey)
	mux.HandleFunc("POST /api/v1/store/keys/{id}/revoke", s.revokeStoreKey)
	mux.HandleFunc("GET /api/v1/store/policy", s.getStorePolicy)
	mux.HandleFunc("PUT /api/v1/store/policy", s.setStorePolicy)
	mux.HandleFunc("GET /api/v1/policies", s.listAutomationPolicies)
	mux.HandleFunc("POST /api/v1/policies", s.createAutomationPolicy)
	mux.HandleFunc("POST /api/v1/policies/{id}/apply", s.applyAutomationPolicy)
	mux.HandleFunc("GET /api/v1/policy-runs", s.listPolicyRuns)
	mux.HandleFunc("GET /api/v1/automations/flows", s.listAutomationFlows)
	mux.HandleFunc("POST /api/v1/automations/flows", s.createAutomationFlow)
	mux.HandleFunc("GET /api/v1/automations/flows/{id}", s.getAutomationFlow)
	mux.HandleFunc("PATCH /api/v1/automations/flows/{id}", s.patchAutomationFlow)
	mux.HandleFunc("DELETE /api/v1/automations/flows/{id}", s.deleteAutomationFlow)
	mux.HandleFunc("POST /api/v1/automations/flows/{id}/validate", s.validateAutomationFlow)
	mux.HandleFunc("POST /api/v1/automations/flows/{id}/publish", s.publishAutomationFlow)
	mux.HandleFunc("POST /api/v1/automations/flows/{id}/enable", s.setAutomationFlowEnabled(true))
	mux.HandleFunc("POST /api/v1/automations/flows/{id}/disable", s.setAutomationFlowEnabled(false))
	mux.HandleFunc("POST /api/v1/automations/flows/{id}/clone", s.cloneAutomationFlow)
	mux.HandleFunc("POST /api/v1/automations/flows/{id}/execute", s.executeAutomationFlow)
	mux.HandleFunc("POST /api/v1/automations/flows/{id}/dry-run", s.dryRunAutomationFlow)
	mux.HandleFunc("GET /api/v1/automations/flows/{id}/versions", s.listAutomationFlowVersions)
	mux.HandleFunc("GET /api/v1/automations/flows/{id}/runs", s.listAutomationFlowRuns)
	mux.HandleFunc("GET /api/v1/automations/flows/{id}/webhook", s.getAutomationWebhook)
	mux.HandleFunc("POST /api/v1/automations/flows/{id}/webhook", s.putAutomationWebhook(false))
	mux.HandleFunc("POST /api/v1/automations/flows/{id}/webhook/rotate", s.putAutomationWebhook(true))
	mux.HandleFunc("GET /api/v1/automations/runs", s.listAutomationRuns)
	mux.HandleFunc("GET /api/v1/automations/runs/{id}", s.getAutomationRun)
	mux.HandleFunc("POST /api/v1/automations/runs/{id}/cancel", s.cancelAutomationRun)
	mux.HandleFunc("POST /api/v1/automations/runs/{id}/retry", s.retryAutomationRun)
	mux.HandleFunc("POST /api/v1/automations/runs/{id}/approve", s.approveAutomationRun)
	mux.HandleFunc("GET /api/v1/automations/runs/{id}/nodes", s.listAutomationRunNodes)
	mux.HandleFunc("GET /api/v1/automations/runs/{id}/logs", s.listAutomationRunLogs)
	mux.HandleFunc("GET /api/v1/automations/connections", s.listAutomationConnections)
	mux.HandleFunc("POST /api/v1/automations/connections", s.createAutomationConnection)
	mux.HandleFunc("PATCH /api/v1/automations/connections/{id}", s.patchAutomationConnection)
	mux.HandleFunc("DELETE /api/v1/automations/connections/{id}", s.deleteAutomationConnection)
	mux.HandleFunc("POST /api/v1/automations/connections/{id}/test", s.testAutomationConnection)
	mux.HandleFunc("GET /api/v1/automations/targets", s.automationTargetsAPI)
	mux.HandleFunc("GET /api/v1/automations/nodes", s.automationCatalogue)
	mux.HandleFunc("GET /api/v1/automations/templates", s.automationTemplatesAPI)
	mux.HandleFunc("POST /api/v1/hooks/flows/{token}", s.automationHookIn)
	mux.HandleFunc("GET /api/v1/approvals", s.listApprovals)
	mux.HandleFunc("GET /api/v1/approvals/{flow}/{version}", s.getApproval)
	mux.HandleFunc("POST /api/v1/approvals/{flow}/{version}/approve", s.decideApproval(true))
	mux.HandleFunc("POST /api/v1/approvals/{flow}/{version}/deny", s.decideApproval(false))
	mux.HandleFunc("GET /api/v1/notifications", s.listNotifications)
	mux.HandleFunc("GET /api/v1/notifications/unread-count", s.notificationsUnread)
	mux.HandleFunc("POST /api/v1/notifications/read", s.markNotificationsRead)
	mux.HandleFunc("GET /api/v1/ai/providers", s.listAIProviders)
	mux.HandleFunc("POST /api/v1/ai/providers", s.createAIProvider)
	mux.HandleFunc("GET /api/v1/ai/profiles", s.listAIProfiles)
	mux.HandleFunc("POST /api/v1/ai/profiles", s.createAIProfile)
	mux.HandleFunc("POST /api/v1/ai/ask", s.aiAsk)
	mux.HandleFunc("GET /api/v1/ai/plans", s.listAIPlans)
	mux.HandleFunc("POST /api/v1/ai/plans", s.createAIPlan)
	mux.HandleFunc("GET /api/v1/ai/plans/{id}", s.getAIPlan)
	mux.HandleFunc("POST /api/v1/ai/plans/{id}/approve", s.approveAIPlan)
	mux.HandleFunc("GET /api/v1/settings/license", s.getLicense)
	mux.HandleFunc("GET /api/v1/license", s.getLicense)
	mux.HandleFunc("POST /api/v1/settings/license", s.activateLicense)
	mux.HandleFunc("POST /api/v1/settings/license/clear", s.clearLicense)
	mux.HandleFunc("GET /api/v1/migration/adapters", s.listMigrationAdapters)
	mux.HandleFunc("GET /api/v1/migration/modes", s.listMigrationModes)
	mux.HandleFunc("GET /api/v1/migration/sources", s.listMigrationSources)
	mux.HandleFunc("POST /api/v1/migration/sources", s.createMigrationSource)
	mux.HandleFunc("GET /api/v1/migration/sources/{id}", s.getMigrationSource)
	mux.HandleFunc("DELETE /api/v1/migration/sources/{id}", s.deleteMigrationSource)
	mux.HandleFunc("POST /api/v1/migration/sources/{id}/discover", s.discoverMigrationSource)
	mux.HandleFunc("POST /api/v1/migration/compatibility", s.migrationCompatibility)
	mux.HandleFunc("POST /api/v1/migration/plans", s.createMigrationPlan)
	mux.HandleFunc("GET /api/v1/migration/jobs", s.listMigrationJobs)
	mux.HandleFunc("POST /api/v1/migration/jobs", s.startMigrationJob)
	mux.HandleFunc("GET /api/v1/migration/jobs/{id}", s.getMigrationJob)
	mux.HandleFunc("POST /api/v1/migration/jobs/{id}/cancel", s.cancelMigrationJob)
	mux.HandleFunc("POST /api/v1/migration/jobs/{id}/retry", s.retryMigrationJob)
	mux.HandleFunc("GET /api/v1/migration/jobs/{id}/diagnostics", s.getMigrationJobDiagnostics)
	mux.HandleFunc("POST /api/v1/migration/jobs/{id}/cleanup", s.cleanupMigrationStaging)
	mux.HandleFunc("POST /api/v1/migration/import/disk", s.importMigrationDisk)
	mux.HandleFunc("POST /api/v1/migration/import/bundle", s.importMigrationBundle)
	mux.HandleFunc("POST /api/v1/migration/export", s.exportMigration)
	mux.HandleFunc("POST /api/v1/cluster/join-tokens", s.createJoinToken)
	mux.HandleFunc("POST /api/v1/cluster/join", s.joinCluster)
	mux.HandleFunc("GET /api/v1/cluster/join-settings", s.getJoinSettings)
	mux.HandleFunc("PUT /api/v1/cluster/join-settings", s.putJoinSettings)
	mux.HandleFunc("POST /api/v1/cluster/nodes/{id}/revoke", s.revokeClusterNode)
	mux.HandleFunc("GET /api/v1/workloads", s.listWorkloads)
	mux.HandleFunc("POST /api/v1/workloads", s.createWorkload)
	mux.HandleFunc("GET /api/v1/workload-extras", s.listWorkloadExtras)
	mux.HandleFunc("POST /api/v1/workload-extras/bulk", s.bulkWorkloadExtras)
	mux.HandleFunc("POST /api/v1/workloads/{id}/setup-extras", s.setupWorkloadExtras)
	mux.HandleFunc("GET /api/v1/workloads/{id}/extensions", s.listWorkloadExtensions)
	mux.HandleFunc("GET /api/v1/cloudflare/credentials", s.listCloudflareCredentials)
	mux.HandleFunc("POST /api/v1/cloudflare/credentials", s.createCloudflareCredential)
	mux.HandleFunc("PUT /api/v1/cloudflare/credentials/{id}", s.updateCloudflareCredential)
	mux.HandleFunc("POST /api/v1/cloudflare/credentials/{id}/validate", s.validateCloudflareCredential)
	mux.HandleFunc("DELETE /api/v1/cloudflare/credentials/{id}", s.deleteCloudflareCredential)
	mux.HandleFunc("GET /api/v1/cloudflare/inventory", s.getCloudflareInventory)
	mux.HandleFunc("GET /api/v1/cloudflare/machines", s.listCloudflareMachines)
	mux.HandleFunc("GET /api/v1/cloudflare/r2/buckets", s.listR2Buckets)
	mux.HandleFunc("POST /api/v1/cloudflare/r2/buckets", s.createR2Bucket)
	mux.HandleFunc("POST /api/v1/cloudflare/r2/buckets/delete", s.deleteR2Bucket)
	mux.HandleFunc("GET /api/v1/cloudflare/permissions", s.cloudflarePermissionsGuide)
	mux.HandleFunc("POST /api/v1/cloudflare/tunnels", s.createCloudflareTunnel)
	mux.HandleFunc("PATCH /api/v1/cloudflare/tunnels/{id}", s.renameCloudflareTunnel)
	mux.HandleFunc("POST /api/v1/cloudflare/tunnels/{id}/delete", s.deleteCloudflareTunnel)
	mux.HandleFunc("POST /api/v1/cloudflare/tunnels/{id}/connectors", s.cloudflareConnector)
	mux.HandleFunc("POST /api/v1/cloudflare/routes", s.upsertCloudflareRoute)
	mux.HandleFunc("POST /api/v1/cloudflare/routes/plan", s.planCloudflareRoute)
	mux.HandleFunc("POST /api/v1/cloudflare/routes/enabled", s.setCloudflareRouteEnabled)
	mux.HandleFunc("POST /api/v1/cloudflare/routes/delete", s.deleteCloudflareRoute)
	mux.HandleFunc("POST /api/v1/cloudflare/routes/sync-ip", s.syncCloudflareRouteIP)
	mux.HandleFunc("POST /api/v1/cloudflare/routes/diagnose", s.diagnoseCloudflareRoute)
	mux.HandleFunc("POST /api/v1/cloudflare/move/preview", s.previewCloudflareMove)
	mux.HandleFunc("POST /api/v1/cloudflare/move", s.moveCloudflareRoutes)
	mux.HandleFunc("GET /api/v1/workloads/{id}/cloudflare", s.cloudflareForWorkload)
	mux.HandleFunc("GET /api/v1/codehold-cli", s.getCodeholdCLI)
	mux.HandleFunc("PUT /api/v1/codehold-cli/settings", s.putCodeholdCLISettings)
	mux.HandleFunc("GET /api/v1/codehold-cli/rollout", s.getCodeholdCLIRollout)
	mux.HandleFunc("POST /api/v1/codehold-cli/rollout", s.startCodeholdCLIRollout)
	mux.HandleFunc("GET /api/v1/codehold", s.getCodehold)
	mux.HandleFunc("POST /api/v1/codehold/detect", s.detectCodehold)
	mux.HandleFunc("POST /api/v1/codehold/connect", s.connectCodehold)
	mux.HandleFunc("POST /api/v1/codehold/disconnect", s.disconnectCodehold)
	mux.HandleFunc("PUT /api/v1/codehold/settings", s.putCodeholdSettings)
	mux.HandleFunc("POST /api/v1/codehold/runners/{id}/retry", s.retryCodeholdRunner)
	mux.HandleFunc("POST /api/v1/codehold/disable", s.disableCodehold)
	mux.HandleFunc("GET /api/v1/codehold/pending", s.getCodeholdPending)
	mux.HandleFunc("POST /api/v1/codehold/pending", s.actOnCodeholdPending)
	mux.HandleFunc("GET /api/v1/proxies/{kind}", s.getProxies)
	mux.HandleFunc("POST /api/v1/proxies/{kind}/sites", s.upsertProxySite)
	mux.HandleFunc("POST /api/v1/proxies/{kind}/sites/delete", s.deleteProxySite)
	mux.HandleFunc("POST /api/v1/proxies/{kind}/service", s.proxyService)
	mux.HandleFunc("POST /api/v1/workloads/{id}/cloudflare/publish", s.publishWorkload)
	mux.HandleFunc("POST /api/v1/workloads/{id}/extensions", s.changeWorkloadExtensions)
	mux.HandleFunc("POST /api/v1/workloads/bulk-delete", s.bulkDeleteWorkloads)
	mux.HandleFunc("POST /api/v1/workloads/import", s.importVM)
	mux.HandleFunc("GET /api/v1/workloads/{id}/migration-diagnostics", s.getWorkloadMigrationDiagnostics)
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
	mux.HandleFunc("POST /api/v1/workloads/{id}/migrate", s.migrateWorkload)
	mux.HandleFunc("GET /api/v1/workloads/{id}/migrate", s.getWorkloadMigrate)
	mux.HandleFunc("POST /api/v1/workloads/{id}/export", s.exportVM)
	mux.HandleFunc("POST /api/v1/workloads/{id}/usb", s.attachUSB)
	mux.HandleFunc("POST /api/v1/workloads/{id}/pci", s.attachPCI)
	mux.HandleFunc("GET /api/v1/templates", s.listTemplates)
	mux.HandleFunc("POST /api/v1/templates", s.createTemplate)
	mux.HandleFunc("POST /api/v1/templates/{id}/deploy", s.deployTemplate)
	mux.HandleFunc("POST /api/v1/workloads/{id}/console/sessions", s.createVMConsole)
	mux.HandleFunc("POST /api/v1/nodes/{id}/terminal/sessions", s.createNodeTerminal)
	mux.HandleFunc("POST /api/v1/workloads/{id}/terminal/sessions", s.createWorkloadTerminal)
	mux.HandleFunc("GET /api/v1/io/sessions", s.listIOSessions)
	mux.HandleFunc("GET /api/v1/io/sessions/{id}", s.getIOSession)
	mux.HandleFunc("GET /api/v1/io/sessions/{id}/ws", s.ioSessionWS)
	mux.HandleFunc("POST /api/v1/io/sessions/{id}/attach", s.attachIOSession)
	mux.HandleFunc("POST /api/v1/io/sessions/{id}/end", s.endIOSession)
	mux.HandleFunc("GET /api/v1/nodes/{id}/files", s.nodeFilesList)
	mux.HandleFunc("GET /api/v1/nodes/{id}/files/stat", s.nodeFilesStat)
	mux.HandleFunc("GET /api/v1/nodes/{id}/files/download", s.nodeFilesDownload)
	mux.HandleFunc("POST /api/v1/nodes/{id}/files/upload", s.nodeFilesUpload)
	mux.HandleFunc("POST /api/v1/nodes/{id}/files/mkdir", s.nodeFilesMkdir)
	mux.HandleFunc("POST /api/v1/nodes/{id}/files/delete", s.nodeFilesDelete)
	mux.HandleFunc("POST /api/v1/nodes/{id}/files/move", s.nodeFilesMove)
	mux.HandleFunc("POST /api/v1/nodes/{id}/files/copy", s.nodeFilesCopy)
	mux.HandleFunc("POST /api/v1/nodes/{id}/files/chmod", s.nodeFilesChmod)
	mux.HandleFunc("POST /api/v1/nodes/{id}/files/chown", s.nodeFilesChown)
	mux.HandleFunc("GET /api/v1/nodes/{id}/files/content", s.nodeFilesContent)
	mux.HandleFunc("GET /api/v1/workloads/{id}/files", s.workloadFilesList)
	mux.HandleFunc("GET /api/v1/workloads/{id}/files/stat", s.workloadFilesStat)
	mux.HandleFunc("GET /api/v1/workloads/{id}/files/download", s.workloadFilesDownload)
	mux.HandleFunc("POST /api/v1/workloads/{id}/files/upload", s.workloadFilesUpload)
	mux.HandleFunc("POST /api/v1/workloads/{id}/files/mkdir", s.workloadFilesMkdir)
	mux.HandleFunc("POST /api/v1/workloads/{id}/files/delete", s.workloadFilesDelete)
	mux.HandleFunc("POST /api/v1/workloads/{id}/files/move", s.workloadFilesMove)
	mux.HandleFunc("POST /api/v1/workloads/{id}/files/copy", s.workloadFilesCopy)
	mux.HandleFunc("POST /api/v1/workloads/{id}/files/chmod", s.workloadFilesChmod)
	mux.HandleFunc("POST /api/v1/workloads/{id}/files/chown", s.workloadFilesChown)
	mux.HandleFunc("GET /api/v1/workloads/{id}/files/content", s.workloadFilesContent)
	mux.HandleFunc("POST /api/v1/lab/qemu-proto", s.labQemuProtoStart)
	mux.HandleFunc("GET /api/v1/lab/qemu-proto", s.labQemuProtoStatus)
	mux.HandleFunc("POST /api/v1/lab/qemu-proto/stop", s.labQemuProtoStop)
	mux.HandleFunc("POST /api/v1/lab/qemu-proto/kill", s.labQemuProtoKill)
	mux.HandleFunc("GET /api/v1/workloads/{id}/snapshots", s.listSnapshots)
	mux.HandleFunc("POST /api/v1/workloads/{id}/snapshots", s.createSnapshot)
	mux.HandleFunc("POST /api/v1/workloads/{id}/snapshots/flatten", s.flattenSnapshots)
	mux.HandleFunc("POST /api/v1/snapshots/{id}/rollback", s.rollbackSnapshot)
	mux.HandleFunc("DELETE /api/v1/snapshots/{id}", s.deleteSnapshot)
	mux.HandleFunc("GET /api/v1/backups/targets", s.listBackupTargets)
	mux.HandleFunc("POST /api/v1/backups/targets", s.createBackupTarget)
	mux.HandleFunc("POST /api/v1/backups/targets/{id}/test", s.testBackupTarget)
	mux.HandleFunc("GET /api/v1/backups/policies", s.listBackupPolicies)
	mux.HandleFunc("POST /api/v1/backups/policies", s.createBackupPolicy)
	mux.HandleFunc("PATCH /api/v1/backups/policies/{id}", s.updateBackupPolicy)
	mux.HandleFunc("POST /api/v1/backups/approvals/{id}", s.decideBackupApproval)
	mux.HandleFunc("GET /api/v1/ddos", s.getDDoS)
	mux.HandleFunc("PUT /api/v1/ddos/settings", s.putDDoSSettings)
	mux.HandleFunc("POST /api/v1/ddos/targets", s.createDDoSTarget)
	mux.HandleFunc("PUT /api/v1/ddos/targets/{id}", s.updateDDoSTarget)
	mux.HandleFunc("DELETE /api/v1/ddos/targets/{id}", s.deleteDDoSTarget)
	mux.HandleFunc("POST /api/v1/ddos/bans", s.banDDoS)
	mux.HandleFunc("DELETE /api/v1/ddos/bans/{addr}", s.unbanDDoS)
	mux.HandleFunc("GET /api/v1/ddos/events", s.listDDoSEvents)
	mux.HandleFunc("POST /api/v1/ddos/apply", s.applyDDoS)
	mux.HandleFunc("GET /api/v1/edge", s.getEdge)
	mux.HandleFunc("POST /api/v1/edge/forwards", s.createEdgeForward)
	mux.HandleFunc("PUT /api/v1/edge/forwards/{id}", s.updateEdgeForward)
	mux.HandleFunc("DELETE /api/v1/edge/forwards/{id}", s.deleteEdgeForward)
	mux.HandleFunc("PUT /api/v1/edge/nodes/{id}", s.putEdgeNode)
	mux.HandleFunc("POST /api/v1/edge/apply", s.applyEdge)
	mux.HandleFunc("DELETE /api/v1/backups/policies/{id}", s.deleteBackupPolicy)
	mux.HandleFunc("POST /api/v1/backups/policies/{id}/run", s.runBackupPolicy)
	mux.HandleFunc("POST /api/v1/backups/policies/{id}/queue/clear", s.clearPolicyQueue)
	mux.HandleFunc("DELETE /api/v1/backups/policies/{id}/runs", s.deleteBackupPolicyHistory)
	mux.HandleFunc("POST /api/v1/backups/reset", s.resetBackups)
	mux.HandleFunc("GET /api/v1/backups/runs", s.listBackupRuns)
	mux.HandleFunc("GET /api/v1/backups/artifacts", s.listBackupArtifacts)
	mux.HandleFunc("GET /api/v1/backups/dr-export", s.exportBackupDR)
	mux.HandleFunc("POST /api/v1/backups/run", s.runBackup)
	mux.HandleFunc("GET /api/v1/backups/workspace", s.getBackupWorkspace)
	mux.HandleFunc("PATCH /api/v1/backups/workspace", s.patchBackupWorkspace)
	mux.HandleFunc("GET /api/v1/backups/restore-points", s.listBackupRestorePoints)
	mux.HandleFunc("GET /api/v1/backups/storage", s.getBackupStorage)
	mux.HandleFunc("POST /api/v1/backups/workspace/maintenance", s.runBackupMaintenanceAPI)
	mux.HandleFunc("POST /api/v1/backups/workspace/relocate", s.relocateBackupRepository)
	mux.HandleFunc("GET /api/v1/backups/targets/{id}/usage", s.getBackupTargetUsage)
	mux.HandleFunc("POST /api/v1/backups/targets/{id}/wipe", s.wipeBackupTarget)
	mux.HandleFunc("GET /api/v1/backups/git-holds", s.listBackupGitHolds)
	mux.HandleFunc("GET /api/v1/workloads/{id}/backup-files", s.getWorkloadBackupFiles)
	mux.HandleFunc("GET /api/v1/workloads/{id}/backup-rules", s.getWorkloadBackupRules)
	mux.HandleFunc("PUT /api/v1/workloads/{id}/backup-rules", s.putWorkloadBackupRules)
	mux.HandleFunc("POST /api/v1/backups/workloads/{id}/delete", s.deleteWorkloadBackups)
	mux.HandleFunc("GET /api/v1/live-updates", s.getLiveUpdates)
	mux.HandleFunc("POST /api/v1/live-updates/bundles", s.postLiveUpdateBundle)
	mux.HandleFunc("POST /api/v1/live-updates/rollback", s.postLiveUpdateRollback)
	mux.HandleFunc("GET /api/v1/platform-backup", s.getPlatformBackup)
	mux.HandleFunc("PATCH /api/v1/platform-backup", s.patchPlatformBackup)
	mux.HandleFunc("POST /api/v1/platform-backup/run", s.runPlatformBackupAPI)
	mux.HandleFunc("POST /api/v1/platform-backup/verify", s.verifyPlatformBackupAPI)
	mux.HandleFunc("POST /api/v1/platform-backup/rotate", s.rotatePlatformKey)
	mux.HandleFunc("GET /api/v1/recovery", s.getRecovery)
	mux.HandleFunc("GET /api/v1/backups/workload-options", s.listBackupWorkloadOptions)
	mux.HandleFunc("PUT /api/v1/backups/workload-options/{id}", s.setBackupWorkloadOptions)
	mux.HandleFunc("POST /api/v1/recovery/workloads/{id}/restore", s.restoreRecoveredWorkloadAPI)
	mux.HandleFunc("POST /api/v1/backups/git-holds/{workload}", s.resolveBackupGitHold)
	mux.HandleFunc("GET /api/v1/backups/locations", s.listBackupLocations)
	mux.HandleFunc("POST /api/v1/backups/key/export", s.exportBackupKey)
	mux.HandleFunc("POST /api/v1/backups/workspace/verify", s.runBackupVerifyAPI)
	mux.HandleFunc("POST /api/v1/backups/artifacts/{id}/protect", s.protectBackupArtifact)
	mux.HandleFunc("DELETE /api/v1/backups/artifacts/{id}", s.deleteBackupArtifact)
	mux.HandleFunc("POST /api/v1/backups/deleted-workloads/purge", s.purgeDeletedWorkloadBackups)
	mux.HandleFunc("POST /api/v1/backups/scope-preview", s.previewBackupScope)
	mux.HandleFunc("POST /api/v1/backups/artifacts/{id}/restore", s.restoreBackup)
	mux.HandleFunc("POST /api/v1/backups/artifacts/{id}/verify", s.verifyBackupArtifact)
	mux.HandleFunc("POST /api/v1/backups/artifacts/{id}/restore-file", s.restoreBackupFile)
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
	mux.HandleFunc("POST /api/v1/updates/rollback/undo", s.undoRollback)
	mux.HandleFunc("POST /api/v1/updates/rollback/wipe", s.wipeRollback)
	mux.HandleFunc("POST /api/v1/updates/repository", s.enableUpdateRepository)
	mux.HandleFunc("POST /api/v1/auth/mfa/verify", s.verifyMFA)
	mux.HandleFunc("GET /api/v1/mfa", s.getMFA)
	mux.HandleFunc("POST /api/v1/mfa/enroll", s.enrollMFA)
	mux.HandleFunc("POST /api/v1/mfa/confirm", s.confirmMFA)
	mux.HandleFunc("GET /api/v1/audit", s.listAudit)
	mux.HandleFunc("POST /api/v1/audit/clear", s.clearActivityLog)
	mux.HandleFunc("GET /api/v1/groups", s.listGroups)
	mux.HandleFunc("POST /api/v1/groups", s.createGroup)
	mux.HandleFunc("POST /api/v1/groups/{id}/members", s.addGroupMember)
	mux.HandleFunc("POST /api/v1/groups/{id}/roles", s.bindGroupRole)
	mux.HandleFunc("GET /api/v1/service-principals", s.listServicePrincipals)
	mux.HandleFunc("POST /api/v1/service-principals", s.createServicePrincipal)
	mux.HandleFunc("GET /api/v1/service-principals/{id}/references", s.servicePrincipalRefsAPI)
	mux.HandleFunc("DELETE /api/v1/service-principals/{id}", s.deleteServicePrincipal)
	mux.HandleFunc("POST /api/v1/secrets/reveal", s.revealSecret)
	mux.HandleFunc("POST /api/v1/cluster/destroy", s.destroyCluster)
	mux.HandleFunc("POST /api/v1/storage/volumes/{id}/unlock", s.unlockVolume)
	mux.HandleFunc("GET /api/v1/gpus", s.listGPUs)
	mux.HandleFunc("GET /api/v1/gpus/runtime", s.gpuRuntime)
	mux.HandleFunc("POST /api/v1/gpus/runtime/install", s.installGPURuntime)
	mux.HandleFunc("POST /api/v1/gpus/assign", s.assignGPU)
	mux.HandleFunc("POST /api/v1/gpus/unassign", s.unassignGPU)
	mux.HandleFunc("GET /api/v1/workloads/{id}/gpus", s.workloadGPUs)
	mux.HandleFunc("GET /api/v1/workloads/{id}/storage", s.getWorkloadStorage)
	mux.HandleFunc("PUT /api/v1/workloads/{id}/storage/mounts", s.putWorkloadMounts)
	mux.HandleFunc("PUT /api/v1/workloads/{id}/storage/mounts/size", s.resizeWorkloadMount)
	mux.HandleFunc("GET /api/v1/workloads/{id}/diagnostics/gpu", s.workloadGPUDiagnostics)
	mux.HandleFunc("POST /api/v1/workloads/{id}/gpus/reapply", s.reapplyWorkloadGPU)
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
	mux.HandleFunc("POST /api/v1/stacks/{id}/members", s.addOCIGroupMember)
	mux.HandleFunc("DELETE /api/v1/stacks/{id}/members/{memberId}", s.removeOCIGroupMember)
	mux.HandleFunc("POST /api/v1/stacks/{id}/power", s.groupPower)
	mux.HandleFunc("PUT /api/v1/workloads/{id}/oci", s.updateOCIConfig)
	mux.HandleFunc("GET /api/v1/host/disk", s.getHostDisk)
	mux.HandleFunc("GET /api/v1/host/disk/usage", s.getHostDiskUsage)
	mux.HandleFunc("GET /api/v1/storage/scan/targets", s.listScanTargets)
	mux.HandleFunc("POST /api/v1/storage/scan", s.startStorageScan)
	mux.HandleFunc("GET /api/v1/storage/scan/{id}", s.getStorageScan)
	mux.HandleFunc("POST /api/v1/storage/scan/{id}/cancel", s.cancelStorageScan)
	mux.HandleFunc("POST /api/v1/storage/scan/{id}/delete", s.deleteScannedItems)
	mux.HandleFunc("POST /api/v1/host/disk/cleanup", s.cleanupHostDisk)
	mux.HandleFunc("POST /api/v1/host/disk/reserve/release", s.releaseHostDiskReserve)
	mux.HandleFunc("GET /api/v1/storage/zfs", s.zfsRuntime)
	mux.HandleFunc("POST /api/v1/storage/zfs/import", s.importZFS)
	mux.HandleFunc("POST /api/v1/storage/zfs/create", s.createZFS)
	mux.HandleFunc("GET /api/v1/storage/lvm", s.lvmRuntime)
	mux.HandleFunc("POST /api/v1/storage/lvm/create", s.createLVM)
	mux.HandleFunc("GET /api/v1/storage/datastores", s.datastoreRuntime)
	mux.HandleFunc("POST /api/v1/storage/nfs", s.createNFS)
	mux.HandleFunc("POST /api/v1/storage/smb", s.createSMB)
	mux.HandleFunc("POST /api/v1/storage/iscsi", s.createISCSI)
	mux.HandleFunc("GET /api/v1/storage/distributed", s.distributedRuntime)
	mux.HandleFunc("POST /api/v1/storage/distributed", s.attachDistributed)
	mux.HandleFunc("POST /api/v1/storage/distributed/osds", s.createDistributedOSD)
	mux.HandleFunc("POST /api/v1/storage/distributed/osds/start", s.startDistributedOSD)
	mux.HandleFunc("POST /api/v1/storage/distributed/osds/stop", s.stopDistributedOSD)
	// Deleting configurable resources (deletions.go).
	mux.HandleFunc("DELETE /api/v1/storage/pools/{id}", s.deleteConfig(appdb.ConfigStoragePool, rbac.StoragePoolCreate, "storage.pool.delete", s.checkPoolUnused))
	mux.HandleFunc("DELETE /api/v1/storage/volumes/{id}", s.deleteVolume)
	mux.HandleFunc("DELETE /api/v1/backups/targets/{id}", s.deleteBackupTarget(s.deleteConfig(appdb.ConfigBackupTarget, rbac.BackupCreate, "backup.target.delete", s.checkBackupTargetUnused)))
	mux.HandleFunc("DELETE /api/v1/templates/{id}", s.deleteConfig(appdb.ConfigVMTemplate, rbac.ComputeCreate, "template.delete", nil))
	mux.HandleFunc("DELETE /api/v1/groups/{id}", s.deleteConfig(appdb.ConfigGroup, rbac.IdentityGroupManage, "group.delete", nil))
	mux.HandleFunc("DELETE /api/v1/alerts/{id}", s.deleteConfig(appdb.ConfigAlertRule, rbac.AlertManage, "alert.rule.delete", nil))
	mux.HandleFunc("DELETE /api/v1/alerts/channels/{id}", s.deleteConfig(appdb.ConfigNotifyChannel, rbac.AlertManage, "alert.channel.delete", nil))
	mux.HandleFunc("DELETE /api/v1/registries/{id}", s.deleteConfig(appdb.ConfigRegistry, rbac.ComputeCreate, "registry.delete", nil))
	mux.HandleFunc("DELETE /api/v1/node-groups/{id}", s.deleteConfig(appdb.ConfigNodeGroup, rbac.NodeUpdate, "node_group.delete", nil))
	mux.HandleFunc("DELETE /api/v1/policies/{id}", s.automationGate(s.deleteConfig(appdb.ConfigPolicy, rbac.PolicyApply, "policy.delete", nil)))
	mux.HandleFunc("DELETE /api/v1/ai/providers/{id}", s.deleteConfig(appdb.ConfigAIProvider, rbac.AIManage, "ai.provider.delete", nil))
	mux.HandleFunc("DELETE /api/v1/ai/profiles/{id}", s.deleteConfig(appdb.ConfigAIProfile, rbac.AIManage, "ai.profile.delete", nil))
	mux.HandleFunc("DELETE /api/v1/networks/vlans/{id}", s.deleteVLAN)
	mux.HandleFunc("DELETE /api/v1/networks/bonds/{id}", s.deleteBond)
	mux.HandleFunc("DELETE /api/v1/networks/overlays/{id}", s.deleteOverlay)
	mux.HandleFunc("DELETE /api/v1/networks/policies/{id}", s.deleteNetPolicy)
	mux.HandleFunc("DELETE /api/v1/cluster/wg/peers/{id}", s.deleteWGPeer)
	mux.HandleFunc("DELETE /api/v1/storage/images/{id}", s.deleteImage)
	if s.UI != nil {
		mux.Handle("/", s.spa())
	}
	return countChanges(s.markReadRequests(mux))
}

func (s *Server) spa() http.Handler {
	fileServer := http.FileServer(http.FS(s.UI))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		rel := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if rel == "" || rel == "index.html" {
			s.writeUIIndex(w)
			return
		}
		if _, err := fs.Stat(s.UI, rel); err == nil {
			if strings.HasPrefix(rel, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(rel, "assets/") || uiLooksLikeStaticAsset(rel) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		s.writeUIIndex(w)
	})
}

func (s *Server) writeUIIndex(w http.ResponseWriter) {
	// no-transform keeps Cloudflare from injecting its Web Analytics beacon,
	// which the page's content security policy and ad blockers reject.
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	index, err := fs.ReadFile(s.UI, "index.html")
	if err != nil {
		index = []byte("<!doctype html><title>No-dal</title>")
	}
	_, _ = w.Write(index)
}

func uiLooksLikeStaticAsset(rel string) bool {
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	return strings.Contains(base, ".")
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	open, _ := s.setupOpen(r.Context())
	status := "ok"
	out := map[string]any{
		"status":      status,
		"service":     "ndl-control",
		"setup_open":  open,
		"tls_enabled": s.TLSRequired,
	}
	// Builds are for the local updater (root on the control socket), not
	// for anyone who can reach the public health check.
	if _, local, _ := s.unixRootPrincipal(r); !local {
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["version"], out["build"] = buildinfo.Version, buildinfo.ID()
	if a, ok := s.Agent.(interface {
		AgentVersion(context.Context) (string, error)
	}); ok {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		if v, err := a.AgentVersion(ctx); err == nil {
			out["agent_build"] = v
		}
		cancel()
	}
	writeJSON(w, http.StatusOK, out)
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
	if s.recoveryLocked() {
		writeErr(w, http.StatusConflict, "this host is being recovered from a platform backup")
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
	if err := s.Store.BindRole(r.Context(), cluster.ID, user.ID, rbac.SuperAdmin); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.setRootUser(r.Context(), cluster.ID, user.ID, user.Username); err != nil {
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
			Role:         "control",
			HostPlatform: plat,
		})
	}
	if err := s.issueSession(w, r, user, 1); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, cluster.ID, user.ID, "setup.claim", "ok", "")
	s.platformOwnerSignedIn(cluster.ID, user, req.Password)
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
	key := s.clientIP(r) + "|" + strings.ToLower(strings.TrimSpace(req.Username))
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
	if user == nil || !auth.VerifyPassword(req.Password, user.PasswordHash) || userDisabled(*user) {
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
	// The password is right: keep this SuperAdmin able to unlock the platform
	// backup with it.
	s.platformOwnerSignedIn(cluster.ID, *user, req.Password)
	if s.refuseBlockedLogin(w, r, *user) {
		s.lock().Success(key)
		return
	}
	method, _, _, err := s.Store.GetMFAMethod(r.Context(), user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "mfa state is unavailable")
		return
	}
	if method != nil && method.Enabled {
		s.lock().Success(key)
		s.writeMFAChallenge(w, r, *user)
		return
	}
	s.lock().Success(key)
	_ = s.Store.TouchLastLogin(r.Context(), user.ID, s.now())
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
	if p.SessID != "" {
		if err := s.Store.RevokeSession(r.Context(), p.SessID); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not revoke session")
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeErr(w, http.StatusInternalServerError, "could not revoke session")
			return
		}
		sess, err := s.Store.GetSessionByHash(r.Context(), secutil.HashSHA256(c.Value))
		if err != nil || sess == nil || sess.RevokedAt == nil {
			writeErr(w, http.StatusInternalServerError, "could not revoke session")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cookieSecure(r), SameSite: http.SameSiteLaxMode})
	s.audit(r, p.User.ClusterID, p.User.ID, "auth.logout", "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p, err := s.principal(r)
	if errors.Is(err, errIPNotAllowed) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error(), "code": "ip_not_allowed", "ip": s.clientIP(r)})
		return
	}
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
	p, err := s.require(w, r, rbac.APIAccessManage)
	if err != nil {
		return
	}
	var req struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
		Preset      string   `json:"preset"`
		TTLHours    int      `json:"ttl_hours"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	perms, err := s.resolveTokenGrants(p, req.Preset, req.Permissions)
	if err != nil {
		if strings.Contains(err.Error(), "exceed") {
			writeErr(w, http.StatusForbidden, err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	expires, err := tokenExpiry(s.now(), req.TTLHours)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ownerID, err := s.tokenOwnerUserID(r.Context(), p.User.ClusterID, p.User.ID)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	tok, plain, ok := issueAPIToken(s, w, r, p.User.ClusterID, ownerID, req.Name, perms, expires)
	if !ok {
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "token.create", "ok", tok.ID)
	s.notifyUser(r.Context(), p.User.ClusterID, ownerID, noteTokenCreated, "New API token: "+tok.Name,
		"An API token was created on your account by "+p.User.Username+". Revoke it on API Access if you did not expect this.", "/api-access")
	out := map[string]any{
		"id":          tok.ID,
		"prefix":      tok.Prefix,
		"token":       plain,
		"name":        tok.Name,
		"permissions": tok.Permissions,
	}
	if tok.ExpiresAt != nil {
		out["expires_at"] = tok.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if req.Preset != "" {
		out["preset"] = strings.TrimSpace(req.Preset)
	}
	writeJSON(w, http.StatusCreated, out)
}

// revokeToken is the older body-based delete; tokens are hard-deleted and
// their history goes to the audit log.
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
	existing, err := s.Store.GetToken(r.Context(), req.ID)
	if err != nil || existing == nil || existing.ClusterID != p.User.ClusterID {
		writeErr(w, http.StatusNotFound, "token not found")
		return
	}
	if existing.UserID != p.User.ID && !rbac.Authorize(p.Grants, rbac.APIAccessManage) {
		writeErr(w, http.StatusNotFound, "token not found")
		return
	}
	if !s.mayActOnUser(r.Context(), p, existing.UserID) {
		writeErr(w, http.StatusForbidden, "that token belongs to someone above you in the role hierarchy")
		return
	}
	if !s.hardDeleteToken(w, r, p, *existing) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) require(w http.ResponseWriter, r *http.Request, perm string) (*principal, error) {
	p, err := s.principal(r)
	if errors.Is(err, errIPNotAllowed) {
		// Signed in elsewhere, but this address is not on the account's
		// allowlist: the UI sends the user back to sign-in, where MFA can
		// recover access.
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error(), "code": "ip_not_allowed", "ip": s.clientIP(r)})
		return nil, err
	}
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return nil, err
	}
	if !rbac.Authorize(p.Grants, perm) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return nil, errors.New("forbidden")
	}
	if !s.enforceWriter(w, r, p.User.ClusterID) {
		return nil, errors.New("not cluster writer")
	}
	return p, nil
}

func (s *Server) principal(r *http.Request) (*principal, error) {
	// In-process calls made by the CodeHold controller (codehold.go). The key
	// is unexported, so no request from outside can carry it.
	if p, ok := r.Context().Value(systemPrincipalKey{}).(*principal); ok && p != nil {
		return p, nil
	}
	if p, ok, err := s.unixRootPrincipal(r); ok || err != nil {
		return p, err
	}
	if tok := bearer(r); tok != "" {
		row, err := s.Store.GetTokenByHash(r.Context(), secutil.HashSHA256(tok))
		if err != nil || row == nil || row.RevokedAt != nil {
			return nil, errors.New("invalid token")
		}
		if row.ExpiresAt != nil && s.now().After(*row.ExpiresAt) {
			return nil, errors.New("invalid token")
		}
		u, err := s.Store.GetUser(r.Context(), row.UserID)
		if err != nil || u == nil || userDisabled(*u) {
			return nil, errors.New("invalid token")
		}
		if s.ipBlocked(r.Context(), *u, s.clientIP(r)) {
			return nil, errIPNotAllowed
		}
		_ = s.Store.TouchTokenLastUsed(r.Context(), row.ID, s.now())
		s.noteTokenUse(r, row.ID)
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
	if err != nil || u == nil || userDisabled(*u) {
		return nil, errors.New("invalid session")
	}
	if s.ipBlocked(r.Context(), *u, s.clientIP(r)) {
		return nil, errIPNotAllowed
	}
	return s.asPrincipal(r.Context(), *u, sess.ID, sess.AAL, "", nil)
}

// grantsForUser resolves what u may do from its roles. Root also holds the
// permissions of roles ranked above SuperAdmin.
func (s *Server) grantsForUser(ctx context.Context, u appdb.User, roles []string) []string {
	if containsString(roles, rbac.SuperAdmin) && u.ID == s.rootUserID(ctx, u.ClusterID) {
		roles = append(append([]string(nil), roles...), rolesAboveSuperAdmin(s.roleDefs(ctx, u.ClusterID))...)
	}
	return s.grantsForRoles(ctx, u.ClusterID, roles)
}

func (s *Server) asPrincipal(ctx context.Context, u appdb.User, sessID string, aal int, tokenID string, tokenPerms []string) (*principal, error) {
	roles, err := s.Store.UserRoles(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	grants := s.grantsForUser(ctx, u, roles)
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
	if user.ID == LocalRootUserID && len(roles) == 0 {
		roles = []string{rbac.SuperAdmin}
	}
	mfaEnabled := false
	if method, _, _, err := s.Store.GetMFAMethod(r.Context(), user.ID); err == nil && method != nil && method.Enabled {
		mfaEnabled = true
	}
	if aal <= 0 {
		aal = 1
	}
	prefs, _ := s.Store.GetUserPrefs(r.Context(), user.ID)
	level, ack, ackAt := prefsJSON(prefs)
	grants := s.grantsForUser(r.Context(), user, roles)
	clusterMFA := false
	if c, err := s.Store.GetCluster(r.Context()); err == nil && c != nil {
		clusterMFA = c.MFARequired
	}
	out := map[string]any{
		"user_id":                 user.ID,
		"username":                user.Username,
		"display_name":            user.DisplayName,
		"roles":                   roles,
		"grants":                  grants,
		"edition":                 edition,
		"cluster_id":              user.ClusterID,
		"aal":                     aal,
		"mfa_enabled":             mfaEnabled,
		"mfa_enforced":            user.MFARequired || clusterMFA,
		"mfa_enrollment_required": (user.MFARequired || clusterMFA) && !mfaEnabled,
		"kind":                    firstNonEmpty(user.Kind, appdb.UserKindPerson),
		"ux_level":                level,
		"expert_ack":              ack,
	}
	if av, _ := s.Store.GetUserAvatar(r.Context(), user.ID); av != nil {
		out["avatar_updated_at"] = av.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if ackAt != "" {
		out["expert_ack_at"] = ackAt
	}
	out["workload_sort"] = ""
	out["workload_order"] = []string{}
	out["ui_prefs"] = map[string]json.RawMessage{}
	if prefs != nil && prefs.UI != nil {
		out["ui_prefs"] = prefs.UI
	}
	if prefs != nil {
		out["workload_sort"] = prefs.WorkloadSort
		if prefs.WorkloadOrder != nil {
			out["workload_order"] = prefs.WorkloadOrder
		}
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
	fields := map[string]string{}
	if detail != "" {
		fields["detail"] = detail
	}
	// A flow's steps call the API as the automation identity; the entry
	// names the flow and its creator.
	if via := automationFlowLabelFrom(r.Context()); via != "" {
		fields["via"] = via
	}
	if len(fields) > 0 {
		b, _ := json.Marshal(fields)
		body = b
	}
	_ = s.Store.InsertAudit(r.Context(), appdb.AuditEvent{
		ID:          uuid.NewString(),
		ClusterID:   clusterID,
		ActorUserID: actor,
		Action:      action,
		Result:      result,
		RemoteAddr:  s.clientIP(r),
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
	// API answers are live state; a cached list shows deleted rows again.
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store")
	}
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
