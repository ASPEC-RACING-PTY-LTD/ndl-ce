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
// whose highest role is below it. SuperAdmins act on everything ranked at or
// below SuperAdmin; only the root SuperAdmin (iam_root.go) manages roles
// ranked above it and the people holding them.

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
// permissions reach them; the SuperAdmin and the automation identity always
// have everything. Approvals permissions come from the IAM settings.
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
		case role == rbac.SuperAdmin || role == rbac.Automation:
			add(rbac.GrantsForRoles([]string{role}))
		case ok && (!d.BuiltIn || d.Customized):
			add(d.Permissions)
		default:
			add(rbac.GrantsForRoles([]string{role}))
		}
	}
	access := s.iamSettings(ctx, clusterID).ApprovalsAccess
	for _, role := range roles {
		add(approvalsPerms(access[role]))
	}
	return out
}

// approvalsPerms keeps only the approvals permissions from a stored list.
func approvalsPerms(in []string) []string {
	var out []string
	for _, p := range in {
		if p == rbac.ApprovalsView || p == rbac.ApprovalsDecide {
			out = append(out, p)
		}
	}
	return out
}

// myRankFor is p's place in the hierarchy: root above everything, other
// SuperAdmins just above SuperAdmin (so roles above it stay out of reach).
func (s *Server) myRankFor(ctx context.Context, p *principal, defs []appdb.RoleDef) int {
	if s.isRoot(ctx, p) {
		return -noRank
	}
	if isOwner(p) {
		return superAdminRank(defs) - 1
	}
	return rankOf(defs, p.Roles)
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

func isOwner(p *principal) bool { return containsString(p.Roles, rbac.SuperAdmin) }

// mayActOnUser reports whether p may manage targetUserID (and its tokens).
func (s *Server) mayActOnUser(ctx context.Context, p *principal, targetUserID string) bool {
	if targetUserID == p.User.ID || p.User.ID == LocalRootUserID {
		return true
	}
	// The root account (the one that claimed the appliance) is managed only
	// by itself or from the host console, never by another admin.
	if root := s.rootUserID(ctx, p.User.ClusterID); root != "" && targetUserID == root {
		return false
	}
	roles, err := s.Store.UserRoles(ctx, targetUserID)
	if err != nil {
		return false
	}
	// SuperAdmins manage each other, but not people holding a role ranked
	// above SuperAdmin.
	defs := s.roleDefs(ctx, p.User.ClusterID)
	return s.myRankFor(ctx, p, defs) < rankOf(defs, roles)
}

// mayActOnRole reports whether p may edit, assign or remove the role.
func (s *Server) mayActOnRole(ctx context.Context, p *principal, role string) bool {
	defs := s.roleDefs(ctx, p.User.ClusterID)
	if isOwner(p) && role == rbac.SuperAdmin {
		return true
	}
	return s.myRankFor(ctx, p, defs) < rankOf(defs, []string{role})
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
	myRank := s.myRankFor(ctx, p, defs)
	root := s.isRoot(ctx, p)
	saRank := superAdminRank(defs)
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
			"owner":     d.Name == rbac.SuperAdmin,
			"above_top": d.Rank < saRank && d.Name != rbac.SuperAdmin,
			"editable":  d.Name != rbac.SuperAdmin && d.Name != rbac.Automation && (myRank < d.Rank) && rbac.Authorize(p.Grants, rbac.RolesManage),
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
			"editable": rbac.Authorize(p.Grants, rbac.IdentityGroupManage) && myRank < rankOf(defs, groles),
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
		"can_order": isOwner(p), "can_order_above_top": root, "is_root": root,
		"can_manage_roles": rbac.Authorize(p.Grants, rbac.RolesManage),
	})
}

var roleNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)

func (s *Server) iamSaveRole(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.RolesManage)
	if err != nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("name")))
	if name == rbac.SuperAdmin {
		writeErr(w, http.StatusForbidden, "the SuperAdmin role always has every permission")
		return
	}
	if name == rbac.Automation {
		writeErr(w, http.StatusForbidden, "the Automation role is the identity flows run as and cannot be changed")
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
	switch {
	case d.BuiltIn:
		// Built-in names are fixed so ids and names keep matching.
		d.Title = existing.Title
	case d.Title == "":
		d.Title = name
	case roleIDFor(d.Title) != name:
		// Ids and names agree; a different name means a different role.
		writeErr(w, http.StatusBadRequest, "the role id must match its name: \""+d.Title+"\" needs the id \""+roleIDFor(d.Title)+"\" (create a new role to rename)")
		return
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

// roleIDFor is the id a role named title gets: lowercase, runs of other
// characters as one dash. The migration (0076) uses the same rule.
func roleIDFor(title string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(strings.TrimSpace(title)) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	id := strings.Trim(b.String(), "-")
	if len(id) > 40 {
		id = id[:40]
	}
	return id
}

func (s *Server) iamDeleteRole(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.RolesManage)
	if err != nil {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == rbac.SuperAdmin {
		writeErr(w, http.StatusForbidden, "the SuperAdmin role cannot be deleted")
		return
	}
	if name == rbac.Automation {
		writeErr(w, http.StatusForbidden, "the Automation role is the identity flows run as and cannot be deleted")
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
		writeErr(w, http.StatusForbidden, "only the SuperAdmin can reorder the role hierarchy")
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
	ctx := r.Context()
	defs := s.roleDefs(ctx, p.User.ClusterID)
	var ordered []string
	if s.isRoot(ctx, p) {
		// Root may place roles above SuperAdmin.
		ordered = names
		if !containsString(ordered, rbac.SuperAdmin) {
			ordered = append([]string{rbac.SuperAdmin}, ordered...)
		}
	} else {
		// Other SuperAdmins order what is below SuperAdmin; roles above it
		// keep their places, and nothing new goes above it.
		above := rolesAboveSuperAdmin(defs)
		sort.SliceStable(above, func(i, j int) bool { return rankOf(defs, above[i:i+1]) < rankOf(defs, above[j:j+1]) })
		ordered = append(above, rbac.SuperAdmin)
		for _, n := range names {
			if n != rbac.SuperAdmin && !containsString(above, n) {
				ordered = append(ordered, n)
			}
		}
	}
	if err := s.Store.SetRoleOrder(ctx, p.User.ClusterID, ordered); err != nil {
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
	if s.myRankFor(ctx, p, defs) >= rankOf(defs, cur) {
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
