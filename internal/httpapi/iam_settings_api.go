package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// IAM settings: who may create and edit automation flows, and which roles
// may see and decide approvals.

// isAdminOrAbove reports whether p holds Admin or a role ranked at or
// above it.
func (s *Server) isAdminOrAbove(ctx context.Context, p *principal) bool {
	if isOwner(p) {
		return true
	}
	defs := s.roleDefs(ctx, p.User.ClusterID)
	return rankOf(defs, p.Roles) <= rankOf(defs, []string{rbac.Admin})
}

// mayCreateAutomations reports whether p may create and edit flows and
// connections under the "Automation creators" setting.
func (s *Server) mayCreateAutomations(ctx context.Context, p *principal) bool {
	if isOwner(p) || p.User.ID == LocalRootUserID {
		return true
	}
	c := s.iamSettings(ctx, p.User.ClusterID).Creators
	if len(c.Roles) > 0 {
		for _, r := range c.Roles {
			if containsString(p.Roles, r) {
				return true
			}
		}
		return false
	}
	defs := s.roleDefs(ctx, p.User.ClusterID)
	min := firstNonEmpty(c.MinRole, rbac.Admin)
	return rankOf(defs, p.Roles) <= rankOf(defs, []string{min})
}

// requireAutomationEdit is requireAutomation(PolicyApply) plus the creators
// setting, for endpoints that create or change flows and connections.
func (s *Server) requireAutomationEdit(w http.ResponseWriter, r *http.Request) (*principal, bool) {
	p, ok := s.requireAutomation(w, r, rbac.PolicyApply)
	if !ok {
		return nil, false
	}
	if !s.mayCreateAutomations(r.Context(), p) {
		writeErr(w, http.StatusForbidden, "your role may not create or edit automations (IAM > Automation creators)")
		return nil, false
	}
	return p, true
}

// mayEditCreators: admins until a SuperAdmin saves the setting, after that
// SuperAdmins only, unless they let admins keep changing it.
func (s *Server) mayEditCreators(ctx context.Context, p *principal, st appdb.IAMSettings) bool {
	if isOwner(p) {
		return true
	}
	return s.isAdminOrAbove(ctx, p) && (!st.CreatorsLocked || st.CreatorsAdminsMayEdit)
}

func (s *Server) iamSettingsJSON(ctx context.Context, p *principal) map[string]any {
	st := s.iamSettings(ctx, p.User.ClusterID)
	rootName := ""
	rootID := s.rootUserID(ctx, p.User.ClusterID)
	if u, _ := s.Store.GetUser(ctx, rootID); u != nil {
		rootName = u.Username
	}
	roles := st.Creators.Roles
	if roles == nil {
		roles = []string{}
	}
	out := map[string]any{
		"root": map[string]any{"user_id": rootID, "username": rootName, "is_me": rootID != "" && rootID == p.User.ID},
		"automation_creators": map[string]any{
			"min_role": firstNonEmpty(st.Creators.MinRole, rbac.Admin), "roles": roles,
			"locked": st.CreatorsLocked, "admins_may_edit": st.CreatorsAdminsMayEdit,
			"can_edit": s.mayEditCreators(ctx, p, st), "i_may_create": s.mayCreateAutomations(ctx, p),
		},
	}
	if isOwner(p) {
		access := map[string][]string{}
		for role, perms := range st.ApprovalsAccess {
			if v := approvalsPerms(perms); len(v) > 0 {
				access[role] = v
			}
		}
		out["approvals_access"] = access
	}
	return out
}

func (s *Server) getIAMSettings(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, s.iamSettingsJSON(r.Context(), p))
}

func (s *Server) putAutomationCreators(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	var req struct {
		MinRole       string   `json:"min_role"`
		Roles         []string `json:"roles"`
		AdminsMayEdit *bool    `json:"admins_may_edit"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx := r.Context()
	st, err := s.Store.GetIAMSettings(ctx, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.mayEditCreators(ctx, p, st) {
		writeErr(w, http.StatusForbidden, "a SuperAdmin has locked the automation creators setting")
		return
	}
	defs := s.roleDefs(ctx, p.User.ClusterID)
	known := map[string]bool{}
	for _, d := range defs {
		known[d.Name] = d.Name != rbac.Automation
	}
	min := strings.TrimSpace(req.MinRole)
	roles := uniqueStrings(req.Roles)
	if min == "" && len(roles) == 0 {
		min = rbac.Admin
	}
	if min != "" && !known[min] {
		writeErr(w, http.StatusBadRequest, "unknown role "+min)
		return
	}
	for _, role := range roles {
		if !known[role] {
			writeErr(w, http.StatusBadRequest, "unknown role "+role)
			return
		}
	}
	sort.Strings(roles)
	if len(roles) > 0 {
		min = ""
	}
	st.ClusterID = p.User.ClusterID
	st.Creators = appdb.AutomationCreators{MinRole: min, Roles: roles}
	if isOwner(p) {
		st.CreatorsLocked = true
		if req.AdminsMayEdit != nil {
			st.CreatorsAdminsMayEdit = *req.AdminsMayEdit
		}
	}
	st.UpdatedBy = p.User.Username
	if err := s.saveIAMSettings(ctx, st); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	detail := "min_role=" + min
	if len(roles) > 0 {
		detail = "roles=" + strings.Join(roles, ",")
	}
	if isOwner(p) {
		detail += map[bool]string{true: " admins_may_edit", false: " locked"}[st.CreatorsAdminsMayEdit]
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "iam.automation_creators.update", "ok", detail)
	writeJSON(w, http.StatusOK, s.iamSettingsJSON(ctx, p))
}

// putApprovalsAccess gives roles approvals.view / approvals.decide.
// SuperAdmins only; SuperAdmins always have both.
func (s *Server) putApprovalsAccess(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.RolesManage)
	if err != nil {
		return
	}
	if !isOwner(p) {
		writeErr(w, http.StatusForbidden, "only SuperAdmins decide who sees approvals")
		return
	}
	var req struct {
		Access map[string][]string `json:"access"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx := r.Context()
	known := map[string]bool{}
	for _, d := range s.roleDefs(ctx, p.User.ClusterID) {
		known[d.Name] = d.Name != rbac.SuperAdmin && d.Name != rbac.Automation
	}
	access := map[string][]string{}
	var parts []string
	for role, perms := range req.Access {
		role = strings.TrimSpace(role)
		if !known[role] {
			writeErr(w, http.StatusBadRequest, "unknown role "+role)
			return
		}
		for _, perm := range perms {
			if perm != rbac.ApprovalsView && perm != rbac.ApprovalsDecide {
				writeErr(w, http.StatusBadRequest, "only approvals.view and approvals.decide can be given here")
				return
			}
		}
		v := uniqueStrings(perms)
		// Deciding needs seeing.
		if containsString(v, rbac.ApprovalsDecide) && !containsString(v, rbac.ApprovalsView) {
			v = append(v, rbac.ApprovalsView)
		}
		if len(v) == 0 {
			continue
		}
		sort.Strings(v)
		access[role] = v
		parts = append(parts, role+"="+strings.Join(v, "+"))
	}
	sort.Strings(parts)
	st, err := s.Store.GetIAMSettings(ctx, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	st.ClusterID, st.ApprovalsAccess, st.UpdatedBy = p.User.ClusterID, access, p.User.Username
	if err := s.saveIAMSettings(ctx, st); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "iam.approvals_access.update", "ok", firstNonEmpty(strings.Join(parts, " "), "none"))
	writeJSON(w, http.StatusOK, s.iamSettingsJSON(ctx, p))
}
