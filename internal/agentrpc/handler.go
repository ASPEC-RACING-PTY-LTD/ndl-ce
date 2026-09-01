package agentrpc

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/identity"
	"github.com/no-dal/ndl-ce/internal/peercred"
)

const version = "0.1.0"

// Handler is the typed agent service.
type Handler struct {
	Ident      identity.Files
	AllowedUID uint32
	Lookup     func() (hostos.Platform, error)
	Peer       func(ctx context.Context) (peercred.Creds, error)
}

var _ agentv1connect.AgentServiceHandler = (*Handler)(nil)

// Hello reports version and host platform.
func (h *Handler) Hello(ctx context.Context, _ *connect.Request[agentv1.HelloRequest]) (*connect.Response[agentv1.HelloResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	p, err := h.platform()
	if err != nil && !errors.As(err, &hostos.Error{}) {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentv1.HelloResponse{
		AgentVersion: version,
		HostPlatform: protoPlatform(p),
	}), nil
}

// Observe is empty in Phase 1.
func (h *Handler) Observe(ctx context.Context, _ *connect.Request[agentv1.ObserveRequest]) (*connect.Response[agentv1.ObserveResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	return connect.NewResponse(&agentv1.ObserveResponse{}), nil
}

// Execute handles typed Ping only.
func (h *Handler) Execute(ctx context.Context, req *connect.Request[agentv1.ExecuteRequest]) (*connect.Response[agentv1.ExecuteResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	if req.Msg.GetPing() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown execute method"))
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "pong"}), nil
}

// Enroll writes durable node identity. Unsupported hosts fail closed.
func (h *Handler) Enroll(ctx context.Context, req *connect.Request[agentv1.EnrollRequest]) (*connect.Response[agentv1.EnrollResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	p, err := h.platform()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	clusterID := req.Msg.GetClusterId()
	if clusterID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("cluster_id is required"))
	}
	nodeID := uuid.NewString()
	if existing, existingCluster, err := h.Ident.LoadNode(); err == nil {
		nodeID = existing
		if existingCluster != "" && existingCluster != clusterID {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("node already enrolled in another cluster"))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := h.Ident.SaveCluster(clusterID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := h.Ident.SaveNode(nodeID, clusterID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentv1.EnrollResponse{
		ClusterId:    clusterID,
		NodeId:       nodeID,
		HostPlatform: protoPlatform(p),
	}), nil
}

// OpenSession is reserved.
func (h *Handler) OpenSession(context.Context, *connect.Request[agentv1.OpenSessionRequest]) (*connect.Response[agentv1.OpenSessionResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("OpenSession is reserved"))
}

func (h *Handler) authorize(ctx context.Context) error {
	if h.Peer == nil {
		if h.AllowedUID == 0 {
			return nil
		}
		return connect.NewError(connect.CodePermissionDenied, errors.New("peer credentials required"))
	}
	c, err := h.Peer(ctx)
	if err != nil {
		return connect.NewError(connect.CodePermissionDenied, err)
	}
	if c.UID != h.AllowedUID {
		return connect.NewError(connect.CodePermissionDenied, errors.New("unauthorized peer"))
	}
	return nil
}

func (h *Handler) platform() (hostos.Platform, error) {
	if h.Lookup != nil {
		return h.Lookup()
	}
	return hostos.Detect()
}

func protoPlatform(p hostos.Platform) *agentv1.HostPlatform {
	return &agentv1.HostPlatform{
		Id:           p.ID,
		VersionId:    p.VersionID,
		Family:       p.Family,
		Architecture: p.Architecture,
		SupportTier:  p.SupportTier,
		Capabilities: p.Capabilities,
	}
}

// HostPlatformJSON encodes the proto platform for Postgres.
func HostPlatformJSON(p *agentv1.HostPlatform) json.RawMessage {
	b, _ := json.Marshal(p)
	return b
}
