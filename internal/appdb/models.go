package appdb

import (
	"context"
	"encoding/json"
	"time"
)

// Store is the Phase 1 control-plane state.
type Store interface {
	GetCluster(ctx context.Context) (*Cluster, error)
	CreateCluster(ctx context.Context, c Cluster) error
	CompleteSetup(ctx context.Context, clusterID string) error

	GetSetup(ctx context.Context) (*SetupToken, error)
	PutSetup(ctx context.Context, clusterID, tokenHash string) error
	ConsumeSetup(ctx context.Context, clusterID string) error

	CreateUser(ctx context.Context, u User) error
	GetUserByName(ctx context.Context, clusterID, username string) (*User, error)
	GetUser(ctx context.Context, id string) (*User, error)
	UpdatePassword(ctx context.Context, userID, passwordHash string) error
	CountAdmins(ctx context.Context, clusterID string) (int, error)
	DeleteUserMFA(ctx context.Context, userID string) error

	EnsureRoles(ctx context.Context, clusterID string, roles map[string][]string) error
	BindRole(ctx context.Context, clusterID, userID, roleName string) error
	UserRoles(ctx context.Context, userID string) ([]string, error)

	CreateSession(ctx context.Context, s Session) error
	GetSessionByHash(ctx context.Context, hash string) (*Session, error)
	RevokeSession(ctx context.Context, id string) error
	RevokeUserSessions(ctx context.Context, userID string) error

	CreateToken(ctx context.Context, t APIToken) error
	GetTokenByHash(ctx context.Context, hash string) (*APIToken, error)
	RevokeToken(ctx context.Context, id, userID string) error

	UpsertNode(ctx context.Context, n Node) error
	GetNode(ctx context.Context, clusterID string) (*Node, error)

	InsertAudit(ctx context.Context, e AuditEvent) error
	ListAuditEvents(ctx context.Context, clusterID string, limit int) ([]AuditEvent, error)

	UpsertInventory(ctx context.Context, row HardwareInventory) error
	GetInventory(ctx context.Context, nodeID string) (*HardwareInventory, error)
	MarkInventoryStale(ctx context.Context, nodeID string) error

	InsertObservation(ctx context.Context, o NodeObservation) error

	UpsertOperation(ctx context.Context, op Operation) error
	ListOperations(ctx context.Context, clusterID string, limit int) ([]Operation, error)

	InsertEvent(ctx context.Context, e Event) error
	ListEvents(ctx context.Context, clusterID string, limit int) ([]Event, error)

	CreateStoragePool(ctx context.Context, p StoragePool) error
	ListStoragePools(ctx context.Context, clusterID string) ([]StoragePool, error)
	GetStoragePool(ctx context.Context, clusterID, id string) (*StoragePool, error)
	UpdateStoragePoolObserved(ctx context.Context, p StoragePool) error

	CreateVolume(ctx context.Context, v Volume) error
	ListVolumes(ctx context.Context, clusterID, poolID string) ([]Volume, error)
	GetVolume(ctx context.Context, clusterID, id string) (*Volume, error)
	UpdateVolumeObserved(ctx context.Context, v Volume) error

	CreateLibraryItem(ctx context.Context, item LibraryItem) error
	ListLibraryItems(ctx context.Context, clusterID, poolID string) ([]LibraryItem, error)
	GetLibraryItem(ctx context.Context, clusterID, id string) (*LibraryItem, error)
	GetLibraryByChecksum(ctx context.Context, poolID, checksum string) (*LibraryItem, error)
	UpdateLibraryObserved(ctx context.Context, item LibraryItem) error

	CreateNetwork(ctx context.Context, n Network) error
	ListNetworks(ctx context.Context, clusterID string) ([]Network, error)
	GetNetwork(ctx context.Context, clusterID, id string) (*Network, error)
	UpdateNetworkObserved(ctx context.Context, n Network) error

	CreateAddress(ctx context.Context, a Address) error
	ListAddresses(ctx context.Context, clusterID, networkID string) ([]Address, error)

	CreateReservation(ctx context.Context, r DHCPReservation) error
	ListReservations(ctx context.Context, clusterID, networkID string) ([]DHCPReservation, error)

	GetOperationByIdempotency(ctx context.Context, clusterID, key string) (*Operation, error)

	CreateWorkload(ctx context.Context, w Workload) error
	ListWorkloads(ctx context.Context, clusterID string) ([]Workload, error)
	GetWorkload(ctx context.Context, clusterID, id string) (*Workload, error)
	GetWorkloadByName(ctx context.Context, clusterID, name string) (*Workload, error)
	GetWorkloadByIdempotency(ctx context.Context, clusterID, key string) (*Workload, error)
	UpdateWorkloadObserved(ctx context.Context, w Workload) error
	UpdateWorkloadSpec(ctx context.Context, w Workload) error

	CreateWorkloadDisk(ctx context.Context, d WorkloadDisk) error
	ListWorkloadDisks(ctx context.Context, clusterID, workloadID string) ([]WorkloadDisk, error)

	CreateWorkloadNIC(ctx context.Context, n WorkloadNIC) error
	ListWorkloadNICs(ctx context.Context, clusterID, workloadID string) ([]WorkloadNIC, error)
	UpdateWorkloadNIC(ctx context.Context, n WorkloadNIC) error

	DeleteWorkload(ctx context.Context, clusterID, id string) error

	UpsertVMCidata(ctx context.Context, row VMCidata) error
	GetVMCidata(ctx context.Context, clusterID, workloadID string) (*VMCidata, error)
	UpsertVMFirmware(ctx context.Context, row VMFirmware) error
	GetVMFirmware(ctx context.Context, clusterID, workloadID string) (*VMFirmware, error)

	CreateIOSession(ctx context.Context, s IOSession) error
	GetIOSession(ctx context.Context, clusterID, id string) (*IOSession, error)
	GetIOSessionByTicketHash(ctx context.Context, ticketHash string) (*IOSession, error)
	UpdateIOSession(ctx context.Context, s IOSession) error

	GetCertificate(ctx context.Context, clusterID string) (*Certificate, error)
	UpsertCertificate(ctx context.Context, c Certificate) error

	CreateSnapshot(ctx context.Context, s Snapshot) error
	ListSnapshots(ctx context.Context, clusterID, workloadID string) ([]Snapshot, error)
	GetSnapshot(ctx context.Context, clusterID, id string) (*Snapshot, error)
	UpdateVolumeLocator(ctx context.Context, clusterID, id, backendRef string) error

	CreateBackupTarget(ctx context.Context, t BackupTarget, password string) error
	ListBackupTargets(ctx context.Context, clusterID string) ([]BackupTarget, error)
	GetBackupTarget(ctx context.Context, clusterID, id string) (*BackupTarget, error)
	UpdateBackupTargetStatus(ctx context.Context, clusterID, id, status string) error

	CreateBackupPolicy(ctx context.Context, p BackupPolicy) error
	ListBackupPolicies(ctx context.Context, clusterID string) ([]BackupPolicy, error)
	GetBackupPolicy(ctx context.Context, clusterID, id string) (*BackupPolicy, error)
	UpdateBackupPolicyLastRun(ctx context.Context, clusterID, id string, at time.Time) error

	CreateBackupRun(ctx context.Context, r BackupRun) error
	ListBackupRuns(ctx context.Context, clusterID string) ([]BackupRun, error)
	GetBackupRun(ctx context.Context, clusterID, id string) (*BackupRun, error)
	UpdateBackupRun(ctx context.Context, r BackupRun) error

	CreateBackupArtifact(ctx context.Context, a BackupArtifact) error
	ListBackupArtifacts(ctx context.Context, clusterID string) ([]BackupArtifact, error)
	ListBackupArtifactsForWorkload(ctx context.Context, clusterID, workloadID, targetID string) ([]BackupArtifact, error)
	GetBackupArtifact(ctx context.Context, clusterID, id string) (*BackupArtifact, error)
	DeleteBackupArtifact(ctx context.Context, clusterID, id string) error

	CreateUpdateOperation(ctx context.Context, op UpdateOperation) error
	ListUpdateOperations(ctx context.Context, clusterID string, limit int) ([]UpdateOperation, error)
	GetLatestUpdateOperation(ctx context.Context, clusterID string) (*UpdateOperation, error)
	UpdateUpdateOperation(ctx context.Context, op UpdateOperation) error

	CreateGroup(ctx context.Context, g Group) error
	ListGroups(ctx context.Context, clusterID string) ([]Group, error)
	GetGroup(ctx context.Context, clusterID, id string) (*Group, error)
	AddGroupMember(ctx context.Context, clusterID, groupID, userID string) error
	ListGroupMembers(ctx context.Context, clusterID, groupID string) ([]string, error)
	BindGroupRole(ctx context.Context, clusterID, groupID, roleName string) error

	UpsertMFAMethod(ctx context.Context, m MFAMethod, totpSecret string, recoveryHashes []string) error
	GetMFAMethod(ctx context.Context, userID string) (*MFAMethod, string, []string, error)
	EnableMFAMethod(ctx context.Context, userID string) error
	ConsumeRecoveryHash(ctx context.Context, userID, hash string) error

	CreateMFAChallenge(ctx context.Context, c MFAChallenge) error
	GetMFAChallengeByHash(ctx context.Context, hash string) (*MFAChallenge, error)
	ConsumeMFAChallenge(ctx context.Context, id string) error

	CreateServicePrincipal(ctx context.Context, sp ServicePrincipal) error
	ListServicePrincipals(ctx context.Context, clusterID string) ([]ServicePrincipal, error)

	GetVolumeEncryption(ctx context.Context, clusterID, volumeID string) (*VolumeEncryption, error)
	UpsertVolumeEncryption(ctx context.Context, e VolumeEncryption) error

	CreateGPUAssignment(ctx context.Context, a GPUAssignment) error
	ListGPUAssignments(ctx context.Context, clusterID string) ([]GPUAssignment, error)
	ListGPUAssignmentsForGPU(ctx context.Context, clusterID, gpuID string) ([]GPUAssignment, error)
	GetGPUAssignment(ctx context.Context, clusterID, id string) (*GPUAssignment, error)
	DeleteGPUAssignment(ctx context.Context, clusterID, id string) error

	UpsertZFSPool(ctx context.Context, p ZFSPool) error
	GetZFSPool(ctx context.Context, poolID string) (*ZFSPool, error)
	GetZFSPoolByGUID(ctx context.Context, guid string) (*ZFSPool, error)
	UpsertZFSDataset(ctx context.Context, d ZFSDataset) error
	GetZFSDataset(ctx context.Context, volumeID string) (*ZFSDataset, error)

	CreateAlertRule(ctx context.Context, r AlertRule) error
	ListAlertRules(ctx context.Context, clusterID string) ([]AlertRule, error)
	GetAlertRule(ctx context.Context, clusterID, id string) (*AlertRule, error)
	UpdateAlertRuleFired(ctx context.Context, clusterID, id string, at time.Time) error

	CreateNotificationChannel(ctx context.Context, c NotificationChannel, webhookURL, smtpPassword string) error
	ListNotificationChannels(ctx context.Context, clusterID string) ([]NotificationChannel, error)
	GetNotificationChannel(ctx context.Context, clusterID, id string) (*NotificationChannel, error)
	NotificationSecrets(ctx context.Context, clusterID, id string) (webhookURL, smtpPassword string, err error)

	GetUserPrefs(ctx context.Context, userID string) (*UserPrefs, error)
	UpsertUserPrefs(ctx context.Context, p UserPrefs) error

	DeleteVolume(ctx context.Context, clusterID, volumeID string) error

	CreateVMTemplate(ctx context.Context, t VMTemplate) error
	ListVMTemplates(ctx context.Context, clusterID string) ([]VMTemplate, error)
	GetVMTemplate(ctx context.Context, clusterID, id string) (*VMTemplate, error)

	CreateUSBAttachment(ctx context.Context, a USBAttachment) error
	ListUSBAttachments(ctx context.Context, clusterID, workloadID string) ([]USBAttachment, error)
	DeleteUSBAttachment(ctx context.Context, clusterID, id string) error

	UpsertGuestObservation(ctx context.Context, g GuestObservation) error
	GetGuestObservation(ctx context.Context, clusterID, workloadID string) (*GuestObservation, error)

	CreateRegistry(ctx context.Context, r Registry, username, password string) error
	ListRegistries(ctx context.Context, clusterID string) ([]Registry, error)
	GetRegistry(ctx context.Context, clusterID, id string) (*Registry, error)
	RegistrySecrets(ctx context.Context, clusterID, id string) (username, password string, err error)
}

