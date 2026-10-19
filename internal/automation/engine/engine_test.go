package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/automation/flowdef"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type harness struct {
	t       *testing.T
	mem     *appdb.Memory
	clk     *clock
	eng     *Engine
	svc     *Service
	cluster string
	mu      sync.Mutex
	calls   map[string]int
	fails   map[string]int
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, mem: appdb.NewMemory(), clk: &clock{t: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}, cluster: uuid.NewString(),
		calls: map[string]int{}, fails: map[string]int{}}
	reg := NewRegistry(BuiltinDeps{})
	reg.Register(NodeType{
		Type: "test.action", DisplayName: "Test action", Category: CategoryNodal, Action: true, Idempotent: true, Destructive: true,
		Fields: []Field{{Key: "name", Label: "Name", Type: "text"}},
		Execute: func(_ context.Context, in *ExecInput) (Result, error) {
			name := in.String("name")
			h.mu.Lock()
			h.calls[name]++
			fail := h.fails[name] > 0
			if fail {
				h.fails[name]--
			}
			h.mu.Unlock()
			if fail {
				return Result{}, fmt.Errorf("%s failed", name)
			}
			return Result{Output: map[string]any{"name": name, "item": in.Item, "password": "hunter2"}}, nil
		},
		Preview: func(_ context.Context, in *ExecInput) (Preview, error) {
			return Preview{Would: "would act on " + in.String("name")}, nil
		},
		TargetOf: func(in *ExecInput) string { return in.String("name") },
	})
	h.eng = &Engine{Store: h.mem, Registry: reg, Now: h.clk.Now, Owner: "worker-1"}
	h.svc = &Service{Store: h.mem, Engine: h.eng, Now: h.clk.Now, Logf: func(string, ...any) {}}
	h.eng.Finished = h.svc.RunFinished
	return h
}

func (h *harness) flow(def *flowdef.Definition, limits flowdef.Limits) appdb.AutomationFlow {
	h.t.Helper()
	if def.SchemaVersion == 0 {
		def.SchemaVersion = 1
	}
	if def.Trigger.ID == "" {
		def.Trigger.ID = flowdef.TriggerID
	}
	if ps := flowdef.Validate(def, h.eng.Registry); len(ps) > 0 {
		h.t.Fatalf("invalid test flow: %+v", ps)
	}
	lim, _ := json.Marshal(limits)
	f := appdb.AutomationFlow{
		ID: uuid.NewString(), ClusterID: h.cluster, Name: "flow-" + uuid.NewString()[:8], Draft: def.Encode(), PublishedVersion: 1,
		Enabled: true, TriggerKind: def.TriggerKind(), EventKind: def.EventKind(), Limits: lim, CreatedAt: h.clk.Now(),
	}
	if err := h.mem.CreateAutomationFlow(context.Background(), f); err != nil {
		h.t.Fatal(err)
	}
	if err := h.mem.CreateAutomationFlowVersion(context.Background(), appdb.AutomationFlowVersion{FlowID: f.ID, ClusterID: h.cluster, Version: 1, Definition: def.Encode()}); err != nil {
		h.t.Fatal(err)
	}
	return f
}

func (h *harness) start(f appdb.AutomationFlow, kind string, payload map[string]any) *appdb.AutomationRun {
	h.t.Helper()
	res, err := h.svc.Start(context.Background(), StartRequest{ClusterID: h.cluster, FlowID: f.ID, TriggerKind: kind, Payload: payload, UserStarted: kind == "manual"})
	if err != nil {
		h.t.Fatal(err)
	}
	return res.Run
}

func (h *harness) step(id string) *appdb.AutomationRun {
	h.t.Helper()
	if err := h.eng.Step(context.Background(), h.cluster, id); err != nil {
		h.t.Fatal(err)
	}
	r, _ := h.mem.GetAutomationRun(context.Background(), h.cluster, id)
	return r
}

func (h *harness) nodeStatus(runID, nodeID string) string {
	nodes, _ := h.mem.ListAutomationNodeRuns(context.Background(), h.cluster, runID)
	for _, n := range nodes {
		if n.NodeID == nodeID && n.Scope == "" {
			return n.Status
		}
	}
	return ""
}

func act(id, name string) flowdef.Node {
	return flowdef.Node{ID: id, Type: "test.action", Config: map[string]any{"name": name}}
}

