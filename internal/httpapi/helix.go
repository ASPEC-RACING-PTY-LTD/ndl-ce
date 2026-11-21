package httpapi

import (
	"context"
	"crypto/subtle"
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
	"github.com/no-dal/ndl-ce/internal/helix"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

// Helix is an external AI operator. It never holds a role or a session: it
// authenticates with an hlx_ credential that only the /api/v1/helix/v1
// routes accept, and every call is checked against the machine grants a
// SuperAdmin set. Nothing Helix can call changes its own grants,
// credentials, approvals or the kill switch.

const (
	helixTokenPrefix = "hlx_"
	helixActorName   = "helix"
	// Limits per credential, per minute.
	helixReadsPerMinute   = 600
	helixChangesPerMinute = 60
	// Changes running at once (commands, deployments being queued, power).
	helixMaxOps = 8
	// The feed keeps a week of events.
	helixEventRetention = 7 * 24 * time.Hour
	helixMaxCredentials = 10
)

type helixRuntime struct {
	mu sync.Mutex // serialises config read-modify-write

	opsMu sync.Mutex
	ops   map[string]*helixOp

	rateMu sync.Mutex
	rate   map[string]*helixWindow

	wakeMu sync.Mutex
	wake   chan struct{}

	usedMu sync.Mutex
	used   map[string]time.Time

	obsMu     sync.Mutex
	unhealthy map[string]bool
	high      map[string]bool
	down      map[string]bool
}

type helixOp struct {
	ID        string    `json:"id"`
	Op        string    `json:"op"`
	MachineID string    `json:"machine_id"`
	Summary   string    `json:"summary"`
	Started   time.Time `json:"started_at"`
	cancel    context.CancelFunc
}

type helixWindow struct {
	start          time.Time
	reads, changes int
}

func (s *Server) helixRT() *helixRuntime {
	s.helixOnce.Do(func() {
		s.helixState = &helixRuntime{
			ops: map[string]*helixOp{}, rate: map[string]*helixWindow{}, wake: make(chan struct{}),
			used: map[string]time.Time{}, unhealthy: map[string]bool{}, high: map[string]bool{}, down: map[string]bool{},
		}
	})
	return s.helixState
}

// ---- config ----------------------------------------------------------------

func (s *Server) helixLoad(ctx context.Context, clusterID string) (helix.Config, error) {
	var c helix.Config
	raw, err := s.Store.HelixConfig(ctx, clusterID)
	if err != nil {
		return c, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, fmt.Errorf("helix settings are corrupt: %w", err)
		}
	}
	c.Defaults()
	return c, nil
}

