package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Update impact notices tell people, before and after an update, about
// changes that turn off or change something they may be using.
//
// Before: a release lists them in its changelog as lines of the form
//
//	* Impact (automation): Existing automation flows are turned off.
//
// and Check for updates shows them, and notifies the audience once per
// version. After: notices this build knows about (impactNotices) are sent
// to their audience once, when they apply here.

// flowDisabledByUpdate is the reason migration 0077 gives flows it turned
// off. The text must match the migration.
const flowDisabledByUpdate = "Turned off by the update that added flow approvals. Review it, then turn it back on."

// rollbackWindow is how long after an update it can be rolled back. Then
// the checkpoint (the old version's packages and database) is wiped.
const rollbackWindow = 72 * time.Hour

// Notice audiences.
const (
	audienceAutomation = "automation" // people who may create and edit flows
	audienceUpdates    = "updates"    // people who manage updates
	audienceEveryone   = "everyone"
)

type impactNotice struct {
	ID       string
	Audience string
	Title    string
	Body     string
	Link     string
	// Applies reports whether the notice concerns this appliance now.
	Applies func(ctx context.Context, s *Server, clusterID string) (bool, string)
}

// impactNotices are the notices this build delivers after it is installed.
var impactNotices = []impactNotice{
	{
		ID:       "0077-flows-disabled",
		Audience: audienceAutomation,
		Title:    "Automation flows were turned off by an update",
		Body:     "The update added approvals for flows with protected steps, and turned off every flow that was on so nothing runs unreviewed. Review each flow, then turn it back on.",
		Link:     "/automation",
		Applies: func(ctx context.Context, s *Server, clusterID string) (bool, string) {
			flows, err := s.Store.ListAutomationFlows(ctx, clusterID)
			if err != nil {
				return false, ""
			}
			var names []string
			for _, f := range flows {
				if f.DisabledReason == flowDisabledByUpdate {
					names = append(names, f.Name)
				}
			}
			if len(names) == 0 {
				return false, ""
			}
			return true, "Turned off: " + strings.Join(names, ", ") + "."
		},
	},
}

// impactLineRe matches a changelog impact line: "* Impact (audience): text".
var impactLineRe = regexp.MustCompile(`(?i)^\s*\*\s*impact(?:\s*\(([a-z0-9_.\- ]+)\))?\s*:\s*(.+)$`)

type releaseImpact struct {
	ID       string `json:"id"`
	Audience string `json:"audience"`
	Text     string `json:"text"`
	Applies  bool   `json:"applies"`
	Detail   string `json:"detail,omitempty"`
	// Version is the release whose changelog entry lists the notice.
	Version string `json:"version,omitempty"`
}