func manualDef(nodes []flowdef.Node, edges []flowdef.Edge) *flowdef.Definition {
	return &flowdef.Definition{Trigger: flowdef.Trigger{Type: flowdef.TriggerManual}, Nodes: nodes, Edges: edges}
}

func TestBranchesSkipTheBranchNotTaken(t *testing.T) {
	h := newHarness(t)
	def := manualDef(
		[]flowdef.Node{
			{ID: "c", Type: "logic.condition", Config: map[string]any{"rules": []any{map[string]any{"left": "{{ trigger.percent }}", "op": ">=", "right": 85}}}},
			act("hot", "hot"), act("cool", "cool"), act("after", "after"),
		},
		[]flowdef.Edge{{From: "trigger", To: "c"}, {From: "c", To: "hot", Port: "true"}, {From: "c", To: "cool", Port: "false"}, {From: "hot", To: "after"}, {From: "cool", To: "after"}},
	)
	f := h.flow(def, flowdef.Limits{})
	run := h.step(h.start(f, "manual", map[string]any{"percent": "91"}).ID)
	if run.Status != appdb.AutoRunSucceeded {
		t.Fatalf("%s %s", run.Status, run.Error)
	}
	if h.calls["hot"] != 1 || h.calls["cool"] != 0 || h.calls["after"] != 1 {
		t.Fatalf("%v", h.calls)
	}
	if h.nodeStatus(run.ID, "cool") != NodeSkipped {
		t.Fatalf("cool %s", h.nodeStatus(run.ID, "cool"))
	}
	// Outputs are redacted before they are stored.
	nodes, _ := h.mem.ListAutomationNodeRuns(context.Background(), h.cluster, run.ID)
	for _, n := range nodes {
		if strings.Contains(string(n.Output), "hunter2") {
			t.Fatalf("secret stored: %s", n.Output)
		}
	}
}

func TestForeachRepeatsChildStepsUpToTheLimit(t *testing.T) {
	h := newHarness(t)
	def := manualDef(
		[]flowdef.Node{
			{ID: "loop", Type: "logic.foreach", Config: map[string]any{"items": "{{ trigger.items }}"}},
			act("each", "each"), act("done", "done"),
		},
		[]flowdef.Edge{{From: "trigger", To: "loop"}, {From: "loop", To: "each", Port: "each"}, {From: "loop", To: "done", Port: "done"}},
	)
	f := h.flow(def, flowdef.Limits{MaxLoopItems: 3})
	run := h.step(h.start(f, "manual", map[string]any{"items": []any{"a", "b", "c", "d", "e"}}).ID)
	if run.Status != appdb.AutoRunSucceeded || h.calls["each"] != 3 || h.calls["done"] != 1 {
		t.Fatalf("%s %s %v", run.Status, run.Error, h.calls)
	}
	nodes, _ := h.mem.ListAutomationNodeRuns(context.Background(), h.cluster, run.ID)
	scopes := map[string]bool{}
	for _, n := range nodes {
		if n.NodeID == "each" {
			scopes[n.Scope] = true
		}
	}
	if !scopes["loop#0"] || !scopes["loop#2"] || scopes["loop#3"] {
		t.Fatalf("%v", scopes)
	}
	st, _ := DecodeState(run.State)
	found := false
	for _, l := range st.Report {
		if l.Kind == "limit" {
			found = true
		}
	}
	if !found {
		t.Fatal("the loop limit hit is reported")
	}
}

func TestDelayParksAndResumes(t *testing.T) {
	h := newHarness(t)
	def := manualDef(
		[]flowdef.Node{{ID: "wait", Type: "logic.delay", Config: map[string]any{"duration": "10m"}}, act("after", "after")},
		[]flowdef.Edge{{From: "trigger", To: "wait"}, {From: "wait", To: "after"}},
	)
	f := h.flow(def, flowdef.Limits{})
	run := h.step(h.start(f, "manual", nil).ID)
	if run.Status != appdb.AutoRunWaiting || run.WakeAt == nil || h.calls["after"] != 0 {
		t.Fatalf("%s %v", run.Status, run.WakeAt)
	}
	due, _ := h.mem.ListDueAutomationRuns(context.Background(), h.clk.Now(), 10)
	if len(due) != 0 {
		t.Fatal("not due yet")
	}
	h.clk.Add(11 * time.Minute)
	due, _ = h.mem.ListDueAutomationRuns(context.Background(), h.clk.Now(), 10)
	if len(due) != 1 {
		t.Fatal("due after the delay")
	}
	// A fresh engine (as after a restart) picks the run up from the store.
	h.eng = &Engine{Store: h.mem, Registry: h.eng.Registry, Now: h.clk.Now, Owner: "worker-2", Finished: h.svc.RunFinished}
	run = h.step(run.ID)
	if run.Status != appdb.AutoRunSucceeded || h.calls["after"] != 1 {
		t.Fatalf("%s %s %v", run.Status, run.Error, h.calls)
	}
}

