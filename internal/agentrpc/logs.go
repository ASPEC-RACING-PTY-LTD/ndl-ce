package agentrpc

import (
	"context"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/journald"
)

// GetLogs returns typed journalctl output. Units are allowlisted.
func (h *Handler) GetLogs(ctx context.Context, req *connect.Request[agentv1.GetLogsRequest]) (*connect.Response[agentv1.GetLogsResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	q := journald.Query{Unit: req.Msg.GetUnit(), Lines: int(req.Msg.GetLines())}
	if s := req.Msg.GetSince(); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			q.Since = t
		}
	}
	eng := h.journal()
	res, err := eng.Read(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&agentv1.GetLogsResponse{
		Status:  res.Status,
		Unit:    res.Unit,
		Lines:   res.Lines,
		Message: res.Message,
	}), nil
}

func (h *Handler) journal() *journald.Engine {
	if h.Journal != nil {
		return h.Journal
	}
	return &journald.Engine{SkipHostCmds: h.SkipHostCmds}
}
