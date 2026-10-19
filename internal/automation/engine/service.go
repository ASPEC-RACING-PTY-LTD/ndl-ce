package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/automation/flowdef"
)

// Service is the automation domain: starting runs under the safety limits,
// dispatching events and schedules, and the circuit breaker.
type Service struct {
	Store  appdb.AutomationStore
	Engine *Engine
	Bus    *Bus
	Now    func() time.Time
	// Audit records an audit line for work no request started.
	Audit func(ctx context.Context, clusterID, actor, action, result, detail string)
	// Notify records a platform event, such as a flow turned off by the
	// circuit breaker.
	Notify func(ctx context.Context, clusterID, kind string, payload map[string]string)
	// Wake asks the worker to look for due runs now.
	Wake func()
	// Logf writes operator log lines (dropped triggers).
	Logf func(format string, args ...any)

	mu       sync.Mutex
	debounce map[string]time.Time
	defs     map[string]*flowdef.Definition
}

// Errors a trigger can return.
var (
	ErrNotFound     = errors.New("flow not found")
	ErrNotPublished = errors.New("publish the flow before running it")
	ErrDisabled     = errors.New("the flow is turned off")
	ErrNotApproved  = errors.New("this version of the flow is not approved")
)

// StartRequest starts one run.
type StartRequest struct {
	ClusterID string
	FlowID    string
	// TriggerKind is manual, schedule, webhook or event.
	TriggerKind    string
	Payload        map[string]any
	Inputs         map[string]any
	IdempotencyKey string
	ActorID        string
	StartedBy      string
	CausedByRun    string
	ChainDepth     int
	RetryOf        string
	// UserStarted runs skip the enabled check and the trigger rate limit
	// (manual runs and retries).
	UserStarted bool
	DryRun      bool
	// Draft tests the flow's draft instead of its published version.
	Draft bool
	// Definition and Version replace the flow's (retries).
	Definition *flowdef.Definition
	Version    int
	Report     []ReportLine
}

// StartResult is a started (or deduplicated, or dropped) run.
type StartResult struct {
	Run      *appdb.AutomationRun
	Existing bool
	Dropped  bool
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func (s *Service) wake() {
	if s.Wake != nil {
		s.Wake()
	}
}

func (s *Service) audit(ctx context.Context, clusterID, actor, action, result, detail string) {
	if s.Audit != nil {
		s.Audit(ctx, clusterID, actor, action, result, detail)
	}
}

// FlowLimits reads a flow's limits with defaults and hard caps applied.
func FlowLimits(f appdb.AutomationFlow) flowdef.Limits {
	var l flowdef.Limits
	_ = json.Unmarshal(f.Limits, &l)
	return l.Normalized()
}

// DefinitionHash identifies a definition's behaviour. Canvas positions do
// not change it.
func DefinitionHash(raw json.RawMessage) string {
	d, err := flowdef.Parse(raw)
	if err != nil {
		return ""
	}
	d.Trigger.Position = nil
	for i := range d.Nodes {
		d.Nodes[i].Position = nil
		d.Nodes[i].Name = ""
	}
	sum := sha256.Sum256(d.Encode())
	return hex.EncodeToString(sum[:])
}

// Definition returns a flow's published definition, cached by version.
func (s *Service) Definition(ctx context.Context, f appdb.AutomationFlow) (*flowdef.Definition, error) {
	if f.PublishedVersion == 0 {
		return nil, ErrNotPublished
	}
	key := fmt.Sprintf("%s#%d", f.ID, f.PublishedVersion)
	s.mu.Lock()
	d := s.defs[key]
	s.mu.Unlock()
	if d != nil {
		return d, nil
	}
	v, err := s.Store.GetAutomationFlowVersion(ctx, f.ClusterID, f.ID, f.PublishedVersion)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, ErrNotPublished
	}
	// Only an approved version runs, and only the definition the approval
	// covered.
	if v.Status != appdb.FlowVersionApproved || (v.Hash != "" && DefinitionHash(v.Definition) != v.Hash) {
		return nil, ErrNotApproved
	}
	d, err = flowdef.Parse(v.Definition)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.defs == nil {
		s.defs = map[string]*flowdef.Definition{}
	}
	s.defs[key] = d
	s.mu.Unlock()
	return d, nil
}