func (s *Server) helixUpdate(ctx context.Context, clusterID, by string, change func(*helix.Config) error) (helix.Config, error) {
	rt := s.helixRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	c, err := s.helixLoad(ctx, clusterID)
	if err != nil {
		return c, err
	}
	if err := change(&c); err != nil {
		return c, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	return c, s.Store.PutHelixConfig(ctx, clusterID, raw, by)
}

// ensureHelixActor is the service user that Helix's actions are recorded
// as. It has no role and cannot sign in.
func (s *Server) ensureHelixActor(ctx context.Context, clusterID string) (string, error) {
	name := "svc-" + helixActorName
	u, err := s.Store.GetUserByName(ctx, clusterID, name)
	if err != nil {
		return "", err
	}
	if u != nil {
		return u.ID, nil
	}
	row := appdb.User{ID: uuid.NewString(), ClusterID: clusterID, Username: name, PasswordHash: "!", Kind: appdb.UserKindService}
	if err := s.Store.CreateUser(ctx, row); err != nil {
		if existing, _ := s.Store.GetUserByName(ctx, clusterID, name); existing != nil {
			return existing.ID, nil
		}
		return "", err
	}
	_ = s.Store.CreateServicePrincipal(ctx, appdb.ServicePrincipal{ID: uuid.NewString(), ClusterID: clusterID, UserID: row.ID, Name: helixActorName})
	return row.ID, nil
}

// helixCall runs one request in-process through an existing handler as the
// Helix service user, holding only the permissions given here. It is used
// after NDL-CE has checked the grant, so the handler's own validation,
// jobs and audit apply as for a person.
func (s *Server) helixCall(ctx context.Context, clusterID string, grants []string, method, path string, body any) (int, []byte, error) {
	actorID, err := s.ensureHelixActor(ctx, clusterID)
	if err != nil {
		return 0, nil, err
	}
	u, err := s.Store.GetUser(ctx, actorID)
	if err != nil || u == nil {
		return 0, nil, errors.New("the helix identity is missing")
	}
	p, err := s.asPrincipal(ctx, *u, "helix", 1, "", nil)
	if err != nil {
		return 0, nil, err
	}
	p.Roles, p.Grants = nil, grants
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(context.WithValue(ctx, systemPrincipalKey{}, p), method, path, strings.NewReader(string(raw)))
	if err != nil {
		return 0, nil, err
	}
	req.RemoteAddr = "helix"
	req.Header.Set("Content-Type", "application/json")
	rec := &memResponse{header: http.Header{}}
	s.internalHandler().ServeHTTP(rec, req)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.status, rec.body, nil
}

// ---- authentication ----------------------------------------------------------

// helixCtx is one authenticated Helix request.
type helixCtx struct {
	ClusterID string
	Config    helix.Config
	Cred      helix.Credential
	IP        string
	ActorID   string
}

func (s *Server) helixAuth(w http.ResponseWriter, r *http.Request, change bool) (*helixCtx, bool) {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	w.Header().Set("Cache-Control", "no-store")
	if !strings.HasPrefix(token, helixTokenPrefix) {
		writeErr(w, http.StatusUnauthorized, "a Helix credential is required (Authorization: Bearer hlx_...)")
		return nil, false
	}
	cluster, err := s.Store.GetCluster(r.Context())
	if err != nil || cluster == nil {
		writeErr(w, http.StatusUnauthorized, "the Helix credential is not valid")
		return nil, false
	}
	cfg, err := s.helixLoad(r.Context(), cluster.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	ip := s.clientIP(r)
	hash := secutil.HashSHA256(token)
	var cred *helix.Credential
	for i := range cfg.Credentials {
		if subtle.ConstantTimeCompare([]byte(cfg.Credentials[i].Hash), []byte(hash)) == 1 {
			cred = &cfg.Credentials[i]
		}
	}
	actorID, _ := s.ensureHelixActor(r.Context(), cluster.ID)
	hc := &helixCtx{ClusterID: cluster.ID, Config: cfg, IP: ip, ActorID: actorID}
	now := s.now()
	if cred == nil || !cred.Active(now) {
		reason := "unknown credential"
		if cred != nil {
			reason = "revoked or expired credential " + cred.Name
		}
		if s.helixFirstInMinute("refused:" + ip) {
			s.helixAudit(r, hc, "helix.auth", "denied", reason)
			s.helixEmit(r.Context(), cluster.ID, "security.helix_credential_refused", "warning", "", map[string]any{"ip": ip, "reason": reason})
		}
		writeErr(w, http.StatusUnauthorized, "the Helix credential is not valid")
		return nil, false
	}
	hc.Cred = *cred
	if !cfg.Enabled {
		writeErr(w, http.StatusForbidden, "the Helix integration is turned off in NDL-CE")
		return nil, false
	}
	if cfg.Kill.Engaged {
		writeErr(w, http.StatusForbidden, "the Helix kill switch is engaged; a SuperAdmin must release it in NDL-CE")
		return nil, false
	}
	if !cfg.IPAllowed(ip) {
		if s.helixFirstInMinute("address:" + ip) {
			s.helixAudit(r, hc, "helix.auth", "denied", "address "+ip+" is not allowed")
			s.helixEmit(r.Context(), cluster.ID, "security.helix_address_refused", "warning", "", map[string]any{"ip": ip, "credential": cred.Name})
		}
		writeErr(w, http.StatusForbidden, "this address may not use the Helix API")
		return nil, false
	}
	if !s.helixAllow(cred.ID, change) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "too many requests; wait a minute")
		return nil, false
	}
	s.helixTouch(r.Context(), cluster.ID, cred.ID, ip)
	return hc, true
}

// helixAllow is a per-credential, per-minute limit.
func (s *Server) helixAllow(credID string, change bool) bool {
	rt := s.helixRT()
	rt.rateMu.Lock()
	defer rt.rateMu.Unlock()
	now := s.now()
	win := rt.rate[credID]
	if win == nil || now.Sub(win.start) >= time.Minute {
		win = &helixWindow{start: now}
		rt.rate[credID] = win
	}
	if change {
		if win.changes >= helixChangesPerMinute {
			return false
		}
		win.changes++
		return true
	}
	if win.reads >= helixReadsPerMinute {
		return false
	}
	win.reads++
	return true
}

