package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/automation/expr"
	"github.com/no-dal/ndl-ce/internal/automation/flowdef"
)

// Node statuses inside a run's state.
const (
	NodeRunning   = "running"
	NodeSucceeded = "succeeded"
	NodeFailed    = "failed"
	NodeSkipped   = "skipped"
	NodeWaiting   = "waiting"
	NodeRetrying  = "retrying"
	NodeApproval  = "waiting_approval"
	NodeCancelled = "cancelled"
)

// ErrBusy means another worker holds the run's lease.
var ErrBusy = errors.New("another worker is stepping this run")

// LimitError is a safety limit refusing to go on.
type LimitError struct{ Msg string }

func (e *LimitError) Error() string { return e.Msg }

// IsLimit reports whether err is a safety limit.
func IsLimit(err error) bool {
	var l *LimitError
	return errors.As(err, &l)
}

// State is a run's resumable state, saved after every step.
type State struct {
	Limits  flowdef.Limits `json:"limits"`
	Vars    map[string]any `json:"vars"`
	Outputs map[string]any `json:"outputs"`
	Frames  []*Frame       `json:"frames"`
	Steps   int            `json:"steps"`
	Report  []ReportLine   `json:"report,omitempty"`
}

// Frame is the top level of a run or one iteration of a foreach.
type Frame struct {
	Loop    string                `json:"loop,omitempty"`
	Scope   string                `json:"scope"`
	Items   []any                 `json:"items,omitempty"`
	Index   int                   `json:"index"`
	Nodes   map[string]*NodeState `json:"nodes"`
	Results []any                 `json:"results,omitempty"`
}

