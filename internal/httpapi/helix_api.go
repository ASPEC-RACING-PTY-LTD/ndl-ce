package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/deploy"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/helix"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// The Helix API: /api/v1/helix/v1/... with an hlx_ credential. See
// docs/helix-integration.md for the contract.

const (
	helixDeployTrigger   = "helix"
	helixRollbackTrigger = "helix-rollback"
	helixExecDefaultSec  = 60
	helixExecDir         = "/tmp"
	helixMaxCommandBytes = 16 << 10
)

var helixUnitRE = regexp.MustCompile(`^[A-Za-z0-9@._:\\-]{1,200}$`)

// helixExecCapable: commands, Docker and services reach system containers
// only (lxc-attach); VMs and app containers are read and power only.
func helixExecCapable(wl appdb.Workload) bool { return wl.Kind == lxc.KindSystemContainer }

// helixMachine loads a machine Helix holds cap on. Machines that are not
// granted answer exactly like machines that do not exist.
func (s *Server) helixMachine(w http.ResponseWriter, r *http.Request, hc *helixCtx, cap string) (appdb.Workload, helix.Grant, bool) {
	id := r.PathValue("id")
	g, ok := hc.Config.Grant(id)
	wl, err := s.Store.GetWorkload(r.Context(), hc.ClusterID, id)
	if !ok || len(g.Caps) == 0 || err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "no machine "+id+" is shared with Helix")
		return appdb.Workload{}, helix.Grant{}, false
	}
	if !g.Has(cap) {
		s.helixAudit(r, hc, "helix.access", "denied", wl.Name+": "+cap+" is not granted")
		writeErr(w, http.StatusForbidden, fmt.Sprintf("%s is not granted on %s", cap, wl.Name))
		return appdb.Workload{}, helix.Grant{}, false
	}
	return *wl, g, true
}

func helixMachineType(wl appdb.Workload) string {
	switch wl.Kind {
	case lxc.KindSystemContainer:
		return "lxc"
	case "vm":
		return "vm"
	}
	return wl.Kind
}

func helixGrantJSON(g helix.Grant) map[string]any {
	users := append([]string{g.RunAs}, g.ExecUsers...)
	return map[string]any{"caps": g.Caps, "protected": g.Protected, "destructive_unattended": g.Destructive, "run_as": g.RunAs, "exec_users": users}
}

func (s *Server) helixMachineJSON(ctx context.Context, wl appdb.Workload, g helix.Grant) map[string]any {
	full := s.workloadJSON(ctx, wl)
	nics := []map[string]any{}
	if list, ok := full["nics"].([]map[string]any); ok {
		for _, n := range list {
			nics = append(nics, map[string]any{"network_id": n["network_id"], "mac": n["mac"], "ipv4": n["ipv4"], "ipv4_address": n["ipv4_address"], "ipv6_address": n["ipv6_address"]})
		}
	}
	return map[string]any{
		"id": wl.ID, "name": wl.Name, "type": helixMachineType(wl), "status": wl.Status, "reason": wl.Reason,
		"desired_power": wl.DesiredPower, "node_id": wl.NodeID, "cpus": wl.CPUs, "memory_bytes": wl.MemoryBytes,
		"disk_bytes": full["disk_bytes"], "nics": nics, "autostart": wl.Autostart, "pending_restart": wl.PendingRestart,
		"image": wl.ImagePin, "created_at": wl.CreatedAt.UTC().Format(time.RFC3339),
		"exec_capable": helixExecCapable(wl), "grant": helixGrantJSON(g),
	}
}