// helixFirstInMinute reports whether key was not seen in the last minute,
// so repeated refusals are recorded once a minute, not once a request.
func (s *Server) helixFirstInMinute(key string) bool {
	rt := s.helixRT()
	rt.usedMu.Lock()
	defer rt.usedMu.Unlock()
	now := s.now()
	if now.Sub(rt.used[key]) < time.Minute {
		return false
	}
	rt.used[key] = now
	return true
}

// helixTouch records when a credential was last used, at most once a minute.
func (s *Server) helixTouch(ctx context.Context, clusterID, credID, ip string) {
	rt := s.helixRT()
	rt.usedMu.Lock()
	last := rt.used[credID]
	now := s.now()
	if now.Sub(last) < time.Minute {
		rt.usedMu.Unlock()
		return
	}
	rt.used[credID] = now
	rt.usedMu.Unlock()
	_, _ = s.helixUpdate(context.WithoutCancel(ctx), clusterID, helixActorName, func(c *helix.Config) error {
		for i := range c.Credentials {
			if c.Credentials[i].ID == credID {
				c.Credentials[i].LastUsedAt, c.Credentials[i].LastUsedIP = &now, ip
			}
		}
		return nil
	})
}

// ---- audit -------------------------------------------------------------------

// helixAudit records a Helix action as the Helix service user, naming the
// credential and address.
func (s *Server) helixAudit(r *http.Request, hc *helixCtx, action, result, detail string) {
	fields := map[string]string{"detail": helix.Summary(detail, 600), "via": "Helix integration"}
	if hc.Cred.Name != "" {
		fields["credential"] = hc.Cred.Name
	}
	body, _ := json.Marshal(fields)
	_ = s.Store.InsertAudit(context.WithoutCancel(r.Context()), appdb.AuditEvent{
		ID: uuid.NewString(), ClusterID: hc.ClusterID, ActorUserID: hc.ActorID, Action: action,
		Result: result, RemoteAddr: hc.IP, Detail: body, CreatedAt: s.now(),
	})
}

// ---- operations and the kill switch -------------------------------------------

// helixBegin registers a running change so the kill switch can stop it.
func (s *Server) helixBegin(ctx context.Context, op, machineID, summary string) (context.Context, func(), error) {
	rt := s.helixRT()
	rt.opsMu.Lock()
	defer rt.opsMu.Unlock()
	if op != "events" {
		running := 0
		for _, o := range rt.ops {
			if o.Op != "events" {
				running++
			}
		}
		if running >= helixMaxOps {
			return nil, nil, errors.New("too many Helix operations are running; wait for one to finish")
		}
	}
	cctx, cancel := context.WithCancel(ctx)
	o := &helixOp{ID: uuid.NewString(), Op: op, MachineID: machineID, Summary: helix.Summary(summary, 200), Started: s.now(), cancel: cancel}
	rt.ops[o.ID] = o
	return cctx, func() {
		cancel()
		rt.opsMu.Lock()
		delete(rt.ops, o.ID)
		rt.opsMu.Unlock()
	}, nil
}

func (s *Server) helixRunningOps() []helixOp {
	rt := s.helixRT()
	rt.opsMu.Lock()
	defer rt.opsMu.Unlock()
	out := make([]helixOp, 0, len(rt.ops))
	for _, o := range rt.ops {
		out = append(out, *o)
	}
	return out
}

// helixStopAll cancels every running Helix operation, every queued or
// running deployment Helix started and every waiting event request.
func (s *Server) helixStopAll(ctx context.Context, clusterID string) (ops, deploys int) {
	rt := s.helixRT()
	rt.opsMu.Lock()
	for _, o := range rt.ops {
		o.cancel()
		ops++
	}
	rt.opsMu.Unlock()
	s.helixWake()
	active, err := s.Store.ListDeployments(ctx, clusterID, appdb.DeploymentFilter{Active: true, Limit: 200})
	if err == nil {
		for _, d := range active {
			if d.Trigger != helixDeployTrigger && d.Trigger != helixRollbackTrigger {
				continue
			}
			code, body, err := s.helixCall(ctx, clusterID, []string{rbac.ComputeModify, rbac.ComputeRead}, http.MethodPost, "/api/v1/docker/deployments/"+d.ID+"/cancel", nil)
			if err != nil || code >= 300 {
				log.Printf("helix kill switch: cancelling deployment %s: %v %s", d.ID, err, apiErrText(code, body))
				continue
			}
			deploys++
		}
	}
	return ops, deploys
}

