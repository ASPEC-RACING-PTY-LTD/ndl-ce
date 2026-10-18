package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Access control: roles, groups and their permissions in one place, with a
// role hierarchy. Roles are ordered (rank 0 is the highest). A user may only
// act on roles ranked below their own highest role, and on users and groups
// whose highest role is below it. The Owner role acts on everything except
// that it can never be deleted.

const noRank = 1 << 20

type roleDefCache struct {
	at   time.Time
	defs []appdb.RoleDef
}

var roleDefsCache sync.Map // clusterID -> roleDefCache

func (s *Server) roleDefs(ctx context.Context, clusterID string) []appdb.RoleDef {
	if v, ok := roleDefsCache.Load(clusterID); ok {
		if c := v.(roleDefCache); time.Since(c.at) < 5*time.Second {
			return c.defs
		}
	}
	defs, err := s.Store.ListRoleDefs(ctx, clusterID)
	if err != nil {
		return nil
	}
	roleDefsCache.Store(clusterID, roleDefCache{at: time.Now(), defs: defs})
	return defs
}

func forgetRoleDefs(clusterID string) { roleDefsCache.Delete(clusterID) }

// grantsForRoles resolves permissions from the stored roles. Built-in roles
// that were never edited keep the built-in list, so upgrades that add
// permissions reach them; the Owner always has everything.
func (s *Server) grantsForRoles(ctx context.Context, clusterID string, roles []string) []string {
	defs := map[string]appdb.RoleDef{}
	for _, d := range s.roleDefs(ctx, clusterID) {
		defs[d.Name] = d
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(perms []string) {
		for _, p := range perms {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				out = append(out, p)
			}
		}
	}
	for _, role := range roles {
		d, ok := defs[role]
		switch {
		case role == rbac.Admin:
			add(rbac.GrantsForRoles([]string{rbac.Admin}))
		case ok && (!d.BuiltIn || d.Customized):
			add(d.Permissions)
		default:
			add(rbac.GrantsForRoles([]string{role}))
		}
	}
	return out
}

func rankOf(defs []appdb.RoleDef, roles []string) int {
	best := noRank
	for _, r := range roles {
		for _, d := range defs {
			if d.Name == r && d.Rank < best {
				best = d.Rank
			}
		}
	}
	return best
}

func isOwner(p *principal) bool { return containsString(p.Roles, rbac.Admin) }

// mayActOnUser reports whether p may manage targetUserID (and its tokens).
func (s *Server) mayActOnUser(ctx context.Context, p *principal, targetUserID string) bool {
	if isOwner(p) || targetUserID == p.User.ID {
		return true
	}
	roles, err := s.Store.UserRoles(ctx, targetUserID)
	if err != nil {
		return false
	}
	defs := s.roleDefs(ctx, p.User.ClusterID)
	return rankOf(defs, p.Roles) < rankOf(defs, roles)
}

// mayActOnRole reports whether p may edit, assign or remove the role.
func (s *Server) mayActOnRole(ctx context.Context, p *principal, role string) bool {
	if isOwner(p) {
		return true
	}
	defs := s.roleDefs(ctx, p.User.ClusterID)
	return rankOf(defs, p.Roles) < rankOf(defs, []string{role})
}

func (s *Server) guardUserHierarchy(w http.ResponseWriter, r *http.Request, p *principal, targetUserID string) bool {
	if s.mayActOnUser(r.Context(), p, targetUserID) {
		return true
	}
	writeErr(w, http.StatusForbidden, "that user holds a role at or above yours in the role hierarchy")
	return false
}

