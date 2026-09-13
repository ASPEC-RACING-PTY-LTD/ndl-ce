package appdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	BackupRunning               = "running"
	BackupSucceeded             = "succeeded"
	BackupSucceededWithWarnings = "succeeded_with_warnings"
	BackupFailed                = "failed"
	BackupInterrupted           = "interrupted"
	BackupLocal                 = "local"
	BackupNFS                   = "nfs"
	BackupSMB                   = "smb"
	BackupS3                    = "s3"
	BackupR2                    = "r2"
	BackupAWS                   = "aws"
	BackupB2                    = "b2"
	BackupMinIO                 = "minio"
	BackupAvailable             = "available"
	BackupUnavailable           = "unavailable"
	BackupNotConfigured         = "not_configured"
	BackupUntested              = "untested"
	BackupAuthFailed            = "authentication_failed"
	BackupPermissionDenied      = "permission_denied"
	BackupBucketUnavailable     = "bucket_unavailable"
	BackupDegraded              = "degraded"
	BackupNightly               = "nightly"
	BackupUnverified            = "unverified"
	BackupVerified              = "verified"
	BackupScopeAll              = "all"
	BackupScopeSelected         = "selected"

	BackupMethodDirectoryArchive   = "directory-archive"
	BackupMethodContentAddressed   = "content-addressed"
	BackupMethodZFSSend            = "zfs-send"
	BackupMethodQCOW2Copy          = "qcow2-copy"

	BackupCaptureSmart  = "smart"
	BackupCaptureCustom = "custom"
	BackupCaptureFull   = "full"

	BackupConsistencyFreezer     = "cgroup-freezer" // historical Directory runs only
	BackupConsistencyLiveCopy    = "live-copy"
	BackupConsistencyApplication = "application"
	BackupConsistencyStopped     = "stopped"
	BackupConsistencyZFSSnapshot = "zfs-snapshot"
	BackupConsistencyLVMSnapshot = "lvm-snapshot"
	BackupConsistencyOverlay     = "qcow2-overlay"

	ArtifactLocalityLocal  = "local"
	ArtifactLocalityObject = "object"
	ArtifactLocalityPull   = "pull"
)

