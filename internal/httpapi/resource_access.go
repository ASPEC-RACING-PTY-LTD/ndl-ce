package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Resource access gives a user, group or role permissions directly, on top
// of what roles give: either everywhere ("cluster" scope, merged into the
// principal's grants when it signs in) or on one item (a workload, game
// server, Docker container or node). require checks item grants when the
// merged grants do not cover a request: the route says which item it is
// about, and that item's grants decide.
//
// Handing out permissions needs permissions.assign: you give only what you
// hold, only to users, groups and roles ranked below you, and only from
// Admin rank up. permissions.assign.override lifts those limits, and only a
// SuperAdmin can give it or "*".

const (
	accessWorkload   = "workload"
	accessGameServer = "gameserver"
	accessDocker     = "docker"
	accessNode       = "node"

	// accessCluster is the scope of a grant that applies everywhere.
	accessCluster = "cluster"

	accessSubjectUser  = "user"
	accessSubjectGroup = "group"
	accessSubjectRole  = "role"
)

// suggestedPerms are the permissions that act on each kind of item, in
// display order. Any permission can be granted; these are the ones the
// item's own pages check.
var suggestedPerms = map[string][]string{
	accessWorkload: {
		rbac.ComputeRead, rbac.ComputeLifecycle, rbac.ComputeStart, rbac.ComputeStop, rbac.ComputeConsole, rbac.ComputeSnapshot,
		rbac.TerminalOpen, rbac.FilesRead, rbac.FilesDownload, rbac.FilesUpload, rbac.FilesCreate, rbac.FilesModify, rbac.FilesDelete,
		rbac.BackupRead, rbac.BackupCreate, rbac.MetricsRead, rbac.EventsRead,
	},
	accessGameServer: {
		rbac.GameServerRead, rbac.GameServerPower, rbac.GameServerConsole, rbac.GameServerFiles, rbac.GameServerConfig,
		rbac.GameServerContent, rbac.GameServerBackup, rbac.GameServerSchedule, rbac.GameServerReinstall,
	},
	accessDocker: {rbac.ComputeRead, rbac.ComputeLifecycle, rbac.TerminalOpen},
	accessNode: {
		rbac.NodeRead, rbac.MetricsRead, rbac.EventsRead, rbac.TerminalOpen,
		rbac.FilesRead, rbac.FilesDownload, rbac.FilesUpload, rbac.FilesCreate, rbac.FilesModify, rbac.FilesDelete,
	},
}

// readPerm is the permission that shows an item in its lists.
var readPerm = map[string]string{
	accessWorkload:   rbac.ComputeRead,
	accessGameServer: rbac.GameServerRead,
	accessDocker:     rbac.ComputeRead,
	accessNode:       rbac.NodeRead,
}

// baseGrants every signed-in user holds, role or not: their own profile,
// notifications, appearance and MFA.
var baseGrants = []string{rbac.IdentityRead, rbac.IdentityMFA}

