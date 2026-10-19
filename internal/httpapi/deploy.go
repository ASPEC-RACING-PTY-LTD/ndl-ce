package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/deploy"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// DeployRPC is the agent surface for project deployments.
type DeployRPC interface {
	Deploy(ctx context.Context, action string, req agentrpc.DeployRequest, out any) error
}

func (s *Server) deployRPC() (DeployRPC, error) {
	if s.Deployer != nil {
		return s.Deployer, nil
	}
	if v, ok := s.Agent.(DeployRPC); ok {
		return v, nil
	}
	return nil, errUnavailable("the node agent is unavailable")
}

// deployRuntime is the control plane's deployment worker state.
type deployRuntime struct {
	once sync.Once
	kick chan struct{}
	busy atomic.Bool
	// mu serialises read-modify-write of deployment rows between the
	// worker and requests. It is never held across an agent call.
	mu sync.Mutex
	// misses counts status polls that could not reach a run.
	misses map[string]int
	// starting are deployments this process is handing to the node.
	starting map[string]bool
	// batchesDone are bulk runs already reported.
	batchesDone map[string]bool
	// hookChecks are projects a webhook is checking.
	hookChecks map[string]bool
	// detecting is set while a round of update checks runs.
	detecting atomic.Bool
	// lastRetention is when old logs were last expired.
	lastRetention time.Time
}

func (s *Server) deployRT() *deployRuntime {
	s.deployState.once.Do(func() {
		s.deployState.kick = make(chan struct{}, 1)
		s.deployState.misses = map[string]int{}
		s.deployState.starting = map[string]bool{}
		s.deployState.batchesDone = map[string]bool{}
		s.deployState.hookChecks = map[string]bool{}
	})
	return &s.deployState
}

func (s *Server) deployKick() {
	select {
	case s.deployRT().kick <- struct{}{}:
	default:
	}
}

func writeDeployErr(w http.ResponseWriter, err error) {
	var se statusError
	if errors.As(err, &se) {
		writeErr(w, se.status, se.msg)
		return
	}
	writeErr(w, http.StatusInternalServerError, err.Error())
}

// requireDeployOwner allows SuperAdmins only: they write the commands that run.
func (s *Server) requireDeployOwner(w http.ResponseWriter, r *http.Request) (*principal, bool) {
	p, ok := s.requireDocker(w, r, rbac.ComputeModify)
	if !ok {
		return nil, false
	}
	if !hasRole(p, rbac.SuperAdmin) {
		writeErr(w, http.StatusForbidden, "only a SuperAdmin can change deployment commands, credentials and automation")
		return nil, false
	}
	return p, true
}

// --- settings ---

func (s *Server) deploySettings(ctx context.Context, clusterID string) appdb.DeploySettings {
	st, err := s.Store.GetDeploySettings(ctx, clusterID)
	if err != nil || st == nil {
		st = appdb.DefaultDeploySettings(clusterID)
	}
	st.ClusterID = clusterID
	return *st
}

func normalizeDeploySettings(in appdb.DeploySettings) (appdb.DeploySettings, error) {
	if in.MaxConcurrent < 1 || in.MaxConcurrent > 50 {
		return in, errBadRequest("simultaneous deployments must be 1 to 50")
	}
	if in.PerContainer < 1 || in.PerContainer > 20 {
		return in, errBadRequest("deployments per container must be 1 to 20")
	}
	dir, err := deploy.LogDir(in.LogDir, false)
	if err != nil && !strings.Contains(err.Error(), "no such file") {
		return in, errBadRequest(err.Error())
	}
	if dir != "" {
		in.LogDir = dir
	}
	if in.RetentionDays < 0 || in.RetentionDays > 3650 {
		return in, errBadRequest("keep logs for 0 (forever) to 3650 days")
	}
	if in.KeepPerProject < 0 || in.KeepPerProject > 10000 {
		return in, errBadRequest("deployments kept per project must be 0 (all) to 10000")
	}
	if in.MaxLogGB < 0 || in.MaxLogGB > 100000 {
		return in, errBadRequest("the log size limit must be 0 (none) or more")
	}
	if in.DefaultCommands, err = cleanDeployCommands(in.DefaultCommands); err != nil {
		return in, errBadRequest(err.Error())
	}
	if in.DetectIntervalSec < 60 || in.DetectIntervalSec > 7*24*3600 {
		return in, errBadRequest("the update check interval must be 60 seconds to 7 days")
	}
	if in.DefaultTimeoutSec < 60 || in.DefaultTimeoutSec > deploy.MaxTimeoutSec {
		return in, errBadRequest("the time limit must be 60 seconds to 24 hours")
	}
	if in.NotifyEvents, err = cleanNotifyEvents(in.NotifyEvents); err != nil {
		return in, errBadRequest(err.Error())
	}
	return in, nil
}

