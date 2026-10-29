package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const (
	deleteUserConfirm    = "delete-user"
	disableUserConfirm   = "disable-user"
	resetPasswordConfirm = "reset-password"
)

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UsersRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListUsers(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	roleFilter := strings.TrimSpace(r.URL.Query().Get("role"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	kindFilter := strings.TrimSpace(r.URL.Query().Get("kind"))
	sortKey := strings.TrimSpace(r.URL.Query().Get("sort"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if page < 1 {
		page = 1
	}
	if perPage <= 0 || perPage > 200 {
		perPage = 50
	}

	type row struct {
		user appdb.User
		json map[string]any
	}
	var rows []row
	for _, u := range items {
		item, err := s.userPublicJSON(r.Context(), u)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if q != "" {
			hay := strings.ToLower(u.Username + " " + u.DisplayName)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		if kindFilter != "" && u.Kind != kindFilter {
			continue
		}
		if statusFilter == "disabled" && u.DisabledAt == nil {
			continue
		}
		if statusFilter == "active" && u.DisabledAt != nil {
			continue
		}
		if roleFilter != "" {
			roles, _ := item["roles"].([]string)
			if !containsString(roles, roleFilter) {
				continue
			}
		}
		rows = append(rows, row{user: u, json: item})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].user, rows[j].user
		switch sortKey {
		case "created":
			return a.CreatedAt.Before(b.CreatedAt)
		case "created_desc":
			return a.CreatedAt.After(b.CreatedAt)
		case "last_login":
			return lastLoginUnix(a) > lastLoginUnix(b)
		case "role":
			return strings.Join(asStringSlice(rows[i].json["roles"]), ",") < strings.Join(asStringSlice(rows[j].json["roles"]), ",")
		default:
			return strings.ToLower(a.Username) < strings.ToLower(b.Username)
		}
	})
	total := len(rows)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	out := make([]map[string]any, 0, end-start)
	for _, row := range rows[start:end] {
		out = append(out, row.json)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    out,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UsersRead)
	if err != nil {
		return
	}
	u, err := s.Store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil || u == nil || u.ClusterID != p.User.ClusterID {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	item, err := s.userPublicJSON(r.Context(), *u)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UsersCreate)
	if err != nil {
		return
	}
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Role = strings.TrimSpace(req.Role)
	if req.Role == "" {
		req.Role = rbac.User
	}
	if !validUsername(req.Username) {
		writeErr(w, http.StatusBadRequest, "username must be 1-64 letters, digits, dot, underscore, or hyphen")
		return
	}
	if req.Role != rbac.NoRole && !containsString(s.loginRoleNames(r.Context(), p.User.ClusterID), req.Role) {
		writeErr(w, http.StatusBadRequest, "unknown sign-in role "+req.Role)
		return
	}
	if req.Role != rbac.NoRole && !s.mayActOnRole(r.Context(), p, req.Role) {
		writeErr(w, http.StatusForbidden, "you cannot assign a role at or above yours")
		return
	}
	if req.Role == rbac.SuperAdmin && !rbac.Authorize(p.Grants, rbac.UsersRolesManage) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	user := appdb.User{
		ID:           uuid.NewString(),
		ClusterID:    p.User.ClusterID,
		Username:     req.Username,
		PasswordHash: hash,
		Kind:         appdb.UserKindPerson,
		DisplayName:  req.DisplayName,
		CreatedAt:    s.now(),
	}
	if err := s.Store.CreateUser(r.Context(), user); err != nil {
		s.audit(r, p.User.ClusterID, p.User.ID, "user.create", "denied", req.Username)
		writeErr(w, http.StatusConflict, "could not create user")
		return
	}
	if req.Role != rbac.NoRole {
		if err := s.Store.BindRole(r.Context(), p.User.ClusterID, user.ID, req.Role); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "user.create", "ok", user.ID)
	item, err := s.userPublicJSON(r.Context(), user)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) patchUser(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UsersUpdate)
	if err != nil {
		return
	}
	u, err := s.Store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil || u == nil || u.ClusterID != p.User.ClusterID {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.guardUserHierarchy(w, r, p, u.ID) {
		return
	}
	var req struct {
		DisplayName    *string `json:"display_name"`
		Disabled       *bool   `json:"disabled"`
		MFARequired    *bool   `json:"mfa_required"`
		Role           *string `json:"role"`
		Username       *string `json:"username"`
		Email          *string `json:"email"`
		EmailSecondary *string `json:"email_secondary"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if len(name) > 128 {
			writeErr(w, http.StatusBadRequest, "display_name is too long")
			return
		}
		u.DisplayName = name
	}
	if req.Username != nil && strings.TrimSpace(*req.Username) != u.Username {
		name := strings.TrimSpace(*req.Username)
		if !validUsername(name) {
			writeErr(w, http.StatusBadRequest, "username must be 1-64 letters, digits, dot, dash or underscore")
			return
		}
		if err := s.Store.RenameUser(r.Context(), p.User.ClusterID, u.ID, name); err != nil {
			if errors.Is(err, appdb.ErrUsernameTaken) {
				writeErr(w, http.StatusConflict, err.Error())
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "user.rename", "ok", u.ID+": "+u.Username+" -> "+name)
		u.Username = name
	}
	if req.Email != nil || req.EmailSecondary != nil {
		prof := s.profileOf(r.Context(), *u)
		if req.Email != nil {
			v, err := cleanEmail(*req.Email)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			if v != prof.Email {
				prof.Email, prof.EmailVerifiedAt = v, nil
			}
		}
		if req.EmailSecondary != nil {
			v, err := cleanEmail(*req.EmailSecondary)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			prof.EmailSecondary = v
		}
		prof.UpdatedAt = s.now()
		if err := s.Store.UpsertAccountProfile(r.Context(), prof); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "user.email.update", "ok", u.ID)
	}
	if req.MFARequired != nil {
		u.MFARequired = *req.MFARequired
		s.audit(r, p.User.ClusterID, p.User.ID, "user.mfa.require", "ok", u.ID)
	}
	if req.Disabled != nil {
		if *req.Disabled {
			if strings.TrimSpace(r.Header.Get(confirmHeader)) != disableUserConfirm {
				writeErr(w, http.StatusUnprocessableEntity, "disabling a user requires X-Nodal-Confirm: disable-user")
				return
			}
			if u.ID == p.User.ID {
				writeErr(w, http.StatusConflict, "cannot disable the current account")
				return
			}
			if err := s.guardLastAdmin(r.Context(), p.User.ClusterID, u.ID, "disable"); err != nil {
				writeErr(w, http.StatusConflict, err.Error())
				return
			}
			now := s.now()
			u.DisabledAt = &now
			_ = s.Store.RevokeUserSessions(r.Context(), u.ID)
			_ = s.Store.RevokeUserTokens(r.Context(), u.ID)
			s.audit(r, p.User.ClusterID, p.User.ID, "user.disable", "ok", u.ID)
		} else {
			u.DisabledAt = nil
			s.audit(r, p.User.ClusterID, p.User.ID, "user.enable", "ok", u.ID)
		}
	}
	if err := s.Store.UpdateUser(r.Context(), *u); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Role != nil {
		if err := s.replaceLoginRole(w, r, p, *u, strings.TrimSpace(*req.Role)); err != nil {
			return
		}
	}
	fresh, err := s.Store.GetUser(r.Context(), u.ID)
	if err != nil || fresh == nil {
		writeErr(w, http.StatusInternalServerError, "user missing after update")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "user.update", "ok", u.ID)
	item, err := s.userPublicJSON(r.Context(), *fresh)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UsersDelete)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != deleteUserConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "deleting a user requires X-Nodal-Confirm: delete-user")
		return
	}
	u, err := s.Store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil || u == nil || u.ClusterID != p.User.ClusterID {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.guardUserHierarchy(w, r, p, u.ID) {
		return
	}
	if u.ID == p.User.ID {
		writeErr(w, http.StatusConflict, "cannot delete the current account")
		return
	}
	if err := s.guardLastAdmin(r.Context(), p.User.ClusterID, u.ID, "delete"); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.Store.DeleteUser(r.Context(), p.User.ClusterID, u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "user.delete", "ok", u.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UsersUpdate)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != resetPasswordConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "password reset requires X-Nodal-Confirm: reset-password")
		return
	}
	u, err := s.Store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil || u == nil || u.ClusterID != p.User.ClusterID {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.guardUserHierarchy(w, r, p, u.ID) {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.UpdatePassword(r.Context(), u.ID, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.Store.RevokeUserSessions(r.Context(), u.ID)
	s.platformOwnerSignedIn(p.User.ClusterID, *u, req.Password)
	s.audit(r, p.User.ClusterID, p.User.ID, "user.password.reset", "ok", u.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UsersSessionsRevoke)
	if err != nil {
		return
	}
	u, err := s.Store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil || u == nil || u.ClusterID != p.User.ClusterID {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.guardUserHierarchy(w, r, p, u.ID) {
		return
	}
	if err := s.Store.RevokeUserSessions(r.Context(), u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "user.session.revoke", "ok", u.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeUserTokens(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UsersSessionsRevoke)
	if err != nil {
		return
	}
	if !rbac.Authorize(p.Grants, rbac.IdentityTokenRevoke) && !rbac.Authorize(p.Grants, rbac.APIAccessManage) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	u, err := s.Store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil || u == nil || u.ClusterID != p.User.ClusterID {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.guardUserHierarchy(w, r, p, u.ID) {
		return
	}
	if err := s.Store.RevokeUserTokens(r.Context(), u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "user.token.revoke", "ok", u.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.RolesManage)
	if err != nil {
		return
	}
	users, err := s.Store.ListUsers(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	counts := map[string]int{}
	for _, u := range users {
		roles, err := s.Store.UserRoles(r.Context(), u.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		seen := map[string]struct{}{}
		for _, role := range roles {
			if _, ok := seen[role]; ok {
				continue
			}
			seen[role] = struct{}{}
			counts[role]++
		}
	}
	cat := rbac.New()
	out := make([]map[string]any, 0, len(rbac.BuiltInRoles()))
	for _, meta := range rbac.BuiltInRoles() {
		out = append(out, map[string]any{
			"name":        meta.Name,
			"title":       meta.Title,
			"summary":     meta.Summary,
			"login":       meta.Login,
			"immutable":   meta.Immutable,
			"permissions": cat.PermissionsForRole(meta.Name),
			"user_count":  counts[meta.Name],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "custom_roles": false})
}

func (s *Server) getSecuritySettings(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsSecurityManage)
	if err != nil {
		return
	}
	cluster, err := s.Store.GetCluster(r.Context())
	if err != nil || cluster == nil || cluster.ID != p.User.ClusterID {
		writeErr(w, http.StatusInternalServerError, "cluster missing")
		return
	}
	writeJSON(w, http.StatusOK, securitySettingsJSON(cluster.MFARequired))
}

func (s *Server) patchSecuritySettings(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsSecurityManage)
	if err != nil {
		return
	}
	var req struct {
		MFARequired *bool `json:"mfa_required"`
	}
	if err := readJSON(r, &req); err != nil || req.MFARequired == nil {
		writeErr(w, http.StatusBadRequest, "mfa_required is required")
		return
	}
	if err := s.Store.SetClusterMFARequired(r.Context(), p.User.ClusterID, *req.MFARequired); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "security.mfa_policy", "ok", strconv.FormatBool(*req.MFARequired))
	writeJSON(w, http.StatusOK, securitySettingsJSON(*req.MFARequired))
}

func securitySettingsJSON(mfaRequired bool) map[string]any {
	return map[string]any{
		"mfa_required":           mfaRequired,
		"session_ttl_hours":      int(sessionTTL / time.Hour),
		"lockout_max_failures":   8,
		"lockout_window_minutes": 15,
		"lockout_minutes":        15,
		"recover_admin":          "host-only",
	}
}

func (s *Server) replaceLoginRole(w http.ResponseWriter, r *http.Request, p *principal, u appdb.User, role string) error {
	if !rbac.Authorize(p.Grants, rbac.UsersRolesManage) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return errForbidden("forbidden")
	}
	loginRoles := s.loginRoleNames(r.Context(), p.User.ClusterID)
	if role == rbac.NoRole {
		return s.clearLoginRoles(w, r, p, u, loginRoles)
	}
	if !containsString(loginRoles, role) {
		writeErr(w, http.StatusBadRequest, "unknown sign-in role "+role)
		return errBadRequest("invalid role")
	}
	if !s.mayActOnRole(r.Context(), p, role) {
		writeErr(w, http.StatusForbidden, "you cannot assign a role at or above yours")
		return errForbidden("forbidden")
	}
	cur, err := s.Store.UserRoles(r.Context(), u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return err
	}
	if containsString(cur, rbac.SuperAdmin) && role != rbac.SuperAdmin {
		if err := s.guardLastAdmin(r.Context(), p.User.ClusterID, u.ID, "demote"); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return err
		}
	}
	if err := s.Store.BindRole(r.Context(), p.User.ClusterID, u.ID, role); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return err
	}
	for _, existing := range loginRoles {
		if existing == role {
			continue
		}
		if err := s.Store.UnbindRole(r.Context(), p.User.ClusterID, u.ID, existing); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return err
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "user.role.change", "ok", u.ID)
	if u.ID != p.User.ID {
		s.notifyUser(r.Context(), p.User.ClusterID, u.ID, noteRolesChanged, "Your role changed",
			p.User.Username+" changed your role to "+s.roleTitle(r.Context(), p.User.ClusterID, role)+".", "/me")
	}
	return nil
}

// clearLoginRoles leaves a user with no sign-in role: they keep only their
// grants on single items.
func (s *Server) clearLoginRoles(w http.ResponseWriter, r *http.Request, p *principal, u appdb.User, loginRoles []string) error {
	cur, err := s.Store.UserRoles(r.Context(), u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return err
	}
	for _, role := range cur {
		if containsString(loginRoles, role) && !s.mayActOnRole(r.Context(), p, role) {
			writeErr(w, http.StatusForbidden, "you cannot change a user whose role is at or above yours")
			return errForbidden("forbidden")
		}
	}
	if containsString(cur, rbac.SuperAdmin) {
		if err := s.guardLastAdmin(r.Context(), p.User.ClusterID, u.ID, "demote"); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return err
		}
	}
	for _, role := range loginRoles {
		if err := s.Store.UnbindRole(r.Context(), p.User.ClusterID, u.ID, role); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return err
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "user.role.change", "ok", u.ID+" none")
	if u.ID != p.User.ID {
		s.notifyUser(r.Context(), p.User.ClusterID, u.ID, noteRolesChanged, "Your role changed",
			p.User.Username+" removed your role. You keep access to the items you were given.", "/me")
	}
	return nil
}

func (s *Server) guardLastAdmin(ctx context.Context, clusterID, userID, action string) error {
	roles, err := s.Store.UserRoles(ctx, userID)
	if err != nil {
		return err
	}
	if !containsString(roles, rbac.SuperAdmin) {
		return nil
	}
	n, err := s.Store.CountEnabledAdmins(ctx, clusterID)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errConflict("cannot " + action + " the last SuperAdmin")
	}
	return nil
}

func (s *Server) userPublicJSON(ctx context.Context, u appdb.User) (map[string]any, error) {
	roles, err := s.Store.UserRoles(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	mfaEnabled := false
	if method, _, _, err := s.Store.GetMFAMethod(ctx, u.ID); err == nil && method != nil && method.Enabled {
		mfaEnabled = true
	}
	tokens, err := s.Store.ListTokens(ctx, u.ClusterID)
	if err != nil {
		return nil, err
	}
	activeTokens := 0
	for _, tok := range tokens {
		if tok.UserID == u.ID && tok.RevokedAt == nil {
			activeTokens++
		}
	}
	status := "active"
	if u.DisabledAt != nil {
		status = "disabled"
	}
	mfaStatus := "not_enrolled"
	if mfaEnabled {
		mfaStatus = "enabled"
	} else if u.MFARequired {
		mfaStatus = "required"
	}
	item := map[string]any{
		"id":              u.ID,
		"username":        u.Username,
		"display_name":    u.DisplayName,
		"kind":            firstNonEmpty(u.Kind, appdb.UserKindPerson),
		"roles":           roles,
		"status":          status,
		"mfa_enabled":     mfaEnabled,
		"mfa_required":    u.MFARequired,
		"mfa_status":      mfaStatus,
		"api_token_count": activeTokens,
		"protected":       false,
		"is_root":         u.ID == s.rootUserID(ctx, u.ClusterID),
	}
	prof := s.profileOf(ctx, u)
	item["email"] = prof.Email
	item["email_secondary"] = prof.EmailSecondary
	item["ip_allowlist_on"] = prof.IPAllowlistOn
	if av, _ := s.Store.GetUserAvatar(ctx, u.ID); av != nil {
		item["avatar_updated_at"] = av.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if !u.CreatedAt.IsZero() {
		item["created_at"] = u.CreatedAt.UTC().Format(time.RFC3339)
	}
	if u.LastLoginAt != nil {
		item["last_login_at"] = u.LastLoginAt.UTC().Format(time.RFC3339)
	}
	if u.DisabledAt != nil {
		item["disabled_at"] = u.DisabledAt.UTC().Format(time.RFC3339)
	}
	if containsString(roles, rbac.SuperAdmin) {
		n, err := s.Store.CountEnabledAdmins(ctx, u.ClusterID)
		if err != nil {
			return nil, err
		}
		item["protected"] = n <= 1 && u.DisabledAt == nil
	}
	return item, nil
}

func validUsername(s string) bool {
	if s == "" || len(s) > 64 || strings.EqualFold(s, "root") {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func asStringSlice(v any) []string {
	items, _ := v.([]string)
	return items
}

func lastLoginUnix(u appdb.User) int64 {
	if u.LastLoginAt == nil {
		return 0
	}
	return u.LastLoginAt.Unix()
}

func userDisabled(u appdb.User) bool {
	return u.DisabledAt != nil
}

// loginRoleNames lists the roles a person can sign in with: the built-in
// sign-in roles plus stored roles marked as sign-in roles.
func (s *Server) loginRoleNames(ctx context.Context, clusterID string) []string {
	out := append([]string{}, rbac.LoginRoles()...)
	for _, d := range s.roleDefs(ctx, clusterID) {
		if d.Login && d.Name != rbac.Automation && !containsString(out, d.Name) {
			out = append(out, d.Name)
		}
	}
	return out
}
