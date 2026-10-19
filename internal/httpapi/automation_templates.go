package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/automation"
	"github.com/no-dal/ndl-ce/internal/automation/flowdef"
)

// automationTemplateDef is a starter flow offered on the Templates tab.
type automationTemplateDef struct {
	ID          string
	Name        string
	Description string
	JSON        string
	Limits      flowdef.Limits
}

func (t automationTemplateDef) definition() *flowdef.Definition {
	d, err := flowdef.Parse([]byte(t.JSON))
	if err != nil {
		panic("automation template " + t.ID + ": " + err.Error())
	}
	return d
}

var automationTemplates = []automationTemplateDef{
	{
		ID: "restart-unhealthy-containers", Name: "Restart unhealthy containers",
		Description: "When a Docker container becomes unhealthy, restart it. The per-target limit stops a container that keeps failing from restarting more than 3 times in 15 minutes.",
		JSON: `{"schema_version":1,"trigger":{"id":"trigger","type":"trigger.event","config":{"kind":"docker.container_unhealthy","conditions":[]}},
"nodes":[{"id":"restart","type":"ndl.docker.container","name":"Restart the container","config":{"container":"{{ trigger.ref }}","action":"restart"}}],
"edges":[{"from":"trigger","to":"restart"}]}`,
		Limits: flowdef.Limits{Concurrency: 2, DebounceSec: 300},
	},
	{
		ID: "recover-down-monitors", Name: "Recover down monitors",
		Description: "When an Uptime monitor goes down, check it again and restart the containers that serve it.",
		JSON: `{"schema_version":1,"trigger":{"id":"trigger","type":"trigger.event","config":{"kind":"uptime.monitor_down","conditions":[]}},
"nodes":[{"id":"recover","type":"ndl.uptime.recover","name":"Check and recover","config":{"monitor":"{{ trigger.name }}"}},
{"id":"tell","type":"action.notify","name":"Tell the team","config":{"title":"Uptime","message":"{{ trigger.name }} went down; recovery: {{ nodes.recover.reason }}","level":"warning"}}],
"edges":[{"from":"trigger","to":"recover"},{"from":"recover","to":"tell"}]}`,
		Limits: flowdef.Limits{DebounceSec: 300},
	},
	{
		ID: "nightly-snapshot", Name: "Nightly snapshot of tagged containers",
		Description: "Every night at 02:00, snapshot every workload whose name contains nightly. Change the name filter to your own convention.",
		JSON: `{"schema_version":1,"trigger":{"id":"trigger","type":"trigger.schedule","config":{"preset":"daily","at":"02:00","timezone":"UTC"}},
"nodes":[{"id":"find","type":"ndl.workloads.list","name":"Find tagged workloads","config":{"name_contains":"nightly"}},
{"id":"each","type":"logic.foreach","name":"For each workload","config":{"items":"{{ nodes.find.items }}","max":50}},
{"id":"snap","type":"ndl.snapshot.create","name":"Take a snapshot","config":{"workload":"{{ item.id }}","name":"nightly-{{ run.id }}"},"on_error":"continue"}],
"edges":[{"from":"trigger","to":"find"},{"from":"find","to":"each"},{"from":"each","to":"snap","port":"each"}]}`,
		Limits: flowdef.Limits{RunTimeoutSec: 4 * 3600},
	},
	{
		ID: "pool-over-85", Name: "Alert when a pool passes 85%",
		Description: "When a storage pool fills past 85%, send a notification.",
		JSON: `{"schema_version":1,"trigger":{"id":"trigger","type":"trigger.event","config":{"kind":"storage.pool_usage_high","conditions":[]}},
"nodes":[{"id":"check","type":"logic.condition","name":"At least 85%?","config":{"match":"all","rules":[{"left":"{{ trigger.percent }}","op":">=","right":85}]}},
{"id":"tell","type":"action.notify","name":"Notify","config":{"title":"Storage","message":"Pool {{ trigger.pool_name }} is {{ trigger.percent }}% full","level":"warning"}}],
"edges":[{"from":"trigger","to":"check"},{"from":"check","to":"tell","port":"true"}]}`,
	},
	{
		ID: "notify-failed-backup", Name: "Notify on failed backup",
		Description: "When a backup fails, send a notification with the error.",
		JSON: `{"schema_version":1,"trigger":{"id":"trigger","type":"trigger.event","config":{"kind":"backup.failed","conditions":[]}},
"nodes":[{"id":"tell","type":"action.notify","name":"Notify","config":{"title":"Backup failed","message":"The backup of {{ trigger.workload_name }} failed: {{ trigger.error }}","level":"critical"}}],
"edges":[{"from":"trigger","to":"tell"}]}`,
	},
}

