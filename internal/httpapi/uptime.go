package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Uptime checks each monitor's public URL (or host:port) from this host.
// A working target is checked every five minutes. A failing one restarts
// the containers assigned to it (and starts any cloudflared tunnel that is
// down on their machines), then is checked every minute until it works.
// This is what brings services back after an update or a reboot without
// anyone starting containers by hand.
const (
	uptimeHealthyEvery = 5 * time.Minute
	uptimeFailingEvery = time.Minute
	uptimeRestartGap   = 5 * time.Minute
	uptimeTick         = 15 * time.Second
	uptimeStartDelay   = 30 * time.Second
)

type uptimeResult struct {
	OK        bool   `json:"ok"`
	Status    int    `json:"status_code"`
	LatencyMS int    `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// uptimeKind says how a target is checked: http for URLs, tcp for host:port.
func uptimeKind(target string) (string, string, error) {
	t := strings.TrimSpace(target)
	if t == "" {
		return "", "", fmt.Errorf("target is required")
	}
	if strings.HasPrefix(t, "tcp://") {
		t = strings.TrimPrefix(t, "tcp://")
		if _, _, err := net.SplitHostPort(t); err != nil {
			return "", "", fmt.Errorf("tcp target must be host:port")
		}
		return "tcp", t, nil
	}
	if strings.Contains(t, "://") {
		u, err := url.Parse(t)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", "", fmt.Errorf("target must be an http(s) URL, host/path or host:port")
		}
		return "http", u.String(), nil
	}
	if h, p, err := net.SplitHostPort(t); err == nil && !strings.Contains(p, "/") {
		if _, perr := strconv.Atoi(p); perr == nil && h != "" {
			return "tcp", t, nil
		}
	}
	u, err := url.Parse("https://" + t)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("target must be an http(s) URL, host/path or host:port")
	}
	return "http", u.String(), nil
}

// statusOK matches a status code against "200-399", "200", or "200,301".
func statusOK(code int, expect string) bool {
	expect = strings.TrimSpace(expect)
	if expect == "" {
		return code >= 200 && code < 400
	}
	for _, part := range strings.Split(expect, ",") {
		part = strings.TrimSpace(part)
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, e1 := strconv.Atoi(strings.TrimSpace(lo))
			b, e2 := strconv.Atoi(strings.TrimSpace(hi))
			if e1 == nil && e2 == nil && code >= a && code <= b {
				return true
			}
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n == code {
			return true
		}
	}
	return false
}

func validExpect(expect string) bool {
	if strings.TrimSpace(expect) == "" {
		return true
	}
	for _, part := range strings.Split(expect, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		if !isRange {
			hi = lo
		}
		a, e1 := strconv.Atoi(strings.TrimSpace(lo))
		b, e2 := strconv.Atoi(strings.TrimSpace(hi))
		if e1 != nil || e2 != nil || a < 100 || b > 599 || a > b {
			return false
		}
	}
	return true
}

var uptimeHTTP = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
		TLSHandshakeTimeout: 8 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives:   true,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

// checkUptime checks one target the way a visitor on the internet would.
func checkUptime(ctx context.Context, target, expect, keyword string) uptimeResult {
	kind, addr, err := uptimeKind(target)
	if err != nil {
		return uptimeResult{Error: err.Error()}
	}
	start := time.Now()
	if kind == "tcp" {
		conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
		res := uptimeResult{LatencyMS: int(time.Since(start).Milliseconds())}
		if err != nil {
			res.Error = err.Error()
			return res
		}
		_ = conn.Close()
		res.OK = true
		return res
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return uptimeResult{Error: err.Error()}
	}
	req.Header.Set("User-Agent", "No-dal-Uptime/1")
	resp, err := uptimeHTTP.Do(req)
	res := uptimeResult{LatencyMS: int(time.Since(start).Milliseconds())}
	if err != nil {
		res.Error = compactNetErr(err)
		return res
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if !statusOK(resp.StatusCode, expect) {
		res.Error = "unexpected status " + resp.Status
		// Cloudflare answers 502/530 with an origin error page when the tunnel
		// cannot reach the container.
		if resp.StatusCode == 502 || resp.StatusCode == 530 || resp.StatusCode == 1033 {
			res.Error += " (the tunnel or proxy could not reach the service)"
		}
		return res
	}
	if keyword != "" && !bytes.Contains(body, []byte(keyword)) {
		res.Error = "the page does not contain " + strconv.Quote(keyword)
		return res
	}
	res.OK = true
	return res
}

func compactNetErr(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i > 0 && strings.HasPrefix(msg, "Get ") {
		msg = msg[i+2:]
	}
	return msg
}

// --- recovery ---

func matchMachine(m docker.Machine, ref string) bool {
	return m.ID == ref || strings.EqualFold(m.Name, ref) || (strings.EqualFold(ref, "host") && m.Kind == docker.KindHost)
}

// uptimeRecover starts or restarts the monitor's containers and starts any
// stopped cloudflared tunnel on their machines. It returns what it did.
func (s *Server) uptimeRecover(ctx context.Context, u appdb.UptimeMonitor) string {
	if !s.dockerEnabled(ctx, u.ClusterID) {
		return "Docker Management is off; nothing restarted"
	}
	inv, err := s.dockerInventory(ctx, u.ClusterID, true)
	if err != nil {
		return "Docker is unreachable: " + err.Error()
	}
	var done []string
	machines := map[string]bool{}
	for _, t := range u.Containers {
		var hit *docker.Container
		for i := range inv.Containers {
			c := &inv.Containers[i]
			if c.Name != t.Name {
				continue
			}
			for _, m := range inv.Machines {
				if m.ID == c.MachineID && (t.MachineID == "" || matchMachine(m, t.MachineID)) {
					hit = c
				}
			}
		}
		if hit == nil {
			done = append(done, t.Name+" not found")
			continue
		}
		machines[hit.MachineID] = true
		action := "restart"
		if hit.State != "running" {
			action = "start"
		}
		if _, err := s.dockerRPC().DockerAction(ctx, docker.ActionRequest{Action: action, MachineID: hit.MachineID, ContainerID: hit.EngineID}); err != nil {
			done = append(done, fmt.Sprintf("could not %s %s: %v", action, hit.Name, err))
			continue
		}
		done = append(done, action+"ed "+hit.Name)
	}
	if rpc, ok := s.dockerRPC().(dockerTunnelRPC); ok && len(machines) > 0 {
		if tunnels, _, err := s.dockerTunnels(ctx, u.ClusterID, true); err == nil {
			for _, t := range tunnels {
				if !t.Active && machines[t.MachineID] {
					if docker.IsHostMachine(t.MachineID) {
						// The host's tunnel is never restarted automatically.
						done = append(done, "host tunnel "+t.Unit+" is down; left alone. "+docker.HostTunnelAdvice(t.Unit))
						continue
					}
					if err := rpc.DockerTunnelUnit(ctx, t.MachineID, t.Unit, "restart", false); err == nil {
						done = append(done, "started tunnel "+t.Unit)
					} else {
						done = append(done, "could not start tunnel "+t.Unit+": "+err.Error())
					}
				}
			}
		}
	}
	if len(done) == 0 {
		return "no containers assigned"
	}
	return strings.Join(done, "; ")
}

var uptimeRunning sync.Map

// uptimeEvaluate checks one monitor, recovers it when it fails, records the
// result and schedules the next check.
func (s *Server) uptimeEvaluate(ctx context.Context, u appdb.UptimeMonitor, job *opsJob) (appdb.UptimeMonitor, uptimeResult) {
	if _, busy := uptimeRunning.LoadOrStore(u.ID, true); busy {
		return u, uptimeResult{Error: "a check is already running"}
	}
	defer uptimeRunning.Delete(u.ID)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	res := checkUptime(cctx, u.Target, u.ExpectStatus, u.Keyword)
	cancel()
	now := s.now()
	u.LastCheckAt = &now
	u.LastStatus, u.LastLatencyMS, u.LastError = res.Status, res.LatencyMS, res.Error
	event := appdb.UptimeEvent{MonitorID: u.ID, At: now, OK: res.OK, StatusCode: res.Status, LatencyMS: res.LatencyMS, Error: res.Error}
	prev := u.State
	if res.OK {
		u.State, u.Failures, u.LastOKAt = "up", 0, &now
		next := now.Add(uptimeHealthyEvery)
		u.NextCheckAt = &next
		if prev == "down" {
			event.Action = "recovered"
		}
	} else {
		u.State = "down"
		u.Failures++
		next := now.Add(uptimeFailingEvery)
		u.NextCheckAt = &next
		if u.AutoRestart && len(u.Containers) > 0 && (u.LastRestartAt == nil || now.Sub(*u.LastRestartAt) >= uptimeRestartGap) {
			rctx, rcancel := context.WithTimeout(ctx, 3*time.Minute)
			event.Action = s.uptimeRecover(rctx, u)
			rcancel()
			u.LastRestartAt = &now
		}
	}
	if prev != u.State {
		u.LastChangeAt = &now
	}
	wctx, wcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	_ = s.Store.PutUptimeMonitor(wctx, u)
	_ = s.Store.AddUptimeEvent(wctx, event)
	wcancel()
	s.automationUptimeEvent(ctx, u, prev)
	if job != nil {
		if res.OK {
			job.log("ok", "%s is up (%d ms)", u.Name, res.LatencyMS)
		} else {
			job.log("error", "%s is down: %s", u.Name, res.Error)
		}
		if event.Action != "" && event.Action != "recovered" {
			job.log("info", "%s: %s", u.Name, event.Action)
		}
	}
	if !res.OK && prev != "down" {
		log.Printf("uptime: %s is down: %s", u.Name, res.Error)
	}
	return u, res
}

// RunUptime checks due monitors until ctx ends.
func (s *Server) RunUptime(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(uptimeStartDelay):
	}
	t := time.NewTicker(uptimeTick)
	defer t.Stop()
	for {
		s.uptimeSweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) uptimeSweep(ctx context.Context) {
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	monitors, err := s.Store.ListUptimeMonitors(lctx, "")
	cancel()
	if err != nil {
		return
	}
	now := s.now()
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, u := range monitors {
		if !u.Enabled || (u.NextCheckAt != nil && u.NextCheckAt.After(now)) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(u appdb.UptimeMonitor) {
			defer wg.Done()
			defer func() { <-sem }()
			s.uptimeEvaluate(ctx, u, nil)
		}(u)
	}
	wg.Wait()
}

// uptimeCheckNamed checks one monitor by name, or every enabled monitor
// when name is empty, logging to job. Used by scripts and automations.
func (s *Server) uptimeCheckNamed(ctx context.Context, clusterID, name string, job *opsJob) error {
	monitors, err := s.Store.ListUptimeMonitors(ctx, clusterID)
	if err != nil {
		return err
	}
	found, down := 0, 0
	for _, u := range monitors {
		if name != "" && !strings.EqualFold(u.Name, name) {
			continue
		}
		if name == "" && !u.Enabled {
			continue
		}
		found++
		if _, res := s.uptimeEvaluate(ctx, u, job); !res.OK {
			down++
		}
	}
	if found == 0 {
		err := fmt.Errorf("no uptime monitor named %q", name)
		if name == "" {
			err = fmt.Errorf("there are no enabled uptime monitors")
		}
		if job != nil {
			job.log("error", "%v", err)
		}
		return err
	}
	if down > 0 {
		return fmt.Errorf("%d of %d monitor(s) down", down, found)
	}
	return nil
}

// --- API ---

type uptimeTargetIn struct {
	Machine string `json:"machine"`
	Name    string `json:"name"`
}

// UnmarshalJSON accepts {"machine":"apps","name":"web"} or "apps/web".
func (t *uptimeTargetIn) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		if m, n, ok := strings.Cut(s, "/"); ok {
			t.Machine, t.Name = strings.TrimSpace(m), strings.TrimSpace(n)
		} else {
			t.Name = strings.TrimSpace(s)
		}
		return nil
	}
	type plain uptimeTargetIn
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*t = uptimeTargetIn(p)
	return nil
}

type uptimeMonitorIn struct {
	Name         string           `json:"name"`
	Target       string           `json:"target"`
	ExpectStatus string           `json:"expect_status"`
	Keyword      string           `json:"keyword"`
	Containers   []uptimeTargetIn `json:"containers"`
	AutoRestart  *bool            `json:"auto_restart"`
	Enabled      *bool            `json:"enabled"`
}

func (in uptimeMonitorIn) validate() error {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 120 {
		return fmt.Errorf("name is required")
	}
	if _, _, err := uptimeKind(in.Target); err != nil {
		return fmt.Errorf("%s: %v", in.Name, err)
	}
	if !validExpect(in.ExpectStatus) {
		return fmt.Errorf("%s: expect_status must look like 200-399, 200 or 200,301", in.Name)
	}
	if len(in.Containers) > 50 {
		return fmt.Errorf("%s: at most 50 containers", in.Name)
	}
	for _, c := range in.Containers {
		if strings.TrimSpace(c.Name) == "" {
			return fmt.Errorf("%s: every container needs a name", in.Name)
		}
	}
	return nil
}

// machineIDFor turns a machine name into its id when Docker knows it.
func (s *Server) machineIDFor(ctx context.Context, clusterID, ref string) string {
	if ref == "" {
		return ""
	}
	if inv, err := s.dockerInventory(ctx, clusterID, false); err == nil {
		for _, m := range inv.Machines {
			if matchMachine(m, ref) {
				return m.ID
			}
		}
	}
	return ref
}

func (s *Server) applyMonitorIn(ctx context.Context, clusterID string, u *appdb.UptimeMonitor, in uptimeMonitorIn) {
	u.Name, u.Target = strings.TrimSpace(in.Name), strings.TrimSpace(in.Target)
	u.ExpectStatus, u.Keyword = strings.TrimSpace(in.ExpectStatus), in.Keyword
	u.Containers = make([]appdb.UptimeTarget, 0, len(in.Containers))
	for _, c := range in.Containers {
		u.Containers = append(u.Containers, appdb.UptimeTarget{MachineID: s.machineIDFor(ctx, clusterID, strings.TrimSpace(c.Machine)), Name: strings.TrimSpace(c.Name)})
	}
	u.AutoRestart = in.AutoRestart == nil || *in.AutoRestart
	u.Enabled = in.Enabled == nil || *in.Enabled
	if u.State == "" {
		u.State = "pending"
	}
	u.NextCheckAt = nil // check soon after any change
}

func uptimeMonitorJSON(u appdb.UptimeMonitor, events []appdb.UptimeEvent) map[string]any {
	ts := func(t *time.Time) any {
		if t == nil {
			return nil
		}
		return t.UTC().Format(time.RFC3339)
	}
	containers := u.Containers
	if containers == nil {
		containers = []appdb.UptimeTarget{}
	}
	out := map[string]any{
		"id": u.ID, "name": u.Name, "target": u.Target, "expect_status": u.ExpectStatus, "keyword": u.Keyword,
		"containers": containers, "auto_restart": u.AutoRestart, "enabled": u.Enabled, "state": u.State,
		"last_check_at": ts(u.LastCheckAt), "last_ok_at": ts(u.LastOKAt), "last_change_at": ts(u.LastChangeAt),
		"last_status": u.LastStatus, "last_latency_ms": u.LastLatencyMS, "last_error": u.LastError,
		"failures": u.Failures, "last_restart_at": ts(u.LastRestartAt), "next_check_at": ts(u.NextCheckAt),
	}
	if events != nil {
		ev := make([]map[string]any, 0, len(events))
		up := 0
		for _, e := range events {
			if e.OK {
				up++
			}
			ev = append(ev, map[string]any{
				"at": e.At.UTC().Format(time.RFC3339), "ok": e.OK, "status_code": e.StatusCode,
				"latency_ms": e.LatencyMS, "error": e.Error, "action": e.Action,
			})
		}
		out["events"] = ev
		if len(events) > 0 {
			out["uptime_percent"] = float64(up*1000/len(events)) / 10
		}
	}
	return out
}

func (s *Server) listUptimeMonitors(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	monitors, err := s.Store.ListUptimeMonitors(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(monitors))
	for _, u := range monitors {
		events, _ := s.Store.ListUptimeEvents(r.Context(), u.ID, 60)
		if events == nil {
			events = []appdb.UptimeEvent{}
		}
		items = append(items, uptimeMonitorJSON(u, events))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createUptimeMonitor(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	var in uptimeMonitorIn
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := appdb.UptimeMonitor{ID: uuid.NewString(), ClusterID: p.User.ClusterID}
	s.applyMonitorIn(r.Context(), p.User.ClusterID, &u, in)
	if err := s.Store.PutUptimeMonitor(r.Context(), u); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "uptime.monitor.create", "ok", u.Name)
	writeJSON(w, http.StatusCreated, uptimeMonitorJSON(u, nil))
}

func (s *Server) updateUptimeMonitor(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	u, err := s.Store.GetUptimeMonitor(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || u == nil {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	}
	var in uptimeMonitorIn
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.applyMonitorIn(r.Context(), p.User.ClusterID, u, in)
	if err := s.Store.PutUptimeMonitor(r.Context(), *u); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "uptime.monitor.update", "ok", u.Name)
	writeJSON(w, http.StatusOK, uptimeMonitorJSON(*u, nil))
}

func (s *Server) deleteUptimeMonitor(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	u, _ := s.Store.GetUptimeMonitor(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if u == nil {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err := s.Store.DeleteUptimeMonitor(r.Context(), p.User.ClusterID, u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "uptime.monitor.delete", "ok", u.Name)
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "deleted": true})
}

// checkUptimeMonitor checks one monitor now, recovering it if it is down.
func (s *Server) checkUptimeMonitor(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeLifecycle)
	if err != nil {
		return
	}
	u, _ := s.Store.GetUptimeMonitor(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if u == nil {
		writeErr(w, http.StatusNotFound, "monitor not found")
		return
	}
	if r.URL.Query().Get("recover") == "true" {
		u.LastRestartAt = nil
	}
	next, _ := s.uptimeEvaluate(r.Context(), *u, nil)
	events, _ := s.Store.ListUptimeEvents(r.Context(), u.ID, 60)
	writeJSON(w, http.StatusOK, uptimeMonitorJSON(next, events))
}

// checkAllUptime checks every enabled monitor now, in the background.
func (s *Server) checkAllUptime(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeLifecycle)
	if err != nil {
		return
	}
	j := s.newOpsJob("uptime", "Check all monitors", p.User.Username)
	s.opsJobs.add(j)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		_ = s.uptimeCheckNamed(ctx, p.User.ClusterID, "", j)
		j.mu.Lock()
		j.FinishedAt = time.Now().UTC()
		j.Status = "succeeded"
		if j.Failed > 0 {
			j.Status = "failed"
		}
		j.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, j.json())
}

// importUptime creates or updates monitors from JSON, matched by name. With
// dry_run it only validates and checks each target once, without saving
// or restarting anything.
func (s *Server) importUptime(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	var req struct {
		Script json.RawMessage `json:"script"`
		DryRun bool            `json:"dry_run"`
	}
	if err := readJSON(r, &req); err != nil || len(req.Script) == 0 {
		writeErr(w, http.StatusBadRequest, "script is required")
		return
	}
	raw := []byte(req.Script)
	var asText string
	if json.Unmarshal(raw, &asText) == nil {
		raw = []byte(asText)
	}
	var doc struct {
		Monitors []uptimeMonitorIn `json:"monitors"`
		Delete   []string          `json:"delete"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"valid": false, "error": "the JSON is not a valid monitor list: " + err.Error()})
		return
	}
	if len(doc.Monitors) == 0 && len(doc.Delete) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"valid": false, "error": "add a monitors list"})
		return
	}
	seen := map[string]bool{}
	for _, m := range doc.Monitors {
		if err := m.validate(); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"valid": false, "error": err.Error()})
			return
		}
		if seen[strings.ToLower(m.Name)] {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"valid": false, "error": "monitor " + m.Name + " is listed twice"})
			return
		}
		seen[strings.ToLower(m.Name)] = true
	}
	existing, _ := s.Store.ListUptimeMonitors(r.Context(), p.User.ClusterID)
	byName := map[string]appdb.UptimeMonitor{}
	for _, u := range existing {
		byName[strings.ToLower(u.Name)] = u
	}
	type row struct {
		Name   string        `json:"name"`
		Change string        `json:"change"`
		Check  *uptimeResult `json:"check,omitempty"`
		Error  string        `json:"error,omitempty"`
	}
	var rows []row
	if req.DryRun {
		var mu sync.Mutex
		var wg sync.WaitGroup
		rows = make([]row, len(doc.Monitors))
		for i, m := range doc.Monitors {
			change := "create"
			if _, ok := byName[strings.ToLower(m.Name)]; ok {
				change = "update"
			}
			wg.Add(1)
			go func(i int, m uptimeMonitorIn, change string) {
				defer wg.Done()
				cctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
				res := checkUptime(cctx, m.Target, m.ExpectStatus, m.Keyword)
				cancel()
				mu.Lock()
				rows[i] = row{Name: m.Name, Change: change, Check: &res}
				mu.Unlock()
			}(i, m, change)
		}
		wg.Wait()
		for _, name := range doc.Delete {
			if _, ok := byName[strings.ToLower(name)]; ok {
				rows = append(rows, row{Name: name, Change: "delete"})
			} else {
				rows = append(rows, row{Name: name, Change: "delete", Error: "no monitor with that name"})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"valid": true, "dry_run": true, "results": rows})
		return
	}
	for _, m := range doc.Monitors {
		u, ok := byName[strings.ToLower(m.Name)]
		change := "updated"
		if !ok {
			u = appdb.UptimeMonitor{ID: uuid.NewString(), ClusterID: p.User.ClusterID}
			change = "created"
		}
		s.applyMonitorIn(r.Context(), p.User.ClusterID, &u, m)
		if err := s.Store.PutUptimeMonitor(r.Context(), u); err != nil {
			rows = append(rows, row{Name: m.Name, Change: "failed", Error: err.Error()})
			continue
		}
		rows = append(rows, row{Name: m.Name, Change: change})
	}
	for _, name := range doc.Delete {
		if u, ok := byName[strings.ToLower(name)]; ok {
			_ = s.Store.DeleteUptimeMonitor(r.Context(), p.User.ClusterID, u.ID)
			rows = append(rows, row{Name: name, Change: "deleted"})
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "uptime.import", "ok", strconv.Itoa(len(rows))+" monitor(s)")
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "results": rows})
}

