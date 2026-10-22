package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/cluster"
)

type ioCtxKey struct{}

// ioFor is the agent that serves terminals and files for this request:
// a worker node's agent when the request was routed to one, else the
// control node's own agent.
func (s *Server) ioFor(ctx context.Context) IORPC {
	if rpc, ok := ctx.Value(ioCtxKey{}).(IORPC); ok && rpc != nil {
		return rpc
	}
	return s.IO
}

func withIO(ctx context.Context, rpc IORPC) context.Context {
	return context.WithValue(ctx, ioCtxKey{}, rpc)
}

// ioNode resolves a node for a host terminal or host files: the control
// node, or a joined worker whose agent this control node can dial.
func (s *Server) ioNode(ctx context.Context, clusterID, id string) (*appdb.Node, IORPC, error) {
	if node, err := s.localNode(ctx, clusterID, id); err == nil {
		return node, s.IO, nil
	}
	node, err := s.Store.GetNodeByID(ctx, clusterID, id)
	if err != nil || node == nil || node.RevokedAt != nil {
		return nil, nil, errNotFound("node not found")
	}
	rpc, err := s.ioOnNode(ctx, *node)
	if err != nil {
		return nil, nil, err
	}
	return node, rpc, nil
}

// ioOnNode is the IO agent of a worker node, over the mutual TLS link.
func (s *Server) ioOnNode(ctx context.Context, node appdb.Node) (IORPC, error) {
	if node.Role != cluster.RoleWorker {
		return s.IO, nil
	}
	remote, _ := s.Store.GetRemoteNode(ctx, node.ClusterID, node.ID)
	if remote == nil || strings.TrimSpace(remote.ListenAddr) == "" {
		return nil, statusError{status: http.StatusBadGateway, msg: "this node has no agent address: set it with Address on the Cluster page"}
	}
	return s.workerAgent(node.ID, remote.ListenAddr), nil
}

// ioForWorkload routes a workload's terminal or files to the node that runs it.
func (s *Server) ioForWorkload(ctx context.Context, wl appdb.Workload) (IORPC, error) {
	nodeID := s.ownerNodeID(wl)
	if nodeID == "" {
		return s.IO, nil
	}
	if _, err := s.localNode(ctx, wl.ClusterID, nodeID); err == nil {
		return s.IO, nil
	}
	node, err := s.Store.GetNodeByID(ctx, wl.ClusterID, nodeID)
	if err != nil || node == nil {
		return s.IO, nil
	}
	return s.ioOnNode(ctx, *node)
}

// ioForSession picks the agent for a terminal session from its target.
func (s *Server) ioForSession(ctx context.Context, clusterID string, row appdb.IOSession) (IORPC, error) {
	switch row.TargetKind {
	case appdb.IOTargetHost:
		_, rpc, err := s.ioNode(ctx, clusterID, row.TargetID)
		return rpc, err
	case appdb.IOTargetDocker:
		return s.IO, nil
	}
	wl, err := s.Store.GetWorkload(ctx, clusterID, row.TargetID)
	if err != nil || wl == nil {
		return s.IO, nil
	}
	return s.ioForWorkload(ctx, *wl)
}