// BackupTarget is a destination for independent copies. Password and encryption keys are never stored on this row.
type BackupTarget struct {
	ID            string
	ClusterID     string
	Name          string
	Kind          string
	Locator       string
	Status        string
	Username      string
	Endpoint      string
	Region        string
	Bucket        string
	Prefix        string
	NoCheckBucket bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// BackupPolicy is a scheduled backup of a workload scope to one target.
type BackupPolicy struct {
	ID          string
	ClusterID   string
	Name        string
	Scope       string
	WorkloadID  string
	WorkloadIDs []string
	TargetID    string
	Schedule    string
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	CaptureMode string
	ScopeJSON   string
	LastRunAt   *time.Time
	CreatedAt   time.Time
}

// NormalizeBackupPolicy fills scope from legacy single-workload rows.
func NormalizeBackupPolicy(p *BackupPolicy) {
	if p == nil {
		return
	}
	if p.Scope != BackupScopeAll && p.Scope != BackupScopeSelected {
		if p.WorkloadID != "" || len(p.WorkloadIDs) > 0 {
			p.Scope = BackupScopeSelected
		} else {
			p.Scope = BackupScopeAll
		}
	}
	if p.Scope == BackupScopeAll {
		p.WorkloadID = ""
		p.WorkloadIDs = nil
		return
	}
	if len(p.WorkloadIDs) == 0 && p.WorkloadID != "" {
		p.WorkloadIDs = []string{p.WorkloadID}
	}
	if p.WorkloadID == "" && len(p.WorkloadIDs) > 0 {
		p.WorkloadID = p.WorkloadIDs[0]
	}
	if p.CaptureMode != BackupCaptureSmart && p.CaptureMode != BackupCaptureCustom && p.CaptureMode != BackupCaptureFull {
		p.CaptureMode = BackupCaptureSmart
	}
}

// BackupRun is an honest backup or restore job.
type BackupRun struct {
	ID                 string
	ClusterID          string
	PolicyID           string
	TargetID           string
	WorkloadID         string
	SnapshotID         string
	Status             string
	Error              string
	RestoredWorkloadID string
	TransferredBytes   int64
	Incremental        bool
	PlanJSON           string
	StartedAt          time.Time
	FinishedAt         *time.Time
}

// BackupPlan is the included vs skipped disk report for a run.
type BackupPlan struct {
	Method      string           `json:"method"`
	Consistency string           `json:"consistency"`
	Included    []BackupPlanItem `json:"included,omitempty"`
	Skipped     []BackupPlanItem `json:"skipped,omitempty"`
	Warning     string           `json:"warning,omitempty"`
}

// BackupPlanItem is one disk or resource in a backup plan.
type BackupPlanItem struct {
	Kind     string `json:"kind"`
	ID       string `json:"id,omitempty"`
	Role     string `json:"role,omitempty"`
	VolumeID string `json:"volume_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// BackupArtifact is a catalogued independent copy.
type BackupArtifact struct {
	ID                  string
	ClusterID           string
	RunID               string
	WorkloadID          string
	ChecksumSHA256      string
	SizeBytes           int64
	Locator             string
	Format              string
	Encrypted           bool
	TransferredBytes    int64
	ParentArtifactID    string
	ObjectKey           string
	Locality            string
	PullURL             string
	VerifyStatus        string
	VerifyError         string
	LastTestedAt        *time.Time
	ThrowawayWorkloadID string
	EngineVersion       string
	BackupID            string
	Namespace           string
	LocalComplete       bool
	RemoteState         string
	LogicalBytes        int64
	PhysicalNewData     int64
	ChunksNew           int
	ChunksReused        int
	CaptureDurationNS   int64
	UploadDurationNS    int64
	Consistency         string
	CaptureMode         string
	BlueprintJSON       string
	StatsJSON           string
	CreatedAt           time.Time
}

// BackupWorkspaceSettings bounds the local V2 repository.
type BackupWorkspaceSettings struct {
	ClusterID          string
	MaxLocalBytes      int64
	MinHostFreeBytes   int64
	CaptureConcurrency int
	UploadWorkers      int
	BandwidthLimitBPS  int64
	UpdatedAt          time.Time
}

// BackupRepository is the catalogued local V2 store.
type BackupRepository struct {
	ID         string
	ClusterID  string
	RootPath   string
	SizeBytes  int64
	Status     string
	UpdatedAt  time.Time
}

// BackupRestorePoint is a V2 restore point distinct from a remote-protected copy.
type BackupRestorePoint struct {
	ID              string
	ClusterID       string
	ArtifactID      string
	RunID           string
	WorkloadID      string
	BackupID        string
	Namespace       string
	CaptureMode     string
	LocalComplete   bool
	RemoteState     string
	LogicalBytes    int64
	PhysicalNewData int64
	CreatedAt       time.Time
}

// BackupUploadJob is a persisted pack upload tracked by the control plane.
type BackupUploadJob struct {
	ID         string
	ClusterID  string
	ArtifactID string
	BackupID   string
	PackID     string
	State      string
	Attempts   int
	UpdatedAt  time.Time
}

// FillArtifactLocality sets locality and pull URL from locator or object key.
// Pull URL is a locator without credentials. Dest agents pull later; this row is not a secret.
func FillArtifactLocality(a *BackupArtifact) {
	if a == nil {
		return
	}
	if a.Locality == "" {
		switch {
		case a.ObjectKey != "" || strings.HasPrefix(a.Locator, "s3://"):
			a.Locality = ArtifactLocalityObject
		case a.PullURL != "":
			a.Locality = ArtifactLocalityPull
		default:
			a.Locality = ArtifactLocalityLocal
		}
	}
	if a.PullURL == "" && a.Locality != ArtifactLocalityLocal && a.Locator != "" {
		a.PullURL = a.Locator
	}
}

func (m *Memory) CreateBackupTarget(_ context.Context, t BackupTarget, password, encryptionKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backupTargets == nil {
		m.backupTargets = map[string]BackupTarget{}
	}
	if m.backupCreds == nil {
		m.backupCreds = map[string][2]string{}
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	t.UpdatedAt = t.CreatedAt
	m.backupTargets[t.ID] = t
	m.backupCreds[t.ID] = [2]string{password, encryptionKey}
	return nil
}

func (m *Memory) ListBackupTargets(_ context.Context, clusterID string) ([]BackupTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BackupTarget
	for _, t := range m.backupTargets {
		if t.ClusterID == clusterID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetBackupTarget(_ context.Context, clusterID, id string) (*BackupTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.backupTargets[id]
	if !ok || t.ClusterID != clusterID {
		return nil, nil
	}
	cp := t
	return &cp, nil
}

func (m *Memory) UpdateBackupTargetStatus(_ context.Context, clusterID, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.backupTargets[id]
	if !ok || t.ClusterID != clusterID {
		return fmt.Errorf("backup target not found")
	}
	t.Status = status
	t.UpdatedAt = time.Now().UTC()
	m.backupTargets[id] = t
	return nil
}

func (m *Memory) BackupCredentials(_ context.Context, clusterID, id string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.backupTargets[id]
	if !ok || t.ClusterID != clusterID {
		return "", "", fmt.Errorf("backup target not found")
	}
	pair := m.backupCreds[id]
	return pair[0], pair[1], nil
}

func copyBackupPolicy(p BackupPolicy) BackupPolicy {
	NormalizeBackupPolicy(&p)
	if len(p.WorkloadIDs) > 0 {
		p.WorkloadIDs = append([]string(nil), p.WorkloadIDs...)
	}
	return p
}

func (m *Memory) CreateBackupPolicy(_ context.Context, p BackupPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backupPolicies == nil {
		m.backupPolicies = map[string]BackupPolicy{}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	m.backupPolicies[p.ID] = copyBackupPolicy(p)
	return nil
}

func (m *Memory) ListBackupPolicies(_ context.Context, clusterID string) ([]BackupPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BackupPolicy
	for _, p := range m.backupPolicies {
		if p.ClusterID == clusterID {
			out = append(out, copyBackupPolicy(p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetBackupPolicy(_ context.Context, clusterID, id string) (*BackupPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.backupPolicies[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	cp := copyBackupPolicy(p)
	return &cp, nil
}

func (m *Memory) UpdateBackupPolicy(_ context.Context, p BackupPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.backupPolicies[p.ID]
	if !ok || existing.ClusterID != p.ClusterID {
		return fmt.Errorf("backup policy not found")
	}
	p.CreatedAt = existing.CreatedAt
	p.LastRunAt = existing.LastRunAt
	m.backupPolicies[p.ID] = copyBackupPolicy(p)
	return nil
}

func (m *Memory) DeleteBackupPolicy(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.backupPolicies[id]
	if !ok || p.ClusterID != clusterID {
		return fmt.Errorf("backup policy not found")
	}
	delete(m.backupPolicies, id)
	return nil
}

func (m *Memory) UpdateBackupPolicyLastRun(_ context.Context, clusterID, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.backupPolicies[id]
	if !ok || p.ClusterID != clusterID {
		return fmt.Errorf("backup policy not found")
	}
	p.LastRunAt = &at
	m.backupPolicies[id] = p
	return nil
}

func (m *Memory) CreateBackupRun(_ context.Context, r BackupRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backupRuns == nil {
		m.backupRuns = map[string]BackupRun{}
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now().UTC()
	}
	m.backupRuns[r.ID] = r
	return nil
}

func (m *Memory) ListBackupRuns(_ context.Context, clusterID string) ([]BackupRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BackupRun
	for _, r := range m.backupRuns {
		if r.ClusterID == clusterID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

func (m *Memory) GetBackupRun(_ context.Context, clusterID, id string) (*BackupRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.backupRuns[id]
	if !ok || r.ClusterID != clusterID {
		return nil, nil
	}
	cp := r
	return &cp, nil
}

func (m *Memory) UpdateBackupRun(_ context.Context, r BackupRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.backupRuns[r.ID]
	if !ok || cur.ClusterID != r.ClusterID {
		return fmt.Errorf("backup run not found")
	}
	m.backupRuns[r.ID] = r
	return nil
}

func (m *Memory) CreateBackupArtifact(_ context.Context, a BackupArtifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backupArtifacts == nil {
		m.backupArtifacts = map[string]BackupArtifact{}
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.VerifyStatus == "" {
		a.VerifyStatus = BackupUnverified
	}
	FillArtifactLocality(&a)
	m.backupArtifacts[a.ID] = a
	return nil
}

func (m *Memory) UpdateBackupArtifact(ctx context.Context, a BackupArtifact) error {
	return m.UpdateBackupArtifactVerify(ctx, a)
}

func (m *Memory) GetBackupWorkspaceSettings(_ context.Context, clusterID string) (*BackupWorkspaceSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backupWorkspace != nil {
		if s, ok := m.backupWorkspace[clusterID]; ok {
			return &s, nil
		}
	}
	return DefaultBackupWorkspaceSettings(clusterID), nil
}

func (m *Memory) UpsertBackupWorkspaceSettings(_ context.Context, s BackupWorkspaceSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backupWorkspace == nil {
		m.backupWorkspace = map[string]BackupWorkspaceSettings{}
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = time.Now().UTC()
	}
	m.backupWorkspace[s.ClusterID] = s
	return nil
}

func (m *Memory) UpsertBackupRepository(_ context.Context, r BackupRepository) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backupRepos == nil {
		m.backupRepos = map[string]BackupRepository{}
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now().UTC()
	}
	m.backupRepos[r.ClusterID] = r
	return nil
}

func (m *Memory) GetBackupRepository(_ context.Context, clusterID string) (*BackupRepository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backupRepos == nil {
		return nil, nil
	}
	r, ok := m.backupRepos[clusterID]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

func (m *Memory) UpsertBackupRestorePoint(_ context.Context, p BackupRestorePoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backupPoints == nil {
		m.backupPoints = map[string]BackupRestorePoint{}
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	m.backupPoints[p.ID] = p
	return nil
}

func (m *Memory) ListBackupRestorePoints(_ context.Context, clusterID string) ([]BackupRestorePoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BackupRestorePoint
	for _, p := range m.backupPoints {
		if p.ClusterID == clusterID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetBackupRestorePoint(_ context.Context, clusterID, id string) (*BackupRestorePoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.backupPoints[id]
	if !ok || p.ClusterID != clusterID {
		return nil, nil
	}
	return &p, nil
}

func (m *Memory) ReplaceBackupUploadJobs(_ context.Context, clusterID string, jobs []BackupUploadJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backupUploadJobs == nil {
		m.backupUploadJobs = map[string][]BackupUploadJob{}
	}
	m.backupUploadJobs[clusterID] = append([]BackupUploadJob(nil), jobs...)
	return nil
}

func (m *Memory) ListBackupUploadJobs(_ context.Context, clusterID string) ([]BackupUploadJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]BackupUploadJob(nil), m.backupUploadJobs[clusterID]...), nil
}

func DefaultBackupWorkspaceSettings(clusterID string) *BackupWorkspaceSettings {
	return &BackupWorkspaceSettings{
		ClusterID:          clusterID,
		MaxLocalBytes:      50 << 30,
		MinHostFreeBytes:   10 << 30,
		CaptureConcurrency: 1,
		UploadWorkers:      4,
	}
}

func (m *Memory) UpdateBackupArtifactVerify(_ context.Context, a BackupArtifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.backupArtifacts[a.ID]
	if !ok || cur.ClusterID != a.ClusterID {
		return fmt.Errorf("backup artifact not found")
	}
	if a.VerifyStatus == "" {
		a.VerifyStatus = BackupUnverified
	}
	m.backupArtifacts[a.ID] = a
	return nil
}

func (m *Memory) ListBackupArtifacts(_ context.Context, clusterID string) ([]BackupArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BackupArtifact
	for _, a := range m.backupArtifacts {
		if a.ClusterID == clusterID {
			FillArtifactLocality(&a)
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) ListBackupArtifactsForWorkload(_ context.Context, clusterID, workloadID, targetID string) ([]BackupArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BackupArtifact
	for _, a := range m.backupArtifacts {
		if a.ClusterID != clusterID || a.WorkloadID != workloadID {
			continue
		}
		if targetID != "" {
			run, ok := m.backupRuns[a.RunID]
			if !ok || run.TargetID != targetID {
				continue
			}
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	for i := range out {
		FillArtifactLocality(&out[i])
	}
	return out, nil
}

func (m *Memory) GetBackupArtifact(_ context.Context, clusterID, id string) (*BackupArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.backupArtifacts[id]
	if !ok || a.ClusterID != clusterID {
		return nil, nil
	}
	cp := a
	FillArtifactLocality(&cp)
	return &cp, nil
}

func (m *Memory) DeleteBackupArtifact(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.backupArtifacts[id]
	if !ok || a.ClusterID != clusterID {
		return fmt.Errorf("backup artifact not found")
	}
	delete(m.backupArtifacts, id)
	return nil
}
