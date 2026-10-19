package httpapi

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// The root SuperAdmin is the account that set the appliance up. It is
// stored in the IAM settings, managed only by itself or from the host, and
// changed only from the host (nodalctl set-root). Only root may rank roles
// above SuperAdmin; it holds those roles' permissions as well.

type iamSettingsCacheEntry struct {
	at time.Time
	s  appdb.IAMSettings
}

var iamSettingsCache sync.Map // clusterID -> iamSettingsCacheEntry

func (s *Server) iamSettings(ctx context.Context, clusterID string) appdb.IAMSettings {
	if v, ok := iamSettingsCache.Load(clusterID); ok {
		if c := v.(iamSettingsCacheEntry); time.Since(c.at) < 5*time.Second {
			return c.s
		}
	}
	st, err := s.Store.GetIAMSettings(ctx, clusterID)
	if err != nil {
		return appdb.DefaultIAMSettings(clusterID)
	}
	iamSettingsCache.Store(clusterID, iamSettingsCacheEntry{at: time.Now(), s: st})
	return st
}

func forgetIAMSettings(clusterID string) { iamSettingsCache.Delete(clusterID) }

// saveIAMSettings stores st and drops the cached copy.
func (s *Server) saveIAMSettings(ctx context.Context, st appdb.IAMSettings) error {
	err := s.Store.PutIAMSettings(ctx, st)
	forgetIAMSettings(st.ClusterID)
	forgetRoleDefs(st.ClusterID)
	return err
}

// rootUserID is the stored root account. Until one is stored (older
// installs, a deleted root) it falls back to the oldest SuperAdmin.
func (s *Server) rootUserID(ctx context.Context, clusterID string) string {
	if id := s.iamSettings(ctx, clusterID).RootUserID; id != "" {
		if u, err := s.Store.GetUser(ctx, id); err == nil && u != nil {
			return id
		}
	}
	users, err := s.Store.ListUsers(ctx, clusterID)
	if err != nil {
		return ""
	}
	best := ""
	var at time.Time
	for _, u := range users {
		if u.Kind == appdb.UserKindService {
			continue
		}
		roles, _ := s.Store.UserRoles(ctx, u.ID)
		if !containsString(roles, rbac.SuperAdmin) {
			continue
		}
		if best == "" || u.CreatedAt.Before(at) || (u.CreatedAt.Equal(at) && u.ID < best) {
			best, at = u.ID, u.CreatedAt
		}
	}
	return best
}

func (s *Server) setRootUser(ctx context.Context, clusterID, userID, by string) error {
	st, err := s.Store.GetIAMSettings(ctx, clusterID)
	if err != nil {
		return err
	}
	st.ClusterID, st.RootUserID, st.UpdatedBy = clusterID, userID, by
	return s.saveIAMSettings(ctx, st)
}

func (s *Server) isRoot(ctx context.Context, p *principal) bool {
	if p == nil {
		return false
	}
	if p.User.ID == LocalRootUserID {
		return true
	}
	return isOwner(p) && p.User.ID == s.rootUserID(ctx, p.User.ClusterID)
}

// superAdminRank is the SuperAdmin role's place in the hierarchy. Roles
// with a lower rank sit above it; only root manages them.
func superAdminRank(defs []appdb.RoleDef) int {
	for _, d := range defs {
		if d.Name == rbac.SuperAdmin {
			return d.Rank
		}
	}
	return 0
}

// rolesAboveSuperAdmin lists the roles ranked above SuperAdmin.
func rolesAboveSuperAdmin(defs []appdb.RoleDef) []string {
	top := superAdminRank(defs)
	var out []string
	for _, d := range defs {
		if d.Rank < top && d.Name != rbac.SuperAdmin {
			out = append(out, d.Name)
		}
	}
	return out
}

// consoleSetRoot makes another SuperAdmin the root account. Root on the
// host only (nodalctl set-root).
func (s *Server) consoleSetRoot(w http.ResponseWriter, r *http.Request) {
	p, err := s.principal(r)
	if err != nil || p == nil || p.User.ID != LocalRootUserID {
		writeErr(w, http.StatusForbidden, "the root account is changed only as root on the host (nodalctl set-root)")
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Username) == "" {
		writeErr(w, http.StatusBadRequest, "username is required")
		return
	}
	ctx := r.Context()
	u, _ := s.Store.GetUserByName(ctx, p.User.ClusterID, strings.TrimSpace(req.Username))
	if u == nil || u.Kind == appdb.UserKindService {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	roles, _ := s.Store.UserRoles(ctx, u.ID)
	if !containsString(roles, rbac.SuperAdmin) {
		writeErr(w, http.StatusBadRequest, "the root account must hold the SuperAdmin role")
		return
	}
	if userDisabled(*u) {
		writeErr(w, http.StatusBadRequest, "that account is disabled")
		return
	}
	prev := s.rootUserID(ctx, p.User.ClusterID)
	if err := s.setRootUser(ctx, p.User.ClusterID, u.ID, "console"); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	prevName := prev
	if pu, _ := s.Store.GetUser(ctx, prev); pu != nil {
		prevName = pu.Username
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "console.set_root", "ok", firstNonEmpty(prevName, "none")+" -> "+u.Username)
	writeJSON(w, http.StatusOK, map[string]any{"root": u.Username})
}
