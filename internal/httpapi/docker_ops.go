package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Agents that report cloudflared tunnels and run one-shot container
// commands implement these; older agents only list and act on containers.
type dockerTunnelRPC interface {
	DockerTunnels(ctx context.Context, hints []docker.MachineHint) ([]docker.Tunnel, error)
	DockerTunnelUnit(ctx context.Context, machineID, unit, action string, hostApproved bool) error
}

type dockerExecRPC interface {
	DockerExecRun(ctx context.Context, machineID, containerID string, argv []string, timeoutSec int) (docker.ExecResult, error)
}

// --- tunnels ---

type tunnelCache struct {
	mu    sync.Mutex
	at    time.Time
	items []docker.Tunnel
}

func (s *Server) dockerTunnels(ctx context.Context, clusterID string, fresh bool) ([]docker.Tunnel, bool, error) {
	rpc, ok := s.dockerRPC().(dockerTunnelRPC)
	if !ok {
		return nil, false, nil
	}
	s.tunnels.mu.Lock()
	if !fresh && !s.tunnels.at.IsZero() && time.Since(s.tunnels.at) < 20*time.Second {
		items := s.tunnels.items
		s.tunnels.mu.Unlock()
		return items, true, nil
	}
	s.tunnels.mu.Unlock()
	tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancel()
	raw, err := rpc.DockerTunnels(tctx, s.dockerHints(tctx, clusterID))
	if err != nil {
		return nil, true, err
	}
	inv, _ := s.dockerInventory(tctx, clusterID, false)
	items := docker.LinkTunnels(raw, inv)
	s.tunnels.mu.Lock()
	s.tunnels.items, s.tunnels.at = items, time.Now()
	s.tunnels.mu.Unlock()
	return items, true, nil
}

func (s *Server) getDockerTunnels(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	items, supported, err := s.dockerTunnels(r.Context(), p.User.ClusterID, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if items == nil {
		items = []docker.Tunnel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "supported": supported})
}

func (s *Server) dockerTunnelAction(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeLifecycle)
	if !ok {
		return
	}
	if !hasRole(p, rbac.SuperAdmin) {
		writeErr(w, http.StatusForbidden, "only admin may restart tunnel services")
		return
	}
	var req struct {
		MachineID string `json:"machine_id"`
		Unit      string `json:"unit"`
		Action    string `json:"action"`
		// ApproveHost is a SuperAdmin's approval to touch the host's own tunnel.
		ApproveHost bool `json:"approve_host"`
	}
	if err := readJSON(r, &req); err != nil || !docker.ValidTunnelUnit(req.Unit) || (req.Action != "start" && req.Action != "restart") {
		writeErr(w, http.StatusBadRequest, "machine_id, a cloudflared unit and action start or restart are required")
		return
	}
	hostOK, ok := s.hostTunnelGate(w, r, p, req.MachineID, req.Unit, req.ApproveHost)
	if !ok {
		return
	}
	rpc, supported := s.dockerRPC().(dockerTunnelRPC)
	if !supported {
		writeErr(w, http.StatusNotImplemented, "this node's agent cannot manage tunnels; update No-dal on the node")
		return
	}
	if err := rpc.DockerTunnelUnit(r.Context(), req.MachineID, req.Unit, req.Action, hostOK); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "docker.tunnel."+req.Action, "ok", req.MachineID+" "+req.Unit)
	_, _, _ = s.dockerTunnels(r.Context(), p.User.ClusterID, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- jobs ---

type jobLine struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// opsJob is one bulk task or script run. It runs in the background so a
// long run outlives the request that started it.
type opsJob struct {
	mu         sync.Mutex
	ID         string
	Kind       string
	Name       string
	Status     string
	StartedBy  string
	StartedAt  time.Time
	FinishedAt time.Time
	Lines      []jobLine
	Failed     int
}

func (j *opsJob) log(level, format string, args ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.Lines) < 2000 {
		j.Lines = append(j.Lines, jobLine{At: time.Now().UTC(), Level: level, Message: fmt.Sprintf(format, args...)})
	}
	if level == "error" {
		j.Failed++
	}
}

