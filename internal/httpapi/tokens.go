package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.APIAccessManage)
	if err != nil {
		return
	}
	items, err := s.Store.ListTokens(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	includeRevoked := r.URL.Query().Get("include_revoked") == "1" || r.URL.Query().Get("include_revoked") == "true"
	clusterWide := rbac.Authorize(p.Grants, rbac.APIAccessManage)
	out := make([]map[string]any, 0, len(items))
	now := s.now()
	for _, tok := range items {
		if tok.UserID != p.User.ID && !clusterWide {
			continue
		}
		if !includeRevoked && tok.RevokedAt != nil {
			continue
		}
		out = append(out, tokenPublicJSON(r, s, tok, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func tokenPublicJSON(r *http.Request, s *Server, tok appdb.APIToken, now time.Time) map[string]any {
	perms := tok.Permissions
	if perms == nil {
		perms = []string{}
	}
	item := map[string]any{
		"id":          tok.ID,
		"name":        tok.Name,
		"prefix":      tok.Prefix,
		"user_id":     tok.UserID,
		"permissions": perms,
		"disabled":    tok.RevokedAt != nil,
	}
	if !tok.CreatedAt.IsZero() {
		item["created_at"] = tok.CreatedAt.UTC().Format(time.RFC3339)
	}
	if tok.ExpiresAt != nil {
		item["expires_at"] = tok.ExpiresAt.UTC().Format(time.RFC3339)
		item["expired"] = now.After(*tok.ExpiresAt)
	}
	if tok.RevokedAt != nil {
		item["revoked_at"] = tok.RevokedAt.UTC().Format(time.RFC3339)
	}
	if tok.LastUsedAt != nil {
		item["last_used_at"] = tok.LastUsedAt.UTC().Format(time.RFC3339)
	}
	if u, err := s.Store.GetUser(r.Context(), tok.UserID); err == nil && u != nil {
		item["username"] = u.Username
		item["user_kind"] = u.Kind
	}
	return item
}

func (s *Server) resolveTokenGrants(p *principal, preset string, requested []string) ([]string, error) {
	preset = strings.TrimSpace(preset)
	if preset != "" {
		got := rbac.PermissionsForTokenPreset(preset)
		if got == nil {
			return nil, fmt.Errorf("unknown token preset")
		}
		requested = got
	}
	for _, perm := range requested {
		if !rbac.Authorize(p.Grants, perm) {
			return nil, fmt.Errorf("token permissions cannot exceed the creator")
		}
	}
	return requested, nil
}

func tokenExpiry(now time.Time, ttlHours int) (*time.Time, error) {
	if ttlHours == 0 {
		return nil, nil
	}
	if ttlHours < 1 || ttlHours > 8760 {
		return nil, fmt.Errorf("ttl_hours must be between 1 and 8760")
	}
	exp := now.Add(time.Duration(ttlHours) * time.Hour).UTC()
	return &exp, nil
}

// tokenOwnerUserID maps the synthetic local-root actor onto a real user row so
// api_tokens.user_id (UUID) can be written. Peer-cred root is admin; the token
// is owned by the first enabled person in the cluster.
func (s *Server) tokenOwnerUserID(ctx context.Context, clusterID, userID string) (string, error) {
	if userID != LocalRootUserID {
		return userID, nil
	}
	users, err := s.Store.ListUsers(ctx, clusterID)
	if err != nil {
		return "", err
	}
	for _, u := range users {
		if u.DisabledAt != nil || u.ID == "" || u.ID == LocalRootUserID {
			continue
		}
		if u.Kind == "" || u.Kind == appdb.UserKindPerson {
			return u.ID, nil
		}
	}
	return "", fmt.Errorf("create a person user before issuing API tokens as local-root")
}

func issueAPIToken(s *Server, w http.ResponseWriter, r *http.Request, clusterID, userID, name string, permissions []string, expires *time.Time) (appdb.APIToken, string, bool) {
	raw, err := secutil.RandomHex(24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return appdb.APIToken{}, "", false
	}
	plain := "ndl_" + raw
	tok := appdb.APIToken{
		ID:          uuid.NewString(),
		ClusterID:   clusterID,
		UserID:      userID,
		Name:        strings.TrimSpace(name),
		TokenHash:   secutil.HashSHA256(plain),
		Prefix:      plain[:8],
		Permissions: permissions,
		ExpiresAt:   expires,
	}
	if err := s.Store.CreateToken(r.Context(), tok); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return appdb.APIToken{}, "", false
	}
	return tok, plain, true
}