func TestRetriesBackOffForReal(t *testing.T) {
	h := newHarness(t)
	n := act("flaky", "flaky")
	n.Retry = &flowdef.Retry{Mode: "exponential", MaxAttempts: 3, DelayMS: 1000, MaxDelayMS: 10000}
	f := h.flow(manualDef([]flowdef.Node{n}, []flowdef.Edge{{From: "trigger", To: "flaky"}}), flowdef.Limits{})
	h.fails["flaky"] = 2
	run := h.step(h.start(f, "manual", nil).ID)
	if run.Status != appdb.AutoRunWaiting || !run.WakeAt.Equal(h.clk.Now().Add(time.Second)) {
		t.Fatalf("first backoff %s %v", run.Status, run.WakeAt)
	}
	run = h.step(run.ID)
	if h.calls["flaky"] != 1 {
		t.Fatal("no retry before the backoff ends")
	}
	h.clk.Add(time.Second)
	run = h.step(run.ID)
	if run.Status != appdb.AutoRunWaiting || !run.WakeAt.Equal(h.clk.Now().Add(2*time.Second)) {
		t.Fatalf("exponential backoff %s %v", run.Status, run.WakeAt)
	}
	h.clk.Add(2 * time.Second)
	run = h.step(run.ID)
	if run.Status != appdb.AutoRunSucceeded || h.calls["flaky"] != 3 {
		t.Fatalf("%s %v", run.Status, h.calls)
	}
	attempts, _ := h.mem.ListAutomationNodeAttempts(context.Background(), h.cluster, run.ID)
	if len(attempts) != 3 || attempts[0].Status != NodeFailed || attempts[2].Status != NodeSucceeded {
		t.Fatalf("%+v", attempts)
	}
}

func TestOnErrorModes(t *testing.T) {
	h := newHarness(t)
	build := func(mode string) *flowdef.Definition {
		bad := act("bad", "bad")
		bad.OnError = mode
		return manualDef(
			[]flowdef.Node{bad, act("next", "next"), act("handler", "handler")},
			[]flowdef.Edge{{From: "trigger", To: "bad"}, {From: "bad", To: "next"}, {From: "bad", To: "handler", Port: "error"}},
		)
	}
	h.fails["bad"] = 3
	run := h.step(h.start(h.flow(build(flowdef.OnErrorContinue), flowdef.Limits{}), "manual", nil).ID)
	if run.Status != appdb.AutoRunSucceeded || h.calls["next"] != 1 || h.calls["handler"] != 0 {
		t.Fatalf("continue: %s %v", run.Status, h.calls)
	}
	run = h.step(h.start(h.flow(build(flowdef.OnErrorFailBranch), flowdef.Limits{}), "manual", nil).ID)
	if run.Status != appdb.AutoRunSucceeded || h.calls["next"] != 1 || h.calls["handler"] != 1 {
		t.Fatalf("fail_branch: %s %v", run.Status, h.calls)
	}
	run = h.step(h.start(h.flow(build(flowdef.OnErrorStop), flowdef.Limits{}), "manual", nil).ID)
	if run.Status != appdb.AutoRunFailed || !strings.Contains(run.Error, "bad failed") || h.calls["next"] != 1 || h.calls["handler"] != 1 {
		t.Fatalf("stop: %s %s %v", run.Status, run.Error, h.calls)
	}
}

