package appdb

import (
	"context"
	"encoding/json"
	"time"
)

// Automation run statuses.
const (
	AutoRunQueued          = "queued"
	AutoRunPending         = "pending"
	AutoRunRunning         = "running"
	AutoRunWaiting         = "waiting"
	AutoRunWaitingApproval = "waiting_approval"
	AutoRunSucceeded       = "succeeded"
	AutoRunFailed          = "failed"
	AutoRunCancelled       = "cancelled"
	AutoRunDropped         = "dropped"
)

// Automation run modes.
const (
	AutoModeLive   = "live"
	AutoModeDryRun = "dry_run"
)

// AutoRunActive reports whether a run still holds a concurrency slot.
func AutoRunActive(status string) bool {
	switch status {
	case AutoRunPending, AutoRunRunning, AutoRunWaiting, AutoRunWaitingApproval:
		return true
	}
	return false
}

// AutoRunFinished reports whether a run has ended.
func AutoRunFinished(status string) bool {
	switch status {
	case AutoRunSucceeded, AutoRunFailed, AutoRunCancelled, AutoRunDropped:
		return true
	}
	return false
}

// AutomationFlow is a flow: an editable draft plus its published version.
type AutomationFlow struct {
	ID          string
	ClusterID   string
	Name        string
	Description string
	Draft       json.RawMessage
	// PublishedVersion is 0 until the first publish.
	PublishedVersion int
	Enabled          bool
	// TriggerKind and EventKind index the published trigger for dispatch.
	TriggerKind string
	EventKind   string
	Limits      json.RawMessage
	// DryRunHash is the hash of the draft whose last test run succeeded.
	DryRunHash          string
	ConsecutiveFailures int
	DisabledReason      string
	// LegacyPolicyID links a flow made from an old policy.
	LegacyPolicyID string
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// AutomationFlowVersion is a frozen, published definition.
type AutomationFlowVersion struct {
	FlowID      string
	ClusterID   string
	Version     int
	Definition  json.RawMessage
	PublishedBy string
	Note        string
	CreatedAt   time.Time
}

// AutomationConnection is a saved credential. Sealed is never returned by
// the API.
type AutomationConnection struct {
	ID        string
	ClusterID string
	Name      string
	Kind      string
	Config    json.RawMessage
	Sealed    string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AutomationWebhook is a flow's inbound endpoint. Only hashes are stored.
type AutomationWebhook struct {
	FlowID     string
	ClusterID  string
	TokenHash  string
	SecretHash string
	RotatedAt  time.Time
}

// AutomationWebhookDelivery is one inbound call, redacted.
type AutomationWebhookDelivery struct {
	ID        string
	ClusterID string
	FlowID    string
	Status    int
	RunID     string
	Error     string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// AutomationSchedule is a published schedule trigger's next run.
type AutomationSchedule struct {
	FlowID    string
	ClusterID string
	Preset    string
	Cron      string
	Timezone  string
	NextRunAt *time.Time
	LastKey   string
}

// AutomationRun is one execution of a flow version (or, for dry runs, of a
// draft). State is the engine's resumable state; Definition is the exact
// definition the run executes.
type AutomationRun struct {
	ID             string
	ClusterID      string
	FlowID         string
	Version        int
	Mode           string
	Status         string
	TriggerKind    string
	TriggerPayload json.RawMessage
	Inputs         json.RawMessage
	Definition     json.RawMessage
	State          json.RawMessage
	Report         json.RawMessage
	IdempotencyKey string
	CorrelationID  string
	CausedByRun    string
	ChainDepth     int
	RetryOf        string
	ActorID        string
	StartedBy      string
	Error          string
	WakeAt         *time.Time
	LeaseOwner     string
	LeaseUntil     *time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// AutomationNodeRun is one step of a run. Scope tells loop iterations
// apart ("" at the top level, "loop#2" inside a foreach).
type AutomationNodeRun struct {
	ID         string
	ClusterID  string
	RunID      string
	NodeID     string
	Scope      string
	Type       string
	Status     string
	Port       string
	Input      json.RawMessage
	Output     json.RawMessage
	Error      string
	Attempts   int
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// AutomationNodeAttempt is one try of a step.
type AutomationNodeAttempt struct {
	ID         string
	ClusterID  string
	RunID      string
	NodeRunID  string
	Attempt    int
	Status     string
	Error      string
	StartedAt  time.Time
	FinishedAt *time.Time
}

// AutomationRunLog is one redacted log line.
type AutomationRunLog struct {
	ID        string
	ClusterID string
	RunID     string
	NodeID    string
	Level     string
	Message   string
	CreatedAt time.Time
}

// AutomationLedgerEntry records a destructive action on a target, for the
// per-target limit across all flows.
type AutomationLedgerEntry struct {
	ID        string
	ClusterID string
	Action    string
	Target    string
	FlowID    string
	RunID     string
	CreatedAt time.Time
}

// AutomationRunFilter narrows ListAutomationRuns.
type AutomationRunFilter struct {
	FlowID string
	Status string
	Mode   string
	Limit  int
}

// AutomationStore is the automation engine's state.
type AutomationStore interface {
	CreateAutomationFlow(ctx context.Context, f AutomationFlow) error
	GetAutomationFlow(ctx context.Context, clusterID, id string) (*AutomationFlow, error)
	ListAutomationFlows(ctx context.Context, clusterID string) ([]AutomationFlow, error)
	// ListAutomationFlowsByTrigger lists enabled, published flows whose
	// trigger is kind (and, for events, listens for eventKind).
	ListAutomationFlowsByTrigger(ctx context.Context, clusterID, kind, eventKind string) ([]AutomationFlow, error)
	UpdateAutomationFlow(ctx context.Context, f AutomationFlow) error
	DeleteAutomationFlow(ctx context.Context, clusterID, id string) error

	CreateAutomationFlowVersion(ctx context.Context, v AutomationFlowVersion) error
	GetAutomationFlowVersion(ctx context.Context, clusterID, flowID string, version int) (*AutomationFlowVersion, error)
	ListAutomationFlowVersions(ctx context.Context, clusterID, flowID string) ([]AutomationFlowVersion, error)

	CreateAutomationConnection(ctx context.Context, c AutomationConnection) error
	GetAutomationConnection(ctx context.Context, clusterID, id string) (*AutomationConnection, error)
	ListAutomationConnections(ctx context.Context, clusterID string) ([]AutomationConnection, error)
	UpdateAutomationConnection(ctx context.Context, c AutomationConnection) error
	DeleteAutomationConnection(ctx context.Context, clusterID, id string) error

	PutAutomationWebhook(ctx context.Context, w AutomationWebhook) error
	GetAutomationWebhook(ctx context.Context, clusterID, flowID string) (*AutomationWebhook, error)
	GetAutomationWebhookByToken(ctx context.Context, tokenHash string) (*AutomationWebhook, error)
	// AddAutomationWebhookDelivery records a delivery and keeps the newest keep.
	AddAutomationWebhookDelivery(ctx context.Context, d AutomationWebhookDelivery, keep int) error
	ListAutomationWebhookDeliveries(ctx context.Context, clusterID, flowID string, limit int) ([]AutomationWebhookDelivery, error)

	PutAutomationSchedule(ctx context.Context, s AutomationSchedule) error
	DeleteAutomationSchedule(ctx context.Context, clusterID, flowID string) error
	// ListDueAutomationSchedules with an empty clusterID lists every cluster's.
	ListDueAutomationSchedules(ctx context.Context, now time.Time, limit int) ([]AutomationSchedule, error)

	CreateAutomationRun(ctx context.Context, r AutomationRun) error
	GetAutomationRun(ctx context.Context, clusterID, id string) (*AutomationRun, error)
	UpdateAutomationRun(ctx context.Context, r AutomationRun) error
	ListAutomationRuns(ctx context.Context, clusterID string, f AutomationRunFilter) ([]AutomationRun, error)
	FindAutomationRunByKey(ctx context.Context, clusterID, flowID, key string, since time.Time) (*AutomationRun, error)
	CountAutomationRunsSince(ctx context.Context, clusterID, flowID string, since time.Time) (int, error)
	// ListDueAutomationRuns lists runs a worker should step now, in every
	// cluster: pending ones, waiting ones whose wake time passed and running
	// ones whose lease expired.
	ListDueAutomationRuns(ctx context.Context, now time.Time, limit int) ([]AutomationRun, error)
	// ClaimAutomationRun takes the run's lease for owner until until. It
	// fails (false) while another owner holds an unexpired lease, so two
	// workers never step the same run.
	ClaimAutomationRun(ctx context.Context, clusterID, id, owner string, now, until time.Time) (bool, error)
	ReleaseAutomationRun(ctx context.Context, clusterID, id, owner string) error

	UpsertAutomationNodeRun(ctx context.Context, n AutomationNodeRun) error
	ListAutomationNodeRuns(ctx context.Context, clusterID, runID string) ([]AutomationNodeRun, error)
	AddAutomationNodeAttempt(ctx context.Context, a AutomationNodeAttempt) error
	ListAutomationNodeAttempts(ctx context.Context, clusterID, runID string) ([]AutomationNodeAttempt, error)
	AddAutomationRunLog(ctx context.Context, l AutomationRunLog) error
	ListAutomationRunLogs(ctx context.Context, clusterID, runID string, limit int) ([]AutomationRunLog, error)

	AddAutomationLedger(ctx context.Context, e AutomationLedgerEntry) error
	CountAutomationLedger(ctx context.Context, clusterID, action, target string, since time.Time) (int, error)
}
