package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/automation"
	"github.com/no-dal/ndl-ce/internal/automation/engine"
	"github.com/no-dal/ndl-ce/internal/automation/flowdef"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// automationFeatureID is the feature switch that turns Automations on.
const automationFeatureID = "automation"

// automationTick is how often the worker looks for due runs and schedules
// when nothing wakes it sooner; observations (pool usage, unhealthy
// containers) run every automationObserveEvery.
const (
	automationTick         = 5 * time.Second
	automationObserveEvery = time.Minute
	automationStartDelay   = 10 * time.Second
)

// automationState is the engine, its service and the worker's bookkeeping.
type automationState struct {
	once sync.Once
	reg  *engine.Registry
	eng  *engine.Engine
	svc  *engine.Service
	bus  *engine.Bus
	wake chan struct{}

	mu        sync.Mutex
	inflight  map[string]bool
	slots     chan struct{}
	poolBand  map[string]int
	unhealthy map[string]bool
	observed  time.Time
	migrated  map[string]bool
}

type automationRunKey struct{}

// automationRunFrom reads the run an in-process call was made for, so the
// events it causes carry the run (self-trigger protection).
func automationRunFrom(ctx context.Context) string {
	id, _ := ctx.Value(automationRunKey{}).(string)
	return id
}

func (s *Server) automation() *automationState {
	st := &s.auto
	st.once.Do(func() {
		st.wake = make(chan struct{}, 1)
		st.inflight = map[string]bool{}
		st.slots = make(chan struct{}, flowdef.MaxRunningFlows)
		st.poolBand = map[string]int{}
		st.unhealthy = map[string]bool{}
		st.migrated = map[string]bool{}
		st.bus = &engine.Bus{}
		st.reg = engine.NewRegistry(engine.BuiltinDeps{
			HTTPClient: func(allowPrivate bool, timeout time.Duration) *http.Client {
				if allowPrivate {
					return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				}
				if s.HTTPClient != nil {
					return s.HTTPClient
				}
				cli := webhookHTTPClient()
				cli.Timeout = timeout
				return cli
			},
			CheckURL:  checkWebhookDestination,
			CheckSelf: s.selfTargetAtRun,
		})
		s.registerAutomationNodes(st.reg)
		st.eng = &engine.Engine{
			Store: s.Store, Registry: st.reg, Now: s.now, Owner: "control-" + uuid.NewString()[:8],
			Connection: s.openAutomationConnection,
			StepContext: func(ctx context.Context, run appdb.AutomationRun) context.Context {
				ctx = context.WithValue(ctx, automationRunKey{}, run.ID)
				if f, _ := s.Store.GetAutomationFlow(ctx, run.ClusterID, run.FlowID); f != nil {
					ctx = context.WithValue(ctx, automationFlowLabelKey{}, automationFlowLabel(*f))
				}
				return ctx
			},
		}
		st.svc = &engine.Service{
			Store: s.Store, Engine: st.eng, Bus: st.bus, Now: s.now,
			Audit: func(_ context.Context, clusterID, actor, action, result, detail string) {
				s.auditInternal(clusterID, actor, action, result, detail)
			},
			Notify: func(ctx context.Context, clusterID, kind string, payload map[string]string) {
				s.emitEvent(ctx, clusterID, "", kind, payload)
				if kind == "automation.flow_disabled" {
					s.notifyFlowDisabled(ctx, clusterID, payload)
				}
			},
			Wake: s.wakeAutomations,
		}
		st.eng.Finished = st.svc.RunFinished
		st.bus.Subscribe(func(ev engine.Event) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if !s.automationEnabled(ctx, ev.ClusterID) {
				return
			}
			st.svc.HandleEvent(ctx, ev)
		})
	})
	return st
}

func (s *Server) wakeAutomations() {
	st := s.automation()
	select {
	case st.wake <- struct{}{}:
	default:
	}
}

func (s *Server) automationEnabled(ctx context.Context, clusterID string) bool {
	row, _ := s.Store.GetFeature(ctx, clusterID, automationFeatureID)
	return row != nil && row.Enabled
}

