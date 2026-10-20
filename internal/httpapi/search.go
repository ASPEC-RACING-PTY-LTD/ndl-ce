package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/iojail"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Global search. Primary indexing (names and basic details of users,
// groups, roles, workloads, nodes and more) is always on and refreshed in
// the background. Deep indexing (file names inside system containers, and
// on the host if allowed) is opt in, runs as a resumable job, and is
// configured on the Search settings page.

const (
	primaryRefresh = 2 * time.Minute
	// deepBatch is how many files are indexed between checkpoints.
	deepBatch = 200
	// permSuperAdmin marks documents only SuperAdmins may see (host files).
	permSuperAdmin = "@superadmin"
)

// SearchSettings is the search document in ui_settings.
type SearchSettings struct {
	Deep    DeepSearchSettings `json:"deep"`
	Filters []SearchFilter     `json:"filters"`
	// HiddenKinds are left out of results for everyone.
	HiddenKinds []string `json:"hidden_kinds"`
}

// DeepSearchSettings says what a deep indexing run covers.
type DeepSearchSettings struct {
	Files     bool     `json:"files"`
	HostFiles bool     `json:"host_files"`
	Roots     []string `json:"roots"`
	Exclude   []string `json:"exclude"`
	MaxDepth  int      `json:"max_depth"`
	MaxFiles  int      `json:"max_files"`
}

// SearchFilter is a saved filter shown as a chip in the search box.
type SearchFilter struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Kinds  []string `json:"kinds"`
	Status string   `json:"status,omitempty"`
	Query  string   `json:"query,omitempty"`
}

func defaultSearchSettings() SearchSettings {
	return SearchSettings{
		Deep: DeepSearchSettings{
			Roots:    []string{"/root", "/home", "/srv", "/opt", "/etc", "/var/www"},
			Exclude:  []string{"node_modules", ".git", ".cache", "__pycache__", "proc", "sys", "dev", "run", "tmp"},
			MaxDepth: 8, MaxFiles: 200000,
		},
		Filters: []SearchFilter{},
	}
}

// searchKinds lists every kind of result, for filters and settings.
var searchKinds = []struct{ ID, Label string }{
	{"user", "Users"}, {"group", "Groups"}, {"role", "Roles"},
	{"workload", "Workloads"}, {"vm", "Virtual machines"}, {"oci", "OCI containers"},
	{"gameserver", "Game servers"}, {"stack", "OCI apps"}, {"node", "Nodes"},
	{"storage", "Storage pools"}, {"network", "Networks"}, {"backup", "Backup policies"},
	{"template", "Templates"}, {"registry", "Registries"}, {"flow", "Automation flows"},
	{"alert", "Alert rules"}, {"deploy", "Deploy projects"}, {"runner", "CodeHold runners"},
	{"file", "Files"},
}

func knownSearchKind(k string) bool {
	for _, x := range searchKinds {
		if x.ID == k {
			return true
		}
	}
	return false
}

func cleanSearchSettings(in SearchSettings) (SearchSettings, error) {
	out := in
	d := &out.Deep
	if d.MaxDepth <= 0 || d.MaxDepth > 32 {
		d.MaxDepth = 8
	}
	if d.MaxFiles <= 0 || d.MaxFiles > 5_000_000 {
		d.MaxFiles = 200000
	}
	var roots []string
	for _, r := range d.Roots {
		r = path.Clean("/" + strings.TrimSpace(r))
		if r == "/" || strings.HasPrefix(r, "/proc") || strings.HasPrefix(r, "/sys") || strings.HasPrefix(r, "/dev") {
			if r == "/" {
				roots = append(roots, r)
			}
			continue
		}
		roots = append(roots, r)
	}
	if len(roots) == 0 {
		roots = defaultSearchSettings().Deep.Roots
	}
	d.Roots = uniqueStrings(roots)
	d.Exclude = uniqueStrings(d.Exclude)
	if len(out.Filters) > 50 {
		return out, errors.New("at most 50 custom filters")
	}
	seen := map[string]bool{}
	for i := range out.Filters {
		f := &out.Filters[i]
		f.Label = strings.TrimSpace(f.Label)
		if f.Label == "" || len(f.Label) > 40 {
			return out, errors.New("every filter needs a label of up to 40 characters")
		}
		if f.ID == "" {
			f.ID = uuid.NewString()
		}
		if seen[f.ID] {
			return out, errors.New("filter ids must be unique")
		}
		seen[f.ID] = true
		for _, k := range f.Kinds {
			if !knownSearchKind(k) {
				return out, fmt.Errorf("unknown result kind %q", k)
			}
		}
		f.Query = strings.TrimSpace(f.Query)
	}
	if out.Filters == nil {
		out.Filters = []SearchFilter{}
	}
	for _, k := range out.HiddenKinds {
		if !knownSearchKind(k) {
			return out, fmt.Errorf("unknown result kind %q", k)
		}
	}
	return out, nil
}

