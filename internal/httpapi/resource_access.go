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

// Resource access gives a user or group permissions on one item (a
// workload, game server, Docker container or node) without a role. require
// checks it when a role does not grant the permission: the route says which
// item a request is about, and the item's grants decide. Only a fixed set
// of permissions can be granted per kind, never creating, deleting,
// migrating or changing what an item is attached to, so a grant cannot
// reach anything beyond its item.

const (
	accessWorkload   = "workload"
	accessGameServer = "gameserver"
	accessDocker     = "docker"
	accessNode       = "node"

	accessSubjectUser  = "user"
	accessSubjectGroup = "group"
)

// grantablePerms are what each kind of item can grant, in display order.
var grantablePerms = map[string][]string{
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
		if (g.SubjectKind == accessSubjectUser && g.SubjectID == p.User.ID) || (g.SubjectKind == accessSubjectGroup && groups[g.SubjectID]) {
			out = append(out, g)
		}
	}
	return out
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
	if kind == "" || !grantable(kind, perm) {
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

func grantable(kind, perm string) bool {
	return containsString(grantablePerms[kind], perm)
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

func (s *Server) accessJSON(ctx context.Context, clusterID string, grants []resourceGrant) []map[string]any {
	users := map[string]string{}
	if list, err := s.Store.ListUsers(ctx, clusterID); err == nil {
		for _, u := range list {
			users[u.ID] = u.Username
		}
	}
	groups := map[string]string{}
	if list, err := s.Store.ListGroups(ctx, clusterID); err == nil {
		for _, g := range list {
			groups[g.ID] = g.Name
		}
	}
	out := make([]map[string]any, 0, len(grants))
	for _, g := range grants {
		name := users[g.SubjectID]
		if g.SubjectKind == accessSubjectGroup {
			name = groups[g.SubjectID]
		}
		out = append(out, map[string]any{
			"id": g.ID, "subject_kind": g.SubjectKind, "subject_id": g.SubjectID, "subject_name": name,
			"resource_kind": g.ResourceKind, "resource_id": g.ResourceID, "permissions": g.Permissions,
			"created_by": g.CreatedBy, "created_at": g.CreatedAt,
		})
	}
	return out
}

// listResourceAccess is GET /iam/resource-access: every grant, and what each kind can grant.
func (s *Server) listResourceAccess(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UsersRolesManage)
	if err != nil {
		return
	}
	doc := s.resourceAccess(r.Context(), p.User.ClusterID)
	writeJSON(w, http.StatusOK, map[string]any{"grants": s.accessJSON(r.Context(), p.User.ClusterID, doc.Grants), "grantable": grantablePerms})
}

// myResourceAccess is GET /me/resource-access: the caller's own item grants.
func (s *Server) myResourceAccess(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": s.accessJSON(r.Context(), p.User.ClusterID, s.subjectGrants(r.Context(), p))})
}

// putResourceAccess is PUT /iam/resource-access: create a grant, or
// replace one when id is set. Empty permissions remove it.
func (s *Server) putResourceAccess(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UsersRolesManage)
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
	if in.SubjectKind != accessSubjectUser && in.SubjectKind != accessSubjectGroup {
		writeErr(w, http.StatusBadRequest, "subject_kind must be user or group")
		return
	}
	if _, ok := grantablePerms[in.ResourceKind]; !ok {
		writeErr(w, http.StatusBadRequest, "resource_kind must be workload, gameserver, docker or node")
		return
	}
	if in.SubjectID == "" || in.ResourceID == "" {
		writeErr(w, http.StatusBadRequest, "choose who and which item")
		return
	}
	ctx := r.Context()
	if !s.accessSubjectExists(ctx, p.User.ClusterID, in.SubjectKind, in.SubjectID) {
		writeErr(w, http.StatusNotFound, in.SubjectKind+" not found")
		return
	}
	perms := []string{}
	for _, perm := range in.Permissions {
		if !grantable(in.ResourceKind, perm) {
			writeErr(w, http.StatusBadRequest, perm+" cannot be granted on a single "+in.ResourceKind)
			return
		}
		perms = append(perms, perm)
	}
	perms = uniqueStrings(perms)
	if perms == nil {
		perms = []string{}
	}
	// Anything granted on an item includes seeing it.
	if len(perms) > 0 && !containsString(perms, readPerm[in.ResourceKind]) {
		perms = append([]string{readPerm[in.ResourceKind]}, perms...)
	}
	order := map[string]int{}
	for i, perm := range grantablePerms[in.ResourceKind] {
		order[perm] = i
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
	writeJSON(w, http.StatusOK, map[string]any{"grants": s.accessJSON(ctx, p.User.ClusterID, next.Grants), "grantable": grantablePerms})
}

// deleteResourceAccess is DELETE /iam/resource-access/{id}.
func (s *Server) deleteResourceAccess(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UsersRolesManage)
	if err != nil {
		return
	}
	if !s.requireWriter(w, r, p.User.ClusterID) {
		return
	}
	ctx := r.Context()
	doc := s.resourceAccess(ctx, p.User.ClusterID)
	next := resourceAccessDoc{}
	found := false
	for _, g := range doc.Grants {
		if g.ID == r.PathValue("id") {
			found = true
			continue
		}
		next.Grants = append(next.Grants, g)
	}
	if !found {
		writeErr(w, http.StatusNotFound, "grant not found")
		return
	}
	if err := s.saveResourceAccess(ctx, p.User.ClusterID, p.User.Username, next); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "access.grant.remove", "ok", r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"grants": s.accessJSON(ctx, p.User.ClusterID, next.Grants), "grantable": grantablePerms})
}

func (s *Server) accessSubjectExists(ctx context.Context, clusterID, kind, id string) bool {
	if kind == accessSubjectGroup {
		list, _ := s.Store.ListGroups(ctx, clusterID)
		for _, g := range list {
			if g.ID == id {
				return true
			}
		}
		return false
	}
	u, err := s.Store.GetUser(ctx, id)
	return err == nil && u != nil && u.ClusterID == clusterID
}
