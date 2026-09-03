package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) advanced() func(context.Context, ndnet.AdvancedOp) (ndnet.AdvancedResult, error) {
	if s.Network != nil {
		return s.Network.NetAdvanced
	}
	return func(context.Context, ndnet.AdvancedOp) (ndnet.AdvancedResult, error) {
		return ndnet.AdvancedResult{Status: ndnet.StatusUnavailable, Reason: "network agent is unavailable"}, nil
	}
}

func (s *Server) createVLAN(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkCreate)
	if err != nil {
		return
	}
	var req struct {
		Name         string `json:"name"`
		NetworkID    string `json:"network_id"`
		VID          int    `json:"vlan_id"`
		ParentIfName string `json:"parent_ifname"`
		AccessIfName string `json:"access_ifname"`
		Mode         string `json:"mode"`
		Confirm      string `json:"confirm_ifname"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := ndnet.ParseVID(req.VID); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	mode, err := ndnet.ParseVLANMode(req.Mode)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	parent := strings.TrimSpace(req.ParentIfName)
	bridge := ""
	if req.NetworkID != "" {
		n, err := s.Store.GetNetwork(r.Context(), p.User.ClusterID, req.NetworkID)
		if err != nil || n == nil {
			writeErr(w, http.StatusNotFound, "network not found")
			return
		}
		bridge = n.BridgeName
		if parent == "" {
			parent = n.BridgeName
		}
	}
	if err := ndnet.ParseVLANParent(parent); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	access := strings.TrimSpace(req.AccessIfName)
	if mode == ndnet.VLANAccess && access != "" && !ndnet.ValidIfName(access) {
		writeErr(w, http.StatusBadRequest, "access interface name is not valid")
		return
	}
	id := uuid.NewString()
	res, err := s.advanced()(r.Context(), ndnet.AdvancedOp{
		Action: ndnet.ActionVLANAdd, ObjectID: id, NetworkID: req.NetworkID, Name: req.Name, VID: req.VID,
		ParentIfName: parent, AccessIfName: access, Mode: mode,
		ConfirmIfName: strings.TrimSpace(req.Confirm), BridgeName: bridge,
	})
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	row := appdb.NetworkVLAN{
		ID: id, ClusterID: p.User.ClusterID, NetworkID: req.NetworkID, Name: strings.TrimSpace(req.Name),
		VID: req.VID, ParentIfName: parent, AccessIfName: access,
		Mode: firstNonEmpty(res.Mode, mode), Locator: res.Locator, Status: res.Status, Reason: res.Reason,
	}
	if err := s.Store.CreateNetworkVLAN(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record vlan")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "network.vlan.create", "ok", id)
	writeJSON(w, http.StatusCreated, vlanJSON(row))
}

func (s *Server) createBond(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkCreate)
	if err != nil {
		return
	}
	var req struct {
		Name    string   `json:"name"`
		Mode    string   `json:"mode"`
		Members []string `json:"members"`
		Confirm string   `json:"confirm_ifname"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	mode, err := ndnet.ParseBondMode(req.Mode)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id := uuid.NewString()
	if err := ndnet.ParseBondMembers(id, req.Members); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.advanced()(r.Context(), ndnet.AdvancedOp{
		Action: ndnet.ActionBondAdd, ObjectID: id, Name: name, Mode: mode, Members: req.Members,
		ConfirmIfName: strings.TrimSpace(req.Confirm),
	})
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	row := appdb.NetworkBond{
		ID: id, ClusterID: p.User.ClusterID, Name: name, Mode: firstNonEmpty(res.Mode, mode),
		Members: req.Members, Locator: res.Locator, Status: res.Status, Reason: res.Reason,
	}
	if err := s.Store.CreateNetworkBond(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record bond")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "network.bond.create", "ok", id)
	writeJSON(w, http.StatusCreated, bondJSON(row))
}

func (s *Server) createPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkCreate)
	if err != nil {
		return
	}
	var req struct {
		Name          string `json:"name"`
		Action        string `json:"action"`
		SrcWorkloadID string `json:"src_workload_id"`
		DstWorkloadID string `json:"dst_workload_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	srcMAC, err := s.workloadMAC(r.Context(), p.User.ClusterID, req.SrcWorkloadID)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	dstMAC, err := s.workloadMAC(r.Context(), p.User.ClusterID, req.DstWorkloadID)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	id := uuid.NewString()
	row := appdb.NetworkPolicy{
		ID: id, ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name),
		Action: firstNonEmpty(req.Action, "deny"), SrcWorkloadID: req.SrcWorkloadID, DstWorkloadID: req.DstWorkloadID,
		SrcMAC: srcMAC, DstMAC: dstMAC, Status: ndnet.StatusUnavailable,
	}
	if err := s.Store.CreateNetworkPolicy(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record policy")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "network.policy.create", "ok", id)
	writeJSON(w, http.StatusCreated, policyJSON(row))
}

func (s *Server) applyPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkApply)
	if err != nil {
		return
	}
	pol, err := s.Store.GetNetworkPolicy(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || pol == nil {
		writeErr(w, http.StatusNotFound, "policy not found")
		return
	}
	items, err := s.Store.ListNetworkPolicies(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Apply the full stored set in one table replace. A single-rule replace
	// would drop every other policy from nft while catalog still said available.
	var policies []ndnet.PolicyRule
	found := false
	for _, item := range items {
		if item.ID == pol.ID {
			found = true
		}
		policies = append(policies, ndnet.PolicyRule{
			ID: item.ID, Action: item.Action, SrcMAC: item.SrcMAC, DstMAC: item.DstMAC,
		})
	}
	if !found {
		writeErr(w, http.StatusNotFound, "policy not found")
		return
	}
	res, err := s.advanced()(r.Context(), ndnet.AdvancedOp{
		Action: ndnet.ActionPolicyApply, ObjectID: pol.ID, PolicyAction: pol.Action,
		SrcMAC: pol.SrcMAC, DstMAC: pol.DstMAC, Policies: policies,
	})
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	for _, item := range items {
		if err := s.Store.UpdateNetworkPolicyStatus(r.Context(), p.User.ClusterID, item.ID, res.Status, res.Reason); err != nil {
			if item.ID == pol.ID {
				writeErr(w, http.StatusInternalServerError, "could not record network policy")
				return
			}
			continue
		}
		if item.ID == pol.ID {
			pol.Status, pol.Reason = res.Status, res.Reason
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "network.policy.apply", "ok", pol.ID)
	writeJSON(w, http.StatusOK, policyJSON(*pol))
}

func (s *Server) createOverlay(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkCreate)
	if err != nil {
		return
	}
	var req struct {
		Name string `json:"name"`
		VNI  uint32 `json:"vni"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := ndnet.ParseOverlayVNI(req.VNI); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id := uuid.NewString()
	res, err := s.advanced()(r.Context(), ndnet.AdvancedOp{
		Action: ndnet.ActionOverlayPrep, ObjectID: id, Name: req.Name, OverlayVNI: req.VNI,
	})
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	row := appdb.NetworkOverlay{
		ID: id, ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name), VNI: int(req.VNI),
		Locator: res.Locator, Status: overlayPrepStatus(res.Status), Reason: overlayPrepReason(res.Status, res.Reason),
	}
	if err := s.Store.CreateNetworkOverlay(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record overlay")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "network.overlay.create", "ok", id)
	writeJSON(w, http.StatusCreated, overlayJSON(row))
}

