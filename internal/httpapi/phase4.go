package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const confirmHeader = "X-Nodal-Confirm"

// NetworkRPC is the privileged agent surface for Phase 4 networking.
type NetworkRPC interface {
	DryRunNetwork(ctx context.Context, spec ndnet.Spec) (ndnet.Preview, error)
	ApplyNetwork(ctx context.Context, spec ndnet.Spec) (ndnet.ApplyResult, error)
	GetNetworks(ctx context.Context, hints []ndnet.Hint) (ndnet.Observation, error)
	NetAdvanced(ctx context.Context, op ndnet.AdvancedOp) (ndnet.AdvancedResult, error)
	WireGuard(ctx context.Context, op ndnet.WGOp) (ndnet.WGResult, error)
}

type networkWriteRequest struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	IPv4CIDR      string `json:"ipv4_cidr"`
	UplinkIfName  string `json:"uplink_ifname"`
	ConfirmIfName string `json:"confirm_ifname"`
	DryRun        bool   `json:"dry_run"`
}

func (s *Server) listNetworks(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkRead)
	if err != nil {
		return
	}
	s.refreshNetworks(r.Context(), p.User.ClusterID)
	items, err := s.Store.ListNetworks(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, n := range items {
		out = append(out, networkJSON(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     out,
		"nics":      s.inventoryNICs(r.Context(), p.User.ClusterID),
		"vlans":     vlanListJSON(s.Store, r.Context(), p.User.ClusterID),
		"bonds":     bondListJSON(s.Store, r.Context(), p.User.ClusterID),
		"policies":  policyListJSON(s.Store, r.Context(), p.User.ClusterID),
		"overlays":  overlayListJSON(s.Store, r.Context(), p.User.ClusterID),
		"first_run": len(items) == 0,
	})
}

func (s *Server) getNetwork(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkRead)
	if err != nil {
		return
	}
	s.refreshNetworks(r.Context(), p.User.ClusterID)
	n, err := s.Store.GetNetwork(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || n == nil {
		writeErr(w, http.StatusNotFound, "network not found")
		return
	}
	writeJSON(w, http.StatusOK, networkJSON(*n))
}

func (s *Server) createNetwork(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkCreate)
	if err != nil {
		return
	}
	var req networkWriteRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Kind = strings.TrimSpace(req.Kind)
	if req.Name == "" {
		req.Name = "isolated"
	}
	if req.Kind == "" {
		req.Kind = ndnet.KindIsolated
	}
	if !ndnet.ValidKind(req.Kind) {
		writeErr(w, http.StatusBadRequest, "kind must be isolated, isolated-nat, or lan-bridge")
		return
	}
	node, err := s.Store.GetNode(r.Context(), p.User.ClusterID)
	if err != nil || node == nil {
		writeErr(w, http.StatusFailedDependency, "local node is not enrolled")
		return
	}
	if s.Network == nil {
		writeErr(w, http.StatusBadGateway, "network agent is unavailable")
		return
	}
	id := uuid.NewString()
	spec := s.specFromRequest(r.Context(), p.User.ClusterID, id, req)
	if err := ndnet.ValidateSpecLocators(spec); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	preview, err := s.Network.DryRunNetwork(r.Context(), spec)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, preview)
		return
	}
	if !s.authorizeDanger(w, r, p, preview, req) {
		return
	}
	spec.ConfirmIfName = strings.TrimSpace(req.ConfirmIfName)
	if preview.RequiresConfirm {
		spec.ArmRollback = true
	}
	op := s.startOp(r.Context(), p.User.ClusterID, node.ID, "network.create", "applying", 20)
	res, err := s.Network.ApplyNetwork(r.Context(), spec)
	if err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		s.audit(r, p.User.ClusterID, p.User.ID, "network.create", "denied", err.Error())
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	row := appdb.Network{
		ID: id, ClusterID: p.User.ClusterID, NodeID: node.ID, Name: req.Name, Kind: req.Kind,
		Status: res.Status, Reason: res.Reason, Danger: preview.Danger, BridgeName: res.BridgeName,
		UplinkIfName: res.UplinkIfName, IPv4CIDR: res.IPv4CIDR, Gateway: res.Gateway,
		DHCP: res.DHCP, DNS: res.DNS, NAT: res.NAT, PersistKind: ndnet.PersistNetworkd,
		Warnings: res.Warnings,
	}
	if res.ManagementIfIndex > 0 {
		idx := res.ManagementIfIndex
		row.ManagementIfIndex = &idx
	}
	if err := s.Store.CreateNetwork(r.Context(), row); err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		writeErr(w, http.StatusConflict, "could not record network")
		return
	}
	if res.Gateway != "" && res.IPv4CIDR != "" {
		_ = s.Store.CreateAddress(r.Context(), appdb.Address{
			ID: uuid.NewString(), ClusterID: p.User.ClusterID, NetworkID: id,
			Family: "ipv4", CIDR: res.IPv4CIDR, Role: "gateway",
		})
	}
	s.finishOp(r.Context(), op, "succeeded", "network created", 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "network.create", "ok", id)
	s.emitEvent(r.Context(), p.User.ClusterID, node.ID, "network.created", map[string]string{"network_id": id, "kind": req.Kind})
	writeJSON(w, http.StatusCreated, networkJSON(row))
}

