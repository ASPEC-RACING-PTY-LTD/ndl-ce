package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

// Teardown of advanced network objects, WireGuard pairings and library
// images. The agent removes what it put on the host first; the record goes
// only after that succeeds, so a failed teardown never leaves an orphan on
// the host that the UI can no longer see.

// deleteRequest carries the typed interface confirmation needed when the
// object sits on the management path.
type deleteRequest struct {
	Confirm string `json:"confirm_ifname"`
}

func readDeleteRequest(r *http.Request) deleteRequest {
	var req deleteRequest
	if r.Body != nil && r.ContentLength != 0 {
		_ = readJSON(r, &req)
	}
	return req
}

func (s *Server) finishNetDelete(w http.ResponseWriter, r *http.Request, p *principal, kind appdb.ConfigKind, action, id string, res ndnet.AdvancedResult, err error) {
	if err != nil {
		s.audit(r, p.User.ClusterID, p.User.ID, action, "denied", err.Error())
		writeErr(w, statusFor(err), err.Error())
		return
	}
	if res.RolledBack {
		writeErr(w, http.StatusConflict, "management probe failed after removal; the change was rolled back")
		return
	}
	if err := s.Store.DeleteConfig(r.Context(), kind, p.User.ClusterID, id); err != nil && !errors.Is(err, appdb.ErrConfigNotFound) {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, action, "ok", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteVLAN(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkCreate)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	items, err := s.Store.ListNetworkVLANs(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var v *appdb.NetworkVLAN
	for i := range items {
		if items[i].ID == id {
			v = &items[i]
		}
	}
	if v == nil {
		writeErr(w, http.StatusNotFound, "vlan not found")
		return
	}
	req := readDeleteRequest(r)
	res, err := s.advanced()(r.Context(), ndnet.AdvancedOp{
		Action: ndnet.ActionVLANRemove, ObjectID: v.ID, NetworkID: v.NetworkID, VID: v.VID,
		ParentIfName: v.ParentIfName, AccessIfName: v.AccessIfName, Mode: v.Mode,
		ConfirmIfName: strings.TrimSpace(req.Confirm),
	})
	s.finishNetDelete(w, r, p, appdb.ConfigNetVLAN, "network.vlan.delete", v.ID, res, err)
}

func (s *Server) deleteBond(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkCreate)
	if err != nil {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	bonds, err := s.Store.ListNetworkBonds(ctx, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var b *appdb.NetworkBond
	for i := range bonds {
		if bonds[i].ID == id {
			b = &bonds[i]
		}
	}
	if b == nil {
		writeErr(w, http.StatusNotFound, "bond not found")
		return
	}
	ifname := firstNonEmpty(b.Locator, "")
	if ifname == "" {
		ifname, _ = ndnet.BondName(b.ID)
	}
	if ifname != "" {
		vlans, _ := s.Store.ListNetworkVLANs(ctx, p.User.ClusterID)
		for _, v := range vlans {
			if strings.EqualFold(v.ParentIfName, ifname) || strings.EqualFold(v.AccessIfName, ifname) {
				writeErr(w, http.StatusConflict, fmt.Sprintf("VLAN %s uses this bond. Delete it first.", firstNonEmpty(v.Name, fmt.Sprint(v.VID))))
				return
			}
		}
		nets, _ := s.Store.ListNetworks(ctx, p.User.ClusterID)
		for _, n := range nets {
			if strings.EqualFold(n.UplinkIfName, ifname) {
				writeErr(w, http.StatusConflict, fmt.Sprintf("network %s uses this bond as its uplink. Delete or change it first.", n.Name))
				return
			}
		}
	}
	req := readDeleteRequest(r)
	res, err := s.advanced()(ctx, ndnet.AdvancedOp{
		Action: ndnet.ActionBondRemove, ObjectID: b.ID, Members: b.Members,
		ConfirmIfName: strings.TrimSpace(req.Confirm),
	})
	s.finishNetDelete(w, r, p, appdb.ConfigNetBond, "network.bond.delete", b.ID, res, err)
}

func (s *Server) deleteOverlay(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkCreate)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	items, err := s.Store.ListNetworkOverlays(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	found := false
	for _, o := range items {
		if o.ID == id {
			found = true
		}
	}
	if !found {
		writeErr(w, http.StatusNotFound, "overlay not found")
		return
	}
	res, err := s.advanced()(r.Context(), ndnet.AdvancedOp{Action: ndnet.ActionOverlayRemove, ObjectID: id})
	s.finishNetDelete(w, r, p, appdb.ConfigNetOverlay, "network.overlay.delete", id, res, err)
}

// deleteNetPolicy reapplies the remaining policy set without this rule, or
// drops the policy table when it was the last one.
func (s *Server) deleteNetPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkApply)
	if err != nil {
		return
	}
	ctx := r.Context()
	pol, err := s.Store.GetNetworkPolicy(ctx, p.User.ClusterID, r.PathValue("id"))
	if err != nil || pol == nil {
		writeErr(w, http.StatusNotFound, "policy not found")
		return
	}
	items, err := s.Store.ListNetworkPolicies(ctx, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var rest []ndnet.PolicyRule
	for _, item := range items {
		// Only rules that were applied are on the host; leave pending ones out.
		if item.ID == pol.ID || item.Status != ndnet.StatusAvailable {
			continue
		}
		rest = append(rest, ndnet.PolicyRule{ID: item.ID, Action: item.Action, SrcMAC: item.SrcMAC, DstMAC: item.DstMAC})
	}
	var res ndnet.AdvancedResult
	switch {
	case pol.Status != ndnet.StatusAvailable:
		// Never applied: nothing to remove from the host.
	case len(rest) == 0:
		res, err = s.advanced()(ctx, ndnet.AdvancedOp{Action: ndnet.ActionPolicyClear, ObjectID: pol.ID})
	default:
		res, err = s.advanced()(ctx, ndnet.AdvancedOp{Action: ndnet.ActionPolicyApply, ObjectID: rest[0].ID, Policies: rest})
	}
	s.finishNetDelete(w, r, p, appdb.ConfigNetPolicy, "network.policy.delete", pol.ID, res, err)
}

// deleteWGPeer removes a WireGuard pairing: the local interface, both peer
// records and the remote node that joined through it.
func (s *Server) deleteWGPeer(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NetworkCreate)
	if err != nil {
		return
	}
	ctx := r.Context()
	clusterID := p.User.ClusterID
	peer, err := s.Store.GetWGPeer(ctx, clusterID, r.PathValue("id"))
	if err != nil || peer == nil {
		writeErr(w, http.StatusNotFound, "peer not found")
		return
	}
	pair := []appdb.WGPeer{*peer}
	all, _ := s.Store.ListWGPeers(ctx, clusterID)
	for _, other := range all {
		if other.ID != peer.ID && other.Role != peer.Role &&
			other.AddressCIDR == peer.AllowedIPs && other.AllowedIPs == peer.AddressCIDR {
			pair = append(pair, other)
			break
		}
	}
	for _, item := range pair {
		if item.Role != "local" {
			continue
		}
		if _, err := s.wireguard()(ctx, ndnet.WGOp{Action: ndnet.ActionWGRemove, PeerID: item.ID}); err != nil {
			s.audit(r, clusterID, p.User.ID, "cluster.wg.peer.delete", "denied", err.Error())
			writeErr(w, statusFor(err), "the agent could not remove the WireGuard interface: "+err.Error())
			return
		}
	}
	for _, item := range pair {
		if node, _ := s.Store.GetRemoteNodeByPeer(ctx, clusterID, item.ID); node != nil {
			if err := s.Store.DeleteConfig(ctx, appdb.ConfigRemoteNode, clusterID, node.ID); err != nil && !errors.Is(err, appdb.ErrConfigNotFound) {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if err := s.Store.DeleteConfig(ctx, appdb.ConfigWGPeer, clusterID, item.ID); err != nil && !errors.Is(err, appdb.ErrConfigNotFound) {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.audit(r, clusterID, p.User.ID, "cluster.wg.peer.delete", "ok", peer.ID)
	w.WriteHeader(http.StatusNoContent)
}

// libraryFileDeleter is implemented by the agent client.
type libraryFileDeleter interface {
	DeleteLibraryFile(ctx context.Context, itemID, backendRef string, hint storage.PoolHint) error
}

// deleteImage removes an uploaded library image that no VM or template uses.
func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StorageVolumeCreate)
	if err != nil {
		return
	}
	ctx := r.Context()
	clusterID := p.User.ClusterID
	item, err := s.Store.GetLibraryItem(ctx, clusterID, r.PathValue("id"))
	if err != nil || item == nil {
		writeErr(w, http.StatusNotFound, "image not found")
		return
	}
	wls, err := s.Store.ListWorkloads(ctx, clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, wl := range wls {
		if len(wl.SpecJSON) == 0 {
			continue
		}
		spec, err := vmspec.Parse(wl.SpecJSON)
		if err == nil && spec.ISOLibraryID == item.ID {
			writeErr(w, http.StatusConflict, fmt.Sprintf("attached to %s as installation media. Eject it first.", wl.Name))
			return
		}
	}
	templates, _ := s.Store.ListVMTemplates(ctx, clusterID)
	for _, t := range templates {
		if bytes.Contains(t.SpecJSON, []byte(item.ID)) {
			writeErr(w, http.StatusConflict, fmt.Sprintf("template %s uses this image. Delete the template first.", t.Name))
			return
		}
	}
	if del, ok := s.Storage.(libraryFileDeleter); ok && item.BackendRef != "" {
		pool, _ := s.Store.GetStoragePool(ctx, clusterID, item.PoolID)
		if pool != nil {
			hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
			if err := del.DeleteLibraryFile(ctx, item.ID, item.BackendRef, hint); err != nil {
				s.audit(r, clusterID, p.User.ID, "storage.image.delete", "denied", err.Error())
				writeErr(w, http.StatusBadGateway, "the agent could not remove the image file: "+err.Error())
				return
			}
		}
	}
	if err := s.Store.DeleteConfig(ctx, appdb.ConfigLibraryItem, clusterID, item.ID); err != nil {
		if errors.Is(err, appdb.ErrConfigNotFound) {
			writeErr(w, http.StatusNotFound, "image not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, clusterID, p.User.ID, "storage.image.delete", "ok", item.ID)
	s.emitEvent(ctx, clusterID, item.NodeID, "storage.image.deleted", map[string]string{"image_id": item.ID})
	w.WriteHeader(http.StatusNoContent)
}