func cleanDeployCommands(in []appdb.DeployCommand) ([]appdb.DeployCommand, error) {
	cmds := make([]deploy.Command, len(in))
	for i, c := range in {
		cmds[i] = deploy.Command{Run: c.Run, ContinueOnError: c.ContinueOnError}
	}
	cmds, err := deploy.CleanCommands(cmds)
	if err != nil {
		return nil, err
	}
	out := make([]appdb.DeployCommand, len(cmds))
	for i, c := range cmds {
		out[i] = appdb.DeployCommand{Run: c.Run, ContinueOnError: c.ContinueOnError}
	}
	return out, nil
}

// Notification events a project can send.
var deployNotifyEvents = map[string]bool{
	"update.available": true, "deploy.succeeded": true, "deploy.failed": true, "bulk.finished": true,
}

func cleanNotifyEvents(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if !deployNotifyEvents[e] {
			return nil, fmt.Errorf("%q is not a deployment event", e)
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Server) getDeploySettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.deploySettings(r.Context(), p.User.ClusterID))
}

func (s *Server) putDeploySettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDeployOwner(w, r)
	if !ok {
		return
	}
	in := s.deploySettings(r.Context(), p.User.ClusterID)
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	in.ClusterID = p.User.ClusterID
	in, err := normalizeDeploySettings(in)
	if err != nil {
		writeDeployErr(w, err)
		return
	}
	if err := s.Store.PutDeploySettings(r.Context(), in); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "docker.deploy.settings", "ok",
		fmt.Sprintf("max %d, per container %d, logs %s", in.MaxConcurrent, in.PerContainer, in.LogDir))
	s.deployKick()
	writeJSON(w, http.StatusOK, s.deploySettings(r.Context(), p.User.ClusterID))
}

// --- projects ---

