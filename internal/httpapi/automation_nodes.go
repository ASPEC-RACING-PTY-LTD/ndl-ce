package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/automation"
	"github.com/no-dal/ndl-ce/internal/automation/engine"
	"github.com/no-dal/ndl-ce/internal/docker"
)

// The No-dal steps. Each one is a thin adapter: it calls the existing API
// handler in-process as the automation identity (automationCall), or the
// existing policy code, and never reimplements LXC, Docker, ZFS or shell
// work. Preview runs the same lookups and permission checks and says what
// would happen.

func autoField(key, label, typ string, required bool, help string) engine.Field {
	return engine.Field{Key: key, Label: label, Type: typ, Required: required, Help: help}
}

func autoTarget(key, label, target string, required bool, help string) engine.Field {
	return engine.Field{Key: key, Label: label, Type: "target", Target: target, Required: required, Help: help}
}

// autoAPIResult turns an in-process API answer into a step result. jobKey
// names the response field that is the job or operation id, if any.
func autoAPIResult(code int, body []byte, jobKey string) (engine.Result, error) {
	var parsed any
	if json.Unmarshal(body, &parsed) != nil {
		parsed = strings.TrimSpace(string(body))
	}
	out := map[string]any{"status": code, "result": parsed}
	if m, ok := parsed.(map[string]any); ok && jobKey != "" {
		if id, ok := m[jobKey]; ok {
			out["job_id"] = id
		}
	}
	if code >= 400 {
		return engine.Result{Output: out}, fmt.Errorf("%s", apiErrText(code, body))
	}
	return engine.Result{Output: out}, nil
}

func (s *Server) autoCall(ctx context.Context, in *engine.ExecInput, method, path string, body any, headers map[string]string, jobKey string) (engine.Result, error) {
	code, raw, err := s.automationCall(ctx, in.ClusterID, method, path, body, headers)
	if err != nil {
		return engine.Result{}, err
	}
	return autoAPIResult(code, raw, jobKey)
}

// autoWorkload finds a workload by id or name.
func (s *Server) autoWorkload(ctx context.Context, clusterID, ref string) (*appdb.Workload, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("choose a workload")
	}
	if wl, _ := s.Store.GetWorkload(ctx, clusterID, ref); wl != nil {
		return wl, nil
	}
	items, err := s.Store.ListWorkloads(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if strings.EqualFold(items[i].Name, ref) {
			return &items[i], nil
		}
	}
	return nil, fmt.Errorf("no workload named %q", ref)
}

func workloadNoun(wl appdb.Workload) string {
	switch wl.Kind {
	case "vm":
		return "VM"
	case "oci":
		return "OCI container"
	}
	return "container"
}

func (s *Server) nodeName(ctx context.Context, clusterID, id string) string {
	if n, _ := s.Store.GetNodeByID(ctx, clusterID, id); n != nil && n.Name != "" {
		return n.Name
	}
	if id == "" {
		return "this server"
	}
	return id
}

func (s *Server) describeWorkload(ctx context.Context, clusterID string, wl appdb.Workload) string {
	return fmt.Sprintf("%s %s on %s", workloadNoun(wl), wl.Name, s.nodeName(ctx, clusterID, firstNonEmpty(wl.DesiredNodeID, wl.NodeID)))
}

// autoPreviewWorkload runs the shared check: the workload lookup. Flows run
// as the system, so there is no grant to check; protected steps are held
// for approval when the flow is published instead.
func (s *Server) autoPreviewWorkload(ctx context.Context, in *engine.ExecInput, verb string) (engine.Preview, *appdb.Workload) {
	p := engine.Preview{}
	wl, err := s.autoWorkload(ctx, in.ClusterID, in.String("workload"))
	if err != nil {
		p.Would = fmt.Sprintf("would %s %s", verb, firstNonEmpty(in.String("workload"), "a workload"))
		p.Problems = append(p.Problems, err.Error())
		return p, nil
	}
	p.Would = fmt.Sprintf("would %s %s", verb, s.describeWorkload(ctx, in.ClusterID, *wl))
	p.Target = wl.ID
	return p, wl
}

