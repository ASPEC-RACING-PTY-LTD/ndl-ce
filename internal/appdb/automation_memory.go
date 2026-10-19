package appdb

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"
)

type autoMem struct {
	flows       map[string]AutomationFlow
	versions    map[string]AutomationFlowVersion
	conns       map[string]AutomationConnection
	webhooks    map[string]AutomationWebhook
	deliveries  []AutomationWebhookDelivery
	schedules   map[string]AutomationSchedule
	runs        map[string]AutomationRun
	nodeRuns    map[string]AutomationNodeRun
	attempts    []AutomationNodeAttempt
	logs        []AutomationRunLog
	ledger      []AutomationLedgerEntry
	logSequence int
}

func (m *Memory) am() *autoMem {
	if m.auto == nil {
		m.auto = &autoMem{
			flows: map[string]AutomationFlow{}, versions: map[string]AutomationFlowVersion{},
			conns: map[string]AutomationConnection{}, webhooks: map[string]AutomationWebhook{},
			schedules: map[string]AutomationSchedule{}, runs: map[string]AutomationRun{},
			nodeRuns: map[string]AutomationNodeRun{},
		}
	}
	return m.auto
}

func (m *Memory) CreateAutomationFlow(_ context.Context, f AutomationFlow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	if _, ok := a.flows[f.ID]; ok {
		return fmt.Errorf("flow already exists")
	}
	for _, existing := range a.flows {
		if existing.ClusterID == f.ClusterID && existing.Name == f.Name {
			return fmt.Errorf("a flow named %q already exists", f.Name)
		}
	}
	now := time.Now().UTC()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	a.flows[f.ID] = f
	return nil
}

func (m *Memory) GetAutomationFlow(_ context.Context, clusterID, id string) (*AutomationFlow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.am().flows[id]
	if !ok || f.ClusterID != clusterID {
		return nil, nil
	}
	return &f, nil
}