// NodeState is one step's progress in a frame.
type NodeState struct {
	Status    string     `json:"status"`
	Port      string     `json:"port,omitempty"`
	Attempts  int        `json:"attempts,omitempty"`
	WakeAt    *time.Time `json:"wake_at,omitempty"`
	Error     string     `json:"error,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
}

// ReportLine is one finding of a test run, or a note on a live run.
type ReportLine struct {
	Node    string       `json:"node,omitempty"`
	Scope   string       `json:"scope,omitempty"`
	Kind    string       `json:"kind"`
	Text    string       `json:"text"`
	Matched *bool        `json:"matched,omitempty"`
	Rules   []RuleResult `json:"rules,omitempty"`
	Values  any          `json:"values,omitempty"`
}

// Report is a test run's outcome.
type Report struct {
	OK    bool         `json:"ok"`
	Lines []ReportLine `json:"lines"`
}

// NewState is the state a run starts with.
func NewState(limits flowdef.Limits, vars map[string]any, lines []ReportLine) json.RawMessage {
	if vars == nil {
		vars = map[string]any{}
	}
	st := State{
		Limits: limits.Normalized(), Vars: vars, Outputs: map[string]any{},
		Frames: []*Frame{{Scope: "", Nodes: map[string]*NodeState{}}}, Report: lines,
	}
	raw, _ := json.Marshal(st)
	return raw
}

// DecodeState reads a run's state.
func DecodeState(raw json.RawMessage) (*State, error) {
	var st State
	if len(raw) > 0 && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &st); err != nil {
			return nil, fmt.Errorf("run state is damaged: %v", err)
		}
	}
	if st.Vars == nil {
		st.Vars = map[string]any{}
	}
	if st.Outputs == nil {
		st.Outputs = map[string]any{}
	}
	if len(st.Frames) == 0 {
		st.Frames = []*Frame{{Scope: "", Nodes: map[string]*NodeState{}}}
	}
	for _, f := range st.Frames {
		if f.Nodes == nil {
			f.Nodes = map[string]*NodeState{}
		}
	}
	st.Limits = st.Limits.Normalized()
	return &st, nil
}

// Engine steps runs against the store.
type Engine struct {
	Store    appdb.AutomationStore
	Registry *Registry
	Now      func() time.Time
	// Owner names this worker on run leases.
	Owner string
	Lease time.Duration
	// Connection opens a saved connection's secret fields.
	Connection func(ctx context.Context, clusterID, id string) (map[string]any, error)
	// Finished is called once when a run ends.
	Finished func(ctx context.Context, run appdb.AutomationRun)
	// StepContext decorates the context adapters run with.
	StepContext func(ctx context.Context, run appdb.AutomationRun) context.Context

	mu      sync.Mutex
	running map[string]*stepper
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e *Engine) lease() time.Duration {
	if e.Lease > 0 {
		return e.Lease
	}
	return 2 * time.Minute
}

// Running reports whether this worker is stepping runID.
func (e *Engine) Running(runID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.running[runID]
	return ok
}

// RequestCancel stops a run this worker is stepping. It reports false when
// the run is not stepping here.
func (e *Engine) RequestCancel(runID, by string) bool {
	e.mu.Lock()
	x, ok := e.running[runID]
	e.mu.Unlock()
	if !ok {
		return false
	}
	x.cancel(by)
	return true
}

// Step advances a run until it finishes, parks (a delay, a retry backoff or
// an approval) or the context ends. Only the lease holder steps a run.
func (e *Engine) Step(ctx context.Context, clusterID, runID string) error {
	now := e.now()
	ok, err := e.Store.ClaimAutomationRun(ctx, clusterID, runID, e.Owner, now, now.Add(e.lease()))
	if err != nil {
		return err
	}
	if !ok {
		return ErrBusy
	}
	defer func() {
		_ = e.Store.ReleaseAutomationRun(context.WithoutCancel(ctx), clusterID, runID, e.Owner)
	}()
	run, err := e.Store.GetAutomationRun(ctx, clusterID, runID)
	if err != nil || run == nil {
		return err
	}
	switch run.Status {
	case appdb.AutoRunPending, appdb.AutoRunRunning, appdb.AutoRunWaiting:
	default:
		return nil
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if e.StepContext != nil {
		sctx = e.StepContext(sctx, *run)
	}
	x := &stepper{e: e, ctx: sctx, stop: cancel, run: run, inputs: map[string]json.RawMessage{}}
	e.mu.Lock()
	if e.running == nil {
		e.running = map[string]*stepper{}
	}
	e.running[runID] = x
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.running, runID)
		e.mu.Unlock()
	}()
	renewDone := make(chan struct{})
	defer close(renewDone)
	go func() {
		t := time.NewTicker(e.lease() / 3)
		defer t.Stop()
		for {
			select {
			case <-renewDone:
				return
			case <-t.C:
				n := e.now()
				_, _ = e.Store.ClaimAutomationRun(context.WithoutCancel(ctx), clusterID, runID, e.Owner, n, n.Add(e.lease()))
			}
		}
	}()
	return x.step()
}

// Approve records an approval decision for a step waiting on one and makes
// the run ready to continue.
func (e *Engine) Approve(ctx context.Context, clusterID, runID, nodeID, by string, approved bool, note string) error {
	now := e.now()
	ok, err := e.Store.ClaimAutomationRun(ctx, clusterID, runID, e.Owner, now, now.Add(e.lease()))
	if err != nil {
		return err
	}
	if !ok {
		return ErrBusy
	}
	defer func() { _ = e.Store.ReleaseAutomationRun(context.WithoutCancel(ctx), clusterID, runID, e.Owner) }()
	run, err := e.Store.GetAutomationRun(ctx, clusterID, runID)
	if err != nil || run == nil {
		return fmt.Errorf("run not found")
	}
	if run.Status != appdb.AutoRunWaitingApproval && run.Status != appdb.AutoRunWaiting {
		return fmt.Errorf("the run is not waiting for an approval")
	}
	st, err := DecodeState(run.State)
	if err != nil {
		return err
	}
	f := st.Frames[len(st.Frames)-1]
	if nodeID == "" {
		for id, ns := range f.Nodes {
			if ns.Status == NodeApproval {
				nodeID = id
			}
		}
	}
	ns := f.Nodes[nodeID]
	if ns == nil || ns.Status != NodeApproval {
		return fmt.Errorf("step %s is not waiting for an approval", nodeID)
	}
	ns.Status, ns.Port = NodeSucceeded, "rejected"
	if approved {
		ns.Port = "approved"
	}
	out := map[string]any{"approved": approved, "by": by, "at": now.Format(time.RFC3339)}
	if note != "" {
		out["note"] = note
	}
	if prev, ok := st.Outputs[nodeID].(map[string]any); ok {
		if msg, ok := prev["message"]; ok {
			out["message"] = msg
		}
	}
	st.Outputs[nodeID] = Redact(out)
	raw, _ := json.Marshal(st)
	run.State, run.Status, run.WakeAt = raw, appdb.AutoRunPending, nil
	if err := e.Store.UpdateAutomationRun(ctx, *run); err != nil {
		return err
	}
	finished := now
	_ = e.Store.UpsertAutomationNodeRun(ctx, appdb.AutomationNodeRun{
		ID: nodeRunID(runID, f.Scope, nodeID), ClusterID: clusterID, RunID: runID, NodeID: nodeID, Scope: f.Scope,
		Type: "logic.approval", Status: NodeSucceeded, Port: ns.Port, Output: RedactJSON(out), StartedAt: ns.StartedAt, FinishedAt: &finished,
	})
	word := "rejected"
	if approved {
		word = "approved"
	}
	_ = e.Store.AddAutomationRunLog(ctx, appdb.AutomationRunLog{
		ID: uuid.NewString(), ClusterID: clusterID, RunID: runID, NodeID: nodeID, Level: "info",
		Message: LogLine(fmt.Sprintf("%s %s by %s", nodeID, word, by)), CreatedAt: now,
	})
	return nil
}

func nodeRunID(runID, scope, nodeID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(runID+"/"+scope+"/"+nodeID)).String()
}

// Backoff is the wait before retry attempt+1.
func Backoff(r flowdef.Retry, attempt int) time.Duration {
	d := time.Duration(r.DelayMS) * time.Millisecond
	if d <= 0 {
		d = time.Second
	}
	if r.Mode == "exponential" {
		for i := 1; i < attempt && d < 24*time.Hour; i++ {
			d *= 2
		}
	}
	maxD := time.Duration(r.MaxDelayMS) * time.Millisecond
	if maxD <= 0 {
		maxD = 5 * time.Minute
	}
	if d > maxD {
		d = maxD
	}
	return d
}

// --- one run ---

type stepper struct {
	e    *Engine
	ctx  context.Context
	stop context.CancelFunc
	run  *appdb.AutomationRun
	def  *flowdef.Definition
	st   *State
	dry  bool

	owners  map[string]string
	payload map[string]any
	inputs  map[string]json.RawMessage

	cmu         sync.Mutex
	cancelledBy string
	imu         sync.Mutex
}

func (x *stepper) setInput(key string, raw json.RawMessage) {
	x.imu.Lock()
	x.inputs[key] = raw
	x.imu.Unlock()
}

func (x *stepper) input(key string) json.RawMessage {
	x.imu.Lock()
	defer x.imu.Unlock()
	return x.inputs[key]
}

func (x *stepper) cancel(by string) {
	x.cmu.Lock()
	if x.cancelledBy == "" {
		x.cancelledBy = firstText(by, "someone")
	}
	x.cmu.Unlock()
	x.stop()
}

func (x *stepper) cancelled() string {
	x.cmu.Lock()
	defer x.cmu.Unlock()
	return x.cancelledBy
}

func (x *stepper) log(nodeID, level, msg string) {
	_ = x.e.Store.AddAutomationRunLog(context.WithoutCancel(x.ctx), appdb.AutomationRunLog{
		ID: uuid.NewString(), ClusterID: x.run.ClusterID, RunID: x.run.ID, NodeID: nodeID, Level: level,
		Message: LogLine(msg), CreatedAt: x.e.now(),
	})
}

func (x *stepper) report(l ReportLine) {
	x.st.Report = append(x.st.Report, l)
}

func (x *stepper) save() error {
	raw, err := json.Marshal(x.st)
	if err != nil {
		return err
	}
	x.run.State = raw
	return x.e.Store.UpdateAutomationRun(context.WithoutCancel(x.ctx), *x.run)
}

func (x *stepper) finish(status, msg string) error {
	now := x.e.now()
	x.run.Status, x.run.Error, x.run.FinishedAt, x.run.WakeAt = status, msg, &now, nil
	if x.dry {
		ok := status == appdb.AutoRunSucceeded
		for _, l := range x.st.Report {
			if l.Kind == "problem" || l.Kind == "limit" {
				ok = false
			}
		}
		raw, _ := json.Marshal(Report{OK: ok, Lines: x.st.Report})
		x.run.Report = raw
	}
	err := x.save()
	line := "run " + status
	if msg != "" {
		line += ": " + msg
	}
	level := "info"
	if status == appdb.AutoRunFailed {
		level = "error"
	}
	x.log("", level, line)
	if x.e.Finished != nil {
		x.e.Finished(context.WithoutCancel(x.ctx), *x.run)
	}
	return err
}

func (x *stepper) step() error {
	def, err := flowdef.Parse(x.run.Definition)
	if err != nil {
		return x.finish(appdb.AutoRunFailed, err.Error())
	}
	x.def = def
	st, err := DecodeState(x.run.State)
	if err != nil {
		return x.finish(appdb.AutoRunFailed, err.Error())
	}
	x.st = st
	x.dry = x.run.Mode == appdb.AutoModeDryRun
	isLoop := func(typ string) bool {
		t, _ := x.e.Registry.Get(typ)
		return t.Loop
	}
	x.owners = flowdef.Owners(def, def.LoopBodies(isLoop))
	x.payload = map[string]any{}
	_ = json.Unmarshal(x.run.TriggerPayload, &x.payload)
	now := x.e.now()
	if x.run.StartedAt == nil {
		x.run.StartedAt = &now
		x.log("", "info", "run started ("+firstText(x.run.TriggerKind, "manual")+")")
	}
	x.run.Status, x.run.WakeAt = appdb.AutoRunRunning, nil
	failed := x.recoverInterrupted()
	if err := x.save(); err != nil {
		return err
	}
	if failed != "" {
		return x.finish(appdb.AutoRunFailed, failed)
	}
	for {
		if by := x.cancelled(); by != "" {
			x.markCancelled()
			return x.finish(appdb.AutoRunCancelled, "cancelled by "+by)
		}
		if x.ctx.Err() != nil {
			// The server is stopping: leave the run for the next worker.
			return x.save()
		}
		now := x.e.now()
		deadline := x.run.StartedAt.Add(time.Duration(x.st.Limits.RunTimeoutSec) * time.Second)
		if now.After(deadline) {
			x.report(ReportLine{Kind: "limit", Text: "the run passed its time limit"})
			return x.finish(appdb.AutoRunFailed, fmt.Sprintf("limit: the run passed its time limit of %s", time.Duration(x.st.Limits.RunTimeoutSec)*time.Second))
		}
		f := x.st.Frames[len(x.st.Frames)-1]
		x.wake(f, now)
		ready, skipped := x.ready(f)
		if len(skipped) > 0 {
			for _, id := range skipped {
				f.Nodes[id] = &NodeState{Status: NodeSkipped}
				x.saveNode(f, id, nil)
			}
			continue
		}
		if len(ready) == 0 {
			if parked, err := x.park(f, deadline); parked {
				return err
			}
			if f.Loop != "" {
				x.nextIteration()
				if err := x.save(); err != nil {
					return err
				}
				continue
			}
			return x.finish(appdb.AutoRunSucceeded, "")
		}
		if x.st.Steps >= x.st.Limits.MaxSteps {
			msg := fmt.Sprintf("limit: the run reached its limit of %d steps", x.st.Limits.MaxSteps)
			x.report(ReportLine{Kind: "limit", Text: msg})
			return x.finish(appdb.AutoRunFailed, msg)
		}
		if loop := x.firstLoop(ready); loop != "" {
			x.startLoop(f, loop)
			if err := x.save(); err != nil {
				return err
			}
			continue
		}
		budget := x.st.Limits.MaxSteps - x.st.Steps
		max := x.st.Limits.MaxParallel
		if max > budget {
			max = budget
		}
		if len(ready) > max {
			ready = ready[:max]
		}
		outcomes := x.runBatch(f, ready)
		for _, o := range outcomes {
			if done, err := x.apply(f, o); done {
				return err
			}
		}
		if err := x.save(); err != nil {
			return err
		}
	}
}

// recoverInterrupted handles steps that were running when a worker died:
// idempotent ones run again, the rest fail honestly. It returns the run's
// failure when such a step's on_error is stop.
func (x *stepper) recoverInterrupted() string {
	failed := ""
	for _, f := range x.st.Frames {
		for id, ns := range f.Nodes {
			if ns.Status != NodeRunning {
				continue
			}
			n := x.def.Node(id)
			if n == nil {
				continue
			}
			t, _ := x.e.Registry.Get(n.Type)
			if t.Loop {
				continue
			}
			if t.Idempotent {
				ns.Status = ""
				x.log(id, "warn", "step "+id+" was interrupted by a restart; running it again")
				continue
			}
			ns.Status, ns.Error = NodeFailed, "interrupted: the server restarted while this step ran, so it is not repeated"
			x.log(id, "error", "step "+id+": "+ns.Error)
			x.saveNode(f, id, nil)
			switch x.def.OnErrorFor(n) {
			case flowdef.OnErrorContinue:
			case flowdef.OnErrorFailBranch:
				ns.Port = flowdef.ErrorPort
			default:
				failed = fmt.Sprintf("step %s failed: %s", id, ns.Error)
			}
		}
	}
	return failed
}

func (x *stepper) markCancelled() {
	now := x.e.now()
	for _, f := range x.st.Frames {
		for id, ns := range f.Nodes {
			switch ns.Status {
			case NodeRunning, NodeWaiting, NodeRetrying, NodeApproval:
				ns.Status = NodeCancelled
				ns.WakeAt = nil
				x.saveNodeAt(f, id, &now)
			}
		}
	}
}

func (x *stepper) wake(f *Frame, now time.Time) {
	for id, ns := range f.Nodes {
		if ns.WakeAt == nil || ns.WakeAt.After(now) {
			continue
		}
		switch ns.Status {
		case NodeWaiting:
			ns.Status, ns.WakeAt = NodeSucceeded, nil
			x.log(id, "info", id+": wait finished")
			x.saveNode(f, id, nil)
		case NodeRetrying:
			ns.Status, ns.WakeAt = "", nil
		}
	}
}

func terminal(status string) bool {
	return status == NodeSucceeded || status == NodeFailed || status == NodeSkipped
}

func edgeActive(p *NodeState, e flowdef.Edge) bool {
	if p.Status == NodeSkipped {
		return false
	}
	// A step that failed with on_error continue has no port, so its plain
	// edges run; fail_branch sets the error port, so only error edges run.
	if e.Port == "" {
		return p.Port != flowdef.ErrorPort
	}
	return e.Port == p.Port
}

// ready lists the frame's steps that can run now, and the ones that will
// never run because every branch into them was not taken.
func (x *stepper) ready(f *Frame) (ready, skipped []string) {
	for _, n := range x.def.Nodes {
		if x.owners[n.ID] != f.Loop {
			continue
		}
		if ns := f.Nodes[n.ID]; ns != nil && ns.Status != "" {
			continue
		}
		resolved, active := true, false
		for _, e := range x.def.Incoming(n.ID) {
			if e.From == flowdef.TriggerID {
				active = true
				continue
			}
			if f.Loop != "" && e.From == f.Loop && e.Port == "each" {
				active = true
				continue
			}
			p := f.Nodes[e.From]
			if p == nil || !terminal(p.Status) {
				resolved = false
				break
			}
			if edgeActive(p, e) {
				active = true
			}
		}
		if !resolved {
			continue
		}
		if active {
			ready = append(ready, n.ID)
		} else {
			skipped = append(skipped, n.ID)
		}
	}
	return ready, skipped
}

func (x *stepper) firstLoop(ids []string) string {
	for _, id := range ids {
		n := x.def.Node(id)
		if t, ok := x.e.Registry.Get(n.Type); ok && t.Loop {
			return id
		}
	}
	return ""
}

// park saves a run that is waiting on a delay, a retry or an approval.
func (x *stepper) park(f *Frame, deadline time.Time) (bool, error) {
	var wake *time.Time
	approval := false
	for _, ns := range f.Nodes {
		switch ns.Status {
		case NodeWaiting, NodeRetrying:
			if ns.WakeAt != nil && (wake == nil || ns.WakeAt.Before(*wake)) {
				t := *ns.WakeAt
				wake = &t
			}
		case NodeApproval:
			approval = true
		}
	}
	if wake == nil && !approval {
		return false, nil
	}
	if wake != nil {
		if wake.After(deadline) {
			msg := "limit: the wait ends after the run's time limit"
			x.report(ReportLine{Kind: "limit", Text: msg})
			return true, x.finish(appdb.AutoRunFailed, msg)
		}
		x.run.Status, x.run.WakeAt = appdb.AutoRunWaiting, wake
	} else {
		x.run.Status, x.run.WakeAt = appdb.AutoRunWaitingApproval, nil
	}
	return true, x.save()
}

func scopeFor(parent, loop string, index int) string {
	s := fmt.Sprintf("%s#%d", loop, index)
	if parent == "" {
		return s
	}
	return parent + "/" + s
}

func (x *stepper) env(f *Frame) map[string]any {
	nodes := map[string]any{}
	for id, out := range x.st.Outputs {
		entry := map[string]any{}
		if m, ok := out.(map[string]any); ok {
			for k, v := range m {
				entry[k] = v
			}
		}
		entry["output"] = out
		nodes[id] = entry
	}
	var item any
	index := 0
	for _, fr := range x.st.Frames {
		if fr.Loop == "" || fr.Index >= len(fr.Items) {
			continue
		}
		item, index = fr.Items[fr.Index], fr.Index
		entry, _ := nodes[fr.Loop].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
			nodes[fr.Loop] = entry
		}
		entry["item"], entry["index"] = item, float64(index)
	}
	inputs := map[string]any{}
	_ = json.Unmarshal(x.run.Inputs, &inputs)
	started := ""
	if x.run.StartedAt != nil {
		started = x.run.StartedAt.Format(time.RFC3339)
	}
	return map[string]any{
		"trigger": x.payload,
		"nodes":   nodes,
		"vars":    x.st.Vars,
		"inputs":  inputs,
		"item":    item,
		"index":   float64(index),
		"run": map[string]any{
			"id": x.run.ID, "flow_id": x.run.FlowID, "version": float64(x.run.Version), "trigger_kind": x.run.TriggerKind,
			"actor_id": x.run.ActorID, "correlation_id": x.run.CorrelationID, "started_at": started, "mode": x.run.Mode,
		},
	}
}

func (x *stepper) render(n *flowdef.Node, t NodeType, env map[string]any) (map[string]any, error) {
	raw := map[string]any{}
	keep := map[string]any{}
	for k, v := range n.Config {
		isExpr := false
		for _, e := range t.Expressions {
			if e == k {
				isExpr = true
			}
		}
		if isExpr {
			keep[k] = v
		} else {
			raw[k] = v
		}
	}
	out, err := expr.Render(raw, env)
	if err != nil {
		return nil, err
	}
	cfg, _ := out.(map[string]any)
	if cfg == nil {
		cfg = map[string]any{}
	}
	for k, v := range keep {
		cfg[k] = v
	}
	return cfg, nil
}

func (x *stepper) startLoop(f *Frame, id string) {
	n := x.def.Node(id)
	now := x.e.now()
	x.st.Steps++
	ns := &NodeState{Status: NodeRunning, StartedAt: &now, Attempts: 1}
	f.Nodes[id] = ns
	t, _ := x.e.Registry.Get(n.Type)
	cfg, err := x.render(n, t, x.env(f))
	fail := func(msg string) {
		ns.Status, ns.Error = NodeFailed, msg
		x.log(id, "error", id+": "+msg)
		x.report(ReportLine{Node: id, Scope: f.Scope, Kind: "problem", Text: msg})
		switch x.def.OnErrorFor(n) {
		case flowdef.OnErrorContinue:
		default:
			ns.Port = flowdef.ErrorPort
		}
		x.saveNode(f, id, nil)
	}
	if err != nil {
		fail(err.Error())
		return
	}
	var items []any
	switch v := expr.Normalize(cfg["items"]).(type) {
	case nil:
	case []any:
		items = v
	case string:
		if strings.TrimSpace(v) != "" {
			if json.Unmarshal([]byte(v), &items) != nil {
				fail("items must be a list")
				return
			}
		}
	default:
		fail("items must be a list")
		return
	}
	limit := x.st.Limits.MaxLoopItems
	if m := (&ExecInput{Config: cfg}).Int("max", 0); m > 0 && m < limit {
		limit = m
	}
	if len(items) > limit {
		msg := fmt.Sprintf("limit: %s has %d items; only the first %d are processed", id, len(items), limit)
		x.log(id, "warn", msg)
		x.report(ReportLine{Node: id, Scope: f.Scope, Kind: "limit", Text: msg})
		items = items[:limit]
	}
	x.setInput(f.Scope+"/"+id, RedactJSON(map[string]any{"count": len(items)}))
	x.report(ReportLine{Node: id, Scope: f.Scope, Kind: "loop", Text: fmt.Sprintf("%s repeats %d time(s)", id, len(items))})
	if len(items) == 0 {
		ns.Status, ns.Port = NodeSucceeded, "done"
		x.st.Outputs[id] = map[string]any{"count": 0, "results": []any{}}
		x.saveNode(f, id, nil)
		return
	}
	x.saveNode(f, id, nil)
	x.log(id, "info", fmt.Sprintf("%s: repeating %d time(s)", id, len(items)))
	x.st.Frames = append(x.st.Frames, &Frame{Loop: id, Scope: scopeFor(f.Scope, id, 0), Items: items, Nodes: map[string]*NodeState{}})
}

func (x *stepper) nextIteration() {
	f := x.st.Frames[len(x.st.Frames)-1]
	result := map[string]any{}
	for id := range f.Nodes {
		if out, ok := x.st.Outputs[id]; ok {
			result[id] = out
		}
	}
	f.Results = append(f.Results, result)
	f.Index++
	if f.Index < len(f.Items) {
		parent := x.st.Frames[len(x.st.Frames)-2]
		f.Scope = scopeFor(parent.Scope, f.Loop, f.Index)
		f.Nodes = map[string]*NodeState{}
		return
	}
	x.st.Frames = x.st.Frames[:len(x.st.Frames)-1]
	parent := x.st.Frames[len(x.st.Frames)-1]
	ns := parent.Nodes[f.Loop]
	if ns == nil {
		ns = &NodeState{}
		parent.Nodes[f.Loop] = ns
	}
	ns.Status, ns.Port = NodeSucceeded, "done"
	x.st.Outputs[f.Loop] = Redact(map[string]any{"count": len(f.Items), "results": f.Results})
	x.saveNode(parent, f.Loop, nil)
}

type outcome struct {
	id      string
	res     Result
	err     error
	attempt int
	would   string
	issues  []string
}

func (x *stepper) runBatch(f *Frame, ids []string) []outcome {
	now := x.e.now()
	for _, id := range ids {
		ns := f.Nodes[id]
		if ns == nil {
			ns = &NodeState{}
			f.Nodes[id] = ns
		}
		ns.Status = NodeRunning
		if ns.StartedAt == nil {
			ns.StartedAt = &now
		}
		x.st.Steps++
	}
	env := x.env(f)
	out := make([]outcome, len(ids))
	if len(ids) == 1 {
		out[0] = x.exec(f, ids[0], env)
		return out
	}
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			out[i] = x.exec(f, id, env)
		}(i, id)
	}
	wg.Wait()
	return out
}

func (x *stepper) exec(f *Frame, id string, env map[string]any) (o outcome) {
	o.id = id
	n := x.def.Node(id)
	ns := f.Nodes[id]
	o.attempt = ns.Attempts + 1
	t, ok := x.e.Registry.Get(n.Type)
	if !ok {
		o.err = fmt.Errorf("unknown step type %s", n.Type)
		return o
	}
	cfg, err := x.render(n, t, env)
	if err != nil {
		o.err = fmt.Errorf("config: %v", err)
		return o
	}
	x.setInput(f.Scope+"/"+id, RedactJSON(cfg))
	in := &ExecInput{
		ClusterID: x.run.ClusterID, FlowID: x.run.FlowID, RunID: x.run.ID, NodeID: id, Scope: f.Scope, ActorID: x.run.ActorID,
		DryRun: x.dry, Config: cfg, Env: env, Item: env["item"], Now: x.e.now(), Limits: x.st.Limits,
		logf: func(level, msg string) { x.log(id, level, id+": "+msg) },
	}
	if idx, ok := env["index"].(float64); ok {
		in.Index = int(idx)
	}
	if x.e.Connection != nil {
		in.Connection = func(cid string) (map[string]any, error) { return x.e.Connection(x.ctx, x.run.ClusterID, cid) }
	}
	if x.dry && t.Action {
		p := Preview{Would: "would run " + t.DisplayName}
		if t.Preview != nil {
			got, err := t.Preview(x.ctx, in)
			if err != nil {
				got.Problems = append(got.Problems, err.Error())
			}
			if got.Would != "" {
				p.Would = got.Would
			}
			p.Problems = got.Problems
		}
		if t.Destructive && t.TargetOf != nil {
			target := t.TargetOf(in)
			since := x.e.now().Add(-flowdef.DestructiveWindow)
			if count, err := x.e.Store.CountAutomationLedger(x.ctx, x.run.ClusterID, n.Type, target, since); err == nil && count >= flowdef.DestructivePerTarget {
				p.Problems = append(p.Problems, fmt.Sprintf("limit: %s already ran on %s %d times in the last %d minutes, so it would be refused now", t.DisplayName, target, count, int(flowdef.DestructiveWindow.Minutes())))
			}
		}
		o.would, o.issues = p.Would, p.Problems
		o.res = Result{Output: map[string]any{"dry_run": true, "would": p.Would, "problems": p.Problems}}
		return o
	}
	if t.Execute == nil {
		o.err = fmt.Errorf("%s cannot run on its own", t.DisplayName)
		return o
	}
	target := ""
	if t.Destructive && t.TargetOf != nil {
		target = t.TargetOf(in)
		since := x.e.now().Add(-flowdef.DestructiveWindow)
		count, err := x.e.Store.CountAutomationLedger(x.ctx, x.run.ClusterID, n.Type, target, since)
		if err == nil && count >= flowdef.DestructivePerTarget {
			o.err = &LimitError{Msg: fmt.Sprintf("limit: %s already ran on %s %d times in the last %d minutes (across all flows)", t.DisplayName, target, count, int(flowdef.DestructiveWindow.Minutes()))}
			return o
		}
	}
	timeout := time.Duration(x.st.Limits.StepTimeoutSec) * time.Second
	if n.TimeoutMS > 0 && time.Duration(n.TimeoutMS)*time.Millisecond < timeout {
		timeout = time.Duration(n.TimeoutMS) * time.Millisecond
	}
	started := x.e.now()
	attemptID := uuid.NewString()
	nrID := nodeRunID(x.run.ID, f.Scope, id)
	_ = x.e.Store.AddAutomationNodeAttempt(context.WithoutCancel(x.ctx), appdb.AutomationNodeAttempt{
		ID: attemptID, ClusterID: x.run.ClusterID, RunID: x.run.ID, NodeRunID: nrID, Attempt: o.attempt, Status: NodeRunning, StartedAt: started,
	})
	cctx, cancel := context.WithTimeout(x.ctx, timeout)
	func() {
		defer func() {
			if p := recover(); p != nil {
				o.err = fmt.Errorf("step crashed: %v", p)
				x.log(id, "error", fmt.Sprintf("%s crashed: %v\n%s", id, p, debug.Stack()))
			}
		}()
		o.res, o.err = t.Execute(cctx, in)
	}()
	if o.err != nil && errors.Is(cctx.Err(), context.DeadlineExceeded) {
		o.err = fmt.Errorf("the step timed out after %s", timeout)
	}
	cancel()
	finished := x.e.now()
	status, msg := NodeSucceeded, ""
	if o.err != nil {
		status, msg = NodeFailed, o.err.Error()
	}
	_ = x.e.Store.AddAutomationNodeAttempt(context.WithoutCancel(x.ctx), appdb.AutomationNodeAttempt{
		ID: attemptID, ClusterID: x.run.ClusterID, RunID: x.run.ID, NodeRunID: nrID, Attempt: o.attempt, Status: status,
		Error: LogLine(msg), StartedAt: started, FinishedAt: &finished,
	})
	if o.err == nil && t.Destructive && target != "" {
		_ = x.e.Store.AddAutomationLedger(context.WithoutCancel(x.ctx), appdb.AutomationLedgerEntry{
			ID: uuid.NewString(), ClusterID: x.run.ClusterID, Action: n.Type, Target: target, FlowID: x.run.FlowID, RunID: x.run.ID, CreatedAt: finished,
		})
	}
	return o
}

// apply records one step's outcome. It reports true when the run ended.
func (x *stepper) apply(f *Frame, o outcome) (bool, error) {
	n := x.def.Node(o.id)
	t, _ := x.e.Registry.Get(n.Type)
	ns := f.Nodes[o.id]
	ns.Attempts = o.attempt
	now := x.e.now()
	if o.err != nil {
		msg := o.err.Error()
		retry := x.def.RetryFor(n)
		if t.Idempotent && !x.dry && !IsLimit(o.err) && retry.Mode != "none" && o.attempt < retry.MaxAttempts {
			wait := Backoff(retry, o.attempt)
			wake := now.Add(wait)
			ns.Status, ns.WakeAt, ns.Error = NodeRetrying, &wake, msg
			x.log(o.id, "warn", fmt.Sprintf("%s: attempt %d failed: %s; trying again in %s", o.id, o.attempt, msg, wait))
			x.saveNode(f, o.id, nil)
			return false, nil
		}
		ns.Status, ns.Error = NodeFailed, msg
		kind := "problem"
		if IsLimit(o.err) {
			kind = "limit"
		}
		x.report(ReportLine{Node: o.id, Scope: f.Scope, Kind: kind, Text: o.id + ": " + msg})
		x.log(o.id, "error", o.id+": "+msg)
		if o.res.Output != nil {
			x.st.Outputs[o.id] = Redact(o.res.Output)
		} else {
			x.st.Outputs[o.id] = map[string]any{"error": LogLine(msg)}
		}
		switch x.def.OnErrorFor(n) {
		case flowdef.OnErrorContinue:
			ns.Port = ""
			x.saveNode(f, o.id, nil)
			return false, nil
		case flowdef.OnErrorFailBranch:
			ns.Port = flowdef.ErrorPort
			x.saveNode(f, o.id, nil)
			return false, nil
		}
		x.saveNode(f, o.id, nil)
		return true, x.finish(appdb.AutoRunFailed, fmt.Sprintf("step %s failed: %s", o.id, msg))
	}
	res := o.res
	ns.Port, ns.Error = res.Port, ""
	if res.Output != nil {
		x.st.Outputs[o.id] = Redact(res.Output)
	} else {
		x.st.Outputs[o.id] = map[string]any{}
	}
	for k, v := range res.Vars {
		x.st.Vars[k] = Redact(v)
	}
	if len(res.Rules) > 0 {
		matched := res.Port == "true"
		x.report(ReportLine{Node: o.id, Scope: f.Scope, Kind: "condition", Text: fmt.Sprintf("%s took the %s branch", o.id, branchWord(res.Port)), Matched: &matched, Rules: res.Rules})
	} else if res.Port != "" && t.Category == CategoryLogic {
		x.report(ReportLine{Node: o.id, Scope: f.Scope, Kind: "branch", Text: fmt.Sprintf("%s took the %s branch", o.id, res.Port)})
	}
	if o.would != "" {
		x.report(ReportLine{Node: o.id, Scope: f.Scope, Kind: "would", Text: o.would})
		x.log(o.id, "info", o.id+": "+o.would)
		for _, p := range o.issues {
			x.report(ReportLine{Node: o.id, Scope: f.Scope, Kind: "problem", Text: o.id + ": " + p})
		}
	}
	if res.Stop {
		ns.Status = NodeSucceeded
		x.saveNode(f, o.id, nil)
		status := appdb.AutoRunSucceeded
		if res.StopFailed {
			status = appdb.AutoRunFailed
		}
		x.report(ReportLine{Node: o.id, Scope: f.Scope, Kind: "stop", Text: firstText(res.Message, "the run stops here")})
		return true, x.finish(status, res.Message)
	}
	switch {
	case !res.WaitUntil.IsZero() && x.dry:
		ns.Status = NodeSucceeded
		x.report(ReportLine{Node: o.id, Scope: f.Scope, Kind: "wait", Text: o.id + ": " + firstText(res.Would, "would wait")})
	case !res.WaitUntil.IsZero() && res.WaitUntil.After(now):
		w := res.WaitUntil.UTC()
		ns.Status, ns.WakeAt = NodeWaiting, &w
		x.log(o.id, "info", fmt.Sprintf("%s: waiting until %s", o.id, w.Format(time.RFC3339)))
	case res.WaitApproval && x.dry:
		ns.Status, ns.Port = NodeSucceeded, "approved"
		x.report(ReportLine{Node: o.id, Scope: f.Scope, Kind: "wait", Text: o.id + ": would wait for an approval; the test continues as approved"})
	case res.WaitApproval:
		ns.Status = NodeApproval
		x.log(o.id, "info", o.id+": waiting for an approval")
	default:
		ns.Status = NodeSucceeded
	}
	x.saveNode(f, o.id, nil)
	return false, nil
}

func branchWord(port string) string {
	switch port {
	case "true":
		return "Yes"
	case "false":
		return "No"
	}
	return port
}

func (x *stepper) saveNode(f *Frame, id string, finished *time.Time) {
	ns := f.Nodes[id]
	if finished == nil && ns != nil && (terminal(ns.Status) || ns.Status == NodeCancelled) {
		now := x.e.now()
		finished = &now
	}
	x.saveNodeAt(f, id, finished)
}

func (x *stepper) saveNodeAt(f *Frame, id string, finished *time.Time) {
	ns := f.Nodes[id]
	if ns == nil {
		return
	}
	n := x.def.Node(id)
	typ := ""
	if n != nil {
		typ = n.Type
	}
	var output json.RawMessage
	if out, ok := x.st.Outputs[id]; ok && (terminal(ns.Status) || ns.Status == NodeWaiting || ns.Status == NodeApproval) {
		output = RedactJSON(out)
	}
	_ = x.e.Store.UpsertAutomationNodeRun(context.WithoutCancel(x.ctx), appdb.AutomationNodeRun{
		ID: nodeRunID(x.run.ID, f.Scope, id), ClusterID: x.run.ClusterID, RunID: x.run.ID, NodeID: id, Scope: f.Scope, Type: typ,
		Status: ns.Status, Port: ns.Port, Input: x.input(f.Scope + "/" + id), Output: output, Error: LogLine(ns.Error),
		Attempts: ns.Attempts, StartedAt: ns.StartedAt, FinishedAt: finished,
	})
}