func (s *Server) applyNetwork(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkApply)
	if err != nil {
		return
	}
	n, err := s.Store.GetNetwork(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || n == nil {
		writeErr(w, http.StatusNotFound, "network not found")
		return
	}
	var req networkWriteRequest
	if r.Body != nil {
		_ = readJSON(r, &req)
	}
	if r.URL.Query().Get("dry_run") == "true" {
		req.DryRun = true
	}
	if s.Network == nil {
		writeErr(w, http.StatusBadGateway, "network agent is unavailable")
		return
	}
	reservations, _ := s.Store.ListReservations(r.Context(), p.User.ClusterID, n.ID)
	spec := ndnet.Spec{
		NetworkID: n.ID, Name: n.Name, Kind: n.Kind, IPv4CIDR: n.IPv4CIDR,
		DHCP: n.DHCP, DNS: n.DNS, UplinkIfName: firstNonEmpty(req.UplinkIfName, n.UplinkIfName),
		ConfirmIfName: req.ConfirmIfName, Reservations: toReservations(reservations),
	}
	if err := ndnet.ValidateSpecLocators(spec); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	preview, err := s.Network.DryRunNetwork(r.Context(), spec)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, preview)
		return
	}
	if !s.authorizeDanger(w, r, p, preview, req) {
		return
	}
	spec.ConfirmIfName = strings.TrimSpace(req.ConfirmIfName)
	spec.ArmRollback = preview.RequiresConfirm
	op := s.startOp(r.Context(), p.User.ClusterID, n.NodeID, "network.apply", "applying", 40)
	res, err := s.Network.ApplyNetwork(r.Context(), spec)
	if err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		s.audit(r, p.User.ClusterID, p.User.ID, "network.apply", "denied", err.Error())
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	n.Status = res.Status
	n.Reason = res.Reason
	n.Warnings = res.Warnings
	if res.ManagementIfIndex > 0 {
		idx := res.ManagementIfIndex
		n.ManagementIfIndex = &idx
	}
	if err := s.Store.UpdateNetworkObserved(r.Context(), *n); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record network")
		return
	}
	s.finishOp(r.Context(), op, "succeeded", "network applied", 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "network.apply", "ok", n.ID)
	s.emitEvent(r.Context(), p.User.ClusterID, n.NodeID, "network.applied", map[string]string{"network_id": n.ID})
	writeJSON(w, http.StatusOK, networkJSON(*n))
}

