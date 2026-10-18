package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/deploy"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// detectScript finds whether a project's branch has new commits.
//
// It asks the remote with ls-remote, which is cheap, and fetches only when
// the remote's commit is not in the folder yet, so it can count how far
// behind the folder is. Nothing in the working tree changes.
//
// CodeHold comes first: when the remote is the CodeHold server (or no
// CodeHold address is known and the remote is not GitHub) and ch is in the
// container, ch runs Git with CodeHold's stored credentials. GitHub remotes
// use gh's credential helper when gh is installed. Plain Git always works
// without either. A failed remote is retried with plain Git, then with the
// fallback remote.
const detectScript = `
set +e
emit() { printf 'ndl.%s=%s\n' "$1" "$(printf '%s' "$2" | tr '\n' ' ')"; }
T=
if command -v timeout >/dev/null 2>&1; then T="timeout 45"; fi
if ! command -v git >/dev/null 2>&1; then emit error "Git is not installed in the container"; exit 0; fi
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then emit error "the working folder is not a Git repository"; exit 0; fi
branch="$NDL_BRANCH"
[ -n "$branch" ] || branch=$(git symbolic-ref --short -q HEAD)
if [ -z "$branch" ]; then emit error "the folder is not on a branch; set the branch to follow"; exit 0; fi
emit branch "$branch"
emit local "$(git rev-parse HEAD 2>/dev/null)"
if [ -n "$(git status --porcelain --untracked-files=no 2>/dev/null | head -n 1)" ]; then emit dirty 1; fi
have_ch=; command -v ch >/dev/null 2>&1 && have_ch=1
have_gh=; command -v gh >/dev/null 2>&1 && have_gh=1
host_of() {
	printf '%s' "$1" | sed -E 's#^[A-Za-z0-9+.-]+://([^/@]*@)?([^/:]+).*#\2#; t; s#^([^@]*@)?([^:/]+):.*#\2#'
}
choose() {
	case "$NDL_PROVIDER" in
	codehold|github|git) echo "$NDL_PROVIDER"; return ;;
	esac
	if [ -n "$have_ch" ] && { [ "$1" = "$NDL_CODEHOLD_HOST" ] || { [ -z "$NDL_CODEHOLD_HOST" ] && [ "$1" != "github.com" ]; }; }; then echo codehold; return; fi
	if [ "$1" = "github.com" ] && [ -n "$have_gh" ]; then echo github; return; fi
	echo git
}
usable() {
	case "$1" in
	codehold) [ -n "$have_ch" ] ;;
	github) [ -n "$have_gh" ] ;;
	*) true ;;
	esac
}
git_with() {
	p="$1"; shift
	case "$p" in
	codehold) $T ch "$@" ;;
	github) $T git -c credential.helper= -c 'credential.helper=!gh auth git-credential' "$@" ;;
	*) $T git "$@" ;;
	esac
}
try_remote() {
	r="$1"
	u=$(git remote get-url "$r" 2>/dev/null)
	if [ -z "$u" ]; then last="the folder has no remote named $r"; return 1; fi
	p=$(choose "$(host_of "$u")")
	if ! usable "$p"; then emit note "$p was chosen for $r but its CLI is not installed in the container; used Git"; p=git; fi
	for q in "$p" git; do
		out=$(git_with "$q" ls-remote "$r" "refs/heads/$branch" 2>&1)
		rc=$?
		sha=$(printf '%s\n' "$out" | awk '$2 ~ /^refs\/heads\// {print $1; exit}')
		if [ $rc -eq 0 ] && [ -n "$sha" ]; then
			prov=$q; remote=$r; url=$u; return 0
		fi
		if [ $rc -eq 0 ]; then last="$r has no branch $branch"; return 1; fi
		last=$(printf '%s\n' "$out" | grep -v '^$' | tail -n 1)
		[ "$q" = git ] && break
	done
	return 1
}
prov=; remote=; url=; sha=; last=
if ! try_remote "$NDL_REMOTE"; then
	first="$last"
	if [ -n "$NDL_FALLBACK" ] && try_remote "$NDL_FALLBACK"; then
		emit note "$NDL_REMOTE could not be reached ($first); used $NDL_FALLBACK"
	else
		emit error "could not reach $NDL_REMOTE: $first"
		exit 0
	fi
fi
emit provider "$prov"
emit remote "$remote"
emit url "$(printf '%s' "$url" | sed -E 's#(://)[^/@]*@#\1#')"
emit remote_commit "$sha"
if [ "$prov" = codehold ]; then
	id=$(ch repo info 2>/dev/null | sed -n 's/.*"repo_id": *"\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$id" ] && emit repo_id "$id"
fi
if [ "$sha" = "$(git rev-parse HEAD 2>/dev/null)" ]; then emit behind 0; emit ahead 0; exit 0; fi
if ! git cat-file -e "$sha^{commit}" 2>/dev/null; then
	git_with "$prov" fetch --quiet --no-tags "$remote" "refs/heads/$branch" >/dev/null 2>&1 || emit note "fetching $remote failed; the count of new commits is unknown"
fi
if git cat-file -e "$sha^{commit}" 2>/dev/null; then
	counts=$(git rev-list --left-right --count "HEAD...$sha" 2>/dev/null)
	emit ahead "$(echo "$counts" | awk '{print $1}')"
	emit behind "$(echo "$counts" | awk '{print $2}')"
	emit subject "$(git log -1 --format=%s "$sha" 2>/dev/null)"
	emit author "$(git log -1 --format=%an "$sha" 2>/dev/null)"
else
	emit behind -1
fi
`

