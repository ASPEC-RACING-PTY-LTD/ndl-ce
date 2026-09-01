package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/placement"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const remoteApplyReason = "placement recorded; remote apply is not wired; migrate dest agent is required"

func (s *Server) placeCreate(ctx context.Context, clusterID string, req createWorkloadRequest) (*appdb.Node, bool, error) {
	nodes, err := s.Store.ListClusterNodes(ctx, clusterID)
	if err != nil {
		return nil, false, err
	}
	maint, _ := s.Store.ListNodeMaintenance(ctx, clusterID)
	maintSet := map[string]bool{}
	for _, m := range maint {
		maintSet[m.NodeID] = true
	}
	pools, _ := s.Store.ListStoragePools(ctx, clusterID)
	groups, _ := s.Store.ListNodeGroups(ctx, clusterID)
	members := map[string][]string{}
	for _, g := range groups {
		ids, _ := s.Store.ListNodeGroupMembers(ctx, clusterID, g.ID)
		members[g.ID] = ids
	}
	affinityNode := ""
	antiNode := ""
	if req.AffinityWorkloadID != "" {
		if w, _ := s.Store.GetWorkload(ctx, clusterID, req.AffinityWorkloadID); w != nil {
			affinityNode = w.DesiredNodeID
			if affinityNode == "" {
				affinityNode = w.NodeID
			}
		}
	}
	if req.AntiAffinityWorkloadID != "" {
		if w, _ := s.Store.GetWorkload(ctx, clusterID, req.AntiAffinityWorkloadID); w != nil {
			antiNode = w.DesiredNodeID
			if antiNode == "" {
				antiNode = w.NodeID
			}
		}
	}
	var cands []placement.Candidate
	for _, n := range nodes {
		invRow, _ := s.Store.GetInventory(ctx, n.ID)
		parsed, ok := decodeInv(invRow)
		c := placement.Candidate{Node: n, Maintaining: maintSet[n.ID]}
		if ok {
			cp := parsed
			c.Inventory = &cp
		}
		cands = append(cands, c)
	}
	res, err := placement.Place(placement.Request{
		Mode:                firstNonEmpty(req.Placement, placement.ModeAutomatic),
		NodeID:              req.NodeID,
		GroupID:             req.NodeGroupID,
		CPUs:                req.CPUs,
		MemoryBytes:         req.MemoryBytes,
		RequireGPU:          req.RequireGPU,
		RequireStorageClass: req.RequireStorageClass,
		AffinityNodeID:      affinityNode,
		AntiAffinityNodeID:  antiNode,
		GroupMembers:        members,
		Pools:               pools,
	}, cands)
	if err != nil {
		return nil, false, errUnprocessable(err.Error())
	}
	target, err := s.Store.GetNodeByID(ctx, clusterID, res.NodeID)
	if err != nil || target == nil {
		return nil, false, errFailedDependency("placed node is not found")
	}
	return target, s.applyLocal(ctx, clusterID, target.ID), nil
}

func (s *Server) applyLocal(ctx context.Context, clusterID, nodeID string) bool {
	control, err := s.Store.GetNode(ctx, clusterID)
	if err != nil || control == nil {
		return false
	}
	return control.ID == nodeID
}

func (s *Server) guardLocalApply(w http.ResponseWriter, r *http.Request, clusterID, nodeID, action string) bool {
	if action != "start" && action != "clone" && action != "restart" {
		return true
	}
	if maint, _ := s.Store.GetNodeMaintenance(r.Context(), clusterID, nodeID); maint != nil {
		writeErr(w, http.StatusConflict, "node is in maintenance; drain with migrate first")
		return false
	}
	if s.applyLocal(r.Context(), clusterID, nodeID) {
		return true
	}
	writeErr(w, http.StatusConflict, "placement would start a copy on the wrong node; remote apply is not wired")
	return false
}

func (s *Server) recordPlacement(ctx context.Context, clusterID, workloadID string, req createWorkloadRequest) {
	mode := firstNonEmpty(req.Placement, placement.ModeAutomatic)
	_ = s.Store.UpsertWorkloadPlacement(ctx, appdb.WorkloadPlacement{
		WorkloadID:             workloadID,
		ClusterID:              clusterID,
		Mode:                   mode,
		NodeGroupID:            req.NodeGroupID,
		RequireGPU:             req.RequireGPU,
		RequireStorageClass:    req.RequireStorageClass,
		AffinityWorkloadID:     req.AffinityWorkloadID,
		AntiAffinityWorkloadID: req.AntiAffinityWorkloadID,
	})
}

