package appdb

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Memory is an in-process Store for tests.
type Memory struct {
	mu                 sync.Mutex
	cluster            *Cluster
	setup              *SetupToken
	users              map[string]User
	roles              map[string][]string
	binds              map[string][]string
	sess               map[string]Session
	tokens             map[string]APIToken
	nodes              map[string]Node
	audit              []AuditEvent
	inventory          map[string]HardwareInventory
	observations       []NodeObservation
	operations         []Operation
	events             []Event
	pools              map[string]StoragePool
	volumes            map[string]Volume
	library            map[string]LibraryItem
	networks           map[string]Network
	addresses          map[string]Address
	reservations       map[string]DHCPReservation
	workloads          map[string]Workload
	workloadDisks      map[string]WorkloadDisk
	workloadNICs       map[string]WorkloadNIC
	vmCidata           map[string]VMCidata
	vmFirmware         map[string]VMFirmware
	ioSessions         map[string]IOSession
	certificate        *Certificate
	snapshots          map[string]Snapshot
	backupTargets      map[string]BackupTarget
	backupCreds        map[string][2]string
	backupPolicies     map[string]BackupPolicy
	backupRuns         map[string]BackupRun
	backupArtifacts    map[string]BackupArtifact
	updateOps          map[string]UpdateOperation
	groups             map[string]Group
	groupMembers       map[string][]string
	groupRoles         map[string][]string
	mfaMethods         map[string]MFAMethod
	mfaSecrets         map[string]string
	mfaRecovery        map[string][]string
	mfaChallenges      map[string]MFAChallenge
	servicePrincipals  map[string]ServicePrincipal
	volumeEnc          map[string]VolumeEncryption
	gpuAssignments     map[string]GPUAssignment
	zfsPools           map[string]ZFSPool
	zfsDatasets        map[string]ZFSDataset
	lvmVGs             map[string]LVMVG
	lvmLVs             map[string]LVMLV
	datastores         map[string]Datastore
	datastoreSecrets   map[string][2]string
	distributedPools   map[string]DistributedPool
	distributedSecrets map[string]string
	distributedOSDs    map[string]DistributedOSD
	alertRules         map[string]AlertRule
	notifyChannels     map[string]NotificationChannel
	notifySecrets      map[string][2]string
	userPrefs          map[string]UserPrefs
	vmTemplates        map[string]VMTemplate
	usbAttachments     map[string]USBAttachment
	guestObs           map[string]GuestObservation
	registries         map[string]Registry
	registrySecrets    map[string][2]string
	stacks             map[string]Stack
	stackMembers       map[string]StackMember
	netVLANs           map[string]NetworkVLAN
	netBonds           map[string]NetworkBond
	netPolicies        map[string]NetworkPolicy
	netOverlays        map[string]NetworkOverlay
	wgPeers            map[string]WGPeer
	remoteNodes        map[string]RemoteNode
	remoteSessions     map[string]RemoteSession
	joinTokens         map[string]JoinToken
	clusterLease       *ClusterLease
	haState            map[string]HAState
	haReplicaDSN       map[string]string
	rollingPlans       map[string]RollingPlan
	rollingSteps       map[string]RollingStep
	features           map[string]Feature
	storePackages      map[string]StorePackage
	storeInstalls      map[string]StoreInstallation
	signingKeys        map[string]SigningKey
	signingSecrets     map[string]string
	packageSigs        map[string]PackageSignature
	storeVerifies      map[string]StoreVerification
	scanResults        map[string]ScanResult
	storePolicies      map[string]StorePolicy
	nodeGroups         map[string]NodeGroup
	nodeGroupMembers   map[string][]string
	nodeMaint          map[string]NodeMaintenance
	placements         map[string]WorkloadPlacement
	migrateJobs        map[string]MigrateJob
	policies           map[string]Policy
	policyRuns         map[string]PolicyRun
	aiProviders        map[string]AIProvider
	aiProviderKeys     map[string]string
	aiProfiles         map[string]AIProfile
	aiPlans            map[string]AIPlan
	aiPlanSteps        map[string]AIPlanStep
	licenseState       map[string]LicenseState
	licenseKeys        map[string]string
	migSources         map[string]MigrationSource
	migSourceCreds     map[string]migCred
	migJobs            map[string]MigrationJob
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{
		users:  map[string]User{},
		roles:  map[string][]string{},
		binds:  map[string][]string{},
		sess:   map[string]Session{},
		tokens: map[string]APIToken{},
	}
}

func (m *Memory) GetCluster(context.Context) (*Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cluster == nil {
		return nil, nil
	}
	c := *m.cluster
	return &c, nil
}

func (m *Memory) CreateCluster(_ context.Context, c Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cluster != nil {
		return fmt.Errorf("cluster already exists")
	}
	m.cluster = &c
	return nil
}

func (m *Memory) CompleteSetup(_ context.Context, clusterID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cluster == nil || m.cluster.ID != clusterID {
		return fmt.Errorf("cluster not found")
	}
	now := time.Now().UTC()
	m.cluster.SetupCompletedAt = &now
	return nil
}