// Start records a run, applying idempotency, the trigger rate limit and
// the flow's concurrency queue.
func (s *Service) Start(ctx context.Context, req StartRequest) (StartResult, error) {
	now := s.now()
	flow, err := s.Store.GetAutomationFlow(ctx, req.ClusterID, req.FlowID)
	if err != nil {
		return StartResult{}, err
	}
	if flow == nil {
		return StartResult{}, ErrNotFound
	}
	def := req.Definition
	version := req.Version
	if def == nil {
		if req.DryRun && req.Draft {
			if def, err = flowdef.Parse(flow.Draft); err != nil {
				return StartResult{}, err
			}
			version = 0
		} else {
			if def, err = s.Definition(ctx, *flow); err != nil {
				return StartResult{}, err
			}
			version = flow.PublishedVersion
		}
	}
	live := !req.DryRun
	if live && !req.UserStarted && !flow.Enabled {
		return StartResult{}, ErrDisabled
	}
	if live && req.IdempotencyKey != "" {
		existing, err := s.Store.FindAutomationRunByKey(ctx, req.ClusterID, flow.ID, req.IdempotencyKey, now.Add(-flowdef.IdempotencyWindow))
		if err != nil {
			return StartResult{}, err
		}
		if existing != nil {
			return StartResult{Run: existing, Existing: true}, nil
		}
	}
	limits := FlowLimits(*flow)
	mode := appdb.AutoModeLive
	if req.DryRun {
		mode = appdb.AutoModeDryRun
	}
	payload, _ := Redact(req.Payload).(map[string]any)
	inputs, _ := Redact(req.Inputs).(map[string]any)
	payloadRaw, _ := json.Marshal(payload)
	inputsRaw, _ := json.Marshal(inputs)
	vars := map[string]any{}
	for k, v := range def.Variables {
		vars[k] = v
	}
	run := appdb.AutomationRun{
		ID: uuid.NewString(), ClusterID: req.ClusterID, FlowID: flow.ID, Version: version, Mode: mode, Status: appdb.AutoRunPending,
		TriggerKind: firstText(req.TriggerKind, "manual"), TriggerPayload: payloadRaw, Inputs: inputsRaw, Definition: def.Encode(),
		State: NewState(limits, vars, req.Report), IdempotencyKey: req.IdempotencyKey, CorrelationID: uuid.NewString(),
		CausedByRun: req.CausedByRun, ChainDepth: req.ChainDepth, RetryOf: req.RetryOf, ActorID: req.ActorID,
		StartedBy: req.StartedBy, CreatedAt: now,
	}
	drop := ""
	if live && !req.UserStarted {
		perMin, err := s.Store.CountAutomationRunsSince(ctx, req.ClusterID, flow.ID, now.Add(-time.Minute))
		if err != nil {
			return StartResult{}, err
		}
		perHour, err := s.Store.CountAutomationRunsSince(ctx, req.ClusterID, flow.ID, now.Add(-time.Hour))
		if err != nil {
			return StartResult{}, err
		}
		if perMin >= limits.PerMinute || perHour >= limits.PerHour {
			drop = fmt.Sprintf("limit: the flow already started %d times this minute and %d this hour (limits %d and %d); this trigger was dropped",
				perMin, perHour, limits.PerMinute, limits.PerHour)
		}
	}
	if live && drop == "" {
		active, queued, err := s.counts(ctx, req.ClusterID, flow.ID)
		if err != nil {
			return StartResult{}, err
		}
		if active >= limits.Concurrency {
			if queued >= limits.QueueDepth {
				drop = fmt.Sprintf("limit: %d run(s) are already waiting in the queue; this trigger was dropped", queued)
			} else {
				run.Status = appdb.AutoRunQueued
			}
		}
	}
	if drop != "" {
		run.Status, run.Error, run.FinishedAt = appdb.AutoRunDropped, drop, &now
		if err := s.Store.CreateAutomationRun(ctx, run); err != nil {
			return StartResult{}, err
		}
		_ = s.Store.AddAutomationRunLog(ctx, appdb.AutomationRunLog{ID: uuid.NewString(), ClusterID: run.ClusterID, RunID: run.ID, Level: "warn", Message: drop, CreatedAt: now})
		s.logf("automation: flow %s: %s", flow.Name, drop)
		s.breakerHit(ctx, flow.ClusterID, flow.ID, drop)
		s.audit(ctx, run.ClusterID, req.ActorID, "automation.flow.execute", "dropped", flow.ID+" "+run.ID)
		return StartResult{Run: &run, Dropped: true}, nil
	}
	if err := s.Store.CreateAutomationRun(ctx, run); err != nil {
		return StartResult{}, err
	}
	if run.Status == appdb.AutoRunQueued {
		_ = s.Store.AddAutomationRunLog(ctx, appdb.AutomationRunLog{ID: uuid.NewString(), ClusterID: run.ClusterID, RunID: run.ID, Level: "info",
			Message: "queued: another run of this flow is still going", CreatedAt: now})
	}
	action := "automation.flow.execute"
	if req.DryRun {
		action = "automation.flow.dry_run"
	}
	s.audit(ctx, run.ClusterID, req.ActorID, action, run.Status, flow.ID+" "+run.ID)
	s.wake()
	return StartResult{Run: &run}, nil
}

