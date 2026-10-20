package httpapi

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Per-person notifications behind the header bell. Each person keeps the
// newest appdb.NotificationsKept; the bell polls the unread count.

// Notification kinds.
const (
	noteApprovalWaiting = "approval.waiting"
	noteApprovalDecided = "approval.decided"
	noteIPRecovery      = "security.ip_recovery"
	noteFlowDisabled    = "automation.disabled"
	noteTokenCreated    = "security.token_created"
	noteUpdateImpact    = "update.impact"
	noteRolesChanged    = "security.roles_changed"
)

// notifyUser stores one notification. Failures are logged, never returned:
// a notification never blocks the action it reports.
func (s *Server) notifyUser(ctx context.Context, clusterID, userID, kind, title, body, link string) {
	if userID == "" || userID == LocalRootUserID {
		return
	}
	if u, _ := s.Store.GetUser(ctx, userID); u == nil || u.Kind == appdb.UserKindService {
		return
	}
	err := s.Store.CreateNotification(ctx, appdb.Notification{
		ID: uuid.NewString(), ClusterID: clusterID, UserID: userID, Kind: kind, Title: title, Body: body, Link: link, CreatedAt: s.now(),
	})
	if err != nil {
		log.Printf("notification %s for %s: %v", kind, userID, err)
	}
}

// notifyWithPermission notifies every enabled person whose roles grant perm,
// except skip.
func (s *Server) notifyWithPermission(ctx context.Context, clusterID, perm, skip, kind, title, body, link string) {
	users, err := s.Store.ListUsers(ctx, clusterID)
	if err != nil {
		return
	}
	for _, u := range users {
		if u.ID == skip || u.Kind == appdb.UserKindService || userDisabled(u) {
			continue
		}
		roles, _ := s.Store.UserRoles(ctx, u.ID)
		if rbac.Authorize(s.grantsForRoles(ctx, clusterID, roles), perm) {
			s.notifyUser(ctx, clusterID, u.ID, kind, title, body, link)
		}
	}
}

func notificationJSON(n appdb.Notification) map[string]any {
	out := map[string]any{
		"id": n.ID, "kind": n.Kind, "title": n.Title, "body": n.Body, "link": n.Link,
		"created_at": n.CreatedAt.UTC().Format(time.RFC3339), "read": n.ReadAt != nil, "read_at": nil,
	}
	if n.ReadAt != nil {
		out["read_at"] = n.ReadAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	ctx := r.Context()
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > appdb.NotificationsKept {
		limit = 50
	}
	items, err := s.Store.ListNotifications(ctx, p.User.ClusterID, p.User.ID, r.URL.Query().Get("unread") == "true", limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	unread, _ := s.Store.CountUnreadNotifications(ctx, p.User.ClusterID, p.User.ID)
	out := make([]map[string]any, 0, len(items))
	for _, n := range items {
		out = append(out, notificationJSON(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "unread": unread})
}

func (s *Server) notificationsUnread(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	unread, err := s.Store.CountUnreadNotifications(r.Context(), p.User.ClusterID, p.User.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread": unread})
}

// markNotificationsRead marks the listed notifications read, or all of
// them when ids is empty.
func (s *Server) markNotificationsRead(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx := r.Context()
	if err := s.Store.MarkNotificationsRead(ctx, p.User.ClusterID, p.User.ID, uniqueStrings(req.IDs), s.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	unread, _ := s.Store.CountUnreadNotifications(ctx, p.User.ClusterID, p.User.ID)
	writeJSON(w, http.StatusOK, map[string]any{"unread": unread})
}

// notifyFlowDisabled tells the flow's creator and the SuperAdmins that the
// circuit breaker turned a flow off.
func (s *Server) notifyFlowDisabled(ctx context.Context, clusterID string, payload map[string]string) {
	f, _ := s.Store.GetAutomationFlow(ctx, clusterID, payload["flow_id"])
	if f == nil {
		return
	}
	title := "Flow turned off: " + f.Name
	link := "/automation/flows/" + f.ID
	creator := ""
	if u, _ := s.Store.GetUserByName(ctx, clusterID, f.CreatedBy); u != nil {
		creator = u.ID
		s.notifyUser(ctx, clusterID, u.ID, noteFlowDisabled, title, payload["reason"], link)
	}
	users, _ := s.Store.ListUsers(ctx, clusterID)
	for _, u := range users {
		if u.ID == creator || userDisabled(u) {
			continue
		}
		if roles, _ := s.Store.UserRoles(ctx, u.ID); containsString(roles, rbac.SuperAdmin) {
			s.notifyUser(ctx, clusterID, u.ID, noteFlowDisabled, title, payload["reason"], link)
		}
	}
}

// roleTitle is a role's display name.
func (s *Server) roleTitle(ctx context.Context, clusterID, role string) string {
	for _, d := range s.roleDefs(ctx, clusterID) {
		if d.Name == role {
			return firstNonEmpty(d.Title, d.Name)
		}
	}
	return role
}
