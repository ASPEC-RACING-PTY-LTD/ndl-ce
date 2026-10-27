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
	"github.com/no-dal/ndl-ce/internal/shield"
)

// ShieldEngine is DDoS protection on this node. Its state lives under the
// agent data dir so it is restored at start without the control plane.
func ShieldEngine(dataDir string) *shield.Engine {
	return &shield.Engine{StateDir: filepath.Join(dataDir, "net", "shield")}
}

func (h *Handler) shield() *shield.Engine {
	h.shieldOnce.Do(func() {
		if h.Shield == nil {
			h.Shield = ShieldEngine(h.dataDir())
		}
	})
	return h.Shield
}

func (h *Handler) execShield(ctx context.Context, m *agentv1.Shield) (*connect.Response[agentv1.ExecuteResponse], error) {
	if h.SkipHostCmds {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("host commands are skipped; DDoS protection is not available"))
	}
	eng := h.shield()
	var (
		st  shield.Status
		err error
	)
	switch action := strings.TrimSpace(m.GetAction()); action {
	case "apply":
		var spec shield.Spec
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
	case "ban":
		if err = eng.Ban(ctx, m.GetAddr(), m.GetUntil()); err == nil {
			st, err = eng.Status(ctx)
		}
	case "unban":
		if err = eng.Unban(ctx, m.GetAddr()); err == nil {
			st, err = eng.Status(ctx)
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("shield action %q is not supported", action))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: m.GetAction(), ResultJson: mustJSON(st)}), nil
}

// Shield runs one DDoS protection action on the agent.
func (c Client) Shield(ctx context.Context, action string, spec *shield.Spec, addr string, until int64) (shield.Status, error) {
	msg := &agentv1.Shield{Action: action, Addr: addr, Until: until}
	if spec != nil {
		raw, err := json.Marshal(spec)
		if err != nil {
			return shield.Status{}, err
		}
		msg.SpecJson = raw
	}
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_Shield{Shield: msg},
	}))
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return shield.Status{}, errors.New(ce.Message())
		}
		return shield.Status{}, err
	}
	var out shield.Status
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return shield.Status{}, err
	}
	return out, nil
}