func (s *Service) counts(ctx context.Context, clusterID, flowID string) (active, queued int, err error) {
	runs, err := s.Store.ListAutomationRuns(ctx, clusterID, appdb.AutomationRunFilter{FlowID: flowID, Mode: appdb.AutoModeLive, Limit: 500})
	if err != nil {
		return 0, 0, err
	}
	for _, r := range runs {
		if appdb.AutoRunActive(r.Status) {
			active++
		}
		if r.Status == appdb.AutoRunQueued {
			queued++
		}
	}
	return active, queued, nil
}

// promote starts queued runs while the flow has free slots.
func (s *Service) promote(ctx context.Context, clusterID, flowID string) {
	flow, err := s.Store.GetAutomationFlow(ctx, clusterID, flowID)
	if err != nil || flow == nil {
		return
	}
	limits := FlowLimits(*flow)
	runs, err := s.Store.ListAutomationRuns(ctx, clusterID, appdb.AutomationRunFilter{FlowID: flowID, Mode: appdb.AutoModeLive, Limit: 500})
	if err != nil {
		return
	}
	active := 0
	var queued []appdb.AutomationRun
	for _, r := range runs {
		if appdb.AutoRunActive(r.Status) {
			active++
		}
		if r.Status == appdb.AutoRunQueued {
			queued = append(queued, r)
		}
	}
	sort.Slice(queued, func(i, j int) bool { return queued[i].CreatedAt.Before(queued[j].CreatedAt) })
	promoted := false
	for _, r := range queued {
		if active >= limits.Concurrency {
			break
		}
		r.Status = appdb.AutoRunPending
		if s.Store.UpdateAutomationRun(ctx, r) == nil {
			active++
			promoted = true
		}
	}
	if promoted {
		s.wake()
	}
}

// RunFinished is the engine's Finished hook: the circuit breaker, the
// test-run record and the next queued run.
func (s *Service) RunFinished(ctx context.Context, run appdb.AutomationRun) {
	if run.Mode == appdb.AutoModeDryRun {
		var rep Report
		_ = json.Unmarshal(run.Report, &rep)
		if flow, _ := s.Store.GetAutomationFlow(ctx, run.ClusterID, run.FlowID); flow != nil && rep.OK && run.Status == appdb.AutoRunSucceeded {
			hash := DefinitionHash(run.Definition)
			if hash != "" && hash == DefinitionHash(flow.Draft) {
				flow.DryRunHash = hash
				_ = s.Store.UpdateAutomationFlow(ctx, *flow)
			}
		}
		return
	}
	switch run.Status {
	case appdb.AutoRunSucceeded:
		if flow, _ := s.Store.GetAutomationFlow(ctx, run.ClusterID, run.FlowID); flow != nil && flow.ConsecutiveFailures != 0 {
			flow.ConsecutiveFailures = 0
			_ = s.Store.UpdateAutomationFlow(ctx, *flow)
		}
	case appdb.AutoRunFailed:
		s.breakerHit(ctx, run.ClusterID, run.FlowID, run.Error)
	}
	s.promote(ctx, run.ClusterID, run.FlowID)
}

