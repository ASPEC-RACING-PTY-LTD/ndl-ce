package agentrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/edge"
)

// EdgeEngine is edge forwarding on this node. Its spec and tunnel keys live
// under the agent data dir so tunnels come back at start without the
// control plane.
func EdgeEngine(dataDir string) *edge.Engine {
	return &edge.Engine{StateDir: filepath.Join(dataDir, "net", "edge")}
}

func (h *Handler) edge() *edge.Engine {
	h.edgeOnce.Do(func() {
		if h.Edge == nil {
			h.Edge = EdgeEngine(h.dataDir())
		}
	})
	return h.Edge
}

func (h *Handler) execEdge(ctx context.Context, m *agentv1.Edge) (*connect.Response[agentv1.ExecuteResponse], error) {
	if h.SkipHostCmds {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("host commands are skipped; edge forwarding is not available"))
	}
	eng := h.edge()
	var (
		st  edge.Status
		err error
	)
	switch action := strings.TrimSpace(m.GetAction()); action {
	case "apply":
		var spec edge.Spec
		if err := json.Unmarshal(m.GetSpecJson(), &spec); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("spec is not valid JSON: %w", err))
		}
		st, err = eng.Apply(ctx, spec)
	case "ensure":
		if err = eng.Ensure(ctx); err == nil {
			st, err = eng.Status(ctx)
		}
	case "status", "":
		st, err = eng.Status(ctx)
	case "disable":
		if err = eng.Disable(ctx); err == nil {
			st, err = eng.Status(ctx)
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("edge action %q is not supported", action))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: m.GetAction(), ResultJson: mustJSON(st)}), nil
}

// Edge runs one edge forwarding action on the agent.
func (c Client) Edge(ctx context.Context, action string, spec *edge.Spec) (edge.Status, error) {
	msg := &agentv1.Edge{Action: action}
	if spec != nil {
		raw, err := json.Marshal(spec)
		if err != nil {
			return edge.Status{}, err
		}
		msg.SpecJson = raw
	}
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_Edge{Edge: msg},
	}))
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return edge.Status{}, errors.New(ce.Message())
		}
		return edge.Status{}, err
	}
	var out edge.Status
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return edge.Status{}, err
	}
	return out, nil
}
