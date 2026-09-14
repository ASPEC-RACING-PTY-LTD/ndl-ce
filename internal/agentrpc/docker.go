package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/docker"
)

func (h *Handler) docker() *docker.Engine {
	if h.Docker != nil {
		return h.Docker
	}
	h.dockerOnce.Do(func() {
		if h.Docker != nil {
			return
		}
		h.Docker = &docker.Engine{
			SkipHostCmds: h.SkipHostCmds,
			LXCInfo: func(ctx context.Context, id string) (int, string, error) {
				if h.Workloads == nil {
					return 0, "", fmt.Errorf("lxc engine is unavailable")
				}
				return h.Workloads.InfoPID(ctx, id)
			},
		}
	})
	return h.Docker
}

func startDockerExec(ctx context.Context, h *Handler, req termRequest) (termSession, error) {
	machine, container := docker.ParseRef(req.TargetID)
	if machine == "" || container == "" {
		return nil, fmt.Errorf("docker target_id must be machine/container")
	}
	sess, err := h.docker().Exec(ctx, docker.ExecRequest{MachineID: machine, ContainerID: container})
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (h *Handler) execDockerMgmt(ctx context.Context, m *agentv1.DockerMgmt) (*connect.Response[agentv1.ExecuteResponse], error) {
	action := strings.TrimSpace(m.GetAction())
	var spec struct {
		Hints []docker.MachineHint `json:"hints"`
		Tail  int                  `json:"tail"`
	}
	if len(m.GetSpecJson()) > 0 {
		_ = json.Unmarshal(m.GetSpecJson(), &spec)
	}
	switch action {
	case "snapshot":
		inv, err := h.docker().Snapshot(ctx, spec.Hints)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "snapshot", ResultJson: mustJSON(inv)}), nil
	case "idle":
		h.docker().Idle()
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "idle"}), nil
	case "start", "stop", "restart", "pull", "recreate", "update", "logs":
		res, err := h.docker().Action(ctx, docker.ActionRequest{
			Action: action, MachineID: m.GetMachineId(), ContainerID: m.GetContainerId(), Tail: spec.Tail,
		}, spec.Hints)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: res.Message, ResultJson: mustJSON(res)}), nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown docker action %s", action))
	}
}