func (s *Server) helixWhoami(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	out := map[string]any{
		"credential": map[string]any{"id": hc.Cred.ID, "name": hc.Cred.Name, "prefix": hc.Cred.Prefix},
		"machines":   len(hc.Config.Grants), "global_events": hc.Config.GlobalEvents, "ip": hc.IP,
		"limits": map[string]any{"reads_per_minute": helixReadsPerMinute, "changes_per_minute": helixChangesPerMinute,
			"concurrent_operations": helixMaxOps, "exec_timeout_max_sec": deploy.ProbeTimeoutSec, "event_retention_hours": int(helixEventRetention.Hours())},
		"api_version": 1,
	}
	if hc.Cred.ExpiresAt != nil {
		out["credential"].(map[string]any)["expires_at"] = hc.Cred.ExpiresAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) helixMachines(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	items := []map[string]any{}
	for _, g := range hc.Config.Grants {
		if !g.Has(helix.CapRead) {
			continue
		}
		wl, err := s.Store.GetWorkload(r.Context(), hc.ClusterID, g.MachineID)
		if err != nil || wl == nil {
			continue
		}
		items = append(items, s.helixMachineJSON(r.Context(), *wl, g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"machines": items})
}

func (s *Server) helixMachineGet(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	wl, g, ok := s.helixMachine(w, r, hc, helix.CapRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.helixMachineJSON(r.Context(), wl, g))
}

func (s *Server) helixMachineMetrics(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	wl, _, ok := s.helixMachine(w, r, hc, helix.CapRead)
	if !ok {
		return
	}
	minutes, _ := strconv.Atoi(r.URL.Query().Get("minutes"))
	if minutes <= 0 {
		minutes = 60
	}
	if minutes > 24*60 {
		minutes = 24 * 60
	}
	out := map[string]any{"machine_id": wl.ID, "minutes": minutes, "status": metrics.StatusUnavailable, "series": []metrics.Series{}}
	if s.Observer != nil {
		to := s.now()
		if res, err := s.Observer.GetMetrics(r.Context(), to.Add(-time.Duration(minutes)*time.Minute), to); err == nil {
			f := filterWorkloadMetrics(res, wl.ID)
			out["status"], out["series"], out["latest"] = f.Status, f.Series, helixLatest(f, wl.ID)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// helixLatest summarises the newest metric points.
func helixLatest(res metrics.QueryResult, id string) map[string]any {
	last := map[string]float64{}
	for _, ser := range res.Series {
		if n := len(ser.Points); n > 0 {
			last[strings.TrimPrefix(ser.Name, metrics.WorkloadMetricPrefix+id+".")] = ser.Points[n-1].Value
		}
	}
	out := map[string]any{}
	if v, ok := last["cpu.busy_ratio"]; ok {
		out["cpu_percent"] = v * 100
	}
	if v, ok := last["memory.current_bytes"]; ok {
		out["memory_used_bytes"] = v
		if m := last["memory.max_bytes"]; m > 0 {
			out["memory_limit_bytes"], out["memory_percent"] = m, v/m*100
		}
	}
	return out
}

// helixProbe runs a short command in a system container and waits.
func (s *Server) helixProbe(ctx context.Context, wl appdb.Workload, runAs, dir, script string, timeoutSec int) (deploy.ProbeResult, error) {
	if !helixExecCapable(wl) {
		return deploy.ProbeResult{}, errors.New("commands run only in system containers (LXC)")
	}
	if wl.Status != lxc.StatusRunning {
		return deploy.ProbeResult{}, fmt.Errorf("%s is %s, not running", wl.Name, firstNonEmpty(wl.Status, "not running"))
	}
	rpc, err := s.deployRPC()
	if err != nil {
		return deploy.ProbeResult{}, err
	}
	var out deploy.ProbeResult
	err = rpc.Deploy(ctx, "deploy-probe", agentrpc.DeployRequest{MachineID: wl.ID, Dir: dir, RunAs: runAs, Script: script, Timeout: timeoutSec}, &out)
	return out, err
}

func (s *Server) helixMachineNetwork(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	wl, g, ok := s.helixMachine(w, r, hc, helix.CapRead)
	if !ok {
		return
	}
	out := map[string]any{"machine_id": wl.ID, "nics": s.helixMachineJSON(r.Context(), wl, g)["nics"], "interfaces": []map[string]any{}}
	if helixExecCapable(wl) && wl.Status == lxc.StatusRunning {
		res, err := s.helixProbe(r.Context(), wl, "root", helixExecDir, "cat /proc/net/dev", 20)
		if err != nil {
			out["error"] = err.Error()
		} else {
			out["interfaces"] = parseProcNetDev(res.Output)
			out["sampled_at"] = s.now().UTC().Format(time.RFC3339)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// parseProcNetDev reads /proc/net/dev counters.
func parseProcNetDev(text string) []map[string]any {
	out := []map[string]any{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 16 {
			continue
		}
		n := func(i int) uint64 { v, _ := strconv.ParseUint(f[i], 10, 64); return v }
		out = append(out, map[string]any{
			"name": strings.TrimSpace(name), "rx_bytes": n(0), "rx_packets": n(1), "rx_errors": n(2), "rx_dropped": n(3),
			"tx_bytes": n(8), "tx_packets": n(9), "tx_errors": n(10), "tx_dropped": n(11),
		})
	}
	return out
}

// ---- Docker ------------------------------------------------------------------

func helixContainerJSON(c docker.Container) map[string]any {
	return map[string]any{
		"id": c.EngineID, "name": c.Name, "project": c.Project, "service": c.Service, "image": c.Image,
		"state": c.State, "status": c.Status, "health": c.Health, "health_reason": c.HealthReason,
		"healthcheck": c.HealthStatus, "exit_code": c.ExitCode, "oom_killed": c.OOMKilled, "restarting": c.Restarting,
		"restart_count": c.RestartCount, "restart_loop": c.RestartLoop, "started_at": c.StartedAt, "uptime": c.Uptime,
		"ports": c.Ports, "cpu_percent": c.CPUPercent, "memory_bytes": c.MemoryBytes, "memory_limit": c.MemoryLimit,
		"working_dir": c.WorkingDir, "error": c.Error,
	}
}

func (s *Server) helixMachineContainers(ctx context.Context, clusterID string, wl appdb.Workload) ([]docker.Container, error) {
	if !helixExecCapable(wl) {
		return nil, errors.New("Docker is reached in system containers (LXC) only")
	}
	if !s.dockerEnabled(ctx, clusterID) {
		return nil, errors.New("Docker Management is turned off in NDL-CE")
	}
	inv, err := s.dockerInventory(ctx, clusterID, false)
	if err != nil {
		return nil, err
	}
	out := []docker.Container{}
	for _, c := range inv.Containers {
		if c.MachineID == wl.ID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *Server) helixContainers(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	wl, _, ok := s.helixMachine(w, r, hc, helix.CapRead)
	if !ok {
		return
	}
	list, err := s.helixMachineContainers(r.Context(), hc.ClusterID, wl)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(list))
	for _, c := range list {
		items = append(items, helixContainerJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"machine_id": wl.ID, "containers": items})
}

// helixFindContainer finds a container on this machine by id or name.
func (s *Server) helixFindContainer(w http.ResponseWriter, r *http.Request, hc *helixCtx, wl appdb.Workload) (docker.Container, bool) {
	list, err := s.helixMachineContainers(r.Context(), hc.ClusterID, wl)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return docker.Container{}, false
	}
	cid := r.PathValue("cid")
	for _, c := range list {
		if cid != "" && (c.EngineID == cid || c.Name == cid || (len(cid) >= 12 && strings.HasPrefix(c.EngineID, cid))) {
			return c, true
		}
	}
	writeErr(w, http.StatusNotFound, "no container "+cid+" on "+wl.Name)
	return docker.Container{}, false
}

func (s *Server) helixContainerLogs(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	wl, _, ok := s.helixMachine(w, r, hc, helix.CapLogs)
	if !ok {
		return
	}
	c, ok := s.helixFindContainer(w, r, hc, wl)
	if !ok {
		return
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 {
		tail = 200
	}
	if tail > 5000 {
		tail = 5000
	}
	res, err := s.dockerRPC().DockerAction(r.Context(), docker.ActionRequest{Action: "logs", MachineID: wl.ID, ContainerID: c.EngineID, Tail: tail})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.helixAudit(r, hc, "helix.container.logs", "ok", wl.Name+"/"+c.Name)
	writeJSON(w, http.StatusOK, map[string]any{"machine_id": wl.ID, "container": c.Name, "logs": res.Logs})
}

func (s *Server) helixContainerAction(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, true)
	if !ok {
		return
	}
	wl, g, ok := s.helixMachine(w, r, hc, helix.CapRead)
	if !ok {
		return
	}
	action := r.PathValue("action")
	if action != "start" && action != "stop" && action != "restart" {
		writeErr(w, http.StatusBadRequest, "action is start, stop or restart")
		return
	}
	c, ok := s.helixFindContainer(w, r, hc, wl)
	if !ok {
		return
	}
	var in struct {
		ApprovalID string `json:"approval_id"`
	}
	_ = readJSON(r, &in)
	summary := action + " container " + c.Name
	if !s.helixGate(w, r, hc, wl, g, helix.Request{Op: helix.OpContainer, Action: action}, in.ApprovalID, summary, map[string]string{"container": c.EngineID, "action": action}) {
		return
	}
	ctx, done, err := s.helixBegin(r.Context(), helix.OpContainer, wl.ID, summary)
	if err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	defer done()
	res, err := s.dockerRPC().DockerAction(ctx, docker.ActionRequest{Action: action, MachineID: wl.ID, ContainerID: c.EngineID})
	if err != nil {
		s.helixAudit(r, hc, "helix.container."+action, "failed", wl.Name+"/"+c.Name+": "+err.Error())
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.helixAudit(r, hc, "helix.container."+action, "ok", wl.Name+"/"+c.Name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.OK, "message": res.Message, "container": c.Name, "action": action})
}

// ---- system services -----------------------------------------------------------

func (s *Server) helixServices(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	wl, _, ok := s.helixMachine(w, r, hc, helix.CapRead)
	if !ok {
		return
	}
	res, err := s.helixProbe(r.Context(), wl, "root", helixExecDir, "systemctl list-units --type=service --all --no-pager --plain --no-legend", 30)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	items := []map[string]any{}
	failed := 0
	for _, line := range strings.Split(res.Output, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.HasSuffix(f[0], ".service") {
			continue
		}
		if f[2] == "failed" {
			failed++
		}
		items = append(items, map[string]any{"unit": f[0], "load": f[1], "active": f[2], "sub": f[3], "description": strings.Join(f[4:], " ")})
	}
	writeJSON(w, http.StatusOK, map[string]any{"machine_id": wl.ID, "services": items, "failed": failed})
}

func (s *Server) helixUnit(w http.ResponseWriter, r *http.Request) (string, bool) {
	unit := r.PathValue("unit")
	if !helixUnitRE.MatchString(unit) || strings.HasPrefix(unit, "-") {
		writeErr(w, http.StatusBadRequest, "not a service name")
		return "", false
	}
	if !strings.Contains(unit, ".") {
		unit += ".service"
	}
	return unit, true
}

func (s *Server) helixServiceLogs(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	wl, _, ok := s.helixMachine(w, r, hc, helix.CapLogs)
	if !ok {
		return
	}
	unit, ok := s.helixUnit(w, r)
	if !ok {
		return
	}
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if lines <= 0 {
		lines = 200
	}
	if lines > 5000 {
		lines = 5000
	}
	res, err := s.helixProbe(r.Context(), wl, "root", helixExecDir,
		fmt.Sprintf("journalctl -u %s -n %d --no-pager -o short-iso", deploy.Quote(unit), lines), 60)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.helixAudit(r, hc, "helix.service.logs", "ok", wl.Name+"/"+unit)
	writeJSON(w, http.StatusOK, map[string]any{"machine_id": wl.ID, "unit": unit, "logs": res.Output, "exit_code": res.ExitCode})
}

func (s *Server) helixServiceAction(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, true)
	if !ok {
		return
	}
	wl, g, ok := s.helixMachine(w, r, hc, helix.CapRead)
	if !ok {
		return
	}
	unit, ok := s.helixUnit(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	if action != "start" && action != "stop" && action != "restart" && action != "reload" {
		writeErr(w, http.StatusBadRequest, "action is start, stop, restart or reload")
		return
	}
	var in struct {
		ApprovalID string `json:"approval_id"`
	}
	_ = readJSON(r, &in)
	summary := action + " service " + unit
	if !s.helixGate(w, r, hc, wl, g, helix.Request{Op: helix.OpService, Action: action}, in.ApprovalID, summary, map[string]string{"unit": unit, "action": action}) {
		return
	}
	ctx, done, err := s.helixBegin(r.Context(), helix.OpService, wl.ID, summary)
	if err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	defer done()
	res, err := s.helixProbe(ctx, wl, "root", helixExecDir, fmt.Sprintf("systemctl %s -- %s", action, deploy.Quote(unit)), 90)
	if err != nil {
		s.helixAudit(r, hc, "helix.service."+action, "failed", wl.Name+"/"+unit+": "+err.Error())
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	result := "ok"
	if res.ExitCode != 0 {
		result = "failed"
	}
	s.helixAudit(r, hc, "helix.service."+action, result, wl.Name+"/"+unit)
	writeJSON(w, http.StatusOK, map[string]any{"unit": unit, "action": action, "exit_code": res.ExitCode, "output": res.Output})
}

// ---- commands ------------------------------------------------------------------

func (s *Server) helixExec(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, true)
	if !ok {
		return
	}
	wl, g, ok := s.helixMachine(w, r, hc, helix.CapRead)
	if !ok {
		return
	}
	var in struct {
		Command    string `json:"command"`
		Cwd        string `json:"cwd"`
		RunAs      string `json:"run_as"`
		TimeoutSec int    `json:"timeout_sec"`
		ApprovalID string `json:"approval_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "send JSON with command")
		return
	}
	cmd := strings.TrimSpace(strings.ReplaceAll(in.Command, "\r\n", "\n"))
	if cmd == "" || len(cmd) > helixMaxCommandBytes || strings.ContainsRune(cmd, 0) {
		writeErr(w, http.StatusBadRequest, "command is required, at most 16 KiB of text")
		return
	}
	if !helixExecCapable(wl) {
		writeErr(w, http.StatusUnprocessableEntity, "commands run only in system containers (LXC)")
		return
	}
	runAs := firstNonEmpty(strings.TrimSpace(in.RunAs), g.RunAs, "root")
	allowedUser := runAs == g.RunAs
	for _, u := range g.ExecUsers {
		allowedUser = allowedUser || u == runAs
	}
	if !allowedUser {
		s.helixAudit(r, hc, "helix.exec", "denied", wl.Name+": run as "+runAs+" is not allowed")
		writeErr(w, http.StatusForbidden, "running as "+runAs+" is not allowed on "+wl.Name)
		return
	}
	dir := firstNonEmpty(strings.TrimSpace(in.Cwd), helixExecDir)
	if _, err := deploy.CleanDir(dir); err != nil {
		writeErr(w, http.StatusBadRequest, "cwd must be an absolute folder other than /")
		return
	}
	timeout := in.TimeoutSec
	if timeout <= 0 {
		timeout = helixExecDefaultSec
	}
	if timeout > deploy.ProbeTimeoutSec {
		timeout = deploy.ProbeTimeoutSec
	}
	summary := "run as " + runAs + " in " + dir + ": " + cmd
	if !s.helixGate(w, r, hc, wl, g, helix.Request{Op: helix.OpExec, Command: cmd}, in.ApprovalID, summary, map[string]string{"command": cmd, "cwd": dir, "run_as": runAs}) {
		return
	}
	ctx, done, err := s.helixBegin(r.Context(), helix.OpExec, wl.ID, summary)
	if err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	defer done()
	started := s.now()
	res, err := s.helixProbe(ctx, wl, runAs, dir, cmd, timeout)
	took := s.now().Sub(started).Milliseconds()
	if err != nil {
		result, code := "failed", http.StatusBadGateway
		if ctx.Err() != nil {
			result, code = "stopped", http.StatusServiceUnavailable
			err = errors.New("stopped by the Helix kill switch")
		} else if took >= int64(timeout)*1000 {
			result, code = "timeout", http.StatusGatewayTimeout
			err = fmt.Errorf("the command did not finish within %d seconds", timeout)
		}
		s.helixAudit(r, hc, "helix.exec", result, wl.Name+": "+helix.Summary(summary, 400)+": "+err.Error())
		writeJSON(w, code, map[string]any{"error": err.Error(), "output": res.Output, "duration_ms": took})
		return
	}
	s.helixAudit(r, hc, "helix.exec", "ok", fmt.Sprintf("%s: exit %d: %s", wl.Name, res.ExitCode, helix.Summary(summary, 400)))
	writeJSON(w, http.StatusOK, map[string]any{"exit_code": res.ExitCode, "output": res.Output, "duration_ms": took, "run_as": runAs, "cwd": dir})
}

// ---- machine power --------------------------------------------------------------

func (s *Server) helixPower(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, true)
	if !ok {
		return
	}
	wl, g, ok := s.helixMachine(w, r, hc, helix.CapRead)
	if !ok {
		return
	}
	action := r.PathValue("action")
	if action != "start" && action != "stop" && action != "restart" {
		writeErr(w, http.StatusBadRequest, "action is start, stop or restart")
		return
	}
	var in struct {
		ApprovalID string `json:"approval_id"`
	}
	_ = readJSON(r, &in)
	summary := action + " machine " + wl.Name
	if !s.helixGate(w, r, hc, wl, g, helix.Request{Op: helix.OpPower, Action: action}, in.ApprovalID, summary, map[string]string{"action": action}) {
		return
	}
	ctx, done, err := s.helixBegin(r.Context(), helix.OpPower, wl.ID, summary)
	if err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	defer done()
	code, body, err := s.helixCall(ctx, hc.ClusterID, []string{rbac.ComputeRead, rbac.ComputeLifecycle, rbac.ComputeStart, rbac.ComputeStop},
		http.MethodPost, "/api/v1/workloads/"+wl.ID+"/"+action, nil)
	if err != nil || code >= 300 {
		msg := apiErrText(code, body)
		if err != nil {
			msg = err.Error()
		}
		s.helixAudit(r, hc, "helix.power."+action, "failed", wl.Name+": "+msg)
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	s.helixAudit(r, hc, "helix.power."+action, "ok", wl.Name)
	var out any
	_ = json.Unmarshal(body, &out)
	writeJSON(w, http.StatusAccepted, map[string]any{"action": action, "machine_id": wl.ID, "result": out})
}

// ---- deployments -------------------------------------------------------------------

func (s *Server) helixProjectJSON(p appdb.DeployProject, g helix.Grant) map[string]any {
	cmds := make([]string, 0, len(p.Config.Commands))
	for _, c := range p.Config.Commands {
		cmds = append(cmds, c.Run)
	}
	return map[string]any{
		"id": p.ID, "name": p.Name, "machine_id": p.WorkloadID, "working_dir": p.WorkingDir, "commands": cmds,
		"branch": firstNonEmpty(p.State.Branch, p.Config.Branch), "local_commit": p.State.LocalCommit, "remote_commit": p.State.RemoteCommit,
		"behind": p.State.Behind, "ahead": p.State.Ahead, "dirty": p.State.Dirty, "last_deploy_id": p.State.LastDeployID,
		"last_deploy_status": p.State.LastDeployStatus, "can_deploy": g.Has(helix.CapDeploy),
	}
}

func (s *Server) helixProjects(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	all, err := s.Store.ListDeployProjects(r.Context(), hc.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := []map[string]any{}
	for _, p := range all {
		if g, ok := hc.Config.Grant(p.WorkloadID); ok && g.Has(helix.CapRead) {
			items = append(items, s.helixProjectJSON(p, g))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": items})
}

// helixProject loads a project on a machine Helix may deploy to.
func (s *Server) helixProject(w http.ResponseWriter, r *http.Request, hc *helixCtx) (appdb.DeployProject, appdb.Workload, helix.Grant, bool) {
	p, err := s.Store.GetDeployProject(r.Context(), hc.ClusterID, r.PathValue("id"))
	g, granted := helix.Grant{}, false
	if err == nil && p != nil {
		g, granted = hc.Config.Grant(p.WorkloadID)
	}
	if !granted || !g.Has(helix.CapRead) {
		writeErr(w, http.StatusNotFound, "no project "+r.PathValue("id")+" is shared with Helix")
		return appdb.DeployProject{}, appdb.Workload{}, helix.Grant{}, false
	}
	wl, err := s.Store.GetWorkload(r.Context(), hc.ClusterID, p.WorkloadID)
	if err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "the project's machine no longer exists")
		return appdb.DeployProject{}, appdb.Workload{}, helix.Grant{}, false
	}
	return *p, *wl, g, true
}

func (s *Server) helixQueueDeploy(w http.ResponseWriter, r *http.Request, hc *helixCtx, p appdb.DeployProject, wl appdb.Workload, trigger, action string) {
	if _, err := s.deployWorkload(r.Context(), hc.ClusterID, p.WorkloadID); err != nil {
		writeDeployErr(w, err)
		return
	}
	d, err := s.enqueueDeployment(r.Context(), p, trigger, "helix ("+hc.Cred.Name+")", "", false)
	if err != nil {
		s.helixAudit(r, hc, "helix."+action, "failed", p.Name+": "+err.Error())
		writeDeployErr(w, err)
		return
	}
	s.helixAudit(r, hc, "helix."+action, "queued", fmt.Sprintf("%s on %s (%s): %s", p.Name, wl.Name, d.ID, commandsSummary(d.Detail.Commands)))
	writeJSON(w, http.StatusAccepted, s.deploymentView(r.Context(), *d, nil))
}

func (s *Server) helixDeploy(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, true)
	if !ok {
		return
	}
	p, wl, g, ok := s.helixProject(w, r, hc)
	if !ok {
		return
	}
	var in struct {
		ApprovalID string `json:"approval_id"`
	}
	_ = readJSON(r, &in)
	if !s.helixGate(w, r, hc, wl, g, helix.Request{Op: helix.OpDeploy}, in.ApprovalID, "deploy "+p.Name, map[string]string{"project": p.ID}) {
		return
	}
	s.helixQueueDeploy(w, r, hc, p, wl, helixDeployTrigger, "deploy")
}

func (s *Server) helixRollback(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, true)
	if !ok {
		return
	}
	p, wl, g, ok := s.helixProject(w, r, hc)
	if !ok {
		return
	}
	var in struct {
		DeploymentID string `json:"deployment_id"`
		Commit       string `json:"commit"`
		ApprovalID   string `json:"approval_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "send deployment_id or commit")
		return
	}
	commit := strings.ToLower(strings.TrimSpace(in.Commit))
	if id := strings.TrimSpace(in.DeploymentID); id != "" {
		d, err := s.Store.GetDeployment(r.Context(), hc.ClusterID, id)
		if err != nil || d == nil || d.ProjectID != p.ID {
			writeErr(w, http.StatusNotFound, "no deployment "+id+" of "+p.Name)
			return
		}
		if d.Detail.CommitBefore == "" {
			writeErr(w, http.StatusUnprocessableEntity, "that deployment did not record the commit it started from")
			return
		}
		commit = d.Detail.CommitBefore
	}
	runs := make([]string, 0, len(p.Config.Commands))
	for _, c := range p.Config.Commands {
		runs = append(runs, c.Run)
	}
	cmds, err := helix.RollbackCommands(runs, commit)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "send deployment_id, or commit as a commit hash")
		return
	}
	if !s.helixGate(w, r, hc, wl, g, helix.Request{Op: helix.OpRollback}, in.ApprovalID, "roll "+p.Name+" back to "+commit, map[string]string{"project": p.ID, "commit": commit}) {
		return
	}
	rb := p
	rb.Config.Commands = make([]appdb.DeployCommand, len(cmds))
	for i, c := range cmds {
		rb.Config.Commands[i] = appdb.DeployCommand{Run: c}
	}
	s.helixQueueDeploy(w, r, hc, rb, wl, helixRollbackTrigger, "rollback")
}

// helixDeploymentFor loads a deployment on a machine Helix may read.
func (s *Server) helixDeploymentFor(w http.ResponseWriter, r *http.Request, hc *helixCtx) (*appdb.Deployment, bool) {
	d, err := s.Store.GetDeployment(r.Context(), hc.ClusterID, r.PathValue("id"))
	if err == nil && d != nil {
		if g, ok := hc.Config.Grant(d.WorkloadID); ok && g.Has(helix.CapRead) {
			return d, true
		}
	}
	writeErr(w, http.StatusNotFound, "no deployment "+r.PathValue("id")+" on a machine shared with Helix")
	return nil, false
}

func (s *Server) helixDeployment(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	d, ok := s.helixDeploymentFor(w, r, hc)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.deploymentView(r.Context(), *d, nil))
}

func (s *Server) helixDeploymentLog(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	d, ok := s.helixDeploymentFor(w, r, hc)
	if !ok {
		return
	}
	if g, _ := hc.Config.Grant(d.WorkloadID); !g.Has(helix.CapLogs) && !g.Has(helix.CapDeploy) {
		writeErr(w, http.StatusForbidden, "reading deployment logs needs logs or deploy on the machine")
		return
	}
	path := "/api/v1/docker/deployments/" + d.ID + "/log"
	if q := r.URL.RawQuery; q != "" {
		path += "?" + q
	}
	code, body, err := s.helixCall(r.Context(), hc.ClusterID, []string{rbac.ComputeRead}, http.MethodGet, path, nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

// ---- approvals and events -------------------------------------------------------------

func (s *Server) helixApprovalGet(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	now := s.now()
	for _, a := range hc.Config.Approvals {
		if a.ID == r.PathValue("id") {
			if (a.Status == helix.ApprovalPending || a.Status == helix.ApprovalApproved) && now.After(a.ExpiresAt) {
				a.Status = helix.ApprovalExpired
			}
			writeJSON(w, http.StatusOK, helixApprovalJSON(a))
			return
		}
	}
	writeErr(w, http.StatusNotFound, "approval not found")
}

// helixEventVisible: machine events only for machines Helix may read;
// other events only when global events are on.
func helixEventVisible(cfg helix.Config, e appdb.HelixEvent) bool {
	if e.MachineID == "" {
		return cfg.GlobalEvents
	}
	g, ok := cfg.Grant(e.MachineID)
	return ok && g.Has(helix.CapRead)
}

func helixEventJSON(e appdb.HelixEvent) map[string]any {
	var body any = map[string]any{}
	if len(e.Body) > 0 {
		_ = json.Unmarshal(e.Body, &body)
	}
	out := map[string]any{"seq": e.Seq, "id": e.ID, "type": e.Type, "severity": e.Severity, "at": e.CreatedAt.UTC().Format(time.RFC3339), "data": body}
	if e.MachineID != "" {
		out["machine_id"] = e.MachineID
	}
	return out
}

func (s *Server) helixEvents(w http.ResponseWriter, r *http.Request) {
	hc, ok := s.helixAuth(w, r, false)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	wait, _ := strconv.Atoi(q.Get("wait"))
	if wait < 0 {
		wait = 0
	}
	if wait > 30 {
		wait = 30
	}
	if q.Get("after") == "" {
		// No cursor: start from now.
		seq, err := s.Store.LatestHelixEventSeq(r.Context(), hc.ClusterID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": []any{}, "next": seq})
		return
	}
	after, err := strconv.ParseInt(q.Get("after"), 10, 64)
	if err != nil || after < 0 {
		writeErr(w, http.StatusBadRequest, "after must be the next value from the previous call")
		return
	}
	ctx, done, err := s.helixBegin(r.Context(), "events", "", "waiting for events")
	if err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	defer done()
	deadline := time.NewTimer(time.Duration(wait) * time.Second)
	defer deadline.Stop()
	for {
		wake := s.helixWaitChan()
		rows, err := s.Store.ListHelixEvents(ctx, hc.ClusterID, after, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		cfg, err := s.helixLoad(ctx, hc.ClusterID)
		if err != nil || !cfg.Enabled || cfg.Kill.Engaged {
			writeErr(w, http.StatusForbidden, "the Helix integration was stopped")
			return
		}
		events := []map[string]any{}
		next := after
		for _, e := range rows {
			next = e.Seq
			if helixEventVisible(cfg, e) {
				events = append(events, helixEventJSON(e))
			}
		}
		if len(events) > 0 || wait == 0 || len(rows) == limit {
			writeJSON(w, http.StatusOK, map[string]any{"events": events, "next": next})
			return
		}
		after = next
		select {
		case <-wake:
		case <-deadline.C:
			writeJSON(w, http.StatusOK, map[string]any{"events": []any{}, "next": after})
			return
		case <-ctx.Done():
			if r.Context().Err() == nil {
				writeErr(w, http.StatusForbidden, "the Helix integration was stopped")
			}
			return
		}
	}
}