func (s *Server) iamAccess(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	ctx := r.Context()
	defs := s.roleDefs(ctx, p.User.ClusterID)
	users, _ := s.Store.ListUsers(ctx, p.User.ClusterID)
	counts := map[string]int{}
	for _, u := range users {
		rs, _ := s.Store.UserRoles(ctx, u.ID)
		for _, r := range uniqueStrings(rs) {
			counts[r]++
		}
	}
	myRank := rankOf(defs, p.Roles)
	if isOwner(p) {
		myRank = -1
	}
	roles := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		perms := d.Permissions
		if d.BuiltIn && !d.Customized {
			perms = rbac.GrantsForRoles([]string{d.Name})
		}
		if perms == nil {
			perms = []string{}
		}
		roles = append(roles, map[string]any{
			"name": d.Name, "title": firstNonEmpty(d.Title, d.Name), "summary": d.Summary, "permissions": perms,
			"rank": d.Rank, "builtin": d.BuiltIn, "login": d.Login, "user_count": counts[d.Name],
			"owner":    d.Name == rbac.Admin,
			"editable": d.Name != rbac.Admin && (myRank < d.Rank) && rbac.Authorize(p.Grants, rbac.RolesManage),
		})
	}
	groups, _ := s.Store.ListGroups(ctx, p.User.ClusterID)
	gout := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		members, _ := s.Store.ListGroupMembers(ctx, p.User.ClusterID, g.ID)
		groles, _ := s.Store.ListGroupRoles(ctx, p.User.ClusterID, g.ID)
		if members == nil {
			members = []string{}
		}
		if groles == nil {
			groles = []string{}
		}
		gout = append(gout, map[string]any{
			"id": g.ID, "name": g.Name, "member_ids": members, "roles": groles,
			"editable": rbac.Authorize(p.Grants, rbac.IdentityGroupManage) && (isOwner(p) || myRank < rankOf(defs, groles)),
		})
	}
	people := make([]map[string]any, 0, len(users))
	for _, u := range users {
		people = append(people, map[string]any{"id": u.ID, "username": u.Username, "display_name": u.DisplayName})
	}
	perms := rbac.AllPermissions()
	sort.Strings(perms)
	writeJSON(w, http.StatusOK, map[string]any{
		"roles": roles, "groups": gout, "users": people, "permissions": perms,
		"can_order": isOwner(p), "can_manage_roles": rbac.Authorize(p.Grants, rbac.RolesManage),
	})
}

var roleNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)