func (s *Server) searchSettings(ctx context.Context, clusterID string) SearchSettings {
	out := defaultSearchSettings()
	st, err := s.Store.GetUISettings(ctx, clusterID)
	if err != nil || len(st.Search) == 0 || string(st.Search) == "{}" {
		return out
	}
	_ = json.Unmarshal(st.Search, &out)
	cleaned, err := cleanSearchSettings(out)
	if err != nil {
		return out
	}
	return cleaned
}

// searchState tracks background indexing.
type searchState struct {
	mu          sync.Mutex
	lastPrimary map[string]time.Time
	running     map[string]context.CancelFunc
	refreshing  bool
}

// ---- primary index ----

func wlKind(k string) string {
	switch k {
	case "vm":
		return "vm"
	case "oci":
		return "oci"
	default:
		return "workload"
	}
}

// primaryDocs lists the basic details of everything, for the index.
func (s *Server) primaryDocs(ctx context.Context, clusterID string) []appdb.SearchDoc {
	var docs []appdb.SearchDoc
	add := func(kind, id, title, subtitle, body, link, node, status, perm string) {
		if strings.TrimSpace(title) == "" {
			return
		}
		docs = append(docs, appdb.SearchDoc{ClusterID: clusterID, Kind: kind, RefID: id, Scope: appdb.SearchScopePrimary,
			Title: title, Subtitle: subtitle, Body: body, Link: link, NodeID: node, Status: status, Permission: perm})
	}
	if users, err := s.Store.ListUsers(ctx, clusterID); err == nil {
		for _, u := range users {
			if u.Kind == appdb.UserKindService {
				continue
			}
			roles, _ := s.Store.UserRoles(ctx, u.ID)
			status := "active"
			if userDisabled(u) {
				status = "disabled"
			}
			add("user", u.ID, firstNonEmpty(u.DisplayName, u.Username), "@"+u.Username, strings.Join(roles, " "), "/users?user="+url.QueryEscape(u.ID), "", status, rbac.UsersRead)
		}
	}
	if groups, err := s.Store.ListGroups(ctx, clusterID); err == nil {
		for _, g := range groups {
			add("group", g.ID, g.Name, "Group", "", "/iam/access?group="+url.QueryEscape(g.ID), "", "", rbac.IdentityRead)
		}
	}
	for _, d := range s.roleDefs(ctx, clusterID) {
		add("role", d.Name, firstNonEmpty(d.Title, d.Name), "Role", d.Name, "/iam/access?role="+url.QueryEscape(d.Name), "", "", rbac.IdentityRead)
	}
	if wls, err := s.Store.ListWorkloads(ctx, clusterID); err == nil {
		for _, w := range wls {
			kind := wlKind(w.Kind)
			label := map[string]string{"vm": "Virtual machine", "oci": "OCI container", "workload": "System container"}[kind]
			add(kind, w.ID, w.Name, label, w.Kind, "/workloads/"+w.ID, w.NodeID, w.Status, rbac.ComputeRead)
		}
	}
	if nodes, err := s.Store.ListClusterNodes(ctx, clusterID); err == nil {
		for _, n := range nodes {
			add("node", n.ID, firstNonEmpty(n.Name, n.Hostname), "Node", n.Hostname, "/node", n.ID, "", rbac.NodeRead)
		}
	}
	if gs, err := s.Store.ListGameServers(ctx, clusterID); err == nil {
		for _, g := range gs {
			add("gameserver", g.ID, g.Name, "Game server", g.Game+" "+g.TemplateName, "/game-servers/"+g.ID, g.NodeID, g.Status, rbac.GameServerRead)
		}
	}
	if stacks, err := s.Store.ListStacks(ctx, clusterID); err == nil {
		for _, st := range stacks {
			add("stack", st.ID, st.Name, "OCI app", "", "/oci?app="+url.QueryEscape(st.ID), "", st.Status, rbac.ComputeRead)
		}
	}
	if pools, err := s.Store.ListStoragePools(ctx, clusterID); err == nil {
		for _, p := range pools {
			add("storage", p.ID, p.Name, "Storage pool", "", "/storage", p.NodeID, p.Status, rbac.StorageRead)
		}
	}
	if nets, err := s.Store.ListNetworks(ctx, clusterID); err == nil {
		for _, n := range nets {
			add("network", n.ID, n.Name, "Network", n.Kind, "/network", n.NodeID, n.Status, rbac.NetworkRead)
		}
	}
	if pols, err := s.Store.ListBackupPolicies(ctx, clusterID); err == nil {
		for _, p := range pols {
			add("backup", p.ID, p.Name, "Backup policy", "", "/backups", "", "", rbac.BackupRead)
		}
	}
	if tpls, err := s.Store.ListVMTemplates(ctx, clusterID); err == nil {
		for _, t := range tpls {
			add("template", t.ID, t.Name, "Template", "", "/templates", "", "", rbac.ComputeRead)
		}
	}
	if regs, err := s.Store.ListRegistries(ctx, clusterID); err == nil {
		for _, rg := range regs {
			add("registry", rg.ID, rg.Name, "Registry", rg.URL, "/registries", "", rg.Status, rbac.ComputeRead)
		}
	}
	if flows, err := s.Store.ListAutomationFlows(ctx, clusterID); err == nil {
		for _, f := range flows {
			status := "off"
			if f.Enabled {
				status = "on"
			}
			add("flow", f.ID, f.Name, "Automation flow", f.Description, "/automation/flows/"+f.ID, "", status, rbac.PolicyRead)
		}
	}
	if rules, err := s.Store.ListAlertRules(ctx, clusterID); err == nil {
		for _, a := range rules {
			add("alert", a.ID, a.Name, "Alert rule", "", "/alerts", "", "", rbac.AlertRead)
		}
	}
	if projects, err := s.Store.ListDeployProjects(ctx, clusterID); err == nil {
		for _, d := range projects {
			add("deploy", d.ID, d.Name, "Deploy project", d.State.RemoteURL, "/docker/deploy", "", "", rbac.ComputeRead)
		}
	}
	if runners, err := s.Store.ListCodeholdRunners(ctx, clusterID); err == nil {
		for _, r := range runners {
			add("runner", r.RunnerID, r.Name, "CodeHold runner", "", "/codehold", "", r.State, rbac.ComputeRead)
		}
	}
	return docs
}

