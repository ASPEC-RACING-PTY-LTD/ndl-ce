package httpapi

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// Leftover system containers: state a node still has (an enabled unit, LXC
// config, last-applied state, a root filesystem) for containers the control
// plane no longer has. They fail on every boot and hold disk space. Only the
// control plane can tell them apart from live containers, so the node lists
// what it has and this file filters by the workload table.

const removeLeftoversConfirm = "remove-leftovers"

// leftoverMinAge keeps a container that is being created right now (state
// written, workload row not yet visible) out of the list.
var leftoverMinAge = 10 * time.Minute

type ctLeftoversRPC interface {
	CtLeftovers(ctx context.Context, action string, ids []string, out any) error
}

func (s *Server) leftoversRPC(ctx context.Context, clusterID, nodeID string) (ctLeftoversRPC, bool) {
	rpc, ok := s.workloadsOn(ctx, clusterID, nodeID)
	if !ok || rpc == nil {
		return nil, false
	}
	lr, ok := rpc.(ctLeftoversRPC)
	return lr, ok
}

// leftovers lists the node's container state no workload owns. recent also
// lists state changed in the last few minutes, for a caller that knows the
// containers are its own (the certification harness cleaning up).
func (s *Server) leftovers(ctx context.Context, clusterID, nodeID string, sized, recent bool) ([]lxc.Leftover, error) {
	rpc, ok := s.leftoversRPC(ctx, clusterID, nodeID)
	if !ok {
		return nil, errUnavailable("this node's agent cannot list container state; update No-dal on the node")
	}
	var all []lxc.Leftover
	if err := rpc.CtLeftovers(ctx, "list", nil, &all); err != nil {
		return nil, errUnavailable(err.Error())
	}
	rows, err := s.Store.ListWorkloads(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, w := range rows {
		known[w.ID] = true
	}
	cutoff := s.now().Add(-leftoverMinAge)
	var out []lxc.Leftover
	var ids []string
	for _, l := range all {
		if known[l.ID] || (!recent && l.Modified.After(cutoff)) {
			continue
		}
		out = append(out, l)
		ids = append(ids, l.ID)
	}
	if sized && len(ids) > 0 {
		var sizes []lxc.Leftover
		if err := rpc.CtLeftovers(ctx, "size", ids, &sizes); err == nil {
			by := map[string]int64{}
			for _, l := range sizes {
				by[l.ID] = l.RootfsBytes
			}
			for i := range out {
				out[i].RootfsBytes = by[out[i].ID]
			}
		}
	}
	return out, nil
}

func (s *Server) listCTLeftovers(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	nodeID := strings.TrimSpace(r.URL.Query().Get("node_id"))
	items, err := s.leftovers(r.Context(), p.User.ClusterID, nodeID, r.URL.Query().Get("sizes") != "0", r.URL.Query().Get("recent") == "1")
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if items == nil {
		items = []lxc.Leftover{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) removeCTLeftovers(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeDelete)
	if err != nil {
		return
	}
	var req struct {
		IDs    []string `json:"ids"`
		NodeID string   `json:"node_id"`
		Recent bool     `json:"recent"`
	}
	if err := readJSON(r, &req); err != nil || len(req.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "ids are required")
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != removeLeftoversConfirm {
		writeErr(w, http.StatusPreconditionRequired, "removing leftover containers deletes their files; send X-Nodal-Confirm: "+removeLeftoversConfirm)
		return
	}
	ctx := r.Context()
	clusterID := p.User.ClusterID
	current, err := s.leftovers(ctx, clusterID, req.NodeID, false, req.Recent)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	isLeftover := map[string]bool{}
	for _, l := range current {
		isLeftover[l.ID] = true
	}
	ids := uniqueWorkloadIDs(req.IDs)
	for _, id := range ids {
		if !isLeftover[id] {
			writeErr(w, http.StatusConflict, id+" is not a leftover container (it belongs to a workload, is gone, or was changed in the last 10 minutes)")
			return
		}
	}
	rpc, _ := s.leftoversRPC(ctx, clusterID, req.NodeID)
	var removed []lxc.LeftoverRemoved
	if err := rpc.CtLeftovers(ctx, "remove", ids, &removed); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	disks, _ := s.Store.ListWorkloadDisks(ctx, clusterID, "")
	attached := map[string]bool{}
	for _, d := range disks {
		attached[d.VolumeID] = true
	}
	results := make([]map[string]any, 0, len(removed))
	for _, res := range removed {
		item := map[string]any{"id": res.ID, "removed_data": res.RemovedData}
		// A root filesystem with a volume row goes through storage, which
		// also unmounts a pool image; one without a row was removed (or
		// kept) by the node.
		if res.Rootfs != "" && filepath.Base(filepath.Dir(res.Rootfs)) == "container-root" {
			if vol, _ := s.Store.GetVolume(ctx, clusterID, filepath.Base(res.Rootfs)); vol != nil && vol.Class == storage.ClassContainerRoot && !attached[vol.ID] {
				if res.RemovedData {
					_ = s.Store.DeleteVolume(ctx, clusterID, vol.ID)
				} else {
					s.destroyOwnedVolume(ctx, clusterID, vol)
					res.RootfsKept, res.KeptReason = false, ""
					item["removed_data"] = true
				}
				item["volume_id"] = vol.ID
			}
		}
		if res.RootfsKept {
			item["kept"] = res.Rootfs
			item["kept_reason"] = res.KeptReason
		}
		results = append(results, item)
		s.audit(r, clusterID, p.User.ID, "workload.leftover.remove", "ok", res.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