// breakerHit counts a failure or limit hit and turns the flow off after
// the flow's breaker threshold of them in a row.
func (s *Service) breakerHit(ctx context.Context, clusterID, flowID, reason string) {
	flow, err := s.Store.GetAutomationFlow(ctx, clusterID, flowID)
	if err != nil || flow == nil {
		return
	}
	flow.ConsecutiveFailures++
	limits := FlowLimits(*flow)
	tripped := false
	if flow.Enabled && flow.ConsecutiveFailures >= limits.BreakerThreshold {
		flow.Enabled = false
		flow.DisabledReason = fmt.Sprintf("turned off after %d failures or limit hits in a row; last: %s", flow.ConsecutiveFailures, reason)
		tripped = true
	}
	if err := s.Store.UpdateAutomationFlow(ctx, *flow); err != nil {
		return
	}
	if tripped {
		_ = s.Store.DeleteAutomationSchedule(ctx, clusterID, flowID)
		s.logf("automation: flow %s %s", flow.Name, flow.DisabledReason)
		if s.Notify != nil {
			s.Notify(ctx, clusterID, "automation.flow_disabled", map[string]string{"flow_id": flow.ID, "name": flow.Name, "reason": flow.DisabledReason})
		}
		s.audit(ctx, clusterID, "", "automation.flow.disable", "circuit_breaker", flow.ID)
	}
}

// EventPayload is the trigger data a flow sees for an event.
func EventPayload(ev Event) map[string]any {
	out := map[string]any{}
	for k, v := range ev.Payload {
		out[k] = v
	}
	out["event"] = map[string]any{
		"kind": ev.Kind, "target": ev.Target, "node_id": ev.NodeID, "actor_id": ev.ActorID, "at": ev.At.UTC().Format(time.RFC3339),
	}
	return out
}

// WhenLines turns WHEN results into test-run lines.
func WhenLines(matched bool, results []flowdef.ConditionResult) []ReportLine {
	rules := make([]RuleResult, 0, len(results))
	for _, r := range results {
		rule := r.Field
		if rule == "" {
			rule = "event is " + r.Kind
		}
		rules = append(rules, RuleResult{Rule: r.Path + " " + rule, Left: r.Actual, Op: r.Op, Right: r.Expected, Matched: r.Matched})
	}
	text := "the WHEN conditions match"
	if !matched {
		text = "the WHEN conditions do not match, so the flow would not start"
	}
	if len(results) == 0 {
		text = "no WHEN conditions: every event of this kind starts the flow"
	}
	m := matched
	return []ReportLine{{Node: flowdef.TriggerID, Kind: "when", Text: text, Matched: &m, Rules: rules}}
}

// InScope reports whether an event on nodeID reaches a flow. An event
// trigger's scope_nodes lists the nodes it applies to; empty means all.
func InScope(def *flowdef.Definition, nodeID string) bool {
	list, _ := def.Trigger.Config["scope_nodes"].([]any)
	if len(list) == 0 {
		return true
	}
	for _, n := range list {
		if fmt.Sprint(n) == nodeID {
			return true
		}
	}
	return false
}