// Cluster is the appliance cluster of one.
type Cluster struct {
	ID               string
	Name             string
	SetupCompletedAt *time.Time
}

// SetupToken is the hashed one-time claim secret.
type SetupToken struct {
	ClusterID  string
	TokenHash  string
	ConsumedAt *time.Time
}

// User is a local account.
type User struct {
	ID           string
	ClusterID    string
	Username     string
	PasswordHash string
	Kind         string
}

// Session is a cookie session.
type Session struct {
	ID        string
	ClusterID string
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
	AAL       int
}

// APIToken is a hashed bearer token.
type APIToken struct {
	ID          string
	ClusterID   string
	UserID      string
	Name        string
	TokenHash   string
	Prefix      string
	Permissions []string
	RevokedAt   *time.Time
}

// Node is the local enrolled node.
type Node struct {
	ID           string
	ClusterID    string
	Name         string
	HostPlatform json.RawMessage
}

// HardwareInventory is cached observed hardware. Not desired state.
type HardwareInventory struct {
	NodeID     string
	ClusterID  string
	Payload    json.RawMessage
	ObservedAt time.Time
	Stale      bool
}

// NodeObservation records that a scrape completed.
type NodeObservation struct {
	ID         string
	ClusterID  string
	NodeID     string
	Kind       string
	ObservedAt time.Time
	Stale      bool
}

// Operation is a job/task with optional honest progress.
type Operation struct {
	ID             string
	ClusterID      string
	NodeID         string
	Kind           string
	State          string
	IdempotencyKey string
	Progress       *int
	Stage          string
	Message        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Event is a platform occurrence. It is not an audit event.
type Event struct {
	ID        string
	ClusterID string
	NodeID    string
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// AuditEvent is a security audit row.
type AuditEvent struct {
	ID          string
	ClusterID   string
	ActorUserID string
	Action      string
	Result      string
	RemoteAddr  string
	Detail      json.RawMessage
	CreatedAt   time.Time
}
