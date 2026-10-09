package httpapi

import (
	"errors"
	"net/http"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// deleteNode removes a node record that is not the host No-dal runs on,
// for example a test node that was enrolled once and never came back. The
// host itself can never be deleted. Nodes that still have workloads are
// refused until those are deleted or moved.
func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRevoke)
	if err != nil {
		return
	}
	if !s.requireWriter(w, r, p.User.ClusterID) {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	node, err := s.Store.GetNodeByID(ctx, p.User.ClusterID, id)
	if err != nil || node == nil {
		// A remote worker that joined over WireGuard is listed with the
		// hosts too.
		if remote, rerr := s.Store.GetRemoteNode(ctx, p.User.ClusterID, id); rerr == nil && remote != nil {
			if err := s.Store.DeleteRemoteNode(ctx, p.User.ClusterID, id); err != nil {
				writeErr(w, http.StatusInternalServerError, "could not delete the node: "+err.Error())
				return
			}
			s.audit(r, p.User.ClusterID, p.User.ID, "node.delete", "ok", id+" "+remote.Name)
			writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
			return
		}
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	if local, err := s.Store.GetNode(ctx, p.User.ClusterID); err != nil || local == nil || local.ID == id {
		writeErr(w, http.StatusConflict, "this is the host No-dal runs on; it cannot be deleted")
		return
	}
	if err := s.Store.DeleteNode(ctx, p.User.ClusterID, id); err != nil {
		var busy appdb.ErrNodeHasWorkloads
		switch {
		case errors.As(err, &busy):
			writeErr(w, http.StatusConflict, busy.Error())
		case errors.Is(err, appdb.ErrNodeNotFound):
			writeErr(w, http.StatusNotFound, "node not found")
		default:
			writeErr(w, http.StatusInternalServerError, "could not delete the node: "+err.Error())
		}
		return
	}
	// Its certificate, if it ever had one, must not work again.
	_ = s.ClusterCA.RevokeNode(id)
	s.audit(r, p.User.ClusterID, p.User.ID, "node.delete", "ok", id+" "+node.Name)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}