func TestConcurrencyQueueAndOverflow(t *testing.T) {
	h := newHarness(t)
	def := manualDef([]flowdef.Node{{ID: "wait", Type: "logic.delay", Config: map[string]any{"duration": "1m"}}}, []flowdef.Edge{{From: "trigger", To: "wait"}})
	def.Trigger.Type = flowdef.TriggerWebhook
	f := h.flow(def, flowdef.Limits{Concurrency: 1, QueueDepth: 1, PerMinute: 60, PerHour: 600})
	first := h.start(f, "webhook", nil)
	second := h.start(f, "webhook", nil)
	third := h.start(f, "webhook", nil)
	if first.Status != appdb.AutoRunPending || second.Status != appdb.AutoRunQueued || third.Status != appdb.AutoRunDropped {
		t.Fatalf("%s %s %s", first.Status, second.Status, third.Status)
	}
	h.step(first.ID)
	h.clk.Add(2 * time.Minute)
	if r := h.step(first.ID); r.Status != appdb.AutoRunSucceeded {
		t.Fatalf("first %s", r.Status)
	}
	r, _ := h.mem.GetAutomationRun(context.Background(), h.cluster, second.ID)
	if r.Status != appdb.AutoRunPending {
		t.Fatalf("the queued run starts when the first finishes: %s", r.Status)
	}
}

func TestTriggerRateLimitAndCircuitBreaker(t *testing.T) {
	h := newHarness(t)
	def := manualDef([]flowdef.Node{act("a", "a")}, []flowdef.Edge{{From: "trigger", To: "a"}})
	def.Trigger.Type = flowdef.TriggerWebhook
	f := h.flow(def, flowdef.Limits{PerMinute: 2, Concurrency: 8, BreakerThreshold: 3})
	for i := 0; i < 2; i++ {
		if r := h.step(h.start(f, "webhook", nil).ID); r.Status != appdb.AutoRunSucceeded {
			t.Fatalf("%s", r.Status)
		}
	}
	if r := h.start(f, "webhook", nil); r.Status != appdb.AutoRunDropped || !strings.Contains(r.Error, "limit") {
		t.Fatalf("rate limit %s %s", r.Status, r.Error)
	}
	// Manual runs are not rate limited.
	if r := h.start(f, "manual", nil); r.Status != appdb.AutoRunPending {
		t.Fatalf("manual %s", r.Status)
	}
	h.clk.Add(2 * time.Minute)
	h.fails["a"] = 10
	for i := 0; i < 2; i++ {
		h.step(h.start(f, "webhook", nil).ID)
	}
	got, _ := h.mem.GetAutomationFlow(context.Background(), h.cluster, f.ID)
	if got.Enabled || !strings.Contains(got.DisabledReason, "in a row") {
		t.Fatalf("breaker %+v", got)
	}
	if _, err := h.svc.Start(context.Background(), StartRequest{ClusterID: h.cluster, FlowID: f.ID, TriggerKind: "webhook"}); err != ErrDisabled {
		t.Fatalf("disabled flow %v", err)
	}
}

