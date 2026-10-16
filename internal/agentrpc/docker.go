package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
			GuestRun: func(ctx context.Context, id string, argv ...string) ([]byte, error) {
				if h.Workloads == nil {
					return nil, fmt.Errorf("lxc engine is unavailable")
				}
				return h.Workloads.GuestCommand(ctx, id, argv...)
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
		Hints   []docker.MachineHint `json:"hints"`
		Tail    int                  `json:"tail"`
		Unit    string               `json:"unit"`
		Command []string             `json:"command"`
		Timeout int                  `json:"timeout"`
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
	case "tunnels":
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "tunnels", ResultJson: mustJSON(h.docker().Tunnels(ctx, spec.Hints))}), nil
	case "connector":
		var req docker.ConnectorRequest
		if err := json.Unmarshal(m.GetSpecJson(), &req); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("connector request is invalid"))
		}
		req.MachineID = m.GetMachineId()
		cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		res, err := h.docker().Connector(cctx, req)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "connector", ResultJson: mustJSON(res)}), nil
	case "proxy":
		var req docker.ProxyRequest
		if err := json.Unmarshal(m.GetSpecJson(), &req); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("proxy request is invalid"))
		}
		if m.GetMachineId() != "" {
			req.MachineID = m.GetMachineId()
		}
		limit := 2 * time.Minute
		if req.Action == docker.ProxyInstall {
			limit = 10 * time.Minute
		}
		pctx, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		res, err := h.docker().Proxy(pctx, req)
		if err != nil {
			// The reply carries the validator output when there is some.
			return connect.NewResponse(&agentv1.ExecuteResponse{Ok: false, Message: err.Error(), ResultJson: mustJSON(res)}), nil
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "proxy", ResultJson: mustJSON(res)}), nil
	case "tunnel-restart", "tunnel-start":
		if err := h.docker().TunnelUnitAction(ctx, m.GetMachineId(), spec.Unit, strings.TrimPrefix(action, "tunnel-")); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action}), nil
	case "exec-run":
		timeout := time.Duration(spec.Timeout) * time.Second
		if timeout <= 0 || timeout > 10*time.Minute {
			timeout = time.Minute
		}
		ectx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		out, code, err := h.docker().ExecRun(ectx, m.GetMachineId(), m.GetContainerId(), spec.Command)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: code == 0, Message: "exec", ResultJson: mustJSON(docker.ExecResult{Output: out, ExitCode: code})}), nil
	case "idle":
		h.docker().Idle()
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "idle"}), nil
	case "start", "stop", "restart", "pull", "recreate", "update", "logs", "remove":
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
