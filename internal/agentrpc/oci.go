package agentrpc

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/oci"
)

func (h *Handler) oci() *oci.Engine {
	if h.OCI != nil {
		return h.OCI
	}
	return &oci.Engine{SkipHostCmds: h.SkipHostCmds}
}

func (h *Handler) execOCIRuntime(ctx context.Context, m *agentv1.OCIRuntime) (*connect.Response[agentv1.ExecuteResponse], error) {
	action := m.GetAction()
	switch action {
	case "create", "apply":
		var spec oci.Spec
		if len(m.GetSpecJson()) > 0 {
			if err := json.Unmarshal(m.GetSpecJson(), &spec); err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, err)
			}
		}
		if spec.WorkloadID == "" {
			spec.WorkloadID = m.GetWorkloadId()
		}
		res, err := h.oci().Create(ctx, spec)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "created", ResultJson: mustJSON(res)}), nil
	case "start", "stop", "restart", "delete":
		res, err := h.oci().Lifecycle(ctx, oci.LifecycleRequest{WorkloadID: m.GetWorkloadId(), Action: action})
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(res)}), nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errUnknownOCIAction(action))
	}
}

func errUnknownOCIAction(action string) error {
	return &ociActionError{action: action}
}

type ociActionError struct{ action string }

func (e *ociActionError) Error() string {
	return "unknown oci runtime action " + e.action
}
