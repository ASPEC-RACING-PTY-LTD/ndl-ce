package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/cluster"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Worker updates. A worker installs No-dal from its own package repository;
// the control plane asks its agent to refresh the package lists and start
// the same platform update the control node runs (in its own systemd unit,
// so the agent restarting during the upgrade cannot interrupt it). Guests
// keep running.

const workerUpdateConfirm = "update-node"

// HostUpdater runs host package actions on a worker's agent.
type HostUpdater interface {
	HostUpdate(ctx context.Context, req hostos.UpdateRequest) (hostos.UpdateResult, error)
}

func (s *Server) workerUpdater(ctx context.Context, clusterID, nodeID string) (HostUpdater, error) {
	if s.WorkerUpdaters != nil {
		if u, ok := s.WorkerUpdaters[nodeID]; ok {
			return u, nil
		}
		return nil, errors.New("this node's agent is not connected")
	}
	remote, err := s.Store.GetRemoteNode(ctx, clusterID, nodeID)
	if err != nil || remote == nil || strings.TrimSpace(remote.ListenAddr) == "" {
		return nil, errors.New("this node has no agent address")
	}
	return s.workerAgent(nodeID, remote.ListenAddr), nil
}

// updateWorker refreshes a worker's package lists and starts its update.
func (s *Server) updateWorker(ctx context.Context, clusterID, nodeID string) (hostos.UpdateResult, error) {
	u, err := s.workerUpdater(ctx, clusterID, nodeID)
	if err != nil {
		return hostos.UpdateResult{}, err
	}
	if _, err := u.HostUpdate(ctx, hostos.UpdateRequest{Action: "check"}); err != nil {
		return hostos.UpdateResult{}, err
	}
	return u.HostUpdate(ctx, hostos.UpdateRequest{Action: "apply"})
}

func (s *Server) workerForUpdate(w http.ResponseWriter, r *http.Request, perm string) (*principal, string, bool) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return nil, "", false
	}
	node, err := s.Store.GetNodeByID(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || node == nil || node.RevokedAt != nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return nil, "", false
	}
	if node.Role != cluster.RoleWorker {
		writeErr(w, http.StatusUnprocessableEntity, "the control node is updated from Updates")
		return nil, "", false
	}
	return p, node.ID, true
}

// updateNode starts a worker's update.
func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.workerForUpdate(w, r, rbac.NodeUpdate)
	if !ok {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != workerUpdateConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "updating a node requires X-Nodal-Confirm: update-node")
		return
	}
	res, err := s.updateWorker(r.Context(), p.User.ClusterID, id)
	if err != nil {
		s.audit(r, p.User.ClusterID, p.User.ID, "node.update", "failed", id)
		writeErr(w, http.StatusBadGateway, linkError(err))
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "node.update", "ok", id)
	writeJSON(w, http.StatusOK, res)
}

// nodeUpdateStatus reports a worker's packages and its last update.
func (s *Server) nodeUpdateStatus(w http.ResponseWriter, r *http.Request) {
	p, id, ok := s.workerForUpdate(w, r, rbac.NodeRead)
	if !ok {
		return
	}
	u, err := s.workerUpdater(r.Context(), p.User.ClusterID, id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	res, err := u.HostUpdate(r.Context(), hostos.UpdateRequest{Action: "apply-status"})
	if err != nil {
		writeErr(w, http.StatusBadGateway, linkError(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}