var (
	gitNameRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	autoWindowR = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)-([01]\d|2[0-3]):([0-5]\d)$`)
)

var deployProviders = map[string]bool{"auto": true, "codehold": true, "github": true, "git": true}

func validGitName(s string) bool {
	return s == "" || (gitNameRE.MatchString(s) && !strings.Contains(s, "..") && !strings.HasSuffix(s, "/") && !strings.HasSuffix(s, ".lock"))
}

// projectInput is what a SuperAdmin sends to create or change a project.
type projectInput struct {
	WorkloadID string                     `json:"workload_id"`
	WorkingDir string                     `json:"working_dir"`
	Name       string                     `json:"name"`
	Config     *appdb.DeployProjectConfig `json:"config"`
	// Env sets variables; values are never sent back. EnvRemove deletes some.
	Env       map[string]string `json:"env"`
	EnvRemove []string          `json:"env_remove"`
}

func (s *Server) normalizeProjectConfig(ctx context.Context, clusterID string, c appdb.DeployProjectConfig, settings appdb.DeploySettings) (appdb.DeployProjectConfig, error) {
	var err error
	if c.Commands, err = cleanDeployCommands(c.Commands); err != nil {
		return c, errBadRequest(err.Error())
	}
	c.RunAs = strings.TrimSpace(c.RunAs)
	if c.RunAs != "" && !deploy.ValidUser(c.RunAs) {
		return c, errBadRequest(fmt.Sprintf("%q is not a valid user name", c.RunAs))
	}
	if c.TimeoutSec != 0 && (c.TimeoutSec < 60 || c.TimeoutSec > deploy.MaxTimeoutSec) {
		return c, errBadRequest("the time limit must be 60 seconds to 24 hours")
	}
	if c.Provider == "" {
		c.Provider = "auto"
	}
	if !deployProviders[c.Provider] {
		return c, errBadRequest("the provider must be auto, codehold, github or git")
	}
	if c.Remote == "" {
		c.Remote = "origin"
	}
	for _, v := range []string{c.Remote, c.Branch, c.FallbackRemote} {
		if !validGitName(v) {
			return c, errBadRequest(fmt.Sprintf("%q is not a valid Git remote or branch name", v))
		}
	}
	if c.DetectIntervalSec != 0 && (c.DetectIntervalSec < 60 || c.DetectIntervalSec > 7*24*3600) {
		return c, errBadRequest("the update check interval must be 60 seconds to 7 days")
	}
	c.AutoWindow = strings.TrimSpace(c.AutoWindow)
	if c.AutoWindow != "" && !autoWindowR.MatchString(c.AutoWindow) {
		return c, errBadRequest("the automatic deployment window must look like 02:00-05:00")
	}
	if c.NotifyEvents == nil {
		c.NotifyEvents = append([]string(nil), settings.NotifyEvents...)
	}
	if c.NotifyEvents, err = cleanNotifyEvents(c.NotifyEvents); err != nil {
		return c, errBadRequest(err.Error())
	}
	if len(c.NotifyChannels) > 0 {
		chs, err := s.Store.ListNotificationChannels(ctx, clusterID)
		if err != nil {
			return c, err
		}
		known := map[string]bool{}
		for _, ch := range chs {
			known[ch.ID] = true
		}
		for _, id := range c.NotifyChannels {
			if !known[id] {
				return c, errBadRequest("notification channel " + id + " does not exist")
			}
		}
	}
	c.EnvKeys = nil
	return c, nil
}

// deployWorkload returns the system container a project lives in.
func (s *Server) deployWorkload(ctx context.Context, clusterID, id string) (*appdb.Workload, error) {
	wl, err := s.Store.GetWorkload(ctx, clusterID, id)
	if err != nil {
		return nil, err
	}
	if wl == nil {
		return nil, errNotFound("that container no longer exists")
	}
	if wl.Kind != lxc.KindSystemContainer {
		return nil, errBadRequest("deployments run in system containers only")
	}
	return wl, nil
}

func (s *Server) createDeployProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDeployOwner(w, r)
	if !ok {
		return
	}
	var in projectInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ctx, cid := r.Context(), p.User.ClusterID
	wl, err := s.deployWorkload(ctx, cid, in.WorkloadID)
	if err != nil {
		writeDeployErr(w, err)
		return
	}
	dir, err := deploy.CleanDir(in.WorkingDir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	settings := s.deploySettings(ctx, cid)
	cfg := appdb.DeployProjectConfig{Commands: settings.DefaultCommands, Detect: true}
	if in.Config != nil {
		cfg = *in.Config
		if len(cfg.Commands) == 0 {
			cfg.Commands = settings.DefaultCommands
		}
	}
	if cfg, err = s.normalizeProjectConfig(ctx, cid, cfg, settings); err != nil {
		writeDeployErr(w, err)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = dir[strings.LastIndexByte(dir, '/')+1:]
	}
	if len(name) > 120 {
		name = name[:120]
	}
	proj := appdb.DeployProject{ID: uuid.NewString(), ClusterID: cid, WorkloadID: wl.ID, Name: name, WorkingDir: dir, Config: cfg}
	proj.State.Behind = -1
	if err := s.Store.PutDeployProject(ctx, proj); err != nil {
		if errors.Is(err, appdb.ErrDeployProjectExists) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.saveProjectEnv(ctx, cid, &proj, in.Env, in.EnvRemove); err != nil {
		writeDeployErr(w, err)
		return
	}
	s.audit(r, cid, p.User.ID, "docker.deploy.project.create", "ok", fmt.Sprintf("%s in %s at %s: %s", proj.Name, wl.Name, dir, commandsSummary(cfg.Commands)))
	s.deployKick()
	writeJSON(w, http.StatusCreated, s.projectView(ctx, proj, nil))
}

func commandsSummary(cmds []appdb.DeployCommand) string {
	parts := make([]string, len(cmds))
	for i, c := range cmds {
		parts[i] = c.Run
	}
	return strings.Join(parts, " ; ")
}

// saveProjectEnv applies env changes and records the variable names.
func (s *Server) saveProjectEnv(ctx context.Context, clusterID string, proj *appdb.DeployProject, set map[string]string, remove []string) error {
	env, hook, err := s.Store.DeployProjectSecrets(ctx, clusterID, proj.ID)
	if err != nil {
		return err
	}
	if env == nil {
		env = map[string]string{}
	}
	for k, v := range set {
		if !deploy.ValidEnvName(k) {
			return errBadRequest(fmt.Sprintf("%q cannot be set as an environment variable", k))
		}
		if len(v) > 32<<10 {
			return errBadRequest(k + " is too long")
		}
		env[k] = v
	}
	for _, k := range remove {
		delete(env, k)
	}
	if len(set) > 0 || len(remove) > 0 {
		if err := s.Store.PutDeployProjectSecrets(ctx, clusterID, proj.ID, env, hook); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	proj.Config.EnvKeys = keys
	return s.Store.PutDeployProject(ctx, *proj)
}

func (s *Server) loadDeployProject(w http.ResponseWriter, r *http.Request, p *principal) (*appdb.DeployProject, bool) {
	proj, err := s.Store.GetDeployProject(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if proj == nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	return proj, true
}

func (s *Server) getDeployProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	proj, ok := s.loadDeployProject(w, r, p)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.projectView(r.Context(), *proj, nil))
}

func (s *Server) putDeployProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDeployOwner(w, r)
	if !ok {
		return
	}
	proj, ok := s.loadDeployProject(w, r, p)
	if !ok {
		return
	}
	var in projectInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ctx, cid := r.Context(), p.User.ClusterID
	if name := strings.TrimSpace(in.Name); name != "" {
		if len(name) > 120 {
			name = name[:120]
		}
		proj.Name = name
	}
	if in.WorkingDir != "" {
		dir, err := deploy.CleanDir(in.WorkingDir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if dir != proj.WorkingDir {
			proj.WorkingDir = dir
			proj.State = appdb.DeployProjectState{Behind: -1, LastDeployID: proj.State.LastDeployID, LastDeployStatus: proj.State.LastDeployStatus, LastDeployAt: proj.State.LastDeployAt, LastSuccessAt: proj.State.LastSuccessAt}
		}
	}
	if in.Config != nil {
		cfg, err := s.normalizeProjectConfig(ctx, cid, *in.Config, s.deploySettings(ctx, cid))
		if err != nil {
			writeDeployErr(w, err)
			return
		}
		cfg.EnvKeys = proj.Config.EnvKeys
		// The webhook is turned on and off with its secret, at /webhook.
		cfg.Webhook = proj.Config.Webhook
		proj.Config = cfg
	}
	if err := s.Store.PutDeployProject(ctx, *proj); err != nil {
		if errors.Is(err, appdb.ErrDeployProjectExists) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.saveProjectEnv(ctx, cid, proj, in.Env, in.EnvRemove); err != nil {
		writeDeployErr(w, err)
		return
	}
	envNote := ""
	if len(in.Env) > 0 || len(in.EnvRemove) > 0 {
		envNote = fmt.Sprintf("; environment changed (%d set, %d removed)", len(in.Env), len(in.EnvRemove))
	}
	s.audit(r, cid, p.User.ID, "docker.deploy.project.update", "ok", fmt.Sprintf("%s at %s: %s%s", proj.Name, proj.WorkingDir, commandsSummary(proj.Config.Commands), envNote))
	s.deployKick()
	writeJSON(w, http.StatusOK, s.projectView(ctx, *proj, nil))
}

func (s *Server) deleteDeployProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDeployOwner(w, r)
	if !ok {
		return
	}
	proj, ok := s.loadDeployProject(w, r, p)
	if !ok {
		return
	}
	ctx, cid := r.Context(), p.User.ClusterID
	deps, err := s.Store.ListDeployments(ctx, cid, appdb.DeploymentFilter{ProjectID: proj.ID})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, d := range deps {
		if appdb.DeployActive(d.Status) {
			writeErr(w, http.StatusConflict, "this project is deploying; cancel it first")
			return
		}
	}
	s.deleteDeploymentLogs(ctx, deps)
	if err := s.Store.DeleteDeployProject(ctx, cid, proj.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, cid, p.User.ID, "docker.deploy.project.delete", "ok", proj.Name+" at "+proj.WorkingDir)
	w.WriteHeader(http.StatusNoContent)
}

// deleteDeploymentLogs removes deployments' log files, grouped by folder.
func (s *Server) deleteDeploymentLogs(ctx context.Context, deps []appdb.Deployment) {
	rpc, err := s.deployRPC()
	if err != nil {
		return
	}
	byDir := map[string][]string{}
	for _, d := range deps {
		if d.Detail.LogDir != "" {
			byDir[d.Detail.LogDir] = append(byDir[d.Detail.LogDir], d.ID)
		}
	}
	for dir, ids := range byDir {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = rpc.Deploy(cctx, "deploy-delete", agentrpc.DeployRequest{LogDir: dir, IDs: ids}, nil)
		cancel()
	}
}

// listDeployProjects returns configured projects, Compose projects in
// system containers that are not configured yet, and the settings.
func (s *Server) listDeployProjects(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	ctx, cid := r.Context(), p.User.ClusterID
	projects, err := s.Store.ListDeployProjects(ctx, cid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	active, _ := s.Store.ListDeployments(ctx, cid, appdb.DeploymentFilter{Active: true})
	byProject := map[string]*appdb.Deployment{}
	for i := range active {
		if cur := byProject[active[i].ProjectID]; cur == nil {
			byProject[active[i].ProjectID] = &active[i]
		}
	}
	names := s.workloadNames(ctx, cid)
	out := make([]map[string]any, 0, len(projects))
	known := map[string]bool{}
	for _, proj := range projects {
		known[proj.WorkloadID+"\x00"+proj.WorkingDir] = true
		out = append(out, s.projectViewNamed(ctx, proj, byProject[proj.ID], names))
	}
	discovered := []map[string]any{}
	if inv, err := s.dockerInventory(ctx, cid, false); err == nil {
		seen := map[string]bool{}
		for _, pr := range inv.Projects {
			if pr.Standalone || pr.WorkingDir == "" || pr.MachineID == "" || pr.MachineID == "host" {
				continue
			}
			key := pr.MachineID + "\x00" + pr.WorkingDir
			if known[key] || seen[key] {
				continue
			}
			seen[key] = true
			discovered = append(discovered, map[string]any{
				"workload_id": pr.MachineID, "workload_name": pr.MachineName, "name": pr.Name,
				"working_dir": pr.WorkingDir, "services": pr.Services, "running": pr.Running,
			})
		}
		sort.Slice(discovered, func(i, j int) bool {
			a, b := discovered[i], discovered[j]
			if a["workload_name"] != b["workload_name"] {
				return fmt.Sprint(a["workload_name"]) < fmt.Sprint(b["workload_name"])
			}
			return fmt.Sprint(a["name"]) < fmt.Sprint(b["name"])
		})
	}
	running := 0
	for _, d := range active {
		if d.Status == appdb.DeployRunning || d.Status == appdb.DeployStarting {
			running++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projects": out, "discovered": discovered, "settings": s.deploySettings(ctx, cid),
		"running": running, "queued": len(active) - running,
		"can_configure": hasRole(p, rbac.SuperAdmin), "can_deploy": rbac.Authorize(p.Grants, rbac.ComputeModify),
	})
}

func (s *Server) workloadNames(ctx context.Context, clusterID string) map[string]appdb.Workload {
	out := map[string]appdb.Workload{}
	wls, err := s.Store.ListWorkloads(ctx, clusterID)
	if err != nil {
		return out
	}
	for _, w := range wls {
		out[w.ID] = w
	}
	return out
}

func (s *Server) projectView(ctx context.Context, proj appdb.DeployProject, active *appdb.Deployment) map[string]any {
	if active == nil {
		if deps, err := s.Store.ListDeployments(ctx, proj.ClusterID, appdb.DeploymentFilter{ProjectID: proj.ID, Active: true, Limit: 1}); err == nil && len(deps) > 0 {
			active = &deps[0]
		}
	}
	return s.projectViewNamed(ctx, proj, active, s.workloadNames(ctx, proj.ClusterID))
}

func (s *Server) projectViewNamed(ctx context.Context, proj appdb.DeployProject, active *appdb.Deployment, wls map[string]appdb.Workload) map[string]any {
	wl, ok := wls[proj.WorkloadID]
	view := map[string]any{
		"id": proj.ID, "workload_id": proj.WorkloadID, "workload_name": wl.Name, "workload_status": wl.Status,
		"workload_missing": !ok, "name": proj.Name, "working_dir": proj.WorkingDir,
		"config": proj.Config, "state": proj.State, "created_at": proj.CreatedAt, "updated_at": proj.UpdatedAt,
		"webhook_path": "/api/v1/hooks/deploy/" + proj.ID,
	}
	if proj.Config.EnvKeys == nil {
		proj.Config.EnvKeys = []string{}
		view["config"] = proj.Config
	}
	if active != nil {
		view["active_deployment"] = s.deploymentView(ctx, *active, nil)
	}
	view["estimate_sec"] = s.deployEstimate(ctx, proj)
	return view
}

// deployEstimate is the mean length of the project's last successful runs.
func (s *Server) deployEstimate(ctx context.Context, proj appdb.DeployProject) int {
	deps, err := s.Store.ListDeployments(ctx, proj.ClusterID, appdb.DeploymentFilter{ProjectID: proj.ID, Limit: 20})
	if err != nil {
		return 0
	}
	total, n := 0.0, 0
	for _, d := range deps {
		if d.Status == appdb.DeploySucceeded && d.StartedAt != nil && d.FinishedAt != nil {
			total += d.FinishedAt.Sub(*d.StartedAt).Seconds()
			n++
			if n == 5 {
				break
			}
		}
	}
	if n == 0 {
		return 0
	}
	return int(total/float64(n) + 0.5)
}

func (s *Server) deploymentView(_ context.Context, d appdb.Deployment, wls map[string]appdb.Workload) map[string]any {
	steps := d.Detail.Steps
	if steps == nil {
		steps = []appdb.DeployStep{}
	}
	current := 0
	for i, st := range steps {
		if st.StartedAt != nil {
			current = i + 1
		}
	}
	view := map[string]any{
		"id": d.ID, "project_id": d.ProjectID, "project_name": d.Detail.ProjectName, "workload_id": d.WorkloadID,
		"batch_id": d.BatchID, "status": d.Status, "trigger": d.Trigger, "actor": d.Actor,
		"queued_at": d.QueuedAt, "started_at": d.StartedAt, "finished_at": d.FinishedAt,
		"working_dir": d.Detail.WorkingDir, "run_as": d.Detail.RunAs, "commands": d.Detail.Commands,
		"commit_before": d.Detail.CommitBefore, "commit_after": d.Detail.CommitAfter,
		"exit_code": d.Detail.ExitCode, "failed_step": d.Detail.FailedStep, "error": d.Detail.Error,
		"waiting_for": d.Detail.WaitingFor, "force": d.Detail.Force, "log_bytes": d.Detail.LogBytes,
		"log_gone": d.Detail.LogGone, "steps": steps, "step_count": len(d.Detail.Commands), "current_step": current,
		"health": d.Detail.Health, "cancel_requested": d.Detail.CancelBy != "",
	}
	if wls != nil {
		view["workload_name"] = wls[d.WorkloadID].Name
	}
	if d.StartedAt != nil {
		end := time.Now().UTC()
		if d.FinishedAt != nil {
			end = *d.FinishedAt
		}
		view["duration_sec"] = int(end.Sub(*d.StartedAt).Seconds())
	}
	return view
}

// --- deployments ---

// enqueueDeployment queues a run of a project unless one is already active.
func (s *Server) enqueueDeployment(ctx context.Context, proj appdb.DeployProject, trigger, actor, batch string, force bool) (*appdb.Deployment, error) {
	rt := s.deployRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	act, err := s.Store.ListDeployments(ctx, proj.ClusterID, appdb.DeploymentFilter{ProjectID: proj.ID, Active: true, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(act) > 0 {
		return nil, errConflict(proj.Name + " is already deploying")
	}
	settings := s.deploySettings(ctx, proj.ClusterID)
	timeout := proj.Config.TimeoutSec
	if timeout == 0 {
		timeout = settings.DefaultTimeoutSec
	}
	d := appdb.Deployment{
		ID: uuid.NewString(), ClusterID: proj.ClusterID, ProjectID: proj.ID, WorkloadID: proj.WorkloadID,
		BatchID: batch, Status: appdb.DeployQueued, Trigger: trigger, Actor: actor, QueuedAt: s.now(),
		Detail: appdb.DeploymentDetail{
			ProjectName: proj.Name, WorkingDir: proj.WorkingDir, RunAs: proj.Config.RunAs,
			Commands: proj.Config.Commands, TimeoutSec: timeout, Force: force,
			CommitBefore: proj.State.LocalCommit,
		},
	}
	if err := s.Store.PutDeployment(ctx, d); err != nil {
		return nil, err
	}
	st := proj.State
	st.LastDeployID, st.LastDeployStatus, st.LastDeployAt = d.ID, d.Status, &d.QueuedAt
	_ = s.Store.UpdateDeployProjectState(ctx, proj.ClusterID, proj.ID, st)
	s.deployKick()
	return &d, nil
}

func (s *Server) deployProjectNow(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeModify)
	if !ok {
		return
	}
	proj, ok := s.loadDeployProject(w, r, p)
	if !ok {
		return
	}
	var in struct {
		Force bool `json:"force"`
	}
	if r.ContentLength != 0 {
		_ = readJSON(r, &in)
	}
	if _, err := s.deployWorkload(r.Context(), p.User.ClusterID, proj.WorkloadID); err != nil {
		writeDeployErr(w, err)
		return
	}
	d, err := s.enqueueDeployment(r.Context(), *proj, "manual", p.User.Username, "", in.Force)
	if err != nil {
		writeDeployErr(w, err)
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "docker.deploy.start", "queued", fmt.Sprintf("%s (%s): %s", proj.Name, d.ID, commandsSummary(d.Detail.Commands)))
	writeJSON(w, http.StatusAccepted, s.deploymentView(r.Context(), *d, nil))
}

func (s *Server) loadDeployment(w http.ResponseWriter, r *http.Request, p *principal) (*appdb.Deployment, bool) {
	d, err := s.Store.GetDeployment(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if d == nil {
		writeErr(w, http.StatusNotFound, "deployment not found")
		return nil, false
	}
	return d, true
}

func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	d, ok := s.loadDeployment(w, r, p)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.deploymentView(r.Context(), *d, s.workloadNames(r.Context(), p.User.ClusterID)))
}

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	f := appdb.DeploymentFilter{ProjectID: r.PathValue("id"), Limit: limit, Active: r.URL.Query().Get("active") == "1"}
	if f.ProjectID == "" {
		f.ProjectID = r.URL.Query().Get("project_id")
	}
	deps, err := s.Store.ListDeployments(r.Context(), p.User.ClusterID, f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	names := s.workloadNames(r.Context(), p.User.ClusterID)
	out := make([]map[string]any, 0, len(deps))
	for _, d := range deps {
		out = append(out, s.deploymentView(r.Context(), d, names))
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": out})
}

// getDeploymentLog returns the log from offset. The cut never splits a
// UTF-8 character; next is where the following read starts.
func (s *Server) getDeploymentLog(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	d, ok := s.loadDeployment(w, r, p)
	if !ok {
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	maxBytes, _ := strconv.Atoi(r.URL.Query().Get("max"))
	if maxBytes <= 0 || maxBytes > 1<<20 {
		maxBytes = 256 << 10
	}
	done := !appdb.DeployActive(d.Status)
	if d.Detail.LogDir == "" {
		writeJSON(w, http.StatusOK, map[string]any{"data": "", "offset": offset, "next": offset, "size": 0, "done": done, "gone": d.Detail.LogGone})
		return
	}
	rpc, err := s.deployRPC()
	if err != nil {
		writeDeployErr(w, err)
		return
	}
	var lg agentrpc.DeployLog
	if err := rpc.Deploy(r.Context(), "deploy-log", agentrpc.DeployRequest{LogDir: d.Detail.LogDir, ID: d.ID, Offset: offset, Max: maxBytes}, &lg); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": "", "offset": offset, "next": offset, "size": 0, "done": done, "gone": true, "error": err.Error()})
		return
	}
	data := utf8Prefix(lg.Data)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": string(data), "offset": offset, "next": offset + int64(len(data)), "size": lg.Size,
		"done": done && offset+int64(len(data)) >= lg.Size,
	})
}

// utf8Prefix drops a trailing partial UTF-8 sequence.
func utf8Prefix(b []byte) []byte {
	for i := 1; i <= 3 && i <= len(b); i++ {
		c := b[len(b)-i]
		if c < 0x80 {
			return b
		}
		if c >= 0xC0 {
			need := 2
			switch {
			case c >= 0xF0:
				need = 4
			case c >= 0xE0:
				need = 3
			}
			if i < need {
				return b[:len(b)-i]
			}
			return b
		}
	}
	return b
}

// downloadDeploymentLog streams the whole log as a file.
func (s *Server) downloadDeploymentLog(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	d, ok := s.loadDeployment(w, r, p)
	if !ok {
		return
	}
	rpc, err := s.deployRPC()
	if err != nil || d.Detail.LogDir == "" {
		writeErr(w, http.StatusNotFound, "this deployment has no log")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="deploy-%s-%s.log"`, safeFileName(d.Detail.ProjectName), d.ID[:8]))
	w.Header().Set("Cache-Control", "no-store")
	var off int64
	for {
		var lg agentrpc.DeployLog
		if err := rpc.Deploy(r.Context(), "deploy-log", agentrpc.DeployRequest{LogDir: d.Detail.LogDir, ID: d.ID, Offset: off, Max: 1 << 20}, &lg); err != nil {
			if off == 0 {
				writeErr(w, http.StatusNotFound, "the log of this deployment is gone")
			}
			return
		}
		if len(lg.Data) == 0 {
			return
		}
		if _, err := w.Write(lg.Data); err != nil {
			return
		}
		off += int64(len(lg.Data))
		if off >= lg.Size {
			return
		}
	}
}

func safeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "project"
	}
	return b.String()
}

func (s *Server) cancelDeployment(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeModify)
	if !ok {
		return
	}
	d, ok := s.loadDeployment(w, r, p)
	if !ok {
		return
	}
	ctx, cid := r.Context(), p.User.ClusterID
	rt := s.deployRT()
	rt.mu.Lock()
	cur, err := s.Store.GetDeployment(ctx, cid, d.ID)
	if err != nil || cur == nil {
		rt.mu.Unlock()
		writeErr(w, http.StatusNotFound, "deployment not found")
		return
	}
	d = cur
	if !appdb.DeployActive(d.Status) {
		rt.mu.Unlock()
		writeErr(w, http.StatusConflict, "this deployment has already finished")
		return
	}
	d.Detail.CancelBy = p.User.Username
	queued := d.Status == appdb.DeployQueued || d.Status == appdb.DeployWaiting
	if queued {
		now := s.now()
		d.Status, d.FinishedAt, d.Detail.Error, d.Detail.WaitingFor = appdb.DeployCancelled, &now, "cancelled before it started", ""
	}
	_ = s.Store.PutDeployment(ctx, *d)
	rt.mu.Unlock()
	if queued {
		s.afterDeployment(context.WithoutCancel(ctx), *d)
	} else if d.Status == appdb.DeployRunning {
		if rpc, err := s.deployRPC(); err == nil {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = rpc.Deploy(cctx, "deploy-cancel", agentrpc.DeployRequest{LogDir: d.Detail.LogDir, ID: d.ID}, nil)
			cancel()
			if err != nil {
				writeErr(w, http.StatusBadGateway, "the node could not stop it: "+err.Error())
				return
			}
		}
	}
	s.audit(r, cid, p.User.ID, "docker.deploy.cancel", "ok", d.Detail.ProjectName+" ("+d.ID+")")
	s.deployKick()
	writeJSON(w, http.StatusAccepted, s.deploymentView(ctx, *d, nil))
}

// forceDeployment starts a deployment that waits for a backup now.
func (s *Server) forceDeployment(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeModify)
	if !ok {
		return
	}
	d, ok := s.loadDeployment(w, r, p)
	if !ok {
		return
	}
	rt := s.deployRT()
	rt.mu.Lock()
	cur, _ := s.Store.GetDeployment(r.Context(), p.User.ClusterID, d.ID)
	if cur == nil || (cur.Status != appdb.DeployQueued && cur.Status != appdb.DeployWaiting) {
		rt.mu.Unlock()
		writeErr(w, http.StatusConflict, "this deployment is not waiting")
		return
	}
	cur.Detail.Force = true
	_ = s.Store.PutDeployment(r.Context(), *cur)
	rt.mu.Unlock()
	s.audit(r, p.User.ClusterID, p.User.ID, "docker.deploy.force", "ok", cur.Detail.ProjectName+" ("+cur.ID+"): deploy anyway")
	s.deployKick()
	writeJSON(w, http.StatusAccepted, s.deploymentView(r.Context(), *cur, nil))
}
