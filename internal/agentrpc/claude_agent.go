package agentrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/claudeagent"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

// Claude Agent: Claude Code in a system container, and host access to the
// game server folders granted to it.

func (h *Handler) claudeGuest(id string) claudeagent.Guest {
	return claudeagent.Guest{Run: func(ctx context.Context, argv ...string) ([]byte, error) {
		return h.Workloads.GuestCommand(ctx, id, argv...)
	}}
}

func (h *Handler) claudeHost() claudeagent.Host {
	return claudeagent.Host{DataDir: func(id string) string { return h.gameHost().RT.DataDir(id) }}
}

// claudeUIDs are the host uids of the container's claude user and of its
// root, which LXC mounts folders as.
func (h *Handler) claudeUIDs(id string) (int, int, error) {
	applied, err := h.Workloads.LastApplied(id)
	if err != nil {
		return 0, 0, fmt.Errorf("the agent container is not on this node")
	}
	if applied.Spec.Privileged {
		return 0, 0, errors.New("the agent container must be unprivileged")
	}
	root := lxc.MappedRootUID(applied.Spec)
	return root + claudeagent.GuestUID, root, nil
}

func (h *Handler) execClaudeAgent(ctx context.Context, m *agentv1.ClaudeAgent) (*connect.Response[agentv1.ExecuteResponse], error) {
	if h.SkipHostCmds || h.Workloads == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("system containers are not available on this node"))
	}
	var req claudeagent.Request
	if raw := m.GetRequestJson(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("request is not valid JSON: %w", err))
		}
	}
	if strings.TrimSpace(req.WorkloadID) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workload_id is required"))
	}
	g := h.claudeGuest(req.WorkloadID)
	var (
		result any
		err    error
	)
	switch action := strings.TrimSpace(m.GetAction()); action {
	case "setup":
		if err = g.Setup(ctx); err == nil {
			if err = g.Configure(ctx, req); err == nil {
				result = g.Status(ctx)
			}
		}
	case "configure":
		if err = g.Configure(ctx, req); err == nil {
			result = g.Status(ctx)
		}
	case "status", "":
		result = g.Status(ctx)
	case "login-start":
		var u string
		if u, err = g.LoginStart(ctx); err == nil {
			st := g.Status(ctx)
			st.LoginURL = u
			result = st
		}
	case "login-code":
		result, err = g.LoginCode(ctx, req.Code)
	case "logout":
		if err = g.Logout(ctx); err == nil {
			result = g.Status(ctx)
		}
	case "restart":
		if err = g.Restart(ctx); err == nil {
			result = g.Status(ctx)
		}
	case "grant":
		var uid, root int
		if uid, root, err = h.claudeUIDs(req.WorkloadID); err == nil {
			var mounts []claudeagent.MountSource
			mounts, err = h.claudeHost().Grant(ctx, uid, root, req.ACLs)
			result = claudeagent.GrantResult{Mounts: mounts}
		}
	case "revoke":
		var uid, root int
		if uid, root, err = h.claudeUIDs(req.WorkloadID); err == nil {
			err = h.claudeHost().Revoke(ctx, uid, root, req.ACLs)
			result = claudeagent.GrantResult{}
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("claude agent action %q is not supported", action))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: m.GetAction(), ResultJson: mustJSON(result)}), nil
}

func (h *Handler) execCtLeftovers(ctx context.Context, m *agentv1.CtLeftovers) (*connect.Response[agentv1.ExecuteResponse], error) {
	if h.SkipHostCmds || h.Workloads == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("system containers are not available on this node"))
	}
	switch action := strings.TrimSpace(m.GetAction()); action {
	case "list", "":
		items, err := h.Workloads.ListContainerState(ctx)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "list", ResultJson: mustJSON(items)}), nil
	case "size":
		items, err := h.Workloads.ListContainerState(ctx)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		want := map[string]bool{}
		for _, id := range m.GetIds() {
			want[id] = true
		}
		picked := items[:0]
		for _, it := range items {
			if want[it.ID] {
				picked = append(picked, it)
			}
		}
		lxc.SizeLeftovers(ctx, picked, 20*time.Second)
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "size", ResultJson: mustJSON(picked)}), nil
	case "remove":
		out := make([]lxc.LeftoverRemoved, 0, len(m.GetIds()))
		for _, id := range m.GetIds() {
			res, err := h.Workloads.RemoveLeftover(ctx, id)
			if err != nil {
				return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s: %w", id, err))
			}
			out = append(out, res)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "remove", ResultJson: mustJSON(out)}), nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("leftover action %q is not supported", action))
	}
}

// ClaudeAgent runs one Claude Agent action on the agent.
func (c Client) ClaudeAgent(ctx context.Context, action string, req claudeagent.Request, out any) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return c.executeJSON(ctx, &agentv1.ExecuteRequest{Method: &agentv1.ExecuteRequest_ClaudeAgent{ClaudeAgent: &agentv1.ClaudeAgent{Action: action, RequestJson: raw}}}, out)
}

// CtLeftovers lists, sizes or removes system container state on the agent.
func (c Client) CtLeftovers(ctx context.Context, action string, ids []string, out any) error {
	return c.executeJSON(ctx, &agentv1.ExecuteRequest{Method: &agentv1.ExecuteRequest_CtLeftovers{CtLeftovers: &agentv1.CtLeftovers{Action: action, Ids: ids}}}, out)
}

func (c Client) executeJSON(ctx context.Context, req *agentv1.ExecuteRequest, out any) error {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(req))
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			if ce.Code() == connect.CodeInvalidArgument && strings.Contains(ce.Message(), "unknown execute method") {
				return errors.New("this node's agent is too old for this; update No-dal on the node")
			}
			return errors.New(ce.Message())
		}
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(res.Msg.GetResultJson(), out)
}
