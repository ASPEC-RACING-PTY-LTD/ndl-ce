package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/deploy"
)

// deployGuest runs deployment commands through the LXC engine.
type deployGuest struct{ h *Handler }

func (g deployGuest) Run(ctx context.Context, id string, argv ...string) ([]byte, error) {
	if g.h.Workloads == nil {
		return nil, fmt.Errorf("lxc engine is unavailable")
	}
	return g.h.Workloads.GuestCommand(ctx, id, argv...)
}

func (g deployGuest) Stream(ctx context.Context, id string, stdin io.Reader, out io.Writer, argv ...string) error {
	if g.h.Workloads == nil {
		return fmt.Errorf("lxc engine is unavailable")
	}
	return g.h.Workloads.GuestStream(ctx, id, stdin, out, argv...)
}

func (h *Handler) deployments() *deploy.Manager {
	h.deployOnce.Do(func() {
		if h.Deploy == nil {
			h.Deploy = &deploy.Manager{Guest: deployGuest{h: h}}
		}
	})
	return h.Deploy
}

// DeployRequest is what the control plane sends for a deployment action.
type DeployRequest struct {
	Spec      deploy.Spec       `json:"spec,omitempty"`
	LogDir    string            `json:"log_dir,omitempty"`
	ID        string            `json:"id,omitempty"`
	IDs       []string          `json:"ids,omitempty"`
	Offset    int64             `json:"offset,omitempty"`
	Max       int               `json:"max,omitempty"`
	MachineID string            `json:"machine_id,omitempty"`
	Dir       string            `json:"dir,omitempty"`
	RunAs     string            `json:"run_as,omitempty"`
	Script    string            `json:"script,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Timeout   int               `json:"timeout,omitempty"`
}

// DeployLog is a slice of a deployment log.
type DeployLog struct {
	Data   []byte `json:"data"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
}

func isDeployAction(a string) bool {
	switch a {
	case "deploy-start", "deploy-status", "deploy-log", "deploy-cancel", "deploy-delete", "deploy-probe", "deploy-running":
		return true
	}
	return false
}

func (h *Handler) execDeploy(ctx context.Context, action string, raw []byte) (*connect.Response[agentv1.ExecuteResponse], error) {
	var req DeployRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("deployment request is invalid"))
	}
	m := h.deployments()
	var (
		out any
		err error
	)
	switch action {
	case "deploy-start":
		out, err = m.Start(req.Spec)
	case "deploy-status":
		out, err = m.Status(req.LogDir, req.ID)
	case "deploy-cancel":
		out, err = m.Cancel(req.LogDir, req.ID)
	case "deploy-log":
		var data []byte
		var size int64
		data, size, err = m.Log(req.LogDir, req.ID, req.Offset, req.Max)
		out = DeployLog{Data: data, Offset: req.Offset, Size: size}
	case "deploy-delete":
		var n int
		n, err = m.Delete(req.LogDir, req.IDs)
		out = map[string]int{"deleted": n}
	case "deploy-running":
		out = map[string][]string{"ids": m.Running()}
	case "deploy-probe":
		out, err = m.Probe(ctx, req.MachineID, req.Dir, req.RunAs, req.Script, req.Env, req.Timeout)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(out)}), nil
}