func TestSelfTriggerAndChainDepth(t *testing.T) {
	h := newHarness(t)
	evDef := func() *flowdef.Definition {
		d := manualDef([]flowdef.Node{act("a", "a")}, []flowdef.Edge{{From: "trigger", To: "a"}})
		d.Trigger = flowdef.Trigger{Type: flowdef.TriggerEvent, Config: map[string]any{
			"kind":       "workload.stopped",
			"conditions": []any{map[string]any{"field": "name", "op": "contains", "value": "web"}},
		}}
		return d
	}
	a := h.flow(evDef(), flowdef.Limits{DebounceSec: -1})
	b := h.flow(evDef(), flowdef.Limits{DebounceSec: -1})
	runs := func(f appdb.AutomationFlow) []appdb.AutomationRun {
		rs, _ := h.mem.ListAutomationRuns(context.Background(), h.cluster, appdb.AutomationRunFilter{FlowID: f.ID})
		return rs
	}
	h.svc.HandleEvent(context.Background(), Event{ClusterID: h.cluster, Kind: "workload.stopped", Target: "w1", Payload: map[string]any{"name": "db-1"}})
	if len(runs(a)) != 0 {
		t.Fatal("WHEN must filter")
	}
	h.svc.HandleEvent(context.Background(), Event{ClusterID: h.cluster, Kind: "workload.stopped", Target: "w1", Payload: map[string]any{"name": "web-1"}})
	if len(runs(a)) != 1 || len(runs(b)) != 1 {
		t.Fatalf("%d %d", len(runs(a)), len(runs(b)))
	}
	cause := runs(a)[0]
	h.svc.HandleEvent(context.Background(), Event{ClusterID: h.cluster, Kind: "workload.stopped", Target: "w1", CausedByRun: cause.ID, Payload: map[string]any{"name": "web-1"}})
	if len(runs(a)) != 1 {
		t.Fatal("a flow is never started by its own run")
	}
	chained := 0
	for _, r := range runs(b) {
		if r.CausedByRun == cause.ID && r.ChainDepth == 1 {
			chained++
		}
	}
	if len(runs(b)) != 2 || chained != 1 {
		t.Fatalf("other flows chain: %d %d", len(runs(b)), chained)
	}
	deepest := appdb.AutomationRun{ID: uuid.NewString(), ClusterID: h.cluster, FlowID: b.ID, Status: appdb.AutoRunSucceeded,
		Mode: appdb.AutoModeLive, ChainDepth: flowdef.MaxChainDepth, CreatedAt: h.clk.Now()}
	_ = h.mem.CreateAutomationRun(context.Background(), deepest)
	before := len(runs(a))
	h.svc.HandleEvent(context.Background(), Event{ClusterID: h.cluster, Kind: "workload.stopped", Target: "w1", CausedByRun: deepest.ID, Payload: map[string]any{"name": "web-1"}})
	if len(runs(a)) != before {
		t.Fatal("chain depth cap")
	}
}

func TestDryRunNeverExecutesActions(t *testing.T) {
	h := newHarness(t)
	def := manualDef(
		[]flowdef.Node{
			{ID: "c", Type: "logic.condition", Config: map[string]any{"expression": "trigger.x == 1"}},
			act("go", "web-1"), {ID: "ask", Type: "logic.approval", Config: map[string]any{"message": "ok?"}},
		},
		[]flowdef.Edge{{From: "trigger", To: "c"}, {From: "c", To: "ask", Port: "true"}, {From: "ask", To: "go", Port: "approved"}},
	)
	f := h.flow(def, flowdef.Limits{})
	res, err := h.svc.DryRun(context.Background(), h.cluster, f.ID, true, map[string]any{"x": 1}, nil, "", "tester")
	if err != nil {
		t.Fatal(err)
	}
	run := h.step(res.Run.ID)
	if run.Status != appdb.AutoRunSucceeded || h.calls["web-1"] != 0 {
		t.Fatalf("%s %v", run.Status, h.calls)
	}
	var rep Report
	_ = json.Unmarshal(run.Report, &rep)
	text := ""
	for _, l := range rep.Lines {
		text += l.Kind + ":" + l.Text + "\n"
	}
	if !rep.OK || !strings.Contains(text, "would act on web-1") || !strings.Contains(text, "condition:c took the Yes branch") || !strings.Contains(text, "would wait for an approval") {
		t.Fatalf("%s", text)
	}
	got, _ := h.mem.GetAutomationFlow(context.Background(), h.cluster, f.ID)
	if got.DryRunHash == "" || got.DryRunHash != DefinitionHash(got.Draft) {
		t.Fatal("a passing test run of the draft is recorded")
	}
}

func TestDestructiveActionsLimitedPerTarget(t *testing.T) {
	h := newHarness(t)
	f := h.flow(manualDef([]flowdef.Node{act("a", "web-1")}, []flowdef.Edge{{From: "trigger", To: "a"}}), flowdef.Limits{Concurrency: 8})
	for i := 0; i < flowdef.DestructivePerTarget; i++ {
		if r := h.step(h.start(f, "manual", nil).ID); r.Status != appdb.AutoRunSucceeded {
			t.Fatalf("%d %s", i, r.Error)
		}
	}
	r := h.step(h.start(f, "manual", nil).ID)
	if r.Status != appdb.AutoRunFailed || !strings.Contains(r.Error, "limit:") || h.calls["web-1"] != flowdef.DestructivePerTarget {
		t.Fatalf("%s %s %v", r.Status, r.Error, h.calls)
	}
	h.clk.Add(16 * time.Minute)
	if r := h.step(h.start(f, "manual", nil).ID); r.Status != appdb.AutoRunSucceeded {
		t.Fatalf("after the window %s", r.Error)
	}
}