func (j *opsJob) json() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	lines := append([]jobLine(nil), j.Lines...)
	out := map[string]any{
		"id": j.ID, "kind": j.Kind, "name": j.Name, "status": j.Status, "started_by": j.StartedBy,
		"started_at": j.StartedAt.Format(time.RFC3339), "failed": j.Failed, "lines": lines,
	}
	if !j.FinishedAt.IsZero() {
		out["finished_at"] = j.FinishedAt.Format(time.RFC3339)
	}
	return out
}

type opsJobs struct {
	mu   sync.Mutex
	jobs []*opsJob
}

func (o *opsJobs) add(j *opsJob) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.jobs = append(o.jobs, j)
	if len(o.jobs) > 30 {
		o.jobs = o.jobs[len(o.jobs)-30:]
	}
}

func (o *opsJobs) get(id string) *opsJob {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, j := range o.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

func (o *opsJobs) list() []*opsJob {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := append([]*opsJob(nil), o.jobs...)
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

func (s *Server) listOpsJobs(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.ComputeRead); err != nil {
		return
	}
	items := []map[string]any{}
	for _, j := range s.opsJobs.list() {
		row := j.json()
		delete(row, "lines")
		items = append(items, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getOpsJob(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.ComputeRead); err != nil {
		return
	}
	j := s.opsJobs.get(r.PathValue("id"))
	if j == nil {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, j.json())
}

// --- scripts ---

// dockerScript is a one-off list of Docker steps, written as JSON in the
// Docker page. It runs once; schedules belong to Automations.
type dockerScript struct {
	Name            string       `json:"name,omitempty"`
	Description     string       `json:"description,omitempty"`
	ContinueOnError bool         `json:"continue_on_error,omitempty"`
	Steps           []dockerStep `json:"steps"`
}

type dockerStep struct {
	Action         string   `json:"action"`
	Machine        string   `json:"machine,omitempty"`
	Container      string   `json:"container,omitempty"`
	Project        string   `json:"project,omitempty"`
	Command        []string `json:"command,omitempty"`
	Unit           string   `json:"unit,omitempty"`
	Seconds        int      `json:"seconds,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	Monitor        string   `json:"monitor,omitempty"`
	IncludeIgnored bool     `json:"include_ignored,omitempty"`
}

var stepActions = map[string]bool{
	"start": true, "stop": true, "restart": true, "pull": true, "recreate": true,
	"start-all": true, "stop-all": true, "restart-all": true, "fix": true,
	"exec": true, "tunnel-restart": true, "tunnel-start": true,
	"wait": true, "wait-healthy": true, "uptime-check": true,
}

func parseDockerScript(raw []byte) (dockerScript, error) {
	var sc dockerScript
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sc); err != nil {
		return sc, fmt.Errorf("the JSON is not a valid script: %v", err)
	}
	if len(sc.Steps) == 0 {
		return sc, fmt.Errorf("the script has no steps")
	}
	if len(sc.Steps) > 100 {
		return sc, fmt.Errorf("a script has at most 100 steps")
	}
	for i := range sc.Steps {
		st := &sc.Steps[i]
		st.Action = strings.ToLower(strings.TrimSpace(st.Action))
		if !stepActions[st.Action] {
			return sc, fmt.Errorf("step %d: unknown action %q", i+1, st.Action)
		}
		switch st.Action {
		case "exec":
			if st.Container == "" || len(st.Command) == 0 {
				return sc, fmt.Errorf("step %d: exec needs container and command", i+1)
			}
		case "wait":
			if st.Seconds < 1 || st.Seconds > 600 {
				return sc, fmt.Errorf("step %d: wait seconds must be 1 to 600", i+1)
			}
		case "wait-healthy":
			if st.Container == "" {
				return sc, fmt.Errorf("step %d: wait-healthy needs a container", i+1)
			}
		case "tunnel-restart", "tunnel-start":
			if st.Machine == "" {
				return sc, fmt.Errorf("step %d: %s needs a machine", i+1, st.Action)
			}
			if st.Unit != "" && !docker.ValidTunnelUnit(st.Unit) {
				return sc, fmt.Errorf("step %d: %q is not a cloudflared service", i+1, st.Unit)
			}
		case "start", "stop", "restart", "pull", "recreate":
			if st.Container == "" && st.Project == "" {
				return sc, fmt.Errorf("step %d: %s needs a container or project (use %s-all for everything)", i+1, st.Action, st.Action)
			}
		}
		if st.Timeout < 0 || st.Timeout > 600 {
			return sc, fmt.Errorf("step %d: timeout must be 0 to 600 seconds", i+1)
		}
	}
	return sc, nil
}

// opsPoll is how often a running job re-reads Docker while it waits.
var opsPoll = 4 * time.Second

// scriptRun executes (or, dry, explains) a script against the live inventory.
type scriptRun struct {
	s         *Server
	clusterID string
	admin     bool
	dry       bool
	job       *opsJob
	inv       docker.Inventory
	ignored   map[string]bool
}

func (x *scriptRun) refresh(ctx context.Context) error {
	inv, err := x.s.dockerInventory(ctx, x.clusterID, true)
	if err != nil {
		return err
	}
	x.inv = inv
	return nil
}

func (x *scriptRun) loadIgnores(ctx context.Context) {
	x.ignored = map[string]bool{}
	prefs, _ := x.s.Store.ListDockerPrefs(ctx, x.clusterID)
	for _, p := range prefs {
		if p.Ignored {
			x.ignored[p.MachineID+"|"+p.Scope+"|"+p.Name] = true
		}
	}
}

func (x *scriptRun) isIgnored(c docker.Container) bool {
	return x.ignored[c.MachineID+"|container|"+c.Name] || (c.Project != "" && x.ignored[c.MachineID+"|project|"+c.Project])
}

func (x *scriptRun) machineID(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	for _, m := range x.inv.Machines {
		if m.ID == name || strings.EqualFold(m.Name, name) || (strings.EqualFold(name, "host") && m.Kind == docker.KindHost) {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("no Docker machine named %q", name)
}

func machineLabel(c docker.Container) string {
	return firstNonEmpty(c.MachineName, c.MachineID)
}

// targets resolves a step's machine, project and container to containers.
func (x *scriptRun) targets(st dockerStep) ([]docker.Container, error) {
	mid, err := x.machineID(st.Machine)
	if err != nil {
		return nil, err
	}
	var out []docker.Container
	for _, c := range x.inv.Containers {
		if mid != "" && c.MachineID != mid {
			continue
		}
		if st.Project != "" && c.Project != st.Project {
			continue
		}
		if st.Container != "" {
			match := c.Name == st.Container || (len(st.Container) >= 6 && strings.HasPrefix(c.EngineID, st.Container)) ||
				(st.Project != "" && c.Service == st.Container)
			if !match {
				continue
			}
		}
		out = append(out, c)
	}
	if st.Container != "" && len(out) > 1 {
		return nil, fmt.Errorf("%q matches %d containers; add the machine", st.Container, len(out))
	}
	return out, nil
}

func (x *scriptRun) do(desc string, fn func() error) error {
	if x.dry {
		x.job.log("plan", "Would %s", desc)
		return nil
	}
	if err := fn(); err != nil {
		x.job.log("error", "Could not %s: %v", desc, err)
		return err
	}
	x.job.log("ok", "%s%s", strings.ToUpper(desc[:1]), desc[1:])
	return nil
}

func (x *scriptRun) containerAction(ctx context.Context, c docker.Container, action string) error {
	return x.do(fmt.Sprintf("%s %s on %s", action, c.Name, machineLabel(c)), func() error {
		_, err := x.s.dockerRPC().DockerAction(ctx, docker.ActionRequest{Action: action, MachineID: c.MachineID, ContainerID: c.EngineID})
		return err
	})
}

// groupsOf splits containers by machine and Compose project, so each group
// is ordered by its own dependencies.
func groupsOf(all []docker.Container, pick []docker.Container) map[string][2][]docker.Container {
	out := map[string][2][]docker.Container{}
	for _, c := range pick {
		key := c.MachineID + "|" + c.Project
		if c.Project == "" {
			key = c.MachineID + "|" + c.Name
		}
		g := out[key]
		g[1] = append(g[1], c)
		out[key] = g
	}
	for key, g := range out {
		for _, c := range all {
			if c.Project != "" && c.MachineID+"|"+c.Project == key {
				g[0] = append(g[0], c)
			}
		}
		if len(g[0]) == 0 {
			g[0] = g[1]
		}
		out[key] = g
	}
	return out
}

func sortedKeys(m map[string][2][]docker.Container) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ordered runs action over containers in dependency order: dependencies
// first to start or restart, last to stop.
func (x *scriptRun) ordered(ctx context.Context, action string, pick []docker.Container) error {
	var firstErr error
	groups := groupsOf(x.inv.Containers, pick)
	for _, key := range sortedKeys(groups) {
		g := groups[key]
		layers := docker.RecoveryOrder(g[0], g[1])
		if action == "stop" {
			for i, j := 0, len(layers)-1; i < j; i, j = i+1, j-1 {
				layers[i], layers[j] = layers[j], layers[i]
			}
		}
		for _, layer := range layers {
			for _, c := range layer {
				if action == "start" && c.State == "running" {
					continue
				}
				if action == "stop" && c.State != "running" && c.State != "restarting" {
					continue
				}
				if err := x.containerAction(ctx, c, action); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	return firstErr
}

func (x *scriptRun) waitRunning(ctx context.Context, cs []docker.Container, timeout time.Duration) {
	if x.dry || len(cs) == 0 {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(opsPoll):
		}
		if x.refresh(ctx) != nil {
			continue
		}
		up := 0
		for _, want := range cs {
			for _, c := range x.inv.Containers {
				if c.MachineID == want.MachineID && c.Name == want.Name && c.State == "running" && c.Health != docker.HealthCritical {
					up++
				}
			}
		}
		if up == len(cs) {
			return
		}
	}
	x.job.log("warn", "Dependencies were still starting after %s; continuing", timeout)
}

// fix restarts unhealthy containers, their dependencies first, then starts
// any cloudflared tunnel that is down on the machines it touched.
func (x *scriptRun) fix(ctx context.Context, st dockerStep) error {
	scope := st
	scope.Container = ""
	pool, err := x.targets(scope)
	if err != nil {
		return err
	}
	var broken []docker.Container
	for _, c := range pool {
		if docker.NeedsFix(c) && (st.IncludeIgnored || !x.isIgnored(c)) {
			broken = append(broken, c)
		}
	}
	machines := map[string]bool{}
	if len(broken) == 0 {
		x.job.log("ok", "No unhealthy containers%s", scopeText(st))
	}
	groups := groupsOf(x.inv.Containers, broken)
	var firstErr error
	for _, key := range sortedKeys(groups) {
		g := groups[key]
		layers := docker.RecoveryOrder(g[0], g[1])
		names := []string{}
		for _, c := range g[1] {
			names = append(names, c.Name)
		}
		x.job.log("info", "Fixing %s (%d layer(s), dependencies first)", strings.Join(names, ", "), len(layers))
		for li, layer := range layers {
			for _, c := range layer {
				machines[c.MachineID] = true
				action := "restart"
				if c.State != "running" {
					action = "start"
				}
				if err := x.containerAction(ctx, c, action); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			if li < len(layers)-1 {
				x.waitRunning(ctx, layer, 60*time.Second)
			}
		}
	}
	if rpc, ok := x.s.dockerRPC().(dockerTunnelRPC); ok {
		tunnels, _, terr := x.s.dockerTunnels(ctx, x.clusterID, true)
		if terr == nil {
			mid, _ := x.machineID(st.Machine)
			for _, t := range tunnels {
				if t.Active || (mid != "" && t.MachineID != mid) {
					continue
				}
				if docker.IsHostMachine(t.MachineID) {
					x.job.log("warn", "left the host's tunnel %s alone (it is %s). %s", t.Unit, firstNonEmpty(t.SubState, t.State), docker.HostTunnelAdvice(t.Unit))
					continue
				}
				t := t
				if err := x.do(fmt.Sprintf("start tunnel %s on %s (it was %s)", t.Unit, t.MachineName, firstNonEmpty(t.SubState, t.State)), func() error {
					return rpc.DockerTunnelUnit(ctx, t.MachineID, t.Unit, "restart", false)
				}); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	if !x.dry && len(broken) > 0 {
		time.Sleep(opsPoll)
		if x.refresh(ctx) == nil {
			var still []string
			for _, c := range x.inv.Containers {
				for _, b := range broken {
					if c.MachineID == b.MachineID && c.Name == b.Name && docker.NeedsFix(c) {
						still = append(still, c.Name)
					}
				}
			}
			if len(still) > 0 {
				x.job.log("warn", "Still unhealthy after restart (a health check may need more time): %s", strings.Join(still, ", "))
			} else {
				x.job.log("ok", "All fixed containers are running")
			}
		}
	}
	return firstErr
}

func scopeText(st dockerStep) string {
	switch {
	case st.Project != "" && st.Machine != "":
		return " in " + st.Project + " on " + st.Machine
	case st.Project != "":
		return " in " + st.Project
	case st.Machine != "":
		return " on " + st.Machine
	}
	return ""
}

func (x *scriptRun) step(ctx context.Context, i int, st dockerStep) error {
	x.job.log("step", "Step %d: %s%s", i+1, st.Action, scopeText(st))
	switch st.Action {
	case "start", "stop", "restart", "pull", "recreate":
		cs, err := x.targets(st)
		if err != nil {
			x.job.log("error", "%v", err)
			return err
		}
		if len(cs) == 0 {
			err := fmt.Errorf("no containers match")
			x.job.log("error", "%v", err)
			return err
		}
		if st.Action == "pull" || st.Action == "recreate" {
			var firstErr error
			for _, c := range cs {
				if err := x.containerAction(ctx, c, st.Action); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		}
		return x.ordered(ctx, st.Action, cs)
	case "start-all", "stop-all", "restart-all":
		scope := st
		scope.Container = ""
		cs, err := x.targets(scope)
		if err != nil {
			x.job.log("error", "%v", err)
			return err
		}
		if len(cs) == 0 {
			x.job.log("ok", "No containers%s", scopeText(st))
			return nil
		}
		return x.ordered(ctx, strings.TrimSuffix(st.Action, "-all"), cs)
	case "fix":
		if err := x.fix(ctx, st); err != nil {
			return err
		}
		return nil
	case "exec":
		if !x.admin {
			err := fmt.Errorf("only admin may run commands in containers")
			x.job.log("error", "%v", err)
			return err
		}
		cs, err := x.targets(st)
		if err == nil && len(cs) != 1 {
			err = fmt.Errorf("%q matches no container", st.Container)
		}
		if err != nil {
			x.job.log("error", "%v", err)
			return err
		}
		rpc, ok := x.s.dockerRPC().(dockerExecRPC)
		if !ok {
			err := fmt.Errorf("this node's agent cannot run commands; update No-dal on the node")
			x.job.log("error", "%v", err)
			return err
		}
		c := cs[0]
		return x.do(fmt.Sprintf("run %q in %s", strings.Join(st.Command, " "), c.Name), func() error {
			res, err := rpc.DockerExecRun(ctx, c.MachineID, c.EngineID, st.Command, firstPositive(st.Timeout, 60))
			if err != nil {
				return err
			}
			out := strings.TrimSpace(res.Output)
			if len(out) > 4000 {
				out = out[len(out)-4000:]
			}
			if out != "" {
				x.job.log("output", "%s", out)
			}
			if res.ExitCode != 0 {
				return fmt.Errorf("exit code %d", res.ExitCode)
			}
			return nil
		})
	case "tunnel-restart", "tunnel-start":
		if !x.admin {
			err := fmt.Errorf("only admin may restart tunnel services")
			x.job.log("error", "%v", err)
			return err
		}
		rpc, ok := x.s.dockerRPC().(dockerTunnelRPC)
		if !ok {
			err := fmt.Errorf("this node's agent cannot manage tunnels; update No-dal on the node")
			x.job.log("error", "%v", err)
			return err
		}
		mid, err := x.machineID(st.Machine)
		if err != nil {
			x.job.log("error", "%v", err)
			return err
		}
		units := []string{}
		if st.Unit != "" {
			units = append(units, st.Unit)
		} else if tunnels, _, terr := x.s.dockerTunnels(ctx, x.clusterID, false); terr == nil {
			for _, t := range tunnels {
				if t.MachineID == mid {
					units = append(units, t.Unit)
				}
			}
		}
		if len(units) == 0 {
			err := fmt.Errorf("no cloudflared service found on %s", st.Machine)
			x.job.log("error", "%v", err)
			return err
		}
		if docker.IsHostMachine(mid) {
			err := fmt.Errorf("No-dal does not manage the host's own tunnel. %s", docker.HostTunnelAdvice(strings.Join(units, " ")))
			x.job.log("error", "%v", err)
			return err
		}
		action := strings.TrimPrefix(st.Action, "tunnel-")
		var firstErr error
		for _, u := range units {
			u := u
			if err := x.do(fmt.Sprintf("%s %s on %s", action, u, st.Machine), func() error {
				return rpc.DockerTunnelUnit(ctx, mid, u, action, false)
			}); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	case "wait":
		if x.dry {
			x.job.log("plan", "Would wait %d second(s)", st.Seconds)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(st.Seconds) * time.Second):
		}
		x.job.log("ok", "Waited %d second(s)", st.Seconds)
		return nil
	case "wait-healthy":
		cs, err := x.targets(st)
		if err == nil && len(cs) != 1 {
			err = fmt.Errorf("%q matches no container", st.Container)
		}
		if err != nil {
			x.job.log("error", "%v", err)
			return err
		}
		timeout := time.Duration(firstPositive(st.Timeout, 120)) * time.Second
		if x.dry {
			x.job.log("plan", "Would wait up to %s for %s to be running and healthy", timeout, cs[0].Name)
			return nil
		}
		deadline := time.Now().Add(timeout)
		for {
			if x.refresh(ctx) == nil {
				for _, c := range x.inv.Containers {
					if c.MachineID == cs[0].MachineID && c.Name == cs[0].Name && c.State == "running" && !docker.NeedsFix(c) {
						x.job.log("ok", "%s is running and healthy", c.Name)
						return nil
					}
				}
			}
			if time.Now().After(deadline) {
				err := fmt.Errorf("%s was not healthy within %s", cs[0].Name, timeout)
				x.job.log("error", "%v", err)
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opsPoll):
			}
		}
	case "uptime-check":
		if x.dry {
			x.job.log("plan", "Would check uptime monitor(s) %s and recover failing ones", firstNonEmpty(st.Monitor, "(all)"))
			return nil
		}
		return x.s.uptimeCheckNamed(ctx, x.clusterID, st.Monitor, x.job)
	}
	return nil
}

func firstPositive(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// runScript runs every step, stopping at the first failure unless the
// script says to continue.
func (x *scriptRun) runScript(ctx context.Context, sc dockerScript) {
	if err := x.refresh(ctx); err != nil {
		x.job.log("error", "Docker inventory is unavailable: %v", err)
		return
	}
	x.loadIgnores(ctx)
	for i, st := range sc.Steps {
		if ctx.Err() != nil {
			x.job.log("error", "Stopped: %v", ctx.Err())
			return
		}
		if err := x.step(ctx, i, st); err != nil && !sc.ContinueOnError && !x.dry {
			x.job.log("error", "Stopped after step %d", i+1)
			return
		}
	}
}

func (s *Server) newOpsJob(kind, name, by string) *opsJob {
	j := &opsJob{ID: uuid.NewString(), Kind: kind, Name: name, Status: "running", StartedBy: by, StartedAt: time.Now().UTC()}
	return j
}

// startScriptJob runs a script in the background and returns its job.
func (s *Server) startScriptJob(clusterID, by string, admin bool, kind string, sc dockerScript, audit func(outcome string)) *opsJob {
	j := s.newOpsJob(kind, firstNonEmpty(sc.Name, kind), by)
	s.opsJobs.add(j)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		x := &scriptRun{s: s, clusterID: clusterID, admin: admin, job: j}
		x.runScript(ctx, sc)
		j.mu.Lock()
		j.FinishedAt = time.Now().UTC()
		switch {
		case j.Failed == 0:
			j.Status = "succeeded"
		default:
			j.Status = "failed"
		}
		status := j.Status
		j.mu.Unlock()
		s.tunnels.mu.Lock()
		s.tunnels.at = time.Time{}
		s.tunnels.mu.Unlock()
		if audit != nil {
			audit(status)
		}
	}()
	return j
}

// dockerBulk runs start-all, stop-all, restart-all or fix over everything,
// one machine or one Compose project.
func (s *Server) dockerBulk(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeLifecycle)
	if !ok {
		return
	}
	var req struct {
		Action         string `json:"action"`
		MachineID      string `json:"machine_id"`
		Project        string `json:"project"`
		IncludeIgnored bool   `json:"include_ignored"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	switch req.Action {
	case "start-all", "stop-all", "restart-all", "fix":
	default:
		writeErr(w, http.StatusBadRequest, "action must be start-all, stop-all, restart-all or fix")
		return
	}
	names := map[string]string{"start-all": "Start all", "stop-all": "Stop all", "restart-all": "Restart all", "fix": "Fix unhealthy"}
	sc := dockerScript{Name: names[req.Action], ContinueOnError: true, Steps: []dockerStep{{
		Action: req.Action, Machine: req.MachineID, Project: req.Project, IncludeIgnored: req.IncludeIgnored,
	}}}
	j := s.startScriptJob(p.User.ClusterID, p.User.Username, hasRole(p, rbac.SuperAdmin), "bulk", sc, func(outcome string) {
		s.auditInternal(p.User.ClusterID, p.User.ID, "docker.bulk."+req.Action, outcome, req.MachineID+" "+req.Project)
	})
	writeJSON(w, http.StatusAccepted, j.json())
}

// runDockerScript tests (dry_run) or runs a JSON script once.
func (s *Server) runDockerScript(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeLifecycle)
	if !ok {
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
	sc, err := parseDockerScript(raw)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	admin := hasRole(p, rbac.SuperAdmin)
	if req.DryRun {
		j := s.newOpsJob("test", firstNonEmpty(sc.Name, "test"), p.User.Username)
		x := &scriptRun{s: s, clusterID: p.User.ClusterID, admin: admin, dry: true, job: j}
		x.runScript(r.Context(), sc)
		out := j.json()
		out["valid"] = j.Failed == 0
		writeJSON(w, http.StatusOK, out)
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "docker.script.run", "started", firstNonEmpty(sc.Name, "script"))
	j := s.startScriptJob(p.User.ClusterID, p.User.Username, admin, "script", sc, func(outcome string) {
		s.auditInternal(p.User.ClusterID, p.User.ID, "docker.script.run", outcome, firstNonEmpty(sc.Name, "script"))
	})
	writeJSON(w, http.StatusAccepted, j.json())
}

// auditInternal records an audit event for background work that has no request.
func (s *Server) auditInternal(clusterID, actor, action, result, detail string) {
	body := json.RawMessage(`{}`)
	if detail != "" {
		b, _ := json.Marshal(map[string]string{"detail": detail})
		body = b
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.Store.InsertAudit(ctx, appdb.AuditEvent{
		ID: uuid.NewString(), ClusterID: clusterID, ActorUserID: actor, Action: action,
		Result: result, RemoteAddr: "internal", Detail: body, CreatedAt: s.now(),
	})
}
