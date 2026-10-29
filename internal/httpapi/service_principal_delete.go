package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// spReference is one place a service account is still associated. Blocking
// references stop a normal delete; the rest are removed with the account.
type spReference struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Label    string `json:"label"`
	Blocking bool   `json:"blocking"`
}

func (s *Server) findServicePrincipal(ctx context.Context, clusterID, id string) (*appdb.ServicePrincipal, error) {
	items, err := s.Store.ListServicePrincipals(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, nil
}

// servicePrincipalReferences lists the account's live tokens and group
// memberships (blocking) and its role bindings (removed with the account).
// Audit history is kept: it is a record, not a reference.
func (s *Server) servicePrincipalReferences(ctx context.Context, clusterID string, sp appdb.ServicePrincipal) ([]spReference, error) {
	refs := []spReference{}
	now := s.now()
	tokens, err := s.Store.ListTokens(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	for _, t := range tokens {
		if t.UserID != sp.UserID || t.RevokedAt != nil || (t.ExpiresAt != nil && !t.ExpiresAt.After(now)) {
			continue
		}
		label := t.Name
		if t.ExpiresAt != nil {
			label += " · expires " + t.ExpiresAt.UTC().Format(time.RFC3339)
		}
		refs = append(refs, spReference{Kind: "token", ID: t.ID, Label: label, Blocking: true})
	}
	groups, err := s.Store.ListGroups(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		members, err := s.Store.ListGroupMembers(ctx, clusterID, g.ID)
		if err != nil {
			return nil, err
		}
		if containsString(members, sp.UserID) {
			refs = append(refs, spReference{Kind: "group", ID: g.ID, Label: g.Name, Blocking: true})
		}
	}
	roles, err := s.Store.UserRoles(ctx, sp.UserID)
	if err != nil {
		return nil, err
	}
	for _, role := range roles {
		refs = append(refs, spReference{Kind: "role", ID: role, Label: role})
	}
	return refs, nil
}

func blockingRefs(refs []spReference) bool {
	for _, r := range refs {
		if r.Blocking {
			return true
		}
	}
	return false
}

func (s *Server) servicePrincipalRefsAPI(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityService)
	if err != nil {
		return
	}
	sp, err := s.findServicePrincipal(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sp == nil {
		writeErr(w, http.StatusNotFound, "service account not found")
		return
	}
	refs, err := s.servicePrincipalReferences(r.Context(), p.User.ClusterID, *sp)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": sp.ID, "name": sp.Name, "references": refs, "can_force": (rbac.Authorize(p.Grants, rbac.IdentityServiceForce)) || s.isRoot(r.Context(), p)})
}

// deleteServicePrincipal removes a service account and its user row, which
// takes its tokens, role bindings and group memberships with it. While it
// still has live tokens or group memberships the delete answers 409 with
// the references; ?force=true (SuperAdmin or root only) deletes anyway.
func (s *Server) deleteServicePrincipal(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityService)
	if err != nil {
		return
	}
	ctx := r.Context()
	sp, err := s.findServicePrincipal(ctx, p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sp == nil {
		writeErr(w, http.StatusNotFound, "service account not found")
		return
	}
	if !s.guardUserHierarchy(w, r, p, sp.UserID) {
		return
	}
	force := r.URL.Query().Get("force") == "true"
	if force && !rbac.Authorize(p.Grants, rbac.IdentityServiceForce) && !s.isRoot(ctx, p) {
		writeErr(w, http.StatusForbidden, "only a SuperAdmin can force delete")
		return
	}
	refs, err := s.servicePrincipalReferences(ctx, p.User.ClusterID, *sp)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !force && blockingRefs(refs) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "this service account is still in use; force delete removes it and every reference",
			"references": refs,
			"can_force":  (rbac.Authorize(p.Grants, rbac.IdentityServiceForce)) || s.isRoot(ctx, p),
		})
		return
	}
	if err := s.Store.DeleteUser(ctx, p.User.ClusterID, sp.UserID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	action := "service-principal.delete"
	if force {
		action = "service-principal.force-delete"
	}
	s.audit(r, p.User.ClusterID, p.User.ID, action, "ok", sp.ID)
	w.WriteHeader(http.StatusNoContent)
}