func (s *Server) listReservations(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListReservations(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, reservationJSON(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createReservation(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkCreate)
	if err != nil {
		return
	}
	n, err := s.Store.GetNetwork(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || n == nil {
		writeErr(w, http.StatusNotFound, "network not found")
		return
	}
	if !ndnet.Isolated(n.Kind) {
		writeErr(w, http.StatusBadRequest, "reservations are only valid on isolated networks")
		return
	}
	var req struct {
		MAC      string `json:"mac"`
		IPv4     string `json:"ipv4"`
		Hostname string `json:"hostname"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	mac := strings.TrimSpace(req.MAC)
	ip := strings.TrimSpace(req.IPv4)
	if err := ndnet.ValidateReservations([]ndnet.Reservation{{MAC: mac, IPv4: ip}}, n.IPv4CIDR); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	row := appdb.DHCPReservation{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, NetworkID: n.ID,
		MAC: mac, IPv4: ip, Hostname: strings.TrimSpace(req.Hostname),
	}
	if err := s.Store.CreateReservation(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "network.reservation.create", "ok", row.ID)
	writeJSON(w, http.StatusCreated, reservationJSON(row))
}

func (s *Server) authorizeDanger(w http.ResponseWriter, r *http.Request, p *principal, preview ndnet.Preview, req networkWriteRequest) bool {
	if preview.Danger != ndnet.DangerDangerous {
		return true
	}
	admin := false
	for _, role := range p.Roles {
		if role == rbac.Admin {
			admin = true
		}
	}
	if !admin {
		s.audit(r, p.User.ClusterID, p.User.ID, "network.confirm", "denied", "operator cannot enslave the management NIC")
		writeErr(w, http.StatusForbidden, "operator cannot enslave the management NIC")
		return false
	}
	token := r.Header.Get(confirmHeader)
	ifname := strings.TrimSpace(req.ConfirmIfName)
	if ifname == "" {
		ifname = preview.TypedIfName
	}
	if !ndnet.ValidConfirm(p.User.ClusterID, p.User.ID, preview.Kind, ifname, token, s.now()) || !strings.EqualFold(ifname, preview.TypedIfName) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":              "confirmation_required",
			"code":               "confirmation_required",
			"danger":             preview.Danger,
			"typed_ifname":       preview.TypedIfName,
			"confirm_token":      ndnet.ConfirmToken(p.User.ClusterID, p.User.ID, preview.Kind, preview.TypedIfName, s.now()),
			"message":            "Enslaving the management NIC requires typing the interface name and sending X-Nodal-Confirm.",
			"management_ifindex": preview.ManagementIfIndex,
		})
		return false
	}
	return true
}

func (s *Server) specFromRequest(ctx context.Context, clusterID, id string, req networkWriteRequest) ndnet.Spec {
	spec := ndnet.Spec{
		NetworkID: id, Name: req.Name, Kind: req.Kind, IPv4CIDR: strings.TrimSpace(req.IPv4CIDR),
		UplinkIfName: strings.TrimSpace(req.UplinkIfName), ConfirmIfName: strings.TrimSpace(req.ConfirmIfName),
		DHCP: ndnet.Isolated(req.Kind), DNS: ndnet.Isolated(req.Kind),
	}
	if items, err := s.Store.ListReservations(ctx, clusterID, id); err == nil {
		spec.Reservations = toReservations(items)
	}
	return spec
}

func (s *Server) refreshNetworks(ctx context.Context, clusterID string) {
	if s.Network == nil {
		return
	}
	items, err := s.Store.ListNetworks(ctx, clusterID)
	if err != nil || len(items) == 0 {
		return
	}
	obs, err := s.Network.GetNetworks(ctx, appdb.NetworkHints(items))
	if err != nil {
		return
	}
	_, _, _ = appdb.ReconcileNetworks(ctx, s.Store, clusterID, items, obs)
}

func (s *Server) inventoryNICs(ctx context.Context, clusterID string) []map[string]any {
	node, err := s.Store.GetNode(ctx, clusterID)
	if err != nil || node == nil {
		return nil
	}
	inv, err := s.Store.GetInventory(ctx, node.ID)
	if err != nil || inv == nil || len(inv.Payload) == 0 {
		return nil
	}
	var parsed inventory.Inventory
	if json.Unmarshal(inv.Payload, &parsed) != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(parsed.NICs))
	for _, nic := range parsed.NICs {
		out = append(out, map[string]any{
			"name": nic.Name, "ifindex": nic.IfIndex, "mac": nic.MAC, "state": nic.State,
			"kind": nic.Kind, "addresses": nic.Addresses,
		})
	}
	return out
}

func toReservations(items []appdb.DHCPReservation) []ndnet.Reservation {
	out := make([]ndnet.Reservation, 0, len(items))
	for _, item := range items {
		out = append(out, ndnet.Reservation{ID: item.ID, MAC: item.MAC, IPv4: item.IPv4, Hostname: item.Hostname})
	}
	return out
}

func networkJSON(n appdb.Network) map[string]any {
	return map[string]any{
		"id": n.ID, "node_id": n.NodeID, "name": n.Name, "kind": n.Kind,
		"status": n.Status, "reason": n.Reason, "danger": n.Danger,
		"bridge_name": n.BridgeName, "uplink_ifname": n.UplinkIfName,
		"ipv4_cidr": n.IPv4CIDR, "gateway": n.Gateway,
		"dhcp": n.DHCP, "dns": n.DNS, "nat": n.NAT,
		"persist_kind": n.PersistKind, "warnings": n.Warnings,
		"management_ifindex": n.ManagementIfIndex,
		"created_at":         n.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func reservationJSON(r appdb.DHCPReservation) map[string]any {
	return map[string]any{
		"id": r.ID, "network_id": r.NetworkID, "mac": r.MAC, "ipv4": r.IPv4,
		"hostname": r.Hostname, "created_at": r.CreatedAt.UTC().Format(time.RFC3339),
	}
}