func (s *Server) registerAutomationNodes(reg *engine.Registry) {
	targetWorkload := func(in *engine.ExecInput) string { return in.String("workload") }
	for _, action := range []string{"start", "stop", "restart"} {
		action := action
		title := strings.ToUpper(action[:1]) + action[1:]
		reg.Register(engine.NodeType{
			Type: "ndl.workload." + action, DisplayName: title + " a workload", Category: engine.CategoryNodal, Action: true,
			Destructive: action != "start", Idempotent: action == "start",
			Description: title + "s a container or VM the same way its page does, as a queued, audited operation.",
			Fields:      []engine.Field{autoTarget("workload", "Workload", "workload", true, "A container or VM, by name or id. Dynamic content works, such as {{ trigger.workload_id }}.")},
			TargetOf:    targetWorkload,
			Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
				wl, err := s.autoWorkload(ctx, in.ClusterID, in.String("workload"))
				if err != nil {
					return engine.Result{}, err
				}
				in.Log("info", "%s %s", action, s.describeWorkload(ctx, in.ClusterID, *wl))
				return s.autoCall(ctx, in, http.MethodPost, "/api/v1/workloads/"+url.PathEscape(wl.ID)+"/"+action, map[string]any{}, nil, "")
			},
			Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
				p, _ := s.autoPreviewWorkload(ctx, in, action)
				return p, nil
			},
		})
	}
	reg.Register(engine.NodeType{
		Type: "ndl.snapshot.create", DisplayName: "Take a snapshot", Category: engine.CategoryNodal, Action: true,
		Description: "Snapshots a workload's disk through the Snapshots page's code.",
		Fields: []engine.Field{
			autoTarget("workload", "Workload", "workload", true, ""),
			{Key: "name", Label: "Snapshot name", Type: "text", Required: true, Default: "auto-{{ run.id }}"},
		},
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			wl, err := s.autoWorkload(ctx, in.ClusterID, in.String("workload"))
			if err != nil {
				return engine.Result{}, err
			}
			return s.autoCall(ctx, in, http.MethodPost, "/api/v1/workloads/"+url.PathEscape(wl.ID)+"/snapshots", map[string]any{"name": in.String("name")}, nil, "id")
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			p, _ := s.autoPreviewWorkload(ctx, in, "snapshot")
			if strings.TrimSpace(in.String("name")) == "" {
				p.Problems = append(p.Problems, "a snapshot name is required")
			}
			p.Would += fmt.Sprintf(" as %q", in.String("name"))
			return p, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "ndl.backup.run", DisplayName: "Run a backup", Category: engine.CategoryNodal, Action: true,
		Description: "Runs a backup policy now, or backs one workload up to a target, through the Backups code.",
		Fields: []engine.Field{
			autoTarget("policy", "Backup policy", "backup_policy", false, "Runs every workload in the policy."),
			autoTarget("workload", "Or one workload", "workload", false, ""),
			autoTarget("target", "To backup target", "backup_target", false, "Needed with a single workload."),
		},
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			body := map[string]any{"policy_id": in.String("policy")}
			if ref := in.String("workload"); ref != "" {
				wl, err := s.autoWorkload(ctx, in.ClusterID, ref)
				if err != nil {
					return engine.Result{}, err
				}
				body["workload_id"], body["target_id"] = wl.ID, in.String("target")
			}
			return s.autoCall(ctx, in, http.MethodPost, "/api/v1/backups/run", body, nil, "id")
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			p := engine.Preview{}
			if ref := in.String("workload"); ref != "" {
				wl, err := s.autoWorkload(ctx, in.ClusterID, ref)
				if err != nil {
					p.Problems = append(p.Problems, err.Error())
					p.Would = "would back up " + ref
					return p, nil
				}
				tgt, _ := s.Store.GetBackupTarget(ctx, in.ClusterID, in.String("target"))
				if tgt == nil {
					p.Problems = append(p.Problems, "choose the backup target")
					p.Would = "would back up " + s.describeWorkload(ctx, in.ClusterID, *wl)
					return p, nil
				}
				p.Would = fmt.Sprintf("would back up %s to %s", s.describeWorkload(ctx, in.ClusterID, *wl), tgt.Name)
				return p, nil
			}
			pol, _ := s.Store.GetBackupPolicy(ctx, in.ClusterID, in.String("policy"))
			if pol == nil {
				p.Problems = append(p.Problems, "choose a backup policy or a workload")
				p.Would = "would run a backup"
				return p, nil
			}
			p.Would = "would run backup policy " + pol.Name
			return p, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "ndl.workload.migrate", DisplayName: "Migrate a workload", Category: engine.CategoryNodal, Action: true, Destructive: true,
		Description: "Moves a workload to another node with the Migrate code, including its checks.",
		Fields: []engine.Field{
			autoTarget("workload", "Workload", "workload", true, ""),
			autoTarget("dest_node", "To node", "node", false, "Leave empty to let placement choose."),
			{Key: "mode", Label: "Mode", Type: "select", Options: []engine.Option{{Value: "", Label: "Automatic"}, {Value: "live", Label: "Live"}, {Value: "offline", Label: "Offline"}}},
		},
		TargetOf: targetWorkload,
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			wl, err := s.autoWorkload(ctx, in.ClusterID, in.String("workload"))
			if err != nil {
				return engine.Result{}, err
			}
			return s.autoCall(ctx, in, http.MethodPost, "/api/v1/workloads/"+url.PathEscape(wl.ID)+"/migrate",
				map[string]any{"dest_node_id": in.String("dest_node"), "mode": in.String("mode")}, nil, "id")
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			p, wl := s.autoPreviewWorkload(ctx, in, "migrate")
			if wl == nil {
				return p, nil
			}
			dest, err := s.migrateDest(ctx, in.ClusterID, *wl, in.String("dest_node"))
			if err != nil {
				p.Problems = append(p.Problems, err.Error())
			} else if dest != nil {
				p.Would += " to " + firstNonEmpty(dest.Name, dest.ID)
			}
			return p, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "ndl.workload.clone", DisplayName: "Clone a workload", Category: engine.CategoryNodal, Action: true,
		Description: "Clones a container with the Clone code.",
		Fields:      []engine.Field{autoTarget("workload", "Workload", "workload", true, ""), autoField("name", "Name of the clone", "text", false, "")},
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			wl, err := s.autoWorkload(ctx, in.ClusterID, in.String("workload"))
			if err != nil {
				return engine.Result{}, err
			}
			return s.autoCall(ctx, in, http.MethodPost, "/api/v1/workloads/"+url.PathEscape(wl.ID)+"/clone", map[string]any{"name": in.String("name")}, nil, "id")
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			p, _ := s.autoPreviewWorkload(ctx, in, "clone")
			return p, nil
		},
	})
	dockerActions := []engine.Option{
		{Value: "restart", Label: "Restart"}, {Value: "start", Label: "Start"}, {Value: "stop", Label: "Stop"},
		{Value: "recreate", Label: "Recreate"}, {Value: "pull", Label: "Pull the image"}, {Value: "update", Label: "Update"},
	}
	reg.Register(engine.NodeType{
		Type: "ndl.docker.container", DisplayName: "Docker container action", Category: engine.CategoryNodal, Action: true, Destructive: true,
		Description: "Restarts, starts, stops, recreates or updates one Docker container, like its menu on the Docker page.",
		Fields: []engine.Field{
			autoTarget("container", "Container", "container", true, "machine/container, such as {{ trigger.ref }}."),
			{Key: "action", Label: "Action", Type: "select", Required: true, Default: "restart", Options: dockerActions},
		},
		TargetOf: func(in *engine.ExecInput) string { return in.String("action") + " " + in.String("container") },
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			machine, container := docker.ParseRef(in.String("container"))
			if machine == "" || container == "" {
				return engine.Result{}, fmt.Errorf("container must be machine/container")
			}
			return s.autoCall(ctx, in, http.MethodPost, "/api/v1/docker/machines/"+url.PathEscape(machine)+"/containers/"+url.PathEscape(container)+"/actions",
				map[string]any{"action": in.String("action")}, nil, "")
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			action := firstNonEmpty(in.String("action"), "restart")
			ref := in.String("container")
			p := engine.Preview{Would: fmt.Sprintf("would %s Docker container %s", action, ref), Target: ref}
			if !s.dockerEnabled(ctx, in.ClusterID) {
				p.Problems = append(p.Problems, "Docker Management is not enabled")
				return p, nil
			}
			machine, container := docker.ParseRef(ref)
			if inv, err := s.dockerInventory(ctx, in.ClusterID, false); err == nil {
				if c := findDockerContainer(inv, machine, container); c != nil {
					p.Would = fmt.Sprintf("would %s Docker container %s on %s", action, c.Name, firstNonEmpty(c.MachineName, c.MachineID))
				} else {
					p.Problems = append(p.Problems, "no Docker container "+ref+" right now")
				}
			}
			return p, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "ndl.docker.fix", DisplayName: "Fix unhealthy Docker containers", Category: engine.CategoryNodal, Action: true, Destructive: true,
		Description: "Restarts unhealthy containers, dependencies first, and starts stopped tunnels (the Docker page's Fix).",
		Fields:      []engine.Field{autoTarget("machine", "Machine", "machine", false, "Leave empty for every machine.")},
		TargetOf:    func(in *engine.ExecInput) string { return firstNonEmpty(in.String("machine"), "all") },
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			return s.autoCall(ctx, in, http.MethodPost, "/api/v1/docker/bulk", map[string]any{"action": "fix", "machine_id": in.String("machine")}, nil, "id")
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			p := engine.Preview{Would: "would fix unhealthy Docker containers on " + firstNonEmpty(in.String("machine"), "every machine")}
			if !s.dockerEnabled(ctx, in.ClusterID) {
				p.Problems = append(p.Problems, "Docker Management is not enabled")
			}
			return p, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "ndl.docker.redeploy", DisplayName: "Redeploy a Compose project", Category: engine.CategoryNodal, Action: true, Destructive: true,
		Description: "Runs a project's deployment, like Deploy now on the Deploy page.",
		Fields:      []engine.Field{autoTarget("project", "Project", "project", true, "")},
		TargetOf:    func(in *engine.ExecInput) string { return in.String("project") },
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			return s.autoCall(ctx, in, http.MethodPost, "/api/v1/docker/deploy/projects/"+url.PathEscape(in.String("project"))+"/deploy", map[string]any{}, nil, "id")
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			p := engine.Preview{Would: "would redeploy project " + in.String("project")}
			if proj, _ := s.Store.GetDeployProject(ctx, in.ClusterID, in.String("project")); proj == nil {
				p.Problems = append(p.Problems, "no deploy project "+in.String("project"))
			} else {
				p.Would = "would redeploy project " + proj.Name
			}
			if !s.dockerEnabled(ctx, in.ClusterID) {
				p.Problems = append(p.Problems, "Docker Management is not enabled")
			}
			return p, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "ndl.docker.prune", DisplayName: "Prune Docker", Category: engine.CategoryNodal, Action: true, Destructive: true,
		Description: "Runs the Docker prune with its saved settings.",
		Fields:      []engine.Field{},
		TargetOf:    func(*engine.ExecInput) string { return "docker" },
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			return s.autoCall(ctx, in, http.MethodPost, "/api/v1/docker/prune?wait=true", map[string]any{}, nil, "id")
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			p := engine.Preview{Would: "would prune Docker with the saved prune settings"}
			if !s.dockerEnabled(ctx, in.ClusterID) {
				p.Problems = append(p.Problems, "Docker Management is not enabled")
			}
			if dockerPruneBusy.Load() {
				p.Problems = append(p.Problems, "a Docker prune is already running")
			}
			return p, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "ndl.script.run", DisplayName: "Run a saved script", Category: engine.CategoryNodal, Action: true, Destructive: true,
		Description: "Runs a saved Docker script as the automation identity; its exec and tunnel steps are refused.",
		Fields:      []engine.Field{autoTarget("script", "Saved script", "script", true, "")},
		TargetOf:    func(in *engine.ExecInput) string { return in.String("script") },
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			saved, err := s.findSavedScript(ctx, in.ClusterID, "docker", in.String("script"))
			if err != nil {
				return engine.Result{}, err
			}
			return s.autoCall(ctx, in, http.MethodPost, "/api/v1/docker/script", map[string]any{"script": saved.Body}, nil, "id")
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			p := engine.Preview{Would: "would run saved script " + in.String("script")}
			saved, err := s.findSavedScript(ctx, in.ClusterID, "docker", in.String("script"))
			if err != nil {
				p.Problems = append(p.Problems, err.Error())
				return p, nil
			}
			// The Docker page's test mode checks every step without running it.
			code, raw, err := s.automationCall(ctx, in.ClusterID, http.MethodPost, "/api/v1/docker/script", map[string]any{"script": saved.Body, "dry_run": true}, nil)
			if err != nil {
				p.Problems = append(p.Problems, err.Error())
				return p, nil
			}
			var res struct {
				Valid bool   `json:"valid"`
				Error string `json:"error"`
			}
			_ = json.Unmarshal(raw, &res)
			if code >= 400 && res.Error == "" {
				p.Problems = append(p.Problems, apiErrText(code, raw))
			} else if !res.Valid {
				p.Problems = append(p.Problems, "the script's test found problems: "+firstNonEmpty(res.Error, "see the Docker page"))
			}
			return p, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "ndl.uptime.recover", DisplayName: "Check and recover Uptime monitors", Category: engine.CategoryNodal, Action: true, Destructive: true,
		Description: "Checks a monitor (or all of them) and restarts the containers of any that are down, like the Uptime policy.",
		Fields:      []engine.Field{autoTarget("monitor", "Monitor", "monitor", false, "Leave empty for every enabled monitor. {{ trigger.name }} is the monitor that went down.")},
		TargetOf:    func(in *engine.ExecInput) string { return firstNonEmpty(in.String("monitor"), "all") },
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			if _, err := s.autoMonitor(ctx, in.ClusterID, in.String("monitor")); err != nil {
				return engine.Result{}, err
			}
			run, err := s.evaluateOpsPolicy(ctx, in.ClusterID, in.ActorID, appdb.Policy{Name: "flow " + in.FlowID, Kind: automation.KindUptimeCheck, SpecYAML: autoSpecYAML(in.String("monitor"))})
			if err != nil {
				return engine.Result{}, err
			}
			return engine.Result{Output: map[string]any{"status": run.Status, "reason": run.Reason, "all_up": run.Status == appdb.PolicySucceeded}}, nil
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			p := engine.Preview{Would: "would check " + firstNonEmpty(in.String("monitor"), "every enabled monitor") + " and restart the containers of any that are down"}
			if _, err := s.autoMonitor(ctx, in.ClusterID, in.String("monitor")); err != nil {
				p.Problems = append(p.Problems, err.Error())
			}
			return p, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "ndl.storage.pressure", DisplayName: "Relieve storage pressure", Category: engine.CategoryNodal, Action: true, Destructive: true,
		Description: "When a pool is at or above the threshold, queues a migrate of its lowest-priority VM (the storage pressure policy).",
		Fields:      []engine.Field{{Key: "threshold_percent", Label: "Threshold percent", Type: "number", Default: 85, Required: true}},
		TargetOf:    func(*engine.ExecInput) string { return "storage" },
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			spec, err := automation.ParseJSONSpec(automation.KindStoragePressure, "", in.Int("threshold_percent", 85), false, "")
			if err != nil {
				return engine.Result{}, err
			}
			run, err := s.evaluatePolicy(ctx, in.ClusterID, in.ActorID, appdb.Policy{Name: "flow " + in.FlowID, Kind: spec.Kind, Action: spec.Action, ThresholdPercent: spec.ThresholdPercent})
			if err != nil {
				return engine.Result{}, err
			}
			out := map[string]any{"status": run.Status, "reason": run.Reason, "operation_ids": run.OperationIDs}
			if len(run.OperationIDs) > 0 {
				out["job_id"] = run.OperationIDs[0]
			}
			if run.Status == appdb.PolicyFailed {
				return engine.Result{Output: out}, fmt.Errorf("%s", run.Reason)
			}
			return engine.Result{Output: out}, nil
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			th := in.Int("threshold_percent", 85)
			p := engine.Preview{Would: fmt.Sprintf("would queue a migrate of the lowest-priority VM on each pool at or above %d%%", th)}
			if _, err := automation.ParseJSONSpec(automation.KindStoragePressure, "", th, false, ""); err != nil {
				p.Problems = append(p.Problems, err.Error())
			}
			pools, _ := s.Store.ListStoragePools(ctx, in.ClusterID)
			var hot []string
			for _, pool := range pools {
				if pct, ok := automation.UsedPercent(pool.TotalBytes, pool.UsableBytes); ok && pct >= th {
					hot = append(hot, fmt.Sprintf("%s (%d%%)", pool.Name, pct))
				}
			}
			if len(hot) > 0 {
				p.Would += "; pools over it now: " + strings.Join(hot, ", ")
			} else {
				p.Would += "; no pool is over it now"
			}
			return p, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "action.notify", DisplayName: "Notify", Category: engine.CategoryAction, Action: true,
		Description: "Sends a message to the alert notification channels and adds it to the Activity feed.",
		Fields: []engine.Field{
			autoField("title", "Title", "text", false, ""),
			autoField("message", "Message", "textarea", true, ""),
			{Key: "level", Label: "Level", Type: "select", Default: "info", Options: []engine.Option{{Value: "info", Label: "Info"}, {Value: "warning", Label: "Warning"}, {Value: "critical", Label: "Critical"}}},
			{Key: "channels", Label: "Channels", Type: "target", Target: "channel", Help: "Leave empty for every channel."},
		},
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			title, msg, level := in.String("title"), in.String("message"), firstNonEmpty(in.String("level"), "info")
			ids := autoList(in.Config["channels"])
			line := msg
			if title != "" {
				line = title + ": " + msg
			}
			s.emitEvent(ctx, in.ClusterID, "", "automation.notify", map[string]string{
				"title": title, "message": msg, "level": level, "flow_id": in.FlowID, "run_id": in.RunID,
			})
			s.notifyChannels(ctx, in.ClusterID, ids, deployMessage("automation.notify", line, map[string]any{"level": level, "flow_id": in.FlowID, "run_id": in.RunID}))
			return engine.Result{Output: map[string]any{"sent": true, "channels": len(ids), "message": line}}, nil
		},
		Preview: func(ctx context.Context, in *engine.ExecInput) (engine.Preview, error) {
			ids := autoList(in.Config["channels"])
			to := "every notification channel"
			if len(ids) > 0 {
				to = fmt.Sprintf("%d channel(s)", len(ids))
			}
			p := engine.Preview{Would: fmt.Sprintf("would notify %s: %s", to, firstNonEmpty(in.String("title"), in.String("message")))}
			if strings.TrimSpace(in.String("message")) == "" {
				p.Problems = append(p.Problems, "the message is empty")
			}
			return p, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "ndl.workloads.list", DisplayName: "List workloads", Category: engine.CategoryNodal, Idempotent: true,
		Description: "Reads matching containers and VMs, for a For each step. Changes nothing.",
		Fields: []engine.Field{
			{Key: "kind", Label: "Kind", Type: "select", Options: []engine.Option{{Value: "", Label: "Any"}, {Value: "system-container", Label: "Containers"}, {Value: "vm", Label: "VMs"}, {Value: "oci", Label: "OCI containers"}}},
			autoField("name_contains", "Name contains", "text", false, "Use a naming convention as a tag, such as nightly-."),
			autoField("status", "Status", "text", false, "Such as running."),
		},
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			items, err := s.Store.ListWorkloads(ctx, in.ClusterID)
			if err != nil {
				return engine.Result{}, err
			}
			kind, name, status := in.String("kind"), strings.ToLower(in.String("name_contains")), in.String("status")
			out := []any{}
			for _, w := range items {
				if (kind != "" && w.Kind != kind) || (name != "" && !strings.Contains(strings.ToLower(w.Name), name)) || (status != "" && !strings.EqualFold(w.Status, status)) {
					continue
				}
				out = append(out, map[string]any{"id": w.ID, "name": w.Name, "kind": w.Kind, "status": w.Status, "node_id": w.NodeID})
			}
			return engine.Result{Output: map[string]any{"items": out, "count": len(out)}}, nil
		},
	})
	reg.Register(engine.NodeType{
		Type: "ndl.docker.containers.list", DisplayName: "List Docker containers", Category: engine.CategoryNodal, Idempotent: true,
		Description: "Reads Docker containers, such as the unhealthy ones, for a For each step. Changes nothing.",
		Fields: []engine.Field{
			{Key: "health", Label: "Health", Type: "select", Default: "unhealthy", Options: []engine.Option{{Value: "unhealthy", Label: "Unhealthy only"}, {Value: "", Label: "All"}}},
			autoTarget("machine", "Machine", "machine", false, ""),
		},
		Execute: func(ctx context.Context, in *engine.ExecInput) (engine.Result, error) {
			if !s.dockerEnabled(ctx, in.ClusterID) {
				return engine.Result{}, fmt.Errorf("Docker Management is not enabled")
			}
			inv, err := s.dockerInventory(ctx, in.ClusterID, false)
			if err != nil {
				return engine.Result{}, err
			}
			out := []any{}
			for _, c := range inv.Containers {
				if in.String("machine") != "" && c.MachineID != in.String("machine") {
					continue
				}
				if in.String("health") == "unhealthy" && c.Health != docker.HealthCritical && c.Health != docker.HealthDegraded {
					continue
				}
				out = append(out, map[string]any{"ref": c.Ref, "name": c.Name, "machine_id": c.MachineID, "machine_name": c.MachineName, "health": c.Health, "state": c.State, "project": c.Project})
			}
			return engine.Result{Output: map[string]any{"items": out, "count": len(out)}}, nil
		},
	})
}