func TestLeaseKeepsTwoWorkersApart(t *testing.T) {
	h := newHarness(t)
	f := h.flow(manualDef([]flowdef.Node{act("a", "a")}, []flowdef.Edge{{From: "trigger", To: "a"}}), flowdef.Limits{})
	run := h.start(f, "manual", nil)
	now := h.clk.Now()
	if ok, _ := h.mem.ClaimAutomationRun(context.Background(), h.cluster, run.ID, "other", now, now.Add(time.Minute)); !ok {
		t.Fatal("claim")
	}
	if err := h.eng.Step(context.Background(), h.cluster, run.ID); err != ErrBusy {
		t.Fatalf("second worker must not step: %v", err)
	}
	h.clk.Add(2 * time.Minute)
	if r := h.step(run.ID); r.Status != appdb.AutoRunSucceeded {
		t.Fatalf("expired lease is taken over: %s", r.Status)
	}
}

func TestApprovalPausesUntilApproved(t *testing.T) {
	h := newHarness(t)
	def := manualDef(
		[]flowdef.Node{{ID: "ask", Type: "logic.approval", Config: map[string]any{"message": "restart?"}}, act("yes", "yes"), act("no", "no")},
		[]flowdef.Edge{{From: "trigger", To: "ask"}, {From: "ask", To: "yes", Port: "approved"}, {From: "ask", To: "no", Port: "rejected"}},
	)
	f := h.flow(def, flowdef.Limits{})
	run := h.step(h.start(f, "manual", nil).ID)
	if run.Status != appdb.AutoRunWaitingApproval {
		t.Fatalf("%s", run.Status)
	}
	if err := h.eng.Approve(context.Background(), h.cluster, run.ID, "", "alice", true, ""); err != nil {
		t.Fatal(err)
	}
	run = h.step(run.ID)
	if run.Status != appdb.AutoRunSucceeded || h.calls["yes"] != 1 || h.calls["no"] != 0 {
		t.Fatalf("%s %v", run.Status, h.calls)
	}
}

func TestCancelAndRetry(t *testing.T) {
	h := newHarness(t)
	def := manualDef(
		[]flowdef.Node{{ID: "wait", Type: "logic.delay", Config: map[string]any{"duration": "1h"}}, act("after", "after")},
		[]flowdef.Edge{{From: "trigger", To: "wait"}, {From: "wait", To: "after"}},
	)
	f := h.flow(def, flowdef.Limits{})
	run := h.step(h.start(f, "manual", map[string]any{"k": "v"}).ID)
	if err := h.svc.Cancel(context.Background(), h.cluster, run.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	got, _ := h.mem.GetAutomationRun(context.Background(), h.cluster, run.ID)
	if got.Status != appdb.AutoRunCancelled {
		t.Fatalf("%s", got.Status)
	}
	res, err := h.svc.Retry(context.Background(), h.cluster, run.ID, "", "alice")
	if err != nil || res.Run.RetryOf != run.ID || !strings.Contains(string(res.Run.TriggerPayload), `"k":"v"`) {
		t.Fatalf("%v %+v", err, res.Run)
	}
}

func TestSchedulesUseTheMinuteAsIdempotencyKey(t *testing.T) {
	h := newHarness(t)
	def := manualDef([]flowdef.Node{act("a", "a")}, []flowdef.Edge{{From: "trigger", To: "a"}})
	def.Trigger = flowdef.Trigger{Type: flowdef.TriggerSchedule, Config: map[string]any{"preset": "every_5_minutes"}}
	f := h.flow(def, flowdef.Limits{Concurrency: 8})
	if err := h.svc.SyncTrigger(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	h.clk.Add(5 * time.Minute)
	if n := h.svc.ProcessDueSchedules(context.Background(), 8); n != 1 {
		t.Fatalf("started %d", n)
	}
	if n := h.svc.ProcessDueSchedules(context.Background(), 8); n != 0 {
		t.Fatalf("again %d", n)
	}
	runs, _ := h.mem.ListAutomationRuns(context.Background(), h.cluster, appdb.AutomationRunFilter{FlowID: f.ID})
	if len(runs) != 1 || !strings.HasPrefix(runs[0].IdempotencyKey, "schedule:2026-10-20T09:05") {
		t.Fatalf("%+v", runs)
	}
}