// refreshPrimary rebuilds the primary index and drops what no longer exists.
func (s *Server) refreshPrimary(ctx context.Context, clusterID string) error {
	start := s.now().Add(-time.Second)
	docs := s.primaryDocs(ctx, clusterID)
	for i := range docs {
		docs[i].UpdatedAt = s.now()
	}
	if err := s.Store.UpsertSearchDocs(ctx, docs); err != nil {
		return err
	}
	if err := s.Store.DeleteStaleSearchDocs(ctx, clusterID, appdb.SearchScopePrimary, nil, start); err != nil {
		return err
	}
	s.search.mu.Lock()
	if s.search.lastPrimary == nil {
		s.search.lastPrimary = map[string]time.Time{}
	}
	s.search.lastPrimary[clusterID] = s.now()
	s.search.mu.Unlock()
	return nil
}

// refreshPrimarySoon refreshes the primary index in the background if it is
// older than primaryRefresh.
func (s *Server) refreshPrimarySoon(clusterID string) {
	s.search.mu.Lock()
	stale := s.now().Sub(s.search.lastPrimary[clusterID]) > primaryRefresh
	if !stale || s.search.refreshing {
		s.search.mu.Unlock()
		return
	}
	s.search.refreshing = true
	s.search.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := s.refreshPrimary(ctx, clusterID); err != nil {
			log.Printf("search: primary index: %v", err)
		}
		s.search.mu.Lock()
		s.search.refreshing = false
		s.search.mu.Unlock()
	}()
}