// HandleEvent starts every enabled flow listening for ev whose WHEN
// conditions match, with self-trigger, chain depth and debounce checks.
func (s *Service) HandleEvent(ctx context.Context, ev Event) {
	flows, err := s.Store.ListAutomationFlowsByTrigger(ctx, ev.ClusterID, "event", ev.Kind)
	if err != nil || len(flows) == 0 {
		return
	}
	depth := 0
	var cause *appdb.AutomationRun
	if ev.CausedByRun != "" {
		cause, _ = s.Store.GetAutomationRun(ctx, ev.ClusterID, ev.CausedByRun)
		if cause != nil {
			depth = cause.ChainDepth + 1
		}
	}
	payload := EventPayload(ev)
	now := s.now()
	for _, flow := range flows {
		if cause != nil && cause.FlowID == flow.ID {
			s.logf("automation: flow %s ignored %s: its own run %s caused it", flow.Name, ev.Kind, cause.ID)
			continue
		}
		if depth > flowdef.MaxChainDepth {
			s.logf("automation: flow %s ignored %s: automations already triggered each other %d times in a row", flow.Name, ev.Kind, depth)
			continue
		}
		def, err := s.Definition(ctx, flow)
		if err != nil {
			continue
		}
		conds, err := flowdef.ParseConditions(def.Trigger.Config["conditions"])
		if err != nil {
			continue
		}
		ok, results := flowdef.MatchConditions(conds, ev.Kind, payload)
		if !ok || !InScope(def, ev.NodeID) {
			continue
		}
		limits := FlowLimits(flow)
		if limits.DebounceSec > 0 {
			key := flow.ID + "|" + ev.Kind + "|" + ev.Target
			s.mu.Lock()
			if s.debounce == nil {
				s.debounce = map[string]time.Time{}
			}
			last, seen := s.debounce[key]
			if seen && now.Sub(last) < time.Duration(limits.DebounceSec)*time.Second {
				s.mu.Unlock()
				continue
			}
			s.debounce[key] = now
			s.mu.Unlock()
		}
		_, err = s.Start(ctx, StartRequest{
			ClusterID: ev.ClusterID, FlowID: flow.ID, TriggerKind: "event", Payload: payload, ActorID: ev.ActorID,
			StartedBy: "event " + ev.Kind, CausedByRun: ev.CausedByRun, ChainDepth: depth, Report: WhenLines(true, results),
		})
		if err != nil && !errors.Is(err, ErrDisabled) {
			s.logf("automation: flow %s could not start for %s: %v", flow.Name, ev.Kind, err)
		}
	}
}

// ProcessDueSchedules starts flows whose schedule is due. The minute is
// the idempotency key, so a schedule never runs twice for one minute.
func (s *Service) ProcessDueSchedules(ctx context.Context, limit int) int {
	now := s.now()
	due, err := s.Store.ListDueAutomationSchedules(ctx, now, limit)
	if err != nil {
		return 0
	}
	started := 0
	for _, sc := range due {
		flow, _ := s.Store.GetAutomationFlow(ctx, sc.ClusterID, sc.FlowID)
		if flow == nil || !flow.Enabled || flow.PublishedVersion == 0 || flow.TriggerKind != "schedule" {
			_ = s.Store.DeleteAutomationSchedule(ctx, sc.ClusterID, sc.FlowID)
			continue
		}
		key := "schedule:" + sc.NextRunAt.UTC().Truncate(time.Minute).Format("2006-01-02T15:04")
		if key != sc.LastKey {
			res, err := s.Start(ctx, StartRequest{
				ClusterID: sc.ClusterID, FlowID: sc.FlowID, TriggerKind: "schedule", IdempotencyKey: key, StartedBy: "schedule",
				Payload: map[string]any{"scheduled_at": sc.NextRunAt.UTC().Format(time.RFC3339), "preset": sc.Preset, "cron": sc.Cron, "timezone": sc.Timezone},
			})
			if err == nil && res.Run != nil && !res.Existing {
				started++
			}
		}
		spec := flowdef.ScheduleSpec{Preset: sc.Preset, Cron: sc.Cron, Timezone: sc.Timezone}
		if def, err := s.Definition(ctx, *flow); err == nil {
			spec = flowdef.ScheduleFromConfig(def.Trigger.Config)
		}
		sc.LastKey = key
		if next, err := spec.Next(now); err == nil {
			sc.NextRunAt = &next
		} else {
			sc.NextRunAt = nil
		}
		_ = s.Store.PutAutomationSchedule(ctx, sc)
	}
	return started
}

