package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Appearance settings: the global theme, themes per role and group, the
// default navigation and Instance layouts, and the design profile limit.
// A person's own choice wins over their group's, then their role's, then
// the global one. Themes are UI tokens only: colours, radius, spacing and
// fonts, never markup or script.

const (
	// maxDesignProfiles is the most design profiles anyone may keep. Only a
	// SuperAdmin may lower it, to 2 or 1, and nothing raises it.
	maxDesignProfiles = 3
	designProfilesKey = "design.profiles"
	maxThemeBytes     = 16 << 10
	maxProfilesBytes  = 96 << 10
)

// Appearance is the appearance document in ui_settings.
type Appearance struct {
	Theme          json.RawMessage            `json:"theme,omitempty"`
	RoleThemes     map[string]json.RawMessage `json:"role_themes,omitempty"`
	GroupThemes    map[string]json.RawMessage `json:"group_themes,omitempty"`
	NavLayout      string                     `json:"nav_layout,omitempty"`
	InstanceLayout string                     `json:"instance_layout,omitempty"`
	ProfileLimit   int                        `json:"profile_limit,omitempty"`
	// NodeNicknames are UI names for nodes, by node ID (see node_nicknames.go).
	NodeNicknames map[string]string `json:"node_nicknames,omitempty"`
}

func (a Appearance) limit() int {
	if a.ProfileLimit < 1 || a.ProfileLimit > maxDesignProfiles {
		return maxDesignProfiles
	}
	return a.ProfileLimit
}

var (
	themeTokenNameRe = regexp.MustCompile(`^--[a-z][a-z0-9-]{0,63}$`)
	// themeValueRe allows colours, lengths, numbers, font names and simple
	// functions such as oklch(), rgb(), calc() and var().
	themeValueRe = regexp.MustCompile(`^[a-zA-Z0-9 #%.,()+\-*/'"_]{1,200}$`)
	themeBadRe   = regexp.MustCompile(`(?i)(url\s*\(|expression\s*\(|javascript:|@import|\\|<|>|;|\{|\})`)
)

// themeKeys are the keys a theme may hold, with the strings each allows
// (nil allows any short safe string).
var themeKeys = map[string][]string{
	"mode": {"system", "light", "dark"}, "theme": nil, "accent": nil, "secondary": nil,
	"style": {"bubble", "plain", "boxy", "compact", "glass"}, "density": {"comfortable", "compact", "spacious"},
	"radius": nil, "font": nil, "tokens": nil, "terminal": nil, "name": nil,
}

// validTheme checks a theme document. It must be a JSON object of known
// keys; tokens and terminal map CSS variable names to safe values.
func validTheme(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if len(raw) > maxThemeBytes {
		return errors.New("theme is too large")
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return errors.New("theme must be a JSON object")
	}
	for k, v := range doc {
		allowed, ok := themeKeys[k]
		if !ok {
			return fmt.Errorf("theme key %q is not supported", k)
		}
		switch k {
		case "tokens", "terminal":
			var m map[string]string
			if err := json.Unmarshal(v, &m); err != nil {
				return fmt.Errorf("theme %s must map names to text", k)
			}
			if len(m) > 200 {
				return fmt.Errorf("theme %s has too many entries", k)
			}
			for name, val := range m {
				if k == "tokens" && !themeTokenNameRe.MatchString(name) {
					return fmt.Errorf("token %q must be a CSS variable name such as --accent", name)
				}
				if k == "terminal" && !regexp.MustCompile(`^[a-zA-Z]{1,32}$`).MatchString(name) {
					return fmt.Errorf("terminal colour %q is not a colour name", name)
				}
				if err := safeThemeValue(val); err != nil {
					return fmt.Errorf("%s %s: %v", k, name, err)
				}
			}
		case "radius":
			var n float64
			if err := json.Unmarshal(v, &n); err != nil || n < 0 || n > 32 {
				return errors.New("theme radius must be a number from 0 to 32")
			}
		default:
			var sv *string
			if err := json.Unmarshal(v, &sv); err != nil {
				return fmt.Errorf("theme %s must be text", k)
			}
			if sv == nil {
				continue
			}
			if allowed != nil && !containsString(allowed, *sv) {
				return fmt.Errorf("theme %s must be one of %s", k, strings.Join(allowed, ", "))
			}
			if err := safeThemeValue(*sv); err != nil && *sv != "" {
				return fmt.Errorf("theme %s: %v", k, err)
			}
		}
	}
	return nil
}