// detectResult is what detectScript printed.
type detectResult struct {
	Error, Note, Branch, Local, Provider, Remote, URL, RemoteCommit, RepoID, Subject, Author string
	Dirty                                                                                    bool
	Behind, Ahead                                                                            int
}

func parseDetect(out string) detectResult {
	r := detectResult{Behind: -1}
	var notes []string
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || !strings.HasPrefix(k, "ndl.") {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimPrefix(k, "ndl.") {
		case "error":
			r.Error = v
		case "note":
			notes = append(notes, v)
		case "branch":
			r.Branch = v
		case "local":
			r.Local = v
		case "dirty":
			r.Dirty = v == "1"
		case "provider":
			r.Provider = v
		case "remote":
			r.Remote = v
		case "url":
			r.URL = v
		case "remote_commit":
			r.RemoteCommit = v
		case "repo_id":
			r.RepoID = v
		case "subject":
			r.Subject = v
		case "author":
			r.Author = v
		case "behind":
			if n, err := strconv.Atoi(v); err == nil {
				r.Behind = n
			}
		case "ahead":
			if n, err := strconv.Atoi(v); err == nil {
				r.Ahead = n
			}
		}
	}
	r.Note = strings.Join(notes, "; ")
	return r
}

// repoName is owner/name from a remote URL, without credentials.
func repoName(raw string) string {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), ".git")
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return strings.Trim(u.Path, "/")
	}
	if _, path, ok := strings.Cut(raw, ":"); ok {
		return strings.Trim(path, "/")
	}
	return raw
}

func (s *Server) codeholdHost(ctx context.Context, clusterID string) string {
	st, err := s.Store.GetCodeholdCLISettings(ctx, clusterID)
	if err != nil || st == nil || st.URL == "" {
		return ""
	}
	u, err := url.Parse(st.URL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// checkProject runs update detection for one project and saves the result.
func (s *Server) checkProject(ctx context.Context, proj appdb.DeployProject) (appdb.DeployProject, error) {
	rpc, err := s.deployRPC()
	if err != nil {
		return proj, err
	}
	settings := s.deploySettings(ctx, proj.ClusterID)
	env := map[string]string{
		"NDL_PROVIDER": proj.Config.Provider, "NDL_REMOTE": firstNonEmpty(proj.Config.Remote, "origin"),
		"NDL_BRANCH": proj.Config.Branch, "NDL_FALLBACK": proj.Config.FallbackRemote,
		"NDL_CODEHOLD_HOST": s.codeholdHost(ctx, proj.ClusterID),
	}
	var res deploy.ProbeResult
	cctx, cancel := context.WithTimeout(ctx, 110*time.Second)
	err = rpc.Deploy(cctx, "deploy-probe", agentrpc.DeployRequest{
		MachineID: proj.WorkloadID, Dir: proj.WorkingDir, RunAs: proj.Config.RunAs, Script: detectScript, Env: env, Timeout: 100,
	}, &res)
	cancel()
	now := s.now()
	interval := proj.Config.DetectIntervalSec
	if interval <= 0 {
		interval = settings.DetectIntervalSec
	}
	next := now.Add(time.Duration(interval) * time.Second)
	cur, gerr := s.Store.GetDeployProject(ctx, proj.ClusterID, proj.ID)
	if gerr != nil || cur == nil {
		return proj, fmt.Errorf("project not found")
	}
	st := cur.State
	st.CheckedAt, st.NextCheckAt = &now, &next
	if err != nil {
		st.CheckError = "the check could not run: " + err.Error()
		_ = s.Store.UpdateDeployProjectState(ctx, proj.ClusterID, proj.ID, st)
		cur.State = st
		return *cur, nil
	}
	r := parseDetect(res.Output)
	st.CheckError = r.Error
	if r.Error == "" && r.RemoteCommit == "" {
		st.CheckError = "the check printed no result"
	}
	if r.Branch != "" {
		st.Branch = r.Branch
	}
	if isCommitID(r.Local) {
		st.LocalCommit = r.Local
	}
	st.Dirty = r.Dirty
	if st.CheckError == "" {
		st.Provider, st.Remote, st.RemoteURL = r.Provider, r.Remote, r.URL
		st.Repo = repoName(r.URL)
		if r.RepoID != "" {
			st.Repo += " (" + r.RepoID + ")"
		}
		st.RemoteCommit, st.Behind, st.Ahead = r.RemoteCommit, r.Behind, r.Ahead
		st.RemoteSubject, st.RemoteAuthor = r.Subject, r.Author
	}
	st.CheckNote = r.Note
	wasDue := st.Due
	st.Due = st.CheckError == "" && st.RemoteCommit != "" && st.RemoteCommit != st.LocalCommit && st.Behind != 0
	switch {
	case st.Due && !wasDue:
		st.DueSince = &now
	case !st.Due:
		st.DueSince = nil
	}
	_ = s.Store.UpdateDeployProjectState(ctx, proj.ClusterID, proj.ID, st)
	cur.State = st
	if st.Due && st.NotifiedDue != st.RemoteCommit {
		s.updateAvailable(ctx, *cur)
	}
	return *cur, nil
}

// tickDeployDetect starts a round of update checks in the background when
// one is not already running.
func (s *Server) tickDeployDetect(ctx context.Context, clusterID string) {
	rt := s.deployRT()
	if !rt.detecting.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer rt.detecting.Store(false)
		s.detectDue(context.WithoutCancel(ctx), clusterID, false)
	}()
}

// detectDue checks every project whose next check is due (or all with
// all). Projects that are deploying are left until they finish.
func (s *Server) detectDue(ctx context.Context, clusterID string, all bool) int {
	projects, err := s.Store.ListDeployProjects(ctx, clusterID)
	if err != nil {
		return 0
	}
	active, _ := s.Store.ListDeployments(ctx, clusterID, appdb.DeploymentFilter{Active: true})
	busy := map[string]bool{}
	for _, d := range active {
		busy[d.ProjectID] = true
	}
	wls := s.workloadNames(ctx, clusterID)
	now := s.now()
	n := 0
	for _, p := range projects {
		wl, ok := wls[p.WorkloadID]
		if ok && !busy[p.ID] && p.Config.AutoDeploy && p.State.Due {
			// A commit seen outside the project's window deploys once it opens.
			s.maybeAutoDeploy(ctx, p)
		}
		if !p.Config.Detect || busy[p.ID] || !ok || (wl.Status != "" && wl.Status != "running") {
			continue
		}
		if !all && p.State.NextCheckAt != nil && p.State.NextCheckAt.After(now) {
			continue
		}
		if _, err := s.checkProject(ctx, p); err == nil {
			n++
		}
		if ctx.Err() != nil {
			break
		}
	}
	return n
}

func (s *Server) checkDeployProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeModify)
	if !ok {
		return
	}
	proj, ok := s.loadDeployProject(w, r, p)
	if !ok {
		return
	}
	got, err := s.checkProject(r.Context(), *proj)
	if err != nil {
		writeDeployErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.projectView(r.Context(), got, nil))
}