func (m *Memory) GetSetup(context.Context) (*SetupToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setup == nil {
		return nil, nil
	}
	s := *m.setup
	return &s, nil
}

func (m *Memory) PutSetup(_ context.Context, clusterID, tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setup = &SetupToken{ClusterID: clusterID, TokenHash: tokenHash}
	return nil
}

func (m *Memory) ConsumeSetup(_ context.Context, clusterID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setup == nil || m.setup.ClusterID != clusterID {
		return fmt.Errorf("setup token missing")
	}
	if m.setup.ConsumedAt != nil {
		return fmt.Errorf("setup already claimed")
	}
	now := time.Now().UTC()
	m.setup.ConsumedAt = &now
	return nil
}

func (m *Memory) CreateUser(_ context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.users {
		if existing.ClusterID == u.ClusterID && existing.Username == u.Username {
			return fmt.Errorf("user exists")
		}
	}
	if u.Kind == "" {
		u.Kind = UserKindPerson
	}
	m.users[u.ID] = u
	return nil
}

func (m *Memory) GetUserByName(_ context.Context, clusterID, username string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ClusterID == clusterID && u.Username == username {
			cp := u
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) GetUser(_ context.Context, id string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return nil, nil
	}
	return &u, nil
}

func (m *Memory) UpdatePassword(_ context.Context, userID, passwordHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return fmt.Errorf("user not found")
	}
	u.PasswordHash = passwordHash
	m.users[userID] = u
	return nil
}

func (m *Memory) CountAdmins(_ context.Context, clusterID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for uid, roles := range m.binds {
		u, ok := m.users[uid]
		if !ok || u.ClusterID != clusterID {
			continue
		}
		for _, r := range roles {
			if r == "admin" {
				n++
				break
			}
		}
	}
	return n, nil
}

func (m *Memory) EnsureRoles(_ context.Context, clusterID string, roles map[string][]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.roles == nil {
		m.roles = map[string][]string{}
	}
	for name, perms := range roles {
		m.roles[clusterID+"/"+name] = perms
	}
	return nil
}

func (m *Memory) BindRole(_ context.Context, _, userID, roleName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.binds == nil {
		m.binds = map[string][]string{}
	}
	for _, existing := range m.binds[userID] {
		if existing == roleName {
			return nil
		}
	}
	m.binds[userID] = append(m.binds[userID], roleName)
	return nil
}

func (m *Memory) UnbindRole(_ context.Context, _, userID, roleName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.binds[userID]
	if len(cur) == 0 {
		return nil
	}
	kept := cur[:0]
	for _, r := range cur {
		if r != roleName {
			kept = append(kept, r)
		}
	}
	m.binds[userID] = kept
	return nil
}

func (m *Memory) UserRoles(_ context.Context, userID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]string{}, m.binds[userID]...)
	for gid, members := range m.groupMembers {
		for _, uid := range members {
			if uid == userID {
				out = append(out, m.groupRoles[gid]...)
			}
		}
	}
	return out, nil
}

func (m *Memory) CreateSession(_ context.Context, s Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sess[s.TokenHash] = s
	return nil
}

func (m *Memory) GetSessionByHash(_ context.Context, hash string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sess[hash]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

func (m *Memory) RevokeSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for k, s := range m.sess {
		if s.ID == id {
			s.RevokedAt = &now
			m.sess[k] = s
		}
	}
	return nil
}

func (m *Memory) RevokeUserSessions(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for k, s := range m.sess {
		if s.UserID == userID {
			s.RevokedAt = &now
			m.sess[k] = s
		}
	}
	return nil
}

func (m *Memory) CreateToken(_ context.Context, t APIToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[t.TokenHash] = t
	return nil
}

func (m *Memory) GetTokenByHash(_ context.Context, hash string) (*APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[hash]
	if !ok {
		return nil, nil
	}
	return &t, nil
}

func (m *Memory) GetToken(_ context.Context, id string) (*APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if t.ID == id {
			cp := t
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) RevokeToken(_ context.Context, id, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for k, t := range m.tokens {
		if t.ID == id && t.UserID == userID {
			t.RevokedAt = &now
			m.tokens[k] = t
			return nil
		}
	}
	return fmt.Errorf("token not found")
}

func (m *Memory) UpsertNode(_ context.Context, n Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nodes == nil {
		m.nodes = map[string]Node{}
	}
	m.nodes[n.ID] = n
	return nil
}

func (m *Memory) GetNode(_ context.Context, clusterID string) (*Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return controlNodeLocked(m.nodes, clusterID), nil
}

func (m *Memory) InsertAudit(_ context.Context, e AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ID == "" {
		e.ID = fmt.Sprintf("audit-%d", len(m.audit)+1)
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	m.audit = append(m.audit, e)
	return nil
}

// Audits returns recorded audit events for tests.
func (m *Memory) Audits() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuditEvent{}, m.audit...)
}

func lessCreatedAtID(a, b time.Time, idA, idB string) bool {
	if !a.Equal(b) {
		return a.Before(b)
	}
	return idA < idB
}