func autoSpecYAML(target string) string {
	if strings.TrimSpace(target) == "" {
		return ""
	}
	raw, _ := json.Marshal(map[string]string{"target": strings.TrimSpace(target)})
	// YAML is a superset of JSON, so policyTarget reads this.
	return string(raw)
}

func autoList(v any) []string {
	switch x := v.(type) {
	case []any:
		var out []string
		for _, item := range x {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		var out []string
		for _, part := range strings.Split(x, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

func (s *Server) autoMonitor(ctx context.Context, clusterID, name string) (*appdb.UptimeMonitor, error) {
	monitors, err := s.Store.ListUptimeMonitors(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	for i := range monitors {
		if name == "" && monitors[i].Enabled {
			return &monitors[i], nil
		}
		if name != "" && (strings.EqualFold(monitors[i].Name, name) || monitors[i].ID == name) {
			return &monitors[i], nil
		}
	}
	if name == "" {
		return nil, fmt.Errorf("there are no enabled uptime monitors")
	}
	return nil, fmt.Errorf("no uptime monitor named %q", name)
}

// automationTargets lists what a target field can pick.
func (s *Server) automationTargets(ctx context.Context, clusterID, kind string) []map[string]any {
	out := []map[string]any{}
	add := func(value, label, meta string) {
		out = append(out, map[string]any{"value": value, "label": label, "meta": meta})
	}
	switch kind {
	case "workload":
		items, _ := s.Store.ListWorkloads(ctx, clusterID)
		for _, w := range items {
			add(w.ID, w.Name, workloadNoun(w)+" · "+w.Status)
		}
	case "node":
		items, _ := s.Store.ListClusterNodes(ctx, clusterID)
		for _, n := range items {
			add(n.ID, firstNonEmpty(n.Name, n.Hostname, n.ID), n.Role)
		}
	case "container", "machine":
		if !s.dockerEnabled(ctx, clusterID) {
			return out
		}
		inv, err := s.dockerInventory(ctx, clusterID, false)
		if err != nil {
			return out
		}
		if kind == "machine" {
			for _, m := range inv.Machines {
				add(m.ID, m.Name, m.Health)
			}
			return out
		}
		for _, c := range inv.Containers {
			add(c.Ref, c.Name, firstNonEmpty(c.MachineName, c.MachineID)+" · "+c.Health)
		}
	case "monitor":
		items, _ := s.Store.ListUptimeMonitors(ctx, clusterID)
		for _, m := range items {
			add(m.Name, m.Name, m.Target)
		}
	case "pool":
		items, _ := s.Store.ListStoragePools(ctx, clusterID)
		for _, p := range items {
			meta := p.BackendType
			if pct, ok := automation.UsedPercent(p.TotalBytes, p.UsableBytes); ok {
				meta += fmt.Sprintf(" · %d%% used", pct)
			}
			add(p.ID, p.Name, meta)
		}
	case "script":
		items, _ := s.Store.ListSavedScripts(ctx, clusterID, "docker")
		for _, sc := range items {
			add(sc.Name, sc.Name, "Docker script")
		}
	case "backup_policy":
		items, _ := s.Store.ListBackupPolicies(ctx, clusterID)
		for _, p := range items {
			add(p.ID, p.Name, "")
		}
	case "backup_target":
		items, _ := s.Store.ListBackupTargets(ctx, clusterID)
		for _, t := range items {
			add(t.ID, t.Name, t.Kind)
		}
	case "project":
		items, _ := s.Store.ListDeployProjects(ctx, clusterID)
		for _, p := range items {
			add(p.ID, p.Name, p.WorkingDir)
		}
	case "channel":
		items, _ := s.Store.ListNotificationChannels(ctx, clusterID)
		for _, c := range items {
			add(c.ID, c.Name, c.Kind)
		}
	case "connection":
		items, _ := s.Store.ListAutomationConnections(ctx, clusterID)
		for _, c := range items {
			add(c.ID, c.Name, c.Kind)
		}
	}
	return out
}