// ---- approvals ---------------------------------------------------------------

// helixGate decides a change. It returns true when the change may run now;
// otherwise it has answered (403, or 202 with the approval to wait for).
func (s *Server) helixGate(w http.ResponseWriter, r *http.Request, hc *helixCtx, wl appdb.Workload, g helix.Grant, req helix.Request, approvalID, summary string, params any) bool {
	d := helix.Decide(g, req)
	if !d.Allowed {
		s.helixAudit(r, hc, "helix."+req.Op, "denied", wl.Name+": "+helix.CapFor(req.Op)+" is not granted")
		writeErr(w, http.StatusForbidden, fmt.Sprintf("%s is not granted on %s", helix.CapFor(req.Op), wl.Name))
		return false
	}
	if !d.NeedsApproval {
		return true
	}
	fp := helix.Fingerprint(req.Op, wl.ID, params)
	now := s.now()
	approvalID = strings.TrimSpace(approvalID)
	var out *helix.Approval
	var useErr error
	_, err := s.helixUpdate(r.Context(), hc.ClusterID, helixActorName, func(c *helix.Config) error {
		helixExpireApprovals(c, now)
		if approvalID != "" {
			for i := range c.Approvals {
				a := &c.Approvals[i]
				if a.ID != approvalID {
					continue
				}
				switch {
				case a.Fingerprint != fp:
					useErr = errors.New("this approval was given for a different request")
				case a.Status == helix.ApprovalApproved:
					a.Status, a.UsedAt = helix.ApprovalUsed, &now
					out = a
					return nil
				default:
					useErr = fmt.Errorf("this approval is %s", a.Status)
				}
				return nil
			}
			useErr = errors.New("approval not found")
			return nil
		}
		for i := range c.Approvals {
			if a := &c.Approvals[i]; a.Fingerprint == fp && a.Status == helix.ApprovalPending {
				out = a
				return nil
			}
		}
		a := helix.Approval{
			ID: uuid.NewString(), Op: req.Op, MachineID: wl.ID, MachineName: wl.Name, Summary: helix.Summary(summary, 400),
			Reasons: d.Reasons, Fingerprint: fp, Status: helix.ApprovalPending, RequestedAt: now,
			RequestedBy: hc.Cred.Name, ExpiresAt: now.Add(helix.PendingTTL),
		}
		c.Approvals = append([]helix.Approval{a}, c.Approvals...)
		if len(c.Approvals) > helix.MaxApprovals {
			c.Approvals = c.Approvals[:helix.MaxApprovals]
		}
		out = &c.Approvals[0]
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if useErr != nil {
		s.helixAudit(r, hc, "helix."+req.Op, "denied", wl.Name+": "+useErr.Error())
		writeErr(w, http.StatusConflict, useErr.Error())
		return false
	}
	if out.Status == helix.ApprovalUsed {
		s.helixAudit(r, hc, "helix."+req.Op, "approved", wl.Name+": running with approval "+out.ID+" ("+out.DecidedBy+")")
		return true
	}
	if out.RequestedAt.Equal(now) {
		s.helixAudit(r, hc, "helix.approval.request", "pending", wl.Name+": "+out.Summary+": "+strings.Join(out.Reasons, "; "))
		s.helixEmit(r.Context(), hc.ClusterID, "helix.approval_requested", "info", wl.ID, map[string]any{"approval_id": out.ID, "summary": out.Summary})
		go s.notifyWithPermission(context.Background(), hc.ClusterID, rbac.HelixManage, "", "helix.approval",
			"Helix is waiting for approval", wl.Name+": "+out.Summary, "/helix")
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "approval_required", "approval": helixApprovalJSON(*out)})
	return false
}

func helixExpireApprovals(c *helix.Config, now time.Time) {
	for i := range c.Approvals {
		a := &c.Approvals[i]
		if (a.Status == helix.ApprovalPending || a.Status == helix.ApprovalApproved) && now.After(a.ExpiresAt) {
			a.Status = helix.ApprovalExpired
		}
	}
}

func helixApprovalJSON(a helix.Approval) map[string]any {
	out := map[string]any{
		"id": a.ID, "op": a.Op, "machine_id": a.MachineID, "machine_name": a.MachineName, "summary": a.Summary,
		"reasons": a.Reasons, "status": a.Status, "requested_at": a.RequestedAt.UTC().Format(time.RFC3339),
		"requested_by": a.RequestedBy, "expires_at": a.ExpiresAt.UTC().Format(time.RFC3339), "note": a.Note,
	}
	if a.DecidedAt != nil {
		out["decided_at"], out["decided_by"] = a.DecidedAt.UTC().Format(time.RFC3339), a.DecidedBy
	}
	if a.UsedAt != nil {
		out["used_at"] = a.UsedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// ---- events ------------------------------------------------------------------

// helixEmit adds an event to Helix's feed. machineID is "" for events that
// are not about one machine; Helix sees those only when global events are
// on. Nothing is recorded while the integration is off.
func (s *Server) helixEmit(ctx context.Context, clusterID, typ, severity, machineID string, body map[string]any) {
	if clusterID == "" || s.Store == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	cfg, err := s.helixLoad(ctx, clusterID)
	if err != nil || !cfg.Enabled {
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	raw, _ := json.Marshal(body)
	if _, err := s.Store.InsertHelixEvent(ctx, appdb.HelixEvent{
		ClusterID: clusterID, ID: uuid.NewString(), Type: typ, Severity: severity, MachineID: machineID, Body: raw, CreatedAt: s.now(),
	}); err != nil {
		log.Printf("helix event %s: %v", typ, err)
		return
	}
	s.helixWake()
}

func (s *Server) helixWake() {
	rt := s.helixRT()
	rt.wakeMu.Lock()
	close(rt.wake)
	rt.wake = make(chan struct{})
	rt.wakeMu.Unlock()
}

func (s *Server) helixWaitChan() <-chan struct{} {
	rt := s.helixRT()
	rt.wakeMu.Lock()
	defer rt.wakeMu.Unlock()
	return rt.wake
}

// helixPlatformEvent maps the automation bus (workload lifecycle, uptime,
// backups, storage) into Helix's feed.
func (s *Server) helixPlatformEvent(ctx context.Context, clusterID, kind, target string, payload map[string]any) {
	typ, severity, machine := "", "info", ""
	switch kind {
	case "workload.started", "workload.stopped", "workload.restarted", "workload.deleted":
		typ, machine = "machine."+strings.TrimPrefix(kind, "workload."), target
		if kind == "workload.stopped" || kind == "workload.deleted" {
			severity = "warning"
		}
	case "uptime.monitor_down":
		typ, severity = "uptime.down", "critical"
	case "uptime.monitor_recovered":
		typ = "uptime.recovered"
	case "backup.failed":
		typ, severity = "backup.failed", "warning"
		if id, _ := payload["workload_id"].(string); id != "" {
			machine = id
		}
	case "storage.pool_usage_high":
		typ, severity = "storage.pool_usage_high", "warning"
	default:
		return
	}
	s.helixEmit(ctx, clusterID, typ, severity, machine, payload)
}

// helixSecurityAudit forwards refused sign-ins and similar to the feed.
func (s *Server) helixSecurityAudit(ctx context.Context, clusterID, action, result, ip, detail string) {
	if result != "denied" || !strings.HasPrefix(action, "auth.") {
		return
	}
	s.helixEmit(ctx, clusterID, "security.signin_refused", "warning", "", map[string]any{"action": action, "ip": ip, "detail": helix.Summary(detail, 300)})
}

// helixDeploymentEvent reports a finished deployment.
func (s *Server) helixDeploymentEvent(ctx context.Context, d appdb.Deployment) {
	severity := "info"
	if d.Status != appdb.DeploySucceeded && d.Status != appdb.DeployCancelled {
		severity = "warning"
	}
	s.helixEmit(ctx, d.ClusterID, "deploy."+strings.ToLower(d.Status), severity, d.WorkloadID, map[string]any{
		"deployment_id": d.ID, "project_id": d.ProjectID, "project": d.Detail.ProjectName, "status": d.Status,
		"trigger": d.Trigger, "error": d.Detail.Error, "commit_before": d.Detail.CommitBefore, "commit_after": d.Detail.CommitAfter,
	})
}