func automationTemplate(id string) (automationTemplateDef, bool) {
	for _, t := range automationTemplates {
		if t.ID == id {
			return t, true
		}
	}
	return automationTemplateDef{}, false
}

// policyFlowDefinition is the flow equivalent of an old policy: a manual
// trigger, an approval step when the policy required one, and the adapter
// step for its kind.
func policyFlowDefinition(pol appdb.Policy) *flowdef.Definition {
	target := policyTarget(pol)
	node := flowdef.Node{ID: "policy", Name: pol.Name}
	switch pol.Kind {
	case automation.KindUptimeCheck:
		node.Type, node.Config = "ndl.uptime.recover", map[string]any{"monitor": target}
	case automation.KindDockerFix:
		node.Type, node.Config = "ndl.docker.fix", map[string]any{"machine": target}
	case automation.KindDockerScript:
		node.Type, node.Config = "ndl.script.run", map[string]any{"script": target}
	default:
		node.Type, node.Config = "ndl.storage.pressure", map[string]any{"threshold_percent": pol.ThresholdPercent}
	}
	d := emptyDefinition()
	if pol.RequireApproval {
		d.Nodes = append(d.Nodes, flowdef.Node{ID: "approve", Type: "logic.approval", Name: "Approve", Config: map[string]any{"message": "Apply policy " + pol.Name + "?"}})
		d.Nodes = append(d.Nodes, node)
		d.Edges = []flowdef.Edge{{From: flowdef.TriggerID, To: "approve"}, {From: "approve", To: "policy", Port: "approved"}}
		return d
	}
	d.Nodes = append(d.Nodes, node)
	d.Edges = []flowdef.Edge{{From: flowdef.TriggerID, To: "policy"}}
	return d
}

// flowFromPolicy makes a published but turned-off flow for a policy, once.
func (s *Server) flowFromPolicy(ctx context.Context, pol appdb.Policy) error {
	flows, err := s.Store.ListAutomationFlows(ctx, pol.ClusterID)
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, f := range flows {
		if f.LegacyPolicyID == pol.ID {
			return nil
		}
		names[f.Name] = true
	}
	name := "Policy: " + pol.Name
	for i := 2; names[name]; i++ {
		name = fmt.Sprintf("Policy: %s (%d)", pol.Name, i)
	}
	d := policyFlowDefinition(pol)
	lim, _ := json.Marshal(flowdef.DefaultLimits())
	f := appdb.AutomationFlow{
		ID: uuid.NewString(), ClusterID: pol.ClusterID, Name: name, Description: "Made from the " + pol.Kind + " policy " + pol.Name + ".",
		Draft: d.Encode(), PublishedVersion: 1, Enabled: false, TriggerKind: d.TriggerKind(), Limits: lim,
		LegacyPolicyID: pol.ID, CreatedBy: automation.ActorName, DisabledReason: "made from a policy; turn it on to run it here",
	}
	if err := s.Store.CreateAutomationFlow(ctx, f); err != nil {
		return err
	}
	return s.Store.CreateAutomationFlowVersion(ctx, appdb.AutomationFlowVersion{
		FlowID: f.ID, ClusterID: f.ClusterID, Version: 1, Definition: d.Encode(), PublishedBy: automation.ActorName, Note: "made from policy " + pol.ID,
	})
}

// migrateLegacyPolicies gives every policy its equivalent flow. The
// policies API keeps working beside it.
func (s *Server) migrateLegacyPolicies(ctx context.Context, clusterID string) {
	pols, err := s.Store.ListPolicies(ctx, clusterID)
	if err != nil {
		return
	}
	for _, pol := range pols {
		if err := s.flowFromPolicy(ctx, pol); err != nil {
			log.Printf("automation: policy %s: %v", pol.Name, err)
		}
	}
}