func (s *Server) checkAllDeployProjects(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeModify)
	if !ok {
		return
	}
	rt := s.deployRT()
	if !rt.detecting.CompareAndSwap(false, true) {
		writeJSON(w, http.StatusAccepted, map[string]any{"started": false, "message": "a check is already running"})
		return
	}
	cid := p.User.ClusterID
	go func() {
		defer rt.detecting.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.detectDue(ctx, cid, true)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

// --- bulk ---

func (s *Server) bulkDeploy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeModify)
	if !ok {
		return
	}
	var in struct {
		ProjectIDs []string `json:"project_ids"`
		Due        bool     `json:"due"`
		Force      bool     `json:"force"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ctx, cid := r.Context(), p.User.ClusterID
	projects, err := s.Store.ListDeployProjects(ctx, cid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	want := map[string]bool{}
	for _, id := range in.ProjectIDs {
		want[id] = true
	}
	if len(want) == 0 && !in.Due {
		writeErr(w, http.StatusBadRequest, "choose projects, or deploy every project with an update")
		return
	}
	batch := newBatchID()
	type skip struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Reason string `json:"reason"`
	}
	queued := []map[string]any{}
	skipped := []skip{}
	wls := s.workloadNames(ctx, cid)
	for _, proj := range projects {
		if len(want) > 0 && !want[proj.ID] {
			continue
		}
		if len(want) == 0 && !proj.State.Due {
			continue
		}
		delete(want, proj.ID)
		if _, ok := wls[proj.WorkloadID]; !ok {
			skipped = append(skipped, skip{proj.ID, proj.Name, "its container no longer exists"})
			continue
		}
		d, err := s.enqueueDeployment(ctx, proj, "bulk", p.User.Username, batch, in.Force)
		if err != nil {
			skipped = append(skipped, skip{proj.ID, proj.Name, err.Error()})
			continue
		}
		queued = append(queued, s.deploymentView(ctx, *d, wls))
	}
	for id := range want {
		skipped = append(skipped, skip{ID: id, Reason: "project not found"})
	}
	if len(queued) == 0 {
		batch = ""
	}
	s.audit(r, cid, p.User.ID, "docker.deploy.bulk", "queued", fmt.Sprintf("batch %s: %d queued, %d skipped", batch, len(queued), len(skipped)))
	writeJSON(w, http.StatusAccepted, map[string]any{"batch_id": batch, "queued": queued, "skipped": skipped})
}

func newBatchID() string {
	return "b-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