type resourceGrant struct {
	ID           string    `json:"id"`
	SubjectKind  string    `json:"subject_kind"`
	SubjectID    string    `json:"subject_id"`
	ResourceKind string    `json:"resource_kind"`
	ResourceID   string    `json:"resource_id"`
	Permissions  []string  `json:"permissions"`
	CreatedBy    string    `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type resourceAccessDoc struct {
	Grants []resourceGrant `json:"grants"`
}

var (
	accessCacheMu sync.Mutex
	accessCache   = map[string]struct {
		doc resourceAccessDoc
		at  time.Time
	}{}
)

const accessCacheTTL = 15 * time.Second

func (s *Server) resourceAccess(ctx context.Context, clusterID string) resourceAccessDoc {
	accessCacheMu.Lock()
	if c, ok := accessCache[clusterID]; ok && time.Since(c.at) < accessCacheTTL {
		accessCacheMu.Unlock()
		return c.doc
	}
	accessCacheMu.Unlock()
	var doc resourceAccessDoc
	if raw, err := s.Store.ResourceAccess(ctx, clusterID); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &doc)
	}
	accessCacheMu.Lock()
	accessCache[clusterID] = struct {
		doc resourceAccessDoc
		at  time.Time
	}{doc, time.Now()}
	accessCacheMu.Unlock()
	return doc
}

func (s *Server) saveResourceAccess(ctx context.Context, clusterID, by string, doc resourceAccessDoc) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := s.Store.PutResourceAccess(ctx, clusterID, raw, by); err != nil {
		return err
	}
	accessCacheMu.Lock()
	delete(accessCache, clusterID)
	accessCacheMu.Unlock()
	return nil
}

// subjectGrants are the grants that apply to the user, directly or through a group.
func (s *Server) subjectGrants(ctx context.Context, p *principal) []resourceGrant {
	if p == nil || p.User.ID == "" {
		return nil
	}
	doc := s.resourceAccess(ctx, p.User.ClusterID)
	if len(doc.Grants) == 0 {
		return nil
	}
	groups := map[string]bool{}
	needGroups := false
	for _, g := range doc.Grants {
		needGroups = needGroups || g.SubjectKind == accessSubjectGroup
	}
	if needGroups {
		if list, err := s.Store.ListGroups(ctx, p.User.ClusterID); err == nil {
			for _, grp := range list {
				members, _ := s.Store.ListGroupMembers(ctx, p.User.ClusterID, grp.ID)
				if containsString(members, p.User.ID) {
					groups[grp.ID] = true
				}
			}
		}
	}
	var out []resourceGrant
	for _, g := range doc.Grants {
		if (g.SubjectKind == accessSubjectUser && g.SubjectID == p.User.ID) ||
			(g.SubjectKind == accessSubjectGroup && groups[g.SubjectID]) ||
			(g.SubjectKind == accessSubjectRole && containsString(p.Roles, g.SubjectID)) {
			out = append(out, g)
		}
	}
	return out
}

// clusterGrants are the permissions granted to the user everywhere,
// directly or through a group or role. They merge with role grants.
func (s *Server) clusterGrants(ctx context.Context, p *principal) []string {
	var out []string
	for _, g := range s.subjectGrants(ctx, p) {
		if g.ResourceKind == accessCluster {
			out = append(out, g.Permissions...)
		}
	}
	return out
}

// mergeGrants adds extra permissions to grants without repeats.
func mergeGrants(grants []string, extra ...string) []string {
	for _, g := range extra {
		if !rbac.Authorize(grants, g) || (g == rbac.All && !containsString(grants, rbac.All)) {
			grants = append(grants, g)
		}
	}
	return grants
}

// knownPerm reports whether perm can be granted at all.
func knownPerm(perm string) bool {
	switch perm {
	case rbac.All, rbac.ApprovalsView, rbac.ApprovalsDecide:
		return true
	}
	return containsString(rbac.AllPermissions(), perm)
}

// itemPerms are the permissions the user holds on one item through grants.
func (s *Server) itemPerms(ctx context.Context, p *principal, kind, id string) []string {
	var out []string
	for _, g := range s.subjectGrants(ctx, p) {
		if g.ResourceKind == kind && strings.EqualFold(g.ResourceID, id) {
			out = append(out, g.Permissions...)
		}
	}
	return out
}

// scopedIDs lists the items of a kind the user may see through grants. ok
// is false when a role already shows them every item, so lists need no
// filtering.
func (s *Server) scopedIDs(ctx context.Context, p *principal, kind string) (ids map[string]bool, limited bool) {
	if p == nil || rbac.Authorize(p.roleGrants(), readPerm[kind]) {
		return nil, false
	}
	ids = map[string]bool{}
	for _, g := range s.subjectGrants(ctx, p) {
		if g.ResourceKind == kind && rbac.Authorize(g.Permissions, readPerm[kind]) {
			ids[strings.ToLower(g.ResourceID)] = true
		}
	}
	return ids, true
}

// routeItem says which item a request is about, from its route. list is
// true for the list of a kind.
func (s *Server) routeItem(r *http.Request, clusterID string) (kind, id string, list bool) {
	pat := r.Pattern
	if i := strings.IndexByte(pat, ' '); i >= 0 {
		pat = pat[i+1:]
	}
	switch {
	case pat == "/api/v1/workloads":
		return accessWorkload, "", r.Method == http.MethodGet
	case strings.HasPrefix(pat, "/api/v1/workloads/{id}"):
		return accessWorkload, r.PathValue("id"), false
	case pat == "/api/v1/game-servers":
		return accessGameServer, "", r.Method == http.MethodGet
	case strings.HasPrefix(pat, "/api/v1/game-servers/{id}"):
		return accessGameServer, r.PathValue("id"), false
	case pat == "/api/v1/nodes":
		return accessNode, "", r.Method == http.MethodGet
	case strings.HasPrefix(pat, "/api/v1/nodes/{id}"):
		return accessNode, r.PathValue("id"), false
	case pat == "/api/v1/docker":
		return accessDocker, "", r.Method == http.MethodGet
	case strings.HasPrefix(pat, "/api/v1/docker/machines/{machine_id}/containers/{container_id}"):
		return accessDocker, s.dockerItemID(r.Context(), clusterID, r.PathValue("machine_id"), r.PathValue("container_id")), false
	}
	return "", "", false
}

// dockerItemID is machine/name of a container, so a grant outlives the
// container being recreated with a new engine ID.
func (s *Server) dockerItemID(ctx context.Context, clusterID, machineID, containerID string) string {
	inv, err := s.dockerInventory(ctx, clusterID, false)
	if err != nil {
		return ""
	}
	if c := findDockerContainer(inv, machineID, containerID); c != nil {
		return dockerAccessID(*c)
	}
	return ""
}

func dockerAccessID(c docker.Container) string {
	return c.MachineID + "/" + strings.TrimPrefix(c.Name, "/")
}

// scopedAllow lets a request through on the item's grants when the user's
// roles do not hold perm. For the rest of the request the principal holds
// the item's granted permissions (and the base ones), so handlers' own
// checks see them; nothing else is added.
func (s *Server) scopedAllow(r *http.Request, p *principal, perm string) bool {
	kind, id, list := s.routeItem(r, p.User.ClusterID)
	if kind == "" {
		return false
	}
	ctx := r.Context()
	var perms []string
	if list {
		if perm != readPerm[kind] {
			return false
		}
		ids, _ := s.scopedIDs(ctx, p, kind)
		if len(ids) == 0 {
			return false
		}
		perms = []string{perm}
	} else {
		if id == "" {
			return false
		}
		perms = s.itemPerms(ctx, p, kind, id)
		if !rbac.Authorize(perms, perm) {
			return false
		}
	}
	p.base = p.roleGrants()
	p.Grants = uniqueStrings(append(append(append([]string(nil), p.base...), perms...), baseGrants...))
	p.Scoped = true
	return true
}

// roleGrants are the grants from roles, before any item grant was added.
func (p *principal) roleGrants() []string {
	if p.Scoped {
		return p.base
	}
	return p.Grants
}

// filterScoped keeps the items a scoped user may see.
func filterScoped[T any](items []T, ids map[string]bool, id func(T) string) []T {
	out := make([]T, 0, len(items))
	for _, it := range items {
		if ids[strings.ToLower(id(it))] {
			out = append(out, it)
		}
	}
	return out
}

// scopedDockerInventory keeps the granted containers and the machines and
// projects they are in, each showing only granted containers.
func scopedDockerInventory(inv docker.Inventory, ids map[string]bool) docker.Inventory {
	keep := func(c docker.Container) bool { return ids[strings.ToLower(dockerAccessID(c))] }
	projects := func(in []docker.Project) []docker.Project {
		var out []docker.Project
		for _, pr := range in {
			var kept []docker.Container
			for _, c := range pr.Containers {
				if keep(c) {
					kept = append(kept, c)
				}
			}
			if len(kept) == 0 {
				continue
			}
			pr.Containers = kept
			pr.Services, pr.Running, pr.Stopped = len(kept), 0, 0
			out = append(out, pr)
		}
		return out
	}
	out := docker.Inventory{ObservedAt: inv.ObservedAt}
	for _, c := range inv.Containers {
		if keep(c) {
			out.Containers = append(out.Containers, c)
		}
	}
	out.Projects = projects(inv.Projects)
	for _, m := range inv.Machines {
		m.Projects = projects(m.Projects)
		if len(m.Projects) == 0 {
			continue
		}
		m.ProjectCount, m.ContainerN = len(m.Projects), 0
		for _, pr := range m.Projects {
			m.ContainerN += len(pr.Containers)
		}
		out.Machines = append(out.Machines, m)
	}
	out.Summary = docker.Summary{Machines: len(out.Machines), Projects: len(out.Projects), Containers: len(out.Containers)}
	return out
}

// --- API ---

// assignablePerms is every permission that can be handed out, in catalog order.
func assignablePerms() []string {
	return append([]string{rbac.All}, append(rbac.AllPermissions(), rbac.ApprovalsView, rbac.ApprovalsDecide)...)
}

func (s *Server) accessListJSON(ctx context.Context, clusterID string, grants []resourceGrant) map[string]any {
	return map[string]any{"grants": s.accessJSON(ctx, clusterID, grants), "suggested": suggestedPerms, "permissions": assignablePerms()}
}

func (s *Server) accessJSON(ctx context.Context, clusterID string, grants []resourceGrant) []map[string]any {
	names := map[string]string{}
	if list, err := s.Store.ListUsers(ctx, clusterID); err == nil {
		for _, u := range list {
			names[accessSubjectUser+":"+u.ID] = u.Username
		}
	}
	if list, err := s.Store.ListGroups(ctx, clusterID); err == nil {
		for _, g := range list {
			names[accessSubjectGroup+":"+g.ID] = g.Name
		}
	}
	for _, d := range s.roleDefs(ctx, clusterID) {
		names[accessSubjectRole+":"+d.Name] = firstNonEmpty(d.Title, d.Name)
	}
	out := make([]map[string]any, 0, len(grants))
	for _, g := range grants {
		out = append(out, map[string]any{
			"id": g.ID, "subject_kind": g.SubjectKind, "subject_id": g.SubjectID, "subject_name": names[g.SubjectKind+":"+g.SubjectID],
			"resource_kind": g.ResourceKind, "resource_id": g.ResourceID, "permissions": g.Permissions,
			"created_by": g.CreatedBy, "created_at": g.CreatedAt,
		})
	}
	return out
}

// listResourceAccess is GET /iam/resource-access: every direct grant, the
// permissions that act on each kind of item, and every permission.
func (s *Server) listResourceAccess(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.PermissionsAssign)
	if err != nil {
		return
	}
	doc := s.resourceAccess(r.Context(), p.User.ClusterID)
	writeJSON(w, http.StatusOK, s.accessListJSON(r.Context(), p.User.ClusterID, doc.Grants))
}

// myResourceAccess is GET /me/resource-access: the caller's own direct grants.
func (s *Server) myResourceAccess(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": s.accessJSON(r.Context(), p.User.ClusterID, s.subjectGrants(r.Context(), p))})
}

// mayAssign applies the rules for giving or taking back permissions: a
// SuperAdmin (or the host console) may do anything; holders of
// permissions.assign.override skip the rank and holding limits; everyone
// else needs Admin rank or above, may give only what they hold, and may
// target only users, groups and roles ranked below them. Only a SuperAdmin
// gives "*" or the override.
func (s *Server) mayAssign(ctx context.Context, p *principal, subjectKind, subjectID string, perms []string) (int, string) {
	owner := isOwner(p) || p.User.ID == LocalRootUserID
	for _, perm := range perms {
		if (perm == rbac.All || perm == rbac.PermissionsAssignOverride) && !owner {
			return http.StatusForbidden, "only a SuperAdmin can give " + perm
		}
	}
	if owner || rbac.Authorize(p.Grants, rbac.PermissionsAssignOverride) {
		return 0, ""
	}
	if !s.isAdminOrAbove(ctx, p) {
		return http.StatusForbidden, "assigning permissions needs Admin rank or above"
	}
	for _, perm := range perms {
		if !rbac.Authorize(p.Grants, perm) {
			return http.StatusForbidden, "you cannot give " + perm + " because you do not have it"
		}
	}
	below := "you can only assign to users, groups and roles ranked below you"
	switch subjectKind {
	case accessSubjectUser:
		if subjectID == p.User.ID || !s.mayActOnUser(ctx, p, subjectID) {
			return http.StatusForbidden, below
		}
	case accessSubjectRole:
		if containsString(p.Roles, subjectID) || !s.mayActOnRole(ctx, p, subjectID) {
			return http.StatusForbidden, below
		}
	case accessSubjectGroup:
		members, _ := s.Store.ListGroupMembers(ctx, p.User.ClusterID, subjectID)
		for _, m := range members {
			if m == p.User.ID || !s.mayActOnUser(ctx, p, m) {
				return http.StatusForbidden, below + ": the group has a member at or above your rank"
			}
		}
	}
	return 0, ""
}

// putResourceAccess is PUT /iam/resource-access: create a grant, or
// replace one when id is set. Empty permissions remove it.
func (s *Server) putResourceAccess(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.PermissionsAssign)
	if err != nil {
		return
	}
	if !s.requireWriter(w, r, p.User.ClusterID) {
		return
	}
	var in resourceGrant
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	in.SubjectID, in.ResourceID = strings.TrimSpace(in.SubjectID), strings.TrimSpace(in.ResourceID)
	if in.ResourceKind == "" {
		in.ResourceKind = accessCluster
	}
	if in.SubjectKind != accessSubjectUser && in.SubjectKind != accessSubjectGroup && in.SubjectKind != accessSubjectRole {
		writeErr(w, http.StatusBadRequest, "subject_kind must be user, group or role")
		return
	}
	if _, ok := suggestedPerms[in.ResourceKind]; !ok && in.ResourceKind != accessCluster {
		writeErr(w, http.StatusBadRequest, "resource_kind must be cluster, workload, gameserver, docker or node")
		return
	}
	if in.ResourceKind == accessCluster {
		in.ResourceID = ""
	}
	if in.SubjectID == "" || (in.ResourceKind != accessCluster && in.ResourceID == "") {
		writeErr(w, http.StatusBadRequest, "choose who and which item")
		return
	}
	ctx := r.Context()
	if !s.accessSubjectExists(ctx, p.User.ClusterID, in.SubjectKind, in.SubjectID) {
		writeErr(w, http.StatusNotFound, in.SubjectKind+" not found")
		return
	}
	var perms []string
	for _, perm := range uniqueStrings(in.Permissions) {
		if !knownPerm(perm) {
			writeErr(w, http.StatusBadRequest, "unknown permission "+perm)
			return
		}
		perms = append(perms, perm)
	}
	if perms == nil {
		perms = []string{}
	}
	// Anything granted on an item includes seeing it.
	if rp := readPerm[in.ResourceKind]; len(perms) > 0 && rp != "" && !rbac.Authorize(perms, rp) {
		perms = append([]string{rp}, perms...)
	}
	order := map[string]int{}
	for i, perm := range append(append([]string(nil), suggestedPerms[in.ResourceKind]...), assignablePerms()...) {
		if _, ok := order[perm]; !ok {
			order[perm] = i
		}
	}
	sort.SliceStable(perms, func(i, j int) bool { return order[perms[i]] < order[perms[j]] })

	doc := s.resourceAccess(ctx, p.User.ClusterID)
	next := resourceAccessDoc{}
	var kept *resourceGrant
	for _, g := range doc.Grants {
		same := (in.ID != "" && g.ID == in.ID) ||
			(in.ID == "" && g.SubjectKind == in.SubjectKind && g.SubjectID == in.SubjectID && g.ResourceKind == in.ResourceKind && strings.EqualFold(g.ResourceID, in.ResourceID))
		if same {
			cp := g
			kept = &cp
			continue
		}
		next.Grants = append(next.Grants, g)
	}
	if in.ID != "" && kept == nil {
		writeErr(w, http.StatusNotFound, "grant not found")
		return
	}
	if kept != nil && (kept.SubjectKind != in.SubjectKind || kept.SubjectID != in.SubjectID) {
		writeErr(w, http.StatusBadRequest, "a grant's subject cannot change; remove it and give a new one")
		return
	}
	// The rules cover what is given and, when editing, what is taken away.
	check := perms
	if kept != nil {
		check = uniqueStrings(append(append([]string(nil), perms...), kept.Permissions...))
	}
	if status, msg := s.mayAssign(ctx, p, in.SubjectKind, in.SubjectID, check); status != 0 {
		s.audit(r, p.User.ClusterID, p.User.ID, "access.grant.set", "denied", msg)
		writeErr(w, status, msg)
		return
	}
	action := "access.grant.remove"
	if len(perms) > 0 {
		g := resourceGrant{
			ID: uuid.NewString(), SubjectKind: in.SubjectKind, SubjectID: in.SubjectID,
			ResourceKind: in.ResourceKind, ResourceID: in.ResourceID, Permissions: perms,
			CreatedBy: p.User.Username, CreatedAt: s.now(),
		}
		if kept != nil {
			g.ID, g.CreatedAt = kept.ID, kept.CreatedAt
		}
		next.Grants = append(next.Grants, g)
		action = "access.grant.set"
	}
	if err := s.saveResourceAccess(ctx, p.User.ClusterID, p.User.Username, next); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, action, "ok", in.SubjectKind+":"+in.SubjectID+" "+in.ResourceKind+":"+in.ResourceID+" "+strings.Join(perms, ","))
	writeJSON(w, http.StatusOK, s.accessListJSON(ctx, p.User.ClusterID, next.Grants))
}

// deleteResourceAccess is DELETE /iam/resource-access/{id}.
func (s *Server) deleteResourceAccess(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.PermissionsAssign)
	if err != nil {
		return
	}
	if !s.requireWriter(w, r, p.User.ClusterID) {
		return
	}
	ctx := r.Context()
	doc := s.resourceAccess(ctx, p.User.ClusterID)
	next := resourceAccessDoc{}
	var found *resourceGrant
	for _, g := range doc.Grants {
		if g.ID == r.PathValue("id") {
			cp := g
			found = &cp
			continue
		}
		next.Grants = append(next.Grants, g)
	}
	if found == nil {
		writeErr(w, http.StatusNotFound, "grant not found")
		return
	}
	if status, msg := s.mayAssign(ctx, p, found.SubjectKind, found.SubjectID, found.Permissions); status != 0 {
		s.audit(r, p.User.ClusterID, p.User.ID, "access.grant.remove", "denied", msg)
		writeErr(w, status, msg)
		return
	}
	if err := s.saveResourceAccess(ctx, p.User.ClusterID, p.User.Username, next); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "access.grant.remove", "ok", r.PathValue("id"))
	writeJSON(w, http.StatusOK, s.accessListJSON(ctx, p.User.ClusterID, next.Grants))
}

func (s *Server) accessSubjectExists(ctx context.Context, clusterID, kind, id string) bool {
	switch kind {
	case accessSubjectGroup:
		list, _ := s.Store.ListGroups(ctx, clusterID)
		for _, g := range list {
			if g.ID == id {
				return true
			}
		}
		return false
	case accessSubjectRole:
		for _, d := range s.roleDefs(ctx, clusterID) {
			if d.Name == id && d.Name != rbac.Automation {
				return true
			}
		}
		return false
	}
	u, err := s.Store.GetUser(ctx, id)
	return err == nil && u != nil && u.ClusterID == clusterID
}