func safeThemeValue(v string) error {
	if themeBadRe.MatchString(v) {
		return errors.New("only colours, sizes and font names are allowed")
	}
	if !themeValueRe.MatchString(v) {
		return errors.New("contains characters a theme value cannot use")
	}
	return nil
}

func (s *Server) appearance(ctx context.Context, clusterID string) Appearance {
	var a Appearance
	st, err := s.Store.GetUISettings(ctx, clusterID)
	if err == nil && len(st.Appearance) > 0 {
		_ = json.Unmarshal(st.Appearance, &a)
	}
	if a.NavLayout != "classic" {
		a.NavLayout = "modern"
	}
	if a.InstanceLayout != "classic" {
		a.InstanceLayout = "instance"
	}
	return a
}

func (s *Server) saveAppearance(ctx context.Context, clusterID, by string, a Appearance) error {
	st, err := s.Store.GetUISettings(ctx, clusterID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	st.ClusterID, st.Appearance, st.UpdatedBy = clusterID, raw, by
	return s.Store.PutUISettings(ctx, st)
}

func appearanceJSON(a Appearance) map[string]any {
	rt, gt := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	for k, v := range a.RoleThemes {
		rt[k] = v
	}
	for k, v := range a.GroupThemes {
		gt[k] = v
	}
	var theme any
	if len(a.Theme) > 0 {
		theme = a.Theme
	}
	return map[string]any{
		"theme": theme, "role_themes": rt, "group_themes": gt,
		"nav_layout": a.NavLayout, "instance_layout": a.InstanceLayout, "profile_limit": a.limit(),
	}
}

func (s *Server) getAppearance(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AppearanceManage)
	if err != nil {
		return
	}
	out := appearanceJSON(s.appearance(r.Context(), p.User.ClusterID))
	out["can_set_profile_limit"] = rbac.Authorize(p.Grants, rbac.AppearanceLimit) || p.User.ID == LocalRootUserID
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putAppearance(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AppearanceManage)
	if err != nil {
		return
	}
	var req struct {
		// Theme is null to clear it; absent leaves it unchanged.
		Theme          json.RawMessage            `json:"theme"`
		RoleThemes     map[string]json.RawMessage `json:"role_themes"`
		GroupThemes    map[string]json.RawMessage `json:"group_themes"`
		NavLayout      *string                    `json:"nav_layout"`
		InstanceLayout *string                    `json:"instance_layout"`
		ProfileLimit   *int                       `json:"profile_limit"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx := r.Context()
	a := s.appearance(ctx, p.User.ClusterID)
	if req.Theme != nil {
		if err := validTheme(req.Theme); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		a.Theme = nil
		if t := bytes.TrimSpace(req.Theme); len(t) > 0 && string(t) != "null" {
			a.Theme = t
		}
	}
	cleanMap := func(in map[string]json.RawMessage) (map[string]json.RawMessage, error) {
		out := map[string]json.RawMessage{}
		for k, v := range in {
			if strings.TrimSpace(k) == "" {
				continue
			}
			if err := validTheme(v); err != nil {
				return nil, fmt.Errorf("%s: %v", k, err)
			}
			if t := bytes.TrimSpace(v); len(t) > 0 && string(t) != "null" {
				out[k] = t
			}
		}
		return out, nil
	}
	if req.RoleThemes != nil {
		defs := s.roleDefs(ctx, p.User.ClusterID)
		for k := range req.RoleThemes {
			known := false
			for _, d := range defs {
				known = known || d.Name == k
			}
			if !known {
				writeErr(w, http.StatusBadRequest, fmt.Sprintf("role %q does not exist", k))
				return
			}
		}
		m, err := cleanMap(req.RoleThemes)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		a.RoleThemes = m
	}
	if req.GroupThemes != nil {
		m, err := cleanMap(req.GroupThemes)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		a.GroupThemes = m
	}
	if req.NavLayout != nil {
		if *req.NavLayout != "modern" && *req.NavLayout != "classic" {
			writeErr(w, http.StatusBadRequest, "nav_layout must be modern or classic")
			return
		}
		a.NavLayout = *req.NavLayout
	}
	if req.InstanceLayout != nil {
		if *req.InstanceLayout != "instance" && *req.InstanceLayout != "classic" {
			writeErr(w, http.StatusBadRequest, "instance_layout must be instance or classic")
			return
		}
		a.InstanceLayout = *req.InstanceLayout
	}
	if req.ProfileLimit != nil && *req.ProfileLimit != a.limit() {
		// Fixed rule, not a permission: only SuperAdmins lower the limit.
		if !rbac.Authorize(p.Grants, rbac.AppearanceLimit) && p.User.ID != LocalRootUserID {
			writeErr(w, http.StatusForbidden, "only a SuperAdmin can change the design profile limit")
			return
		}
		if *req.ProfileLimit < 1 || *req.ProfileLimit > maxDesignProfiles {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("the design profile limit is 1 to %d", maxDesignProfiles))
			return
		}
		a.ProfileLimit = *req.ProfileLimit
	}
	if err := s.saveAppearance(ctx, p.User.ClusterID, p.User.Username, a); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "settings.appearance.update", "ok", "")
	out := appearanceJSON(a)
	out["can_set_profile_limit"] = rbac.Authorize(p.Grants, rbac.AppearanceLimit) || p.User.ID == LocalRootUserID
	writeJSON(w, http.StatusOK, out)
}

// getMyAppearance is what the signed-in person inherits: the theme of
// their first group with one, else their highest role with one, else the
// global theme; plus the layout defaults and their profile limit.
func (s *Server) getMyAppearance(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	ctx := r.Context()
	a := s.appearance(ctx, p.User.ClusterID)
	var inherited json.RawMessage
	source := ""
	if len(a.GroupThemes) > 0 {
		if groups, err := s.Store.ListGroups(ctx, p.User.ClusterID); err == nil {
			for _, g := range groups {
				t, ok := a.GroupThemes[g.ID]
				if !ok {
					continue
				}
				members, _ := s.Store.ListGroupMembers(ctx, p.User.ClusterID, g.ID)
				if containsString(members, p.User.ID) {
					inherited, source = t, "group:"+g.Name
					break
				}
			}
		}
	}
	if inherited == nil && len(a.RoleThemes) > 0 {
		defs := s.roleDefs(ctx, p.User.ClusterID)
		best := -1
		for _, d := range defs {
			t, ok := a.RoleThemes[d.Name]
			if ok && containsString(p.Roles, d.Name) && (best < 0 || d.Rank < best) {
				inherited, source, best = t, "role:"+firstNonEmpty(d.Title, d.Name), d.Rank
			}
		}
	}
	if inherited == nil && len(a.Theme) > 0 {
		inherited, source = a.Theme, "global"
	}
	var theme any
	if inherited != nil {
		theme = inherited
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"theme": theme, "theme_source": source, "nav_layout": a.NavLayout, "instance_layout": a.InstanceLayout,
		"profile_limit": a.limit(), "can_manage": rbac.Authorize(p.Grants, rbac.AppearanceManage),
	})
}

type designProfile struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

// checkDesignProfiles enforces the profile limit on a ui_prefs write. When
// the limit was lowered below what someone already has, the extra profiles
// stay but are read-only: they may be deleted, not changed or added to.
func checkDesignProfiles(cur, next json.RawMessage, limit int) error {
	if len(next) > maxProfilesBytes {
		return errors.New("design profiles are too large")
	}
	var after []designProfile
	if err := json.Unmarshal(next, &after); err != nil {
		return errors.New("design profiles must be a list")
	}
	var before []designProfile
	if len(cur) > 0 {
		_ = json.Unmarshal(cur, &before)
	}
	seen := map[string]bool{}
	for _, d := range after {
		name := strings.TrimSpace(d.Name)
		if d.ID == "" || name == "" || len(name) > 40 {
			return errors.New("every design profile needs an id and a name of up to 40 characters")
		}
		if seen[d.ID] {
			return errors.New("design profile ids must be unique")
		}
		seen[d.ID] = true
	}
	if len(after) <= limit {
		return nil
	}
	// Over the limit: only allowed if nothing was added and the extras are
	// unchanged.
	old := map[string]designProfile{}
	for _, d := range before {
		old[d.ID] = d
	}
	if len(after) > len(before) {
		return fmt.Errorf("you can keep up to %d design profiles", limit)
	}
	for i, d := range after {
		o, ok := old[d.ID]
		if !ok {
			return fmt.Errorf("you can keep up to %d design profiles", limit)
		}
		if i >= limit && (o.Name != d.Name || !bytes.Equal(bytes.TrimSpace(o.Data), bytes.TrimSpace(d.Data))) {
			return fmt.Errorf("profiles past the limit of %d are read-only; delete one to make room", limit)
		}
	}
	return nil
}