func (s *Server) workloadMAC(ctx context.Context, clusterID, workloadID string) (string, error) {
	id := strings.TrimSpace(workloadID)
	if id == "" {
		return "", errBadRequest("src_workload_id and dst_workload_id are required")
	}
	wl, err := s.Store.GetWorkload(ctx, clusterID, id)
	if err != nil || wl == nil {
		return "", errNotFound("workload not found")
	}
	nics, err := s.Store.ListWorkloadNICs(ctx, clusterID, wl.ID)
	if err != nil || len(nics) == 0 || strings.TrimSpace(nics[0].MAC) == "" {
		return "", errUnprocessable("workload has no NIC MAC")
	}
	return nics[0].MAC, nil
}

func vlanJSON(v appdb.NetworkVLAN) map[string]any {
	return map[string]any{
		"id": v.ID, "network_id": v.NetworkID, "name": v.Name, "vlan_id": v.VID,
		"parent_ifname": v.ParentIfName, "access_ifname": v.AccessIfName, "mode": v.Mode,
		"locator": v.Locator, "status": v.Status, "reason": v.Reason,
	}
}

func bondJSON(b appdb.NetworkBond) map[string]any {
	return map[string]any{
		"id": b.ID, "name": b.Name, "mode": b.Mode, "members": b.Members,
		"locator": b.Locator, "status": b.Status, "reason": b.Reason,
	}
}

func policyJSON(p appdb.NetworkPolicy) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "action": p.Action,
		"src_workload_id": p.SrcWorkloadID, "dst_workload_id": p.DstWorkloadID,
		"src_mac": p.SrcMAC, "dst_mac": p.DstMAC, "status": p.Status, "reason": p.Reason,
	}
}

const overlayPrepHonest = "local prep, mesh not joined"

func overlayPrepStatus(status string) string {
	if status == ndnet.StatusAvailable || status == "" {
		return "pending"
	}
	return status
}

func overlayPrepReason(status, reason string) string {
	if status == ndnet.StatusAvailable || strings.TrimSpace(reason) == "" {
		return overlayPrepHonest
	}
	return reason
}

func overlayJSON(o appdb.NetworkOverlay) map[string]any {
	return map[string]any{
		"id": o.ID, "name": o.Name, "vni": o.VNI, "locator": o.Locator, "status": o.Status, "reason": o.Reason,
	}
}

func vlanListJSON(st appdb.Store, ctx context.Context, clusterID string) []map[string]any {
	items, _ := st.ListNetworkVLANs(ctx, clusterID)
	out := make([]map[string]any, 0, len(items))
	for _, v := range items {
		out = append(out, vlanJSON(v))
	}
	return out
}

func bondListJSON(st appdb.Store, ctx context.Context, clusterID string) []map[string]any {
	items, _ := st.ListNetworkBonds(ctx, clusterID)
	out := make([]map[string]any, 0, len(items))
	for _, b := range items {
		out = append(out, bondJSON(b))
	}
	return out
}

func policyListJSON(st appdb.Store, ctx context.Context, clusterID string) []map[string]any {
	items, _ := st.ListNetworkPolicies(ctx, clusterID)
	out := make([]map[string]any, 0, len(items))
	for _, p := range items {
		out = append(out, policyJSON(p))
	}
	return out
}

func overlayListJSON(st appdb.Store, ctx context.Context, clusterID string) []map[string]any {
	items, _ := st.ListNetworkOverlays(ctx, clusterID)
	out := make([]map[string]any, 0, len(items))
	for _, o := range items {
		out = append(out, overlayJSON(o))
	}
	return out
}