func (s *Server) previewPlacement(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	var req createWorkloadRequest
	_ = readJSON(r, &req)
	node, local, err := s.placeCreate(r.Context(), p.User.ClusterID, req)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node_id": node.ID, "name": node.Name, "role": node.Role, "apply_local": local,
	})
}

func (s *Server) listNodeGroups(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	groups, err := s.Store.ListNodeGroups(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		members, _ := s.Store.ListNodeGroupMembers(r.Context(), p.User.ClusterID, g.ID)
		if members == nil {
			members = []string{}
		}
		items = append(items, map[string]any{"id": g.ID, "name": g.Name, "members": members})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createNodeGroup(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeUpdate)
	if err != nil {
		return
	}
	var req struct {
		Name    string   `json:"name"`
		NodeIDs []string `json:"node_ids"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	g := appdb.NodeGroup{ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name)}
	if err := s.Store.CreateNodeGroup(r.Context(), g); err != nil {
		writeErr(w, http.StatusConflict, "could not record node group")
		return
	}
	for _, id := range req.NodeIDs {
		_ = s.Store.AddNodeGroupMember(r.Context(), p.User.ClusterID, g.ID, id)
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "node.group.create", "ok", g.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"id": g.ID, "name": g.Name})
}

func (s *Server) maintainNode(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeUpdate)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	node, err := s.Store.GetNodeByID(r.Context(), p.User.ClusterID, id)
	if err != nil || node == nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = readJSON(r, &req)
	if err := s.Store.SetNodeMaintenance(r.Context(), appdb.NodeMaintenance{
		NodeID: id, ClusterID: p.User.ClusterID, Reason: strings.TrimSpace(req.Reason), Since: s.now(),
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	workloads, _ := s.Store.ListWorkloads(r.Context(), p.User.ClusterID)
	toMove := []map[string]any{}
	for _, wl := range workloads {
		if wl.NodeID == id || wl.DesiredNodeID == id {
			op := s.startOp(r.Context(), p.User.ClusterID, id, "workload.migrate", "queued", 0)
			op.State = "queued"
			op.Message = "queued until dest is chosen"
			_ = s.Store.UpsertOperation(r.Context(), op)
			item := map[string]any{"id": wl.ID, "name": wl.Name, "kind": wl.Kind, "migrate_operation_id": op.ID}
			if s.Migrate != nil {
				dest, err := s.migrateDest(r.Context(), p.User.ClusterID, wl, "")
				if err == nil && dest != nil {
					out, code, msg := s.runMigrate(r.Context(), wl, dest, migrateModeFor(wl))
					item["migrate"] = out
					item["migrate_status"] = code
					if msg != "" {
						item["migrate_error"] = msg
					}
				}
			}
			toMove = append(toMove, item)
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "node.maintain", "ok", id)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "maintenance": true, "workloads": toMove,
		"warning": "Workloads are listed and migrate jobs are queued. Dest agent is required to empty the node.",
	})
}

func (s *Server) exitMaintenance(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeUpdate)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	if err := s.Store.ClearNodeMaintenance(r.Context(), p.User.ClusterID, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "node.maintain.exit", "ok", id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "maintenance": false})
}

func remotePlacedWorkload(clusterID string, node *appdb.Node, req createWorkloadRequest, id, kind string) appdb.Workload {
	power := req.DesiredPower
	if power == "" {
		power = "stopped"
	}
	return appdb.Workload{
		ID: id, ClusterID: clusterID, NodeID: node.ID, OwnerNodeID: node.ID, DesiredNodeID: node.ID,
		Name: req.Name, Kind: kind, Status: "unavailable", Reason: remoteApplyReason,
		DesiredPower: power, ImagePin: req.ImagePin, CPUs: req.CPUs, MemoryBytes: req.MemoryBytes,
		Privileged: req.Privileged, MigrateBlockers: json.RawMessage(`["remote apply is not wired","dest agent is required"]`),
	}
}