// requireAutomation authorizes perm, then refuses while the Automation
// feature is off.
func (s *Server) requireAutomation(w http.ResponseWriter, r *http.Request, perm string) (*principal, bool) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return nil, false
	}
	if !s.automationEnabled(r.Context(), p.User.ClusterID) {
		writeErr(w, http.StatusConflict, "automation is disabled")
		return nil, false
	}
	return p, true
}

// automationGate wraps an older handler (the policies shim) with the
// feature check.
func (s *Server) automationGate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p, err := s.principal(r); err == nil && p != nil && !s.automationEnabled(r.Context(), p.User.ClusterID) {
			writeErr(w, http.StatusConflict, "automation is disabled")
			return
		}
		next(w, r)
	}
}

// automationPrincipal is the system identity flows run as. It has every
// permission: a flow does exactly what it was built to do. What keeps that
// safe is the guard at publish time (protected steps wait for approval,
// calls to this instance's own API are refused) and the run-time checks.
func (s *Server) automationPrincipal(ctx context.Context, clusterID string) (*principal, error) {
	actorID, err := s.ensureAutomationActor(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	u, err := s.Store.GetUser(ctx, actorID)
	if err != nil || u == nil {
		return nil, fmt.Errorf("the automation identity is missing")
	}
	p, err := s.asPrincipal(ctx, *u, "automation", 1, "", nil)
	if err != nil {
		return nil, err
	}
	p.Grants = []string{rbac.All}
	return p, nil
}

type automationFlowLabelKey struct{}

// automationFlowLabel names a flow and who created it, for audit entries
// made by its steps: "Automation: nightly restart (created by alex)".
func automationFlowLabel(f appdb.AutomationFlow) string {
	return fmt.Sprintf("Automation: %s (created by %s)", f.Name, firstNonEmpty(f.CreatedBy, "unknown"))
}

func automationFlowLabelFrom(ctx context.Context) string {
	v, _ := ctx.Value(automationFlowLabelKey{}).(string)
	return v
}

// automationCall runs one API request in-process as the automation
// identity, so an action step goes through exactly the same handler,
// validation, permission checks, job queue and audit as a person would.
func (s *Server) automationCall(ctx context.Context, clusterID, method, path string, body any, headers map[string]string) (int, []byte, error) {
	p, err := s.automationPrincipal(ctx, clusterID)
	if err != nil {
		return 0, nil, err
	}
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(context.WithValue(ctx, systemPrincipalKey{}, p), method, path, strings.NewReader(string(raw)))
	if err != nil {
		return 0, nil, err
	}
	req.RemoteAddr = "automation"
	req.Header.Set("Content-Type", "application/json")
	if run := automationRunFrom(ctx); run != "" {
		req.Header.Set("X-Nodal-Automation-Run", run)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := &memResponse{header: http.Header{}}
	s.internalHandler().ServeHTTP(rec, req)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.status, rec.body, nil
}

// --- events ---

// automationEmit publishes an event for flows. It is a no-op until the
// engine has started.
func (s *Server) automationEmit(ctx context.Context, clusterID, kind, target, nodeID string, payload map[string]any) {
	if clusterID == "" {
		return
	}
	s.automation().bus.Publish(engine.Event{
		ClusterID: clusterID, Kind: kind, Target: target, NodeID: nodeID, CausedByRun: automationRunFrom(ctx), Payload: payload, At: s.now(),
	})
}

// automationPlatformEvent maps platform events that flows can react to.
func (s *Server) automationPlatformEvent(ctx context.Context, e appdb.Event) {
	kind := map[string]string{"vm.started": "workload.started", "vm.stopped": "workload.stopped", "vm.deleted": "workload.deleted"}[e.Type]
	if kind == "" {
		return
	}
	var fields map[string]string
	_ = json.Unmarshal(e.Payload, &fields)
	id := fields["workload_id"]
	payload := map[string]any{"workload_id": id, "kind": "vm", "node_id": e.NodeID}
	if wl, _ := s.Store.GetWorkload(ctx, e.ClusterID, id); wl != nil {
		payload["name"], payload["status"] = wl.Name, wl.Status
	}
	s.automationEmit(ctx, e.ClusterID, kind, id, e.NodeID, payload)
}

// automationWorkloadEvent is the container lifecycle hook.
func (s *Server) automationWorkloadEvent(ctx context.Context, clusterID string, wl appdb.Workload, action string) {
	kind := map[string]string{"start": "workload.started", "stop": "workload.stopped", "restart": "workload.restarted", "delete": "workload.deleted"}[action]
	if kind == "" {
		return
	}
	s.automationEmit(ctx, clusterID, kind, wl.ID, wl.NodeID, map[string]any{
		"workload_id": wl.ID, "name": wl.Name, "kind": wl.Kind, "node_id": wl.NodeID,
	})
}

// automationUptimeEvent is the Uptime state change hook.
func (s *Server) automationUptimeEvent(ctx context.Context, u appdb.UptimeMonitor, prev string) {
	if prev == u.State || (u.State != "down" && prev != "down") {
		return
	}
	kind := "uptime.monitor_down"
	if u.State != "down" {
		kind = "uptime.monitor_recovered"
	}
	s.automationEmit(ctx, u.ClusterID, kind, u.ID, "", map[string]any{
		"monitor_id": u.ID, "name": u.Name, "target": u.Target, "state": u.State, "status": u.LastStatus, "error": u.LastError, "failures": u.Failures,
	})
}

// automationBackupEvent is the backup result hook.
func (s *Server) automationBackupEvent(ctx context.Context, clusterID, workloadID string, run appdb.BackupRun, err error) {
	kind := "backup.succeeded"
	msg := run.Error
	switch {
	case errors.Is(err, errBackupSuspended):
		return
	case err != nil:
		kind, msg = "backup.failed", firstNonEmpty(msg, err.Error())
	case run.Status == appdb.BackupFailed:
		kind = "backup.failed"
	case run.Status != appdb.BackupSucceeded && run.Status != appdb.BackupSucceededWithWarnings:
		return
	}
	payload := map[string]any{"workload_id": workloadID, "run_id": run.ID, "policy_id": run.PolicyID, "target_id": run.TargetID, "status": run.Status, "error": msg}
	if wl, _ := s.Store.GetWorkload(context.WithoutCancel(ctx), clusterID, workloadID); wl != nil {
		payload["workload_name"] = wl.Name
	}
	s.automationEmit(ctx, clusterID, kind, workloadID, "", payload)
}

// --- worker ---

// RunAutomations steps due runs, starts due schedules and watches pool
// usage and container health for event flows, until ctx ends.
func (s *Server) RunAutomations(ctx context.Context) {
	st := s.automation()
	select {
	case <-ctx.Done():
		return
	case <-time.After(automationStartDelay):
	}
	t := time.NewTicker(automationTick)
	defer t.Stop()
	for {
		s.automationSweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-st.wake:
		}
	}
}

func (s *Server) automationSweep(ctx context.Context) {
	st := s.automation()
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cluster, err := s.Store.GetCluster(lctx)
	if err != nil || cluster == nil || !s.automationEnabled(lctx, cluster.ID) {
		return
	}
	st.mu.Lock()
	first := !st.migrated[cluster.ID]
	st.migrated[cluster.ID] = true
	st.mu.Unlock()
	if first {
		s.migrateLegacyPolicies(lctx, cluster.ID)
	}
	st.svc.ProcessDueSchedules(lctx, flowdef.MaxRunningFlows)
	due, err := s.Store.ListDueAutomationRuns(lctx, s.now(), 32)
	if err == nil {
		for _, run := range due {
			if run.ClusterID != cluster.ID {
				continue
			}
			s.automationStepAsync(ctx, run.ClusterID, run.ID)
		}
	}
	st.mu.Lock()
	observe := time.Since(st.observed) >= automationObserveEvery
	if observe {
		st.observed = time.Now()
	}
	st.mu.Unlock()
	if observe {
		s.automationObserve(lctx, cluster.ID)
	}
}

// automationStepAsync steps a run on a worker slot. At most
// flowdef.MaxRunningFlows runs step at once; the rest wait for the next
// sweep.
func (s *Server) automationStepAsync(ctx context.Context, clusterID, runID string) bool {
	st := s.automation()
	st.mu.Lock()
	if st.inflight[runID] {
		st.mu.Unlock()
		return false
	}
	select {
	case st.slots <- struct{}{}:
	default:
		st.mu.Unlock()
		return false
	}
	st.inflight[runID] = true
	st.mu.Unlock()
	go func() {
		defer func() {
			<-st.slots
			st.mu.Lock()
			delete(st.inflight, runID)
			st.mu.Unlock()
		}()
		if err := st.eng.Step(ctx, clusterID, runID); err != nil && err != engine.ErrBusy {
			log.Printf("automation: run %s: %v", runID, err)
		}
	}()
	return true
}

// automationObserve turns pool usage and container health into events,
// only when some flow listens for them. Each fires on the way up only.
func (s *Server) automationObserve(ctx context.Context, clusterID string) {
	st := s.automation()
	if flows, _ := s.Store.ListAutomationFlowsByTrigger(ctx, clusterID, "event", "storage.pool_usage_high"); len(flows) > 0 {
		pools, _ := s.Store.ListStoragePools(ctx, clusterID)
		for _, p := range pools {
			pct, ok := automation.UsedPercent(p.TotalBytes, p.UsableBytes)
			if !ok {
				continue
			}
			band := 0
			if pct >= 70 {
				band = pct / 5 * 5
			}
			st.mu.Lock()
			last := st.poolBand[p.ID]
			st.poolBand[p.ID] = band
			st.mu.Unlock()
			if band > last {
				s.automationEmit(ctx, clusterID, "storage.pool_usage_high", p.ID, p.NodeID, map[string]any{
					"pool_id": p.ID, "pool_name": p.Name, "percent": pct, "band": band, "backend": p.BackendType, "node_id": p.NodeID,
				})
			}
		}
	}
	if flows, _ := s.Store.ListAutomationFlowsByTrigger(ctx, clusterID, "event", "docker.container_unhealthy"); len(flows) > 0 && s.dockerEnabled(ctx, clusterID) {
		inv, err := s.dockerInventory(ctx, clusterID, false)
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, c := range inv.Containers {
			if c.Health != docker.HealthCritical && c.Health != docker.HealthDegraded {
				continue
			}
			seen[c.Ref] = true
			st.mu.Lock()
			was := st.unhealthy[c.Ref]
			st.mu.Unlock()
			if was {
				continue
			}
			s.automationEmit(ctx, clusterID, "docker.container_unhealthy", c.Ref, "", map[string]any{
				"ref": c.Ref, "machine_id": c.MachineID, "machine_name": c.MachineName, "container_id": c.EngineID, "name": c.Name,
				"project": c.Project, "health": c.Health, "reason": c.HealthReason, "state": c.State, "restart_count": c.RestartCount,
			})
		}
		st.mu.Lock()
		st.unhealthy = seen
		st.mu.Unlock()
	}
}

// --- connections ---

func automationConnectionAAD(clusterID, id string) string {
	return "automation-connection/" + clusterID + "/" + id
}

// openAutomationConnection decrypts a connection's secret fields for a
// running step. They are never returned by the API.
func (s *Server) openAutomationConnection(ctx context.Context, clusterID, id string) (map[string]any, error) {
	c, err := s.Store.GetAutomationConnection(ctx, clusterID, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("connection %s not found", id)
	}
	out := map[string]any{}
	_ = json.Unmarshal(c.Config, &out)
	if c.Sealed != "" {
		plain, err := s.cfSealer().Open(automationConnectionAAD(clusterID, id), c.Sealed)
		if err != nil {
			return nil, err
		}
		var secret map[string]any
		if err := json.Unmarshal([]byte(plain), &secret); err != nil {
			return nil, fmt.Errorf("stored credential is damaged")
		}
		for k, v := range secret {
			out[k] = v
		}
	}
	return out, nil
}