func (m *Memory) ListAutomationFlows(_ context.Context, clusterID string) ([]AutomationFlow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AutomationFlow
	for _, f := range m.am().flows {
		if clusterID == "" || f.ClusterID == clusterID {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) ListAutomationFlowsByTrigger(_ context.Context, clusterID, kind, eventKind string) ([]AutomationFlow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AutomationFlow
	for _, f := range m.am().flows {
		if f.ClusterID != clusterID || !f.Enabled || f.PublishedVersion == 0 || f.TriggerKind != kind {
			continue
		}
		if eventKind != "" && f.EventKind != eventKind {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) UpdateAutomationFlow(_ context.Context, f AutomationFlow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	old, ok := a.flows[f.ID]
	if !ok || old.ClusterID != f.ClusterID {
		return fmt.Errorf("flow not found")
	}
	for _, existing := range a.flows {
		if existing.ID != f.ID && existing.ClusterID == f.ClusterID && existing.Name == f.Name {
			return fmt.Errorf("a flow named %q already exists", f.Name)
		}
	}
	f.CreatedAt = old.CreatedAt
	f.UpdatedAt = time.Now().UTC()
	a.flows[f.ID] = f
	return nil
}

func (m *Memory) DeleteAutomationFlow(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	f, ok := a.flows[id]
	if !ok || f.ClusterID != clusterID {
		return nil
	}
	delete(a.flows, id)
	delete(a.webhooks, id)
	delete(a.schedules, id)
	for k, v := range a.versions {
		if v.FlowID == id {
			delete(a.versions, k)
		}
	}
	return nil
}

func versionKey(flowID string, v int) string { return flowID + "#" + strconv.Itoa(v) }

func (m *Memory) CreateAutomationFlowVersion(_ context.Context, v AutomationFlowVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	key := versionKey(v.FlowID, v.Version)
	if _, ok := a.versions[key]; ok {
		return fmt.Errorf("version %d already exists", v.Version)
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	a.versions[key] = v
	return nil
}

func (m *Memory) GetAutomationFlowVersion(_ context.Context, clusterID, flowID string, version int) (*AutomationFlowVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.am().versions[versionKey(flowID, version)]
	if !ok || v.ClusterID != clusterID {
		return nil, nil
	}
	return &v, nil
}

func (m *Memory) ListAutomationFlowVersions(_ context.Context, clusterID, flowID string) ([]AutomationFlowVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AutomationFlowVersion
	for _, v := range m.am().versions {
		if v.ClusterID == clusterID && v.FlowID == flowID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

func (m *Memory) CreateAutomationConnection(_ context.Context, c AutomationConnection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	for _, existing := range a.conns {
		if existing.ClusterID == c.ClusterID && existing.Name == c.Name {
			return fmt.Errorf("a connection named %q already exists", c.Name)
		}
	}
	now := time.Now().UTC()
	c.CreatedAt, c.UpdatedAt = now, now
	a.conns[c.ID] = c
	return nil
}

func (m *Memory) GetAutomationConnection(_ context.Context, clusterID, id string) (*AutomationConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.am().conns[id]
	if !ok || c.ClusterID != clusterID {
		return nil, nil
	}
	return &c, nil
}

func (m *Memory) ListAutomationConnections(_ context.Context, clusterID string) ([]AutomationConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AutomationConnection
	for _, c := range m.am().conns {
		if c.ClusterID == clusterID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Memory) UpdateAutomationConnection(_ context.Context, c AutomationConnection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	old, ok := a.conns[c.ID]
	if !ok || old.ClusterID != c.ClusterID {
		return fmt.Errorf("connection not found")
	}
	c.CreatedAt = old.CreatedAt
	c.UpdatedAt = time.Now().UTC()
	a.conns[c.ID] = c
	return nil
}

func (m *Memory) DeleteAutomationConnection(_ context.Context, clusterID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	if c, ok := a.conns[id]; ok && c.ClusterID == clusterID {
		delete(a.conns, id)
	}
	return nil
}

func (m *Memory) PutAutomationWebhook(_ context.Context, w AutomationWebhook) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.am().webhooks[w.FlowID] = w
	return nil
}

func (m *Memory) GetAutomationWebhook(_ context.Context, clusterID, flowID string) (*AutomationWebhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.am().webhooks[flowID]
	if !ok || w.ClusterID != clusterID {
		return nil, nil
	}
	return &w, nil
}

func (m *Memory) GetAutomationWebhookByToken(_ context.Context, tokenHash string) (*AutomationWebhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.am().webhooks {
		if w.TokenHash == tokenHash {
			cp := w
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) AddAutomationWebhookDelivery(_ context.Context, d AutomationWebhookDelivery, keep int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	a.deliveries = append(a.deliveries, d)
	n := 0
	for i := len(a.deliveries) - 1; i >= 0; i-- {
		if a.deliveries[i].FlowID == d.FlowID {
			n++
			if keep > 0 && n > keep {
				a.deliveries = append(a.deliveries[:i], a.deliveries[i+1:]...)
			}
		}
	}
	return nil
}

func (m *Memory) ListAutomationWebhookDeliveries(_ context.Context, clusterID, flowID string, limit int) ([]AutomationWebhookDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AutomationWebhookDelivery
	a := m.am()
	for i := len(a.deliveries) - 1; i >= 0; i-- {
		d := a.deliveries[i]
		if d.ClusterID == clusterID && (flowID == "" || d.FlowID == flowID) {
			out = append(out, d)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *Memory) PutAutomationSchedule(_ context.Context, s AutomationSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.am().schedules[s.FlowID] = s
	return nil
}

func (m *Memory) DeleteAutomationSchedule(_ context.Context, clusterID, flowID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	if s, ok := a.schedules[flowID]; ok && s.ClusterID == clusterID {
		delete(a.schedules, flowID)
	}
	return nil
}

func (m *Memory) ListDueAutomationSchedules(_ context.Context, now time.Time, limit int) ([]AutomationSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AutomationSchedule
	for _, s := range m.am().schedules {
		if s.NextRunAt != nil && !s.NextRunAt.After(now) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextRunAt.Before(*out[j].NextRunAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) CreateAutomationRun(_ context.Context, r AutomationRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	if _, ok := a.runs[r.ID]; ok {
		return fmt.Errorf("run already exists")
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	a.runs[r.ID] = r
	return nil
}

func (m *Memory) GetAutomationRun(_ context.Context, clusterID, id string) (*AutomationRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.am().runs[id]
	if !ok || (clusterID != "" && r.ClusterID != clusterID) {
		return nil, nil
	}
	return &r, nil
}

func (m *Memory) UpdateAutomationRun(_ context.Context, r AutomationRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	old, ok := a.runs[r.ID]
	if !ok || old.ClusterID != r.ClusterID {
		return fmt.Errorf("run not found")
	}
	r.CreatedAt = old.CreatedAt
	r.UpdatedAt = time.Now().UTC()
	// The lease is only changed by Claim and Release.
	r.LeaseOwner, r.LeaseUntil = old.LeaseOwner, old.LeaseUntil
	a.runs[r.ID] = r
	return nil
}

func (m *Memory) ListAutomationRuns(_ context.Context, clusterID string, f AutomationRunFilter) ([]AutomationRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AutomationRun
	for _, r := range m.am().runs {
		if r.ClusterID != clusterID || (f.FlowID != "" && r.FlowID != f.FlowID) || (f.Status != "" && r.Status != f.Status) || (f.Mode != "" && r.Mode != f.Mode) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (m *Memory) FindAutomationRunByKey(_ context.Context, clusterID, flowID, key string, since time.Time) (*AutomationRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.am().runs {
		if r.ClusterID == clusterID && r.FlowID == flowID && r.IdempotencyKey == key && key != "" && !r.CreatedAt.Before(since) && r.Mode == AutoModeLive {
			cp := r
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *Memory) CountAutomationRunsSince(_ context.Context, clusterID, flowID string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.am().runs {
		if r.ClusterID == clusterID && r.FlowID == flowID && r.Mode == AutoModeLive && !r.CreatedAt.Before(since) && r.TriggerKind != "manual" && r.Status != AutoRunDropped {
			n++
		}
	}
	return n, nil
}

func (m *Memory) ListDueAutomationRuns(_ context.Context, now time.Time, limit int) ([]AutomationRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AutomationRun
	for _, r := range m.am().runs {
		if autoRunDue(r, now) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func autoRunDue(r AutomationRun, now time.Time) bool {
	leased := r.LeaseUntil != nil && r.LeaseUntil.After(now)
	switch r.Status {
	case AutoRunPending:
		return !leased
	case AutoRunWaiting:
		return !leased && r.WakeAt != nil && !r.WakeAt.After(now)
	case AutoRunRunning:
		return !leased
	}
	return false
}

func (m *Memory) ClaimAutomationRun(_ context.Context, clusterID, id, owner string, now, until time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	r, ok := a.runs[id]
	if !ok || r.ClusterID != clusterID {
		return false, nil
	}
	if r.LeaseOwner != "" && r.LeaseOwner != owner && r.LeaseUntil != nil && r.LeaseUntil.After(now) {
		return false, nil
	}
	u := until
	r.LeaseOwner, r.LeaseUntil = owner, &u
	a.runs[id] = r
	return true, nil
}

func (m *Memory) ReleaseAutomationRun(_ context.Context, clusterID, id, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	r, ok := a.runs[id]
	if !ok || r.ClusterID != clusterID || r.LeaseOwner != owner {
		return nil
	}
	r.LeaseOwner, r.LeaseUntil = "", nil
	a.runs[id] = r
	return nil
}

func (m *Memory) UpsertAutomationNodeRun(_ context.Context, n AutomationNodeRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	key := n.RunID + "/" + n.Scope + "/" + n.NodeID
	if old, ok := a.nodeRuns[key]; ok {
		n.ID = old.ID
		if len(n.Input) == 0 {
			n.Input = old.Input
		}
	}
	a.nodeRuns[key] = n
	return nil
}

func (m *Memory) ListAutomationNodeRuns(_ context.Context, clusterID, runID string) ([]AutomationNodeRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AutomationNodeRun
	for _, n := range m.am().nodeRuns {
		if n.ClusterID == clusterID && n.RunID == runID {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := out[i].StartedAt, out[j].StartedAt
		if ai == nil || aj == nil {
			return ai != nil
		}
		return ai.Before(*aj)
	})
	return out, nil
}

func (m *Memory) AddAutomationNodeAttempt(_ context.Context, at AutomationNodeAttempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	for i := range a.attempts {
		if a.attempts[i].ID == at.ID {
			a.attempts[i] = at
			return nil
		}
	}
	a.attempts = append(a.attempts, at)
	return nil
}

func (m *Memory) ListAutomationNodeAttempts(_ context.Context, clusterID, runID string) ([]AutomationNodeAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AutomationNodeAttempt
	for _, at := range m.am().attempts {
		if at.ClusterID == clusterID && at.RunID == runID {
			out = append(out, at)
		}
	}
	return out, nil
}

func (m *Memory) AddAutomationRunLog(_ context.Context, l AutomationRunLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.am()
	a.logSequence++
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now().UTC()
	}
	a.logs = append(a.logs, l)
	return nil
}

func (m *Memory) ListAutomationRunLogs(_ context.Context, clusterID, runID string, limit int) ([]AutomationRunLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AutomationRunLog
	for _, l := range m.am().logs {
		if l.ClusterID == clusterID && l.RunID == runID {
			out = append(out, l)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (m *Memory) AddAutomationLedger(_ context.Context, e AutomationLedgerEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	a := m.am()
	a.ledger = append(a.ledger, e)
	return nil
}

func (m *Memory) CountAutomationLedger(_ context.Context, clusterID, action, target string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.am().ledger {
		if e.ClusterID == clusterID && e.Action == action && e.Target == target && !e.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}