// SyncTrigger saves or removes a flow's schedule after publish, enable or
// disable.
func (s *Service) SyncTrigger(ctx context.Context, flow appdb.AutomationFlow) error {
	if !flow.Enabled || flow.PublishedVersion == 0 || flow.TriggerKind != "schedule" {
		return s.Store.DeleteAutomationSchedule(ctx, flow.ClusterID, flow.ID)
	}
	def, err := s.Definition(ctx, flow)
	if err != nil {
		return err
	}
	spec := flowdef.ScheduleFromConfig(def.Trigger.Config)
	next, err := spec.Next(s.now())
	if err != nil {
		return err
	}
	line, _ := spec.CronLine()
	return s.Store.PutAutomationSchedule(ctx, appdb.AutomationSchedule{
		FlowID: flow.ID, ClusterID: flow.ClusterID, Preset: spec.Preset, Cron: line, Timezone: spec.Timezone, NextRunAt: &next,
	})
}

// Cancel stops a run that has not finished.
func (s *Service) Cancel(ctx context.Context, clusterID, runID, by string) error {
	run, err := s.Store.GetAutomationRun(ctx, clusterID, runID)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("run not found")
	}
	if appdb.AutoRunFinished(run.Status) {
		return fmt.Errorf("the run already finished")
	}
	if s.Engine.RequestCancel(runID, by) {
		return nil
	}
	now := s.now()
	ok, err := s.Store.ClaimAutomationRun(ctx, clusterID, runID, s.Engine.Owner, now, now.Add(time.Minute))
	if err != nil {
		return err
	}
	if !ok {
		return ErrBusy
	}
	defer func() { _ = s.Store.ReleaseAutomationRun(context.WithoutCancel(ctx), clusterID, runID, s.Engine.Owner) }()
	run, err = s.Store.GetAutomationRun(ctx, clusterID, runID)
	if err != nil || run == nil {
		return fmt.Errorf("run not found")
	}
	if st, err := DecodeState(run.State); err == nil {
		for _, f := range st.Frames {
			for id, ns := range f.Nodes {
				switch ns.Status {
				case NodeWaiting, NodeRetrying, NodeApproval, NodeRunning:
					ns.Status, ns.WakeAt = NodeCancelled, nil
					_ = s.Store.UpsertAutomationNodeRun(ctx, appdb.AutomationNodeRun{
						ID: nodeRunID(runID, f.Scope, id), ClusterID: clusterID, RunID: runID, NodeID: id, Scope: f.Scope,
						Status: NodeCancelled, Attempts: ns.Attempts, StartedAt: ns.StartedAt, FinishedAt: &now,
					})
				}
			}
		}
		run.State, _ = json.Marshal(st)
	}
	run.Status, run.Error, run.FinishedAt, run.WakeAt = appdb.AutoRunCancelled, "cancelled by "+by, &now, nil
	if err := s.Store.UpdateAutomationRun(ctx, *run); err != nil {
		return err
	}
	_ = s.Store.AddAutomationRunLog(ctx, appdb.AutomationRunLog{ID: uuid.NewString(), ClusterID: clusterID, RunID: runID, Level: "info", Message: "run cancelled by " + by, CreatedAt: now})
	s.RunFinished(ctx, *run)
	return nil
}

// Retry starts a new run of the same version with the same trigger data.
func (s *Service) Retry(ctx context.Context, clusterID, runID, actorID, by string) (StartResult, error) {
	run, err := s.Store.GetAutomationRun(ctx, clusterID, runID)
	if err != nil {
		return StartResult{}, err
	}
	if run == nil {
		return StartResult{}, fmt.Errorf("run not found")
	}
	if !appdb.AutoRunFinished(run.Status) {
		return StartResult{}, fmt.Errorf("the run has not finished")
	}
	def, err := flowdef.Parse(run.Definition)
	if err != nil {
		return StartResult{}, err
	}
	var payload, inputs map[string]any
	_ = json.Unmarshal(run.TriggerPayload, &payload)
	_ = json.Unmarshal(run.Inputs, &inputs)
	return s.Start(ctx, StartRequest{
		ClusterID: clusterID, FlowID: run.FlowID, TriggerKind: run.TriggerKind, Payload: payload, Inputs: inputs, ActorID: actorID,
		StartedBy: "retry by " + by, RetryOf: run.ID, UserStarted: true, DryRun: run.Mode == appdb.AutoModeDryRun,
		Definition: def, Version: run.Version,
	})
}