func (s *Server) iamSaveRole(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.RolesManage)
	if err != nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("name")))
	if name == rbac.Admin {
		writeErr(w, http.StatusForbidden, "the Owner role always has every permission")
		return
	}
	if !roleNameRe.MatchString(name) {
		writeErr(w, http.StatusBadRequest, "role names are 2-40 lowercase letters, digits or dashes")
		return
	}
	var req struct {
		Title       string   `json:"title"`
		Summary     string   `json:"summary"`
		Permissions []string `json:"permissions"`
		Login       *bool    `json:"login"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx := r.Context()
	defs := s.roleDefs(ctx, p.User.ClusterID)
	var existing *appdb.RoleDef
	maxRank := 0
	for i := range defs {
		if defs[i].Name == name {
			existing = &defs[i]
		}
		if defs[i].Rank > maxRank {
			maxRank = defs[i].Rank
		}
	}
	if existing != nil && !s.mayActOnRole(ctx, p, name) {
		writeErr(w, http.StatusForbidden, "that role is at or above yours in the role hierarchy")
		return
	}
	known := map[string]struct{}{}
	for _, perm := range rbac.AllPermissions() {
		known[perm] = struct{}{}
	}
	perms := make([]string, 0, len(req.Permissions))
	for _, perm := range uniqueStrings(req.Permissions) {
		if _, ok := known[perm]; !ok {
			writeErr(w, http.StatusBadRequest, "unknown permission "+perm)
			return
		}
		if !isOwner(p) && !rbac.Authorize(p.Grants, perm) {
			writeErr(w, http.StatusForbidden, "you cannot grant "+perm+" because you do not have it")
			return
		}
		perms = append(perms, perm)
	}
	d := appdb.RoleDef{ClusterID: p.User.ClusterID, Name: name, Title: strings.TrimSpace(req.Title), Summary: strings.TrimSpace(req.Summary), Permissions: perms, Login: true, Rank: maxRank + 1}
	if existing != nil {
		d.ID, d.Rank, d.BuiltIn, d.Login = existing.ID, existing.Rank, existing.BuiltIn, existing.Login
		d.Customized = existing.BuiltIn
	}
	if req.Login != nil {
		d.Login = *req.Login
	}
	if d.Title == "" {
		d.Title = name
	}
	if err := s.Store.SaveRoleDef(ctx, d); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	forgetRoleDefs(p.User.ClusterID)
	action := "role.update"
	if existing == nil {
		action = "role.create"
	}
	s.audit(r, p.User.ClusterID, p.User.ID, action, "ok", name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) iamDeleteRole(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.RolesManage)
	if err != nil {
		return
	}
	name := r.PathValue("name")
	if name == rbac.Admin {
		writeErr(w, http.StatusForbidden, "the Owner role cannot be deleted")
		return
	}
	ctx := r.Context()
	if !s.mayActOnRole(ctx, p, name) {
		writeErr(w, http.StatusForbidden, "that role is at or above yours in the role hierarchy")
		return
	}
	// Users who held the role can move to another one first.
	if to := strings.TrimSpace(r.URL.Query().Get("reassign")); to != "" && to != name {
		if !s.mayActOnRole(ctx, p, to) {
			writeErr(w, http.StatusForbidden, "you cannot assign that role")
			return
		}
		users, _ := s.Store.ListUsers(ctx, p.User.ClusterID)
		for _, u := range users {
			rs, _ := s.Store.UserRoles(ctx, u.ID)
			if containsString(rs, name) {
				_ = s.Store.BindRole(ctx, p.User.ClusterID, u.ID, to)
			}
		}
	}
	if err := s.Store.DeleteRoleDef(ctx, p.User.ClusterID, name); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	forgetRoleDefs(p.User.ClusterID)
	s.audit(r, p.User.ClusterID, p.User.ID, "role.delete", "ok", name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) iamRoleOrder(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.RolesManage)
	if err != nil {
		return
	}
	if !isOwner(p) {
		writeErr(w, http.StatusForbidden, "only the Owner can reorder the role hierarchy")
		return
	}
	var req struct {
		Names []string `json:"names"`
	}
	if err := readJSON(r, &req); err != nil || len(req.Names) == 0 {
		writeErr(w, http.StatusBadRequest, "names is required")
		return
	}
	names := uniqueStrings(req.Names)
	// The Owner role always stays on top.
	ordered := []string{rbac.Admin}
	for _, n := range names {
		if n != rbac.Admin {
			ordered = append(ordered, n)
		}
	}
	if err := s.Store.SetRoleOrder(r.Context(), p.User.ClusterID, ordered); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	forgetRoleDefs(p.User.ClusterID)
	s.audit(r, p.User.ClusterID, p.User.ID, "role.order", "ok", strings.Join(ordered, ","))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) iamSaveGroup(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityGroupManage)
	if err != nil {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	g, err := s.Store.GetGroup(ctx, p.User.ClusterID, id)
	if err != nil || g == nil {
		writeErr(w, http.StatusNotFound, "group not found")
		return
	}
	var req struct {
		Name    string    `json:"name"`
		Members *[]string `json:"member_ids"`
		Roles   *[]string `json:"roles"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	defs := s.roleDefs(ctx, p.User.ClusterID)
	cur, _ := s.Store.ListGroupRoles(ctx, p.User.ClusterID, id)
	if !isOwner(p) && rankOf(defs, p.Roles) >= rankOf(defs, cur) {
		writeErr(w, http.StatusForbidden, "that group holds a role at or above yours in the role hierarchy")
		return
	}
	var members, roles []string
	if req.Members != nil {
		members = uniqueStrings(*req.Members)
		if members == nil {
			members = []string{}
		}
	}
	if req.Roles != nil {
		roles = uniqueStrings(*req.Roles)
		if roles == nil {
			roles = []string{}
		}
		for _, role := range roles {
			if !s.mayActOnRole(ctx, p, role) {
				writeErr(w, http.StatusForbidden, "you cannot give a group the "+role+" role")
				return
			}
		}
	}
	if err := s.Store.SetGroup(ctx, p.User.ClusterID, id, strings.TrimSpace(req.Name), "", members, roles); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "group.update", "ok", id)
	w.WriteHeader(http.StatusNoContent)
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