// changelogHeadRe matches a Debian changelog entry's first line,
// "nodal (1.2.3) trixie; urgency=medium", capturing the version.
var changelogHeadRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+.\-]*\s+\(([^)\s]+)\)`)

var versionNumRe = regexp.MustCompile(`\d+`)

// compareVersions orders two package versions by their numbers
// (1.10.0 > 1.9.2), ignoring an epoch. It returns -1, 0 or 1.
func compareVersions(a, b string) int {
	nums := func(v string) []int {
		if i := strings.Index(v, ":"); i >= 0 {
			v = v[i+1:]
		}
		var out []int
		for _, d := range versionNumRe.FindAllString(v, -1) {
			n, _ := strconv.Atoi(d)
			out = append(out, n)
		}
		return out
	}
	na, nb := nums(a), nums(b)
	for i := 0; i < len(na) || i < len(nb); i++ {
		var x, y int
		if i < len(na) {
			x = na[i]
		}
		if i < len(nb) {
			y = nb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// pendingChangelog keeps the changelog entries newer than installed, so an
// update over several releases lists every one of them and the installed
// release's own entries are left out. A changelog without Debian entry
// lines is kept whole.
func pendingChangelog(changelog, installed string) string {
	if installed == "" {
		return changelog
	}
	var b strings.Builder
	keep, sawHead := false, false
	for _, line := range strings.Split(changelog, "\n") {
		if m := changelogHeadRe.FindStringSubmatch(line); m != nil {
			sawHead = true
			keep = compareVersions(m[1], installed) > 0
		}
		if keep {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if !sawHead {
		return changelog
	}
	return b.String()
}

// parseImpactNotices reads impact lines from a changelog. A line's text
// continues on the indented lines after it. The same notice in several
// releases is listed once, under the newest.
func parseImpactNotices(changelog string) []releaseImpact {
	var out []releaseImpact
	seen := map[string]bool{}
	version := ""
	lines := strings.Split(changelog, "\n")
	for i := 0; i < len(lines); i++ {
		if h := changelogHeadRe.FindStringSubmatch(lines[i]); h != nil {
			version = h[1]
			continue
		}
		m := impactLineRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		text := strings.TrimSpace(m[2])
		for i+1 < len(lines) {
			next := lines[i+1]
			trimmed := strings.TrimSpace(next)
			if trimmed == "" || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "--") || !strings.HasPrefix(next, " ") {
				break
			}
			text += " " + trimmed
			i++
		}
		audience := strings.ToLower(strings.TrimSpace(m[1]))
		if audience == "" {
			audience = audienceUpdates
		}
		sum := sha256.Sum256([]byte(audience + "\x00" + text))
		id := hex.EncodeToString(sum[:8])
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, releaseImpact{ID: id, Audience: audience, Text: text, Version: version})
	}
	return out
}

// impactCache holds the notices of the newest checked release.
type impactCache struct {
	mu      sync.Mutex
	version string
	notices []releaseImpact
}

func (c *impactCache) set(version string, n []releaseImpact) {
	c.mu.Lock()
	c.version, c.notices = version, n
	c.mu.Unlock()
}

func (c *impactCache) get() (string, []releaseImpact) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version, append([]releaseImpact(nil), c.notices...)
}

// impactApplies decides, before an update, whether a notice concerns this
// appliance. Only audiences with a local check can say no.
func (s *Server) impactApplies(ctx context.Context, clusterID string, n releaseImpact) (bool, string) {
	if n.Audience == audienceAutomation {
		flows, err := s.Store.ListAutomationFlows(ctx, clusterID)
		if err != nil {
			return true, ""
		}
		on := 0
		for _, f := range flows {
			if f.Enabled {
				on++
			}
		}
		if on == 0 {
			return false, "No flows are on here."
		}
		return true, fmt.Sprintf("%d flow(s) are on here.", on)
	}
	return true, ""
}

// releaseNotices evaluates a release's impact notices, caches them, and
// notifies each audience once per version.
func (s *Server) releaseNotices(ctx context.Context, clusterID, version, changelog string) []releaseImpact {
	notices := parseImpactNotices(changelog)
	for i := range notices {
		notices[i].Applies, notices[i].Detail = s.impactApplies(ctx, clusterID, notices[i])
	}
	s.impact.set(version, notices)
	if version == "" {
		return notices
	}
	for _, n := range notices {
		if !n.Applies {
			continue
		}
		id := "pending:" + version + ":" + n.ID
		if done, _ := s.Store.UpdateNoticeDelivered(ctx, clusterID, id); done {
			continue
		}
		s.notifyAudience(ctx, clusterID, n.Audience, noteUpdateImpact,
			"Update "+version+" changes something here", strings.TrimSpace(n.Text+" "+n.Detail), "/settings/updates")
		_ = s.Store.MarkUpdateNoticeDelivered(ctx, clusterID, id)
	}
	return notices
}

// inAudience reports whether one person belongs to a notice audience.
// SuperAdmins are always told.
func (s *Server) inAudience(ctx context.Context, clusterID string, u appdb.User, roles []string, audience string) bool {
	p := &principal{User: u, Roles: roles}
	if isOwner(p) {
		return true
	}
	switch audience {
	case audienceEveryone:
		return true
	case audienceAutomation:
		grants := s.grantsForRoles(ctx, clusterID, roles)
		return rbac.Authorize(grants, rbac.PolicyApply) && s.mayCreateAutomations(ctx, p)
	case audienceUpdates:
		return rbac.Authorize(s.grantsForRoles(ctx, clusterID, roles), rbac.UpdatesManage)
	default:
		// An audience naming a permission reaches whoever holds it.
		return rbac.Authorize(s.grantsForRoles(ctx, clusterID, roles), audience)
	}
}

func (s *Server) notifyAudience(ctx context.Context, clusterID, audience, kind, title, body, link string) {
	users, err := s.Store.ListUsers(ctx, clusterID)
	if err != nil {
		return
	}
	for _, u := range users {
		if u.Kind == appdb.UserKindService || userDisabled(u) {
			continue
		}
		roles, _ := s.Store.UserRoles(ctx, u.ID)
		if s.inAudience(ctx, clusterID, u, roles, audience) {
			s.notifyUser(ctx, clusterID, u.ID, kind, title, body, link)
		}
	}
}

// deliverImpactNotices sends this build's notices that apply here and were
// not sent yet.
func (s *Server) deliverImpactNotices(ctx context.Context, clusterID string) {
	for _, n := range impactNotices {
		if done, err := s.Store.UpdateNoticeDelivered(ctx, clusterID, n.ID); err != nil || done {
			continue
		}
		ok, detail := n.Applies(ctx, s, clusterID)
		if !ok {
			continue
		}
		s.notifyAudience(ctx, clusterID, n.Audience, noteUpdateImpact, n.Title, strings.TrimSpace(n.Body+" "+detail), n.Link)
		if err := s.Store.MarkUpdateNoticeDelivered(ctx, clusterID, n.ID); err != nil {
			log.Printf("update notice %s: %v", n.ID, err)
		}
	}
}

// RunUpdateHousekeeping delivers impact notices after an update and wipes
// checkpoints whose rollback window has closed.
func (s *Server) RunUpdateHousekeeping(ctx context.Context) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		if id := s.clusterIDCached(ctx); id != "" {
			c, cancel := context.WithTimeout(ctx, 2*time.Minute)
			s.deliverImpactNotices(c, id)
			s.wipeExpiredCheckpoint(c, id)
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// rollbackEnds is when an apply's rollback window closes.
func rollbackEnds(op appdb.UpdateOperation) time.Time {
	at := op.StartedAt
	if op.FinishedAt != nil {
		at = *op.FinishedAt
	}
	return at.Add(rollbackWindow)
}

// wipeExpiredCheckpoint deletes the checkpoints of the last update and the
// last rollback once their 3-day windows have closed.
func (s *Server) wipeExpiredCheckpoint(ctx context.Context, clusterID string) {
	for _, action := range []string{"apply", "rollback"} {
		last, err := s.Store.GetLatestUpdateOperationByAction(ctx, clusterID, action)
		if err != nil || last == nil || last.CheckpointID == "" || last.Status == appdb.UpdateRunning {
			continue
		}
		if s.now().Before(rollbackEnds(*last)) {
			continue
		}
		if at, _ := s.Store.CheckpointWiped(ctx, last.ID); at != nil {
			continue
		}
		if err := s.wipeCheckpoint(ctx, *last, "system"); err != nil {
			log.Printf("update: wiping the checkpoint of %s: %v", last.ID, err)
		}
	}
}

func (s *Server) wipeCheckpoint(ctx context.Context, op appdb.UpdateOperation, by string) error {
	res, err := s.updater().HostUpdate(ctx, hostos.UpdateRequest{Action: hostos.UpdateCheckpointWipe, Channel: hostos.ChannelStable, CheckpointID: op.CheckpointID})
	if err != nil {
		return err
	}
	if res.Supported && res.Status == appdb.UpdateFailed {
		return fmt.Errorf("%s", res.Reason)
	}
	return s.Store.MarkCheckpointWiped(ctx, op.ID, by, s.now())
}

// wipeRollback deletes the previous version kept for rollback. Only
// SuperAdmins may, and no permission changes that.
func (s *Server) wipeRollback(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.UpdatesManage)
	if err != nil {
		return
	}
	if !isOwner(p) && p.User.ID != LocalRootUserID {
		writeErr(w, http.StatusForbidden, "only a SuperAdmin can wipe the previous version")
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != "wipe-rollback" {
		writeErr(w, http.StatusUnprocessableEntity, "wiping requires X-Nodal-Confirm: wipe-rollback")
		return
	}
	ctx := r.Context()
	// The copies kept for the last update and for the last rollback.
	var targets []appdb.UpdateOperation
	for _, action := range []string{"apply", "rollback"} {
		last, err := s.Store.GetLatestUpdateOperationByAction(ctx, p.User.ClusterID, action)
		if err != nil || last == nil || last.CheckpointID == "" {
			continue
		}
		if last.Status == appdb.UpdateRunning {
			writeErr(w, http.StatusConflict, "the "+action+" is still running")
			return
		}
		if at, _ := s.Store.CheckpointWiped(ctx, last.ID); at != nil {
			continue
		}
		targets = append(targets, *last)
	}
	if len(targets) == 0 {
		writeErr(w, http.StatusConflict, "there is no previous version to wipe")
		return
	}
	for _, op := range targets {
		if err := s.wipeCheckpoint(ctx, op, p.User.Username); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "update.rollback.wipe", "ok", op.ID)
	}
	writeJSON(w, http.StatusOK, rollbackJSON(s.planRollback(ctx, p.User.ClusterID)))
}