// DryRun tests a flow: the trigger and WHEN conditions are checked against
// the sample payload, the definition is validated and the run records what
// each action would do without doing it.
func (s *Service) DryRun(ctx context.Context, clusterID, flowID string, draft bool, payload, inputs map[string]any, actorID, by string) (StartResult, error) {
	flow, err := s.Store.GetAutomationFlow(ctx, clusterID, flowID)
	if err != nil {
		return StartResult{}, err
	}
	if flow == nil {
		return StartResult{}, ErrNotFound
	}
	var def *flowdef.Definition
	if draft {
		def, err = flowdef.Parse(flow.Draft)
	} else {
		def, err = s.Definition(ctx, *flow)
	}
	if err != nil {
		return StartResult{}, err
	}
	var lines []ReportLine
	problems := flowdef.Validate(def, s.Engine.Registry)
	for _, p := range problems {
		lines = append(lines, ReportLine{Kind: "problem", Text: p.Path + ": " + p.Message})
	}
	start := len(problems) == 0
	switch def.Trigger.Type {
	case flowdef.TriggerEvent:
		conds, _ := flowdef.ParseConditions(def.Trigger.Config["conditions"])
		kind := def.EventKind()
		if _, has := payload["event"]; !has {
			payload = EventPayload(Event{Kind: kind, Payload: payload, At: s.now()})
		}
		ok, results := flowdef.MatchConditions(conds, kind, payload)
		lines = append(lines, WhenLines(ok, results)...)
		start = start && ok
	case flowdef.TriggerSchedule:
		spec := flowdef.ScheduleFromConfig(def.Trigger.Config)
		if next, err := spec.Next(s.now()); err == nil {
			lines = append(lines, ReportLine{Node: flowdef.TriggerID, Kind: "trigger", Text: "the schedule next runs at " + next.Format(time.RFC3339)})
		}
	default:
		lines = append(lines, ReportLine{Node: flowdef.TriggerID, Kind: "trigger", Text: "starts from " + firstText(def.TriggerKind(), "a manual run")})
	}
	if !start {
		now := s.now()
		version := flow.PublishedVersion
		if draft {
			version = 0
		}
		rep, _ := json.Marshal(Report{OK: false, Lines: lines})
		if len(problems) == 0 {
			rep, _ = json.Marshal(Report{OK: true, Lines: lines})
		}
		pr, _ := json.Marshal(Redact(payload))
		status, msg := appdb.AutoRunSucceeded, "the flow would not start"
		if len(problems) > 0 {
			status, msg = appdb.AutoRunFailed, fmt.Sprintf("the flow has %d problem(s)", len(problems))
		}
		run := appdb.AutomationRun{
			ID: uuid.NewString(), ClusterID: clusterID, FlowID: flowID, Version: version, Mode: appdb.AutoModeDryRun, Status: status,
			TriggerKind: def.TriggerKind(), TriggerPayload: pr, Definition: def.Encode(), State: NewState(FlowLimits(*flow), nil, lines),
			Report: rep, ActorID: actorID, StartedBy: by, Error: msg, StartedAt: &now, FinishedAt: &now, CreatedAt: now,
		}
		if err := s.Store.CreateAutomationRun(ctx, run); err != nil {
			return StartResult{}, err
		}
		s.audit(ctx, clusterID, actorID, "automation.flow.dry_run", status, flowID+" "+run.ID)
		return StartResult{Run: &run}, nil
	}
	return s.Start(ctx, StartRequest{
		ClusterID: clusterID, FlowID: flowID, TriggerKind: def.TriggerKind(), Payload: payload, Inputs: inputs,
		ActorID: actorID, StartedBy: by, DryRun: true, Draft: draft, UserStarted: true, Report: lines,
	})
}
