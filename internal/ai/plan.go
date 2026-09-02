package ai

import (
	"fmt"
	"strings"
)

const (
	ActionCreateWorkload = "create_workload"
	ActionRestart        = "restart_workload"
	ActionInstallStore   = "install_store_app"
	ActionCreatePolicy   = "create_policy"
)

// PlanStep is one existing API call. It is never a shell.
type PlanStep struct {
	Ordinal    int
	Action     string
	Permission string
	Method     string
	Path       string
	Body       map[string]any
	Title      string
}

var planBanned = []string{"host.exec", "host_exec", "/bin/sh", "/bin/bash", "rm -rf"}

// CompilePlan turns a prompt into typed existing-API steps. Unknown destructive exec fails closed.
func CompilePlan(prompt, nodeID, nodeName, storeAppID string) ([]PlanStep, error) {
	q := strings.ToLower(prompt)
	for _, bad := range planBanned {
		if strings.Contains(q, bad) {
			return nil, fmt.Errorf("plan cannot include %s", strings.TrimSpace(bad))
		}
	}
	for _, key := range []string{"exec", "shell", "bash", "argv"} {
		if strings.Contains(q, key+" ") || strings.HasSuffix(q, key) {
			if strings.Contains(q, "host") || strings.Contains(q, "shell") || strings.Contains(q, "bash") {
				return nil, fmt.Errorf("plan cannot include %s", key)
			}
		}
	}
	var steps []PlanStep
	if strings.Contains(q, "85") && (strings.Contains(q, "storage") || strings.Contains(q, "pool") || strings.Contains(q, "migrate") || strings.Contains(q, "move")) {
		steps = append(steps, PlanStep{
			Action: ActionCreatePolicy, Permission: "policy.apply", Method: "POST", Path: "/api/v1/policies",
			Title: "Bind a storage-pressure policy to the Phase 40 engine",
			Body: map[string]any{
				"name": "storage pressure", "kind": "storage_pressure",
				"action": "enqueue_migrate_low_priority", "threshold_percent": 85, "require_approval": true,
			},
		})
	}
	if (strings.Contains(q, "install") && (strings.Contains(q, "database") || strings.Contains(q, "postgres") || strings.Contains(q, "app"))) || strings.Contains(q, "create workload") {
		name := "database"
		if strings.Contains(q, "postgres") {
			name = "postgres"
		}
		body := map[string]any{"name": name, "kind": "oci"}
		if nodeID != "" {
			body["node_id"] = nodeID
		}
		if nodeName != "" {
			body["node_name"] = nodeName
		}
		path := "/api/v1/workloads"
		action := ActionCreateWorkload
		perm := "compute.create"
		if storeAppID != "" && !strings.Contains(q, "database") {
			action = ActionInstallStore
			perm = "store.install"
			path = "/api/v1/store/apps/" + storeAppID + "/install"
			body = map[string]any{"name": name, "node_id": nodeID}
		}
		steps = append(steps, PlanStep{
			Action: action, Permission: perm, Method: "POST", Path: path,
			Title: "Install " + name + " using the existing compute API", Body: body,
		})
	}
	if strings.Contains(q, "restart") {
		steps = append(steps, PlanStep{
			Action: ActionRestart, Permission: "compute.lifecycle", Method: "POST", Path: "/api/v1/workloads/{id}/restart",
			Title: "Restart via compute restart (Store ai_actions declaration only)",
			Body:  map[string]any{"declaration": "Calls compute restart. Not a shell."},
		})
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("prompt did not match a known existing API plan")
	}
	for i := range steps {
		steps[i].Ordinal = i + 1
	}
	return steps, nil
}