// --- saved scripts ---

var scriptKinds = map[string]bool{"docker": true, "uptime": true}

func savedScriptJSON(sc appdb.SavedScript) map[string]any {
	return map[string]any{
		"id": sc.ID, "kind": sc.Kind, "name": sc.Name, "body": sc.Body, "created_by": sc.CreatedBy,
		"updated_at": sc.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) listSavedScripts(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListSavedScripts(r.Context(), p.User.ClusterID, r.URL.Query().Get("kind"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, sc := range items {
		out = append(out, savedScriptJSON(sc))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) saveScript(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	var req struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 120 || len(req.Body) > 256<<10 {
		writeErr(w, http.StatusBadRequest, "a name and a body under 256 KiB are required")
		return
	}
	if !json.Valid([]byte(req.Body)) {
		writeErr(w, http.StatusBadRequest, "the body is not valid JSON")
		return
	}
	sc := appdb.SavedScript{ClusterID: p.User.ClusterID, Name: req.Name, Body: req.Body, CreatedBy: p.User.Username}
	if id := r.PathValue("id"); id != "" {
		cur, _ := s.Store.GetSavedScript(r.Context(), p.User.ClusterID, id)
		if cur == nil {
			writeErr(w, http.StatusNotFound, "saved script not found")
			return
		}
		sc.ID, sc.Kind = cur.ID, cur.Kind
	} else {
		if !scriptKinds[req.Kind] {
			writeErr(w, http.StatusBadRequest, "kind must be docker or uptime")
			return
		}
		sc.ID, sc.Kind = uuid.NewString(), req.Kind
	}
	if err := s.Store.PutSavedScript(r.Context(), sc); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	saved, _ := s.Store.GetSavedScript(r.Context(), p.User.ClusterID, sc.ID)
	if saved == nil {
		saved = &sc
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "script.save", "ok", sc.Kind+" "+sc.Name)
	writeJSON(w, http.StatusOK, savedScriptJSON(*saved))
}

func (s *Server) deleteSavedScript(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	if err := s.Store.DeleteSavedScript(r.Context(), p.User.ClusterID, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "script.delete", "ok", r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