// RunSearchIndexer keeps the primary index fresh and resumes a deep
// indexing run that a restart or update interrupted.
func (s *Server) RunSearchIndexer(ctx context.Context) {
	resumed := false
	t := time.NewTicker(primaryRefresh)
	defer t.Stop()
	for {
		if id := s.clusterIDCached(ctx); id != "" {
			c, cancel := context.WithTimeout(ctx, time.Minute)
			if err := s.refreshPrimary(c, id); err != nil {
				log.Printf("search: primary index: %v", err)
			}
			cancel()
			if !resumed {
				resumed = true
				if j, err := s.Store.LatestSearchJob(ctx, id); err == nil && j != nil && j.Status == appdb.SearchJobRunning {
					log.Printf("search: resuming indexing run %s at %d of about %d", j.ID, j.Done, j.Total)
					s.startDeepJob(*j)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---- deep index ----

// deepCursor is where a deep run is. Queue holds directories still to
// list; the one being listed when a run stopped is still at its head, so a
// resumed run lists it again.
type deepCursor struct {
	Targets []deepTarget `json:"targets"`
	Target  int          `json:"target"`
	Queue   []deepDir    `json:"queue"`
	Started bool         `json:"started"`
}

type deepTarget struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Jail  string `json:"jail"`
	Node  string `json:"node"`
	Label string `json:"label"`
}

type deepDir struct {
	Path  string `json:"path"`
	Depth int    `json:"depth"`
}

func (s *Server) deepTargets(ctx context.Context, clusterID string, set DeepSearchSettings) []deepTarget {
	var out []deepTarget
	if set.Files {
		if wls, err := s.Store.ListWorkloads(ctx, clusterID); err == nil {
			for _, w := range wls {
				if w.Kind != lxc.KindSystemContainer || w.Status != "running" {
					continue
				}
				if _, jail, err := s.workloadIO(ctx, clusterID, w.ID); err == nil {
					out = append(out, deepTarget{Kind: w.Kind, ID: w.ID, Name: w.Name, Jail: jail, Node: w.NodeID, Label: w.Name})
				}
			}
		}
	}
	if set.HostFiles {
		if nodes, err := s.Store.ListClusterNodes(ctx, clusterID); err == nil {
			for _, n := range nodes {
				if node, err := s.localNode(ctx, clusterID, n.ID); err == nil && node != nil {
					out = append(out, deepTarget{Kind: iojail.TargetHost, ID: node.ID, Name: node.Name, Jail: "/", Node: node.ID, Label: firstNonEmpty(node.Name, "Host")})
				}
			}
		}
	}
	return out
}

func (s *Server) startDeepJob(j appdb.SearchJob) {
	ctx, cancel := context.WithCancel(context.Background())
	s.search.mu.Lock()
	if s.search.running == nil {
		s.search.running = map[string]context.CancelFunc{}
	}
	if old := s.search.running[j.ClusterID]; old != nil {
		old()
	}
	s.search.running[j.ClusterID] = cancel
	s.search.mu.Unlock()
	go func() {
		defer cancel()
		s.runDeepJob(ctx, j)
		s.search.mu.Lock()
		delete(s.search.running, j.ClusterID)
		s.search.mu.Unlock()
	}()
}

func (s *Server) runDeepJob(ctx context.Context, j appdb.SearchJob) {
	set := s.searchSettings(ctx, j.ClusterID).Deep
	var cur deepCursor
	_ = json.Unmarshal(j.Cursor, &cur)
	if !cur.Started {
		cur = deepCursor{Targets: s.deepTargets(ctx, j.ClusterID, set), Started: true}
		// The old deep index goes once a run starts from scratch; documents a
		// resumed run already wrote are simply rewritten.
		_ = s.Store.DeleteSearchDocs(ctx, j.ClusterID, appdb.SearchScopeDeep)
		j.Done, j.Total = 0, 0
	}
	exclude := map[string]bool{}
	for _, e := range set.Exclude {
		exclude[e] = true
	}
	save := func() {
		raw, _ := json.Marshal(cur)
		j.Cursor = raw
		if err := s.Store.UpdateSearchJob(context.Background(), j); err != nil {
			log.Printf("search: saving run %s: %v", j.ID, err)
		}
	}
	finish := func(status, msg string) {
		now := s.now()
		j.Status, j.Error, j.FinishedAt = status, msg, &now
		if status == appdb.SearchJobDone {
			j.Total = j.Done
		}
		save()
	}
	var batch []appdb.SearchDoc
	flush := func() bool {
		if err := s.Store.UpsertSearchDocs(ctx, batch); err != nil {
			finish(appdb.SearchJobFailed, err.Error())
			return false
		}
		batch = batch[:0]
		save()
		return true
	}
	for cur.Target < len(cur.Targets) {
		t := cur.Targets[cur.Target]
		if len(cur.Queue) == 0 {
			for _, r := range set.Roots {
				cur.Queue = append(cur.Queue, deepDir{Path: strings.TrimPrefix(r, "/"), Depth: 0})
			}
		}
		for len(cur.Queue) > 0 {
			if ctx.Err() != nil {
				// Cancelled or shutting down: the cursor is saved, a restart resumes.
				if len(batch) > 0 {
					_ = s.Store.UpsertSearchDocs(context.Background(), batch)
				}
				save()
				return
			}
			if j.Done >= int64(set.MaxFiles) {
				finish(appdb.SearchJobDone, fmt.Sprintf("stopped at the limit of %d files", set.MaxFiles))
				return
			}
			dir := cur.Queue[0]
			entries, err := s.listForIndex(ctx, t, dir.Path)
			cur.Queue = cur.Queue[1:]
			if err != nil {
				continue
			}
			for _, e := range entries {
				if exclude[e.Name] {
					continue
				}
				rel := e.Path
				if rel == "" {
					rel = strings.TrimPrefix(path.Join(dir.Path, e.Name), "/")
				}
				if e.Type == "dir" {
					if dir.Depth+1 < set.MaxDepth {
						cur.Queue = append(cur.Queue, deepDir{Path: rel, Depth: dir.Depth + 1})
					}
					continue
				}
				batch = append(batch, s.fileDoc(j.ClusterID, t, rel, e.Size))
				j.Done++
			}
			// Estimate the total from what is left to list.
			j.Total = j.Done + int64(len(cur.Queue))*8
			if len(batch) >= deepBatch && !flush() {
				return
			}
		}
		cur.Target++
		if !flush() {
			return
		}
	}
	finish(appdb.SearchJobDone, "")
}

type indexEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	Path string `json:"path"`
}

func (s *Server) listForIndex(ctx context.Context, t deepTarget, rel string) ([]indexEntry, error) {
	if s.IO == nil {
		return nil, errors.New("io agent is unavailable")
	}
	if _, err := iojail.CleanRel(rel); err != nil {
		return nil, err
	}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := s.IO.FilesOp(c, agentrpc.FilesCall{TargetKind: t.Kind, TargetID: t.ID, JailRoot: t.Jail, Action: "list", Path: rel})
	if err != nil {
		return nil, err
	}
	var out struct {
		Entries []indexEntry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Entries, nil
}

func (s *Server) fileDoc(clusterID string, t deepTarget, rel string, size int64) appdb.SearchDoc {
	full := "/" + strings.TrimPrefix(rel, "/")
	dir := path.Dir(full)
	link := "/workloads/" + t.ID + "/files?path=" + url.QueryEscape(dir)
	perm := rbac.FilesRead
	if t.Kind == iojail.TargetHost {
		link = "/nodes/" + t.ID + "/files?path=" + url.QueryEscape(dir)
		perm = permSuperAdmin
	}
	return appdb.SearchDoc{
		ClusterID: clusterID, Kind: "file", RefID: t.ID + ":" + full, Scope: appdb.SearchScopeDeep,
		Title: path.Base(full), Subtitle: t.Label + ":" + full, Body: strings.ReplaceAll(full, "/", " ") + " " + humanSize(size),
		Link: link, NodeID: t.Node, Permission: perm, UpdatedAt: s.now(),
	}
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// ---- API ----

func searchJobJSON(j *appdb.SearchJob, now time.Time) map[string]any {
	if j == nil {
		return nil
	}
	out := map[string]any{
		"id": j.ID, "status": j.Status, "done": j.Done, "total": j.Total, "error": j.Error,
		"started_by": j.StartedBy, "started_at": j.StartedAt.UTC().Format(time.RFC3339), "updated_at": j.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if j.FinishedAt != nil {
		out["finished_at"] = j.FinishedAt.UTC().Format(time.RFC3339)
	}
	if j.Status == appdb.SearchJobRunning && j.Done > 0 && j.Total > j.Done {
		elapsed := now.Sub(j.StartedAt).Seconds()
		if elapsed > 0 {
			rate := float64(j.Done) / elapsed
			out["eta_seconds"] = int64(float64(j.Total-j.Done) / rate)
		}
	}
	if j.Total > 0 {
		pct := float64(j.Done) / float64(j.Total) * 100
		if pct > 100 {
			pct = 100
		}
		out["percent"] = int(pct)
	}
	return out
}

func (s *Server) searchAllowed(p *principal) func(string) bool {
	owner := isOwner(p) || p.User.ID == LocalRootUserID
	return func(perm string) bool {
		if perm == permSuperAdmin {
			return owner
		}
		return rbac.Authorize(p.Grants, perm)
	}
}

func (s *Server) searchQuery(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	q := r.URL.Query()
	ctx := r.Context()
	s.refreshPrimarySoon(p.User.ClusterID)
	set := s.searchSettings(ctx, p.User.ClusterID)
	var kinds []string
	for _, k := range strings.Split(q.Get("kind"), ",") {
		k = strings.TrimSpace(k)
		if k != "" && knownSearchKind(k) && !containsString(set.HiddenKinds, k) {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 && len(set.HiddenKinds) > 0 {
		for _, k := range searchKinds {
			if !containsString(set.HiddenKinds, k.ID) {
				kinds = append(kinds, k.ID)
			}
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	text := strings.TrimSpace(q.Get("q"))
	if len(text) > 200 {
		text = text[:200]
	}
	docs, err := s.Store.SearchDocs(ctx, appdb.SearchQuery{
		ClusterID: p.User.ClusterID, Text: text, Kinds: kinds, Status: strings.TrimSpace(q.Get("status")),
		NodeID: strings.TrimSpace(q.Get("node")), Allowed: s.searchAllowed(p), Limit: limit,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(docs))
	counts := map[string]int{}
	for _, d := range docs {
		counts[d.Kind]++
		items = append(items, map[string]any{
			"kind": d.Kind, "id": d.RefID, "title": d.Title, "subtitle": d.Subtitle, "link": d.Link,
			"status": d.Status, "node_id": d.NodeID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "counts": counts, "filters": set.Filters})
}

func (s *Server) getSearchSettings(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SearchManage)
	if err != nil {
		return
	}
	ctx := r.Context()
	counts, _ := s.Store.CountSearchDocs(ctx, p.User.ClusterID)
	job, _ := s.Store.LatestSearchJob(ctx, p.User.ClusterID)
	kinds := make([]map[string]string, 0, len(searchKinds))
	for _, k := range searchKinds {
		kinds = append(kinds, map[string]string{"id": k.ID, "label": k.Label})
	}
	s.search.mu.Lock()
	last := s.search.lastPrimary[p.User.ClusterID]
	s.search.mu.Unlock()
	body := map[string]any{
		"settings": s.searchSettings(ctx, p.User.ClusterID), "kinds": kinds,
		"primary_count": counts[appdb.SearchScopePrimary], "deep_count": counts[appdb.SearchScopeDeep],
		"job": searchJobJSON(job, s.now()), "can_index_host": isOwner(p) || p.User.ID == LocalRootUserID,
	}
	if !last.IsZero() {
		body["primary_refreshed_at"] = last.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) putSearchSettings(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SearchManage)
	if err != nil {
		return
	}
	var req SearchSettings
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	set, err := cleanSearchSettings(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	cur := s.searchSettings(ctx, p.User.ClusterID)
	// Host files are only readable by SuperAdmins, so only they may index them.
	if set.Deep.HostFiles != cur.Deep.HostFiles && !isOwner(p) && p.User.ID != LocalRootUserID {
		writeErr(w, http.StatusForbidden, "only a SuperAdmin can change host file indexing")
		return
	}
	st, err := s.Store.GetUISettings(ctx, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	raw, _ := json.Marshal(set)
	st.ClusterID, st.Search, st.UpdatedBy = p.User.ClusterID, raw, p.User.Username
	if err := s.Store.PutUISettings(ctx, st); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "search.settings.update", "ok", "")
	s.getSearchSettings(w, r)
}

func (s *Server) startSearchIndex(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SearchManage)
	if err != nil {
		return
	}
	ctx := r.Context()
	if j, _ := s.Store.LatestSearchJob(ctx, p.User.ClusterID); j != nil && j.Status == appdb.SearchJobRunning {
		writeErr(w, http.StatusConflict, "an indexing run is already going")
		return
	}
	set := s.searchSettings(ctx, p.User.ClusterID)
	if !set.Deep.Files && !set.Deep.HostFiles {
		// Nothing deep to do: refresh the primary index now instead.
		if err := s.refreshPrimary(ctx, p.User.ClusterID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.getSearchSettings(w, r)
		return
	}
	j := appdb.SearchJob{ID: uuid.NewString(), ClusterID: p.User.ClusterID, Scope: appdb.SearchScopeDeep, Status: appdb.SearchJobRunning,
		Cursor: json.RawMessage(`{}`), StartedBy: p.User.Username, StartedAt: s.now()}
	if err := s.Store.CreateSearchJob(ctx, j); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "search.index.start", "ok", j.ID)
	s.startDeepJob(j)
	s.getSearchSettings(w, r)
}

func (s *Server) cancelSearchIndex(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SearchManage)
	if err != nil {
		return
	}
	ctx := r.Context()
	s.search.mu.Lock()
	if c := s.search.running[p.User.ClusterID]; c != nil {
		c()
	}
	s.search.mu.Unlock()
	if j, _ := s.Store.LatestSearchJob(ctx, p.User.ClusterID); j != nil && j.Status == appdb.SearchJobRunning {
		now := s.now()
		j.Status, j.FinishedAt = appdb.SearchJobCanceled, &now
		_ = s.Store.UpdateSearchJob(ctx, *j)
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "search.index.cancel", "ok", "")
	s.getSearchSettings(w, r)
}

func (s *Server) clearDeepIndex(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SearchManage)
	if err != nil {
		return
	}
	if err := s.Store.DeleteSearchDocs(r.Context(), p.User.ClusterID, appdb.SearchScopeDeep); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "search.index.clear", "ok", "")
	s.getSearchSettings(w, r)
}
