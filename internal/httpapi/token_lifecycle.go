package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

// API tokens are hard-deleted when deleted or expired. Before a token row
// goes, its whole life (owner, creation, every source IP with first and last
// use, how long it was active) is written to the audit log, which is the only
// record kept.

const (
	tokenSweepTick     = time.Minute
	tokenUsageThrottle = time.Minute
)

// tokenSummary is the audit detail for a token that is being removed.
func tokenSummary(tok appdb.APIToken, usage []appdb.TokenUsage, reason string, now time.Time) map[string]any {
	out := map[string]any{
		"detail":     tok.ID,
		"name":       tok.Name,
		"owner":      tok.UserID,
		"prefix":     tok.Prefix,
		"created_at": tok.CreatedAt.UTC().Format(time.RFC3339),
		"ended_at":   now.UTC().Format(time.RFC3339),
		"reason":     reason,
		"ips":        len(usage),
	}
	if tok.ExpiresAt != nil {
		out["expires_at"] = tok.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if tok.LastUsedAt != nil {
		out["last_used_at"] = tok.LastUsedAt.UTC().Format(time.RFC3339)
	}
	var first, last time.Time
	var uses int
	ips := make([]map[string]any, 0, len(usage))
	for _, u := range usage {
		if first.IsZero() || u.FirstAt.Before(first) {
			first = u.FirstAt
		}
		if u.LastAt.After(last) {
			last = u.LastAt
		}
		uses += u.Count
		ips = append(ips, map[string]any{"ip": u.IP, "first_at": u.FirstAt.UTC().Format(time.RFC3339), "last_at": u.LastAt.UTC().Format(time.RFC3339), "count": u.Count})
	}
	if !first.IsZero() {
		out["first_used_at"] = first.UTC().Format(time.RFC3339)
		out["active_for"] = last.Sub(first).Round(time.Second).String()
	}
	out["uses"] = uses
	out["usage"] = ips
	return out
}

func (s *Server) auditTokenEnd(ctx context.Context, r *http.Request, clusterID, actor, action string, tok appdb.APIToken, reason string) {
	usage, _ := s.Store.ListTokenUsage(ctx, tok.ID)
	body, _ := json.Marshal(tokenSummary(tok, usage, reason, s.now()))
	remote := "internal"
	if r != nil {
		remote = s.clientIP(r)
	}
	_ = s.Store.InsertAudit(ctx, appdb.AuditEvent{
		ID: uuid.NewString(), ClusterID: clusterID, ActorUserID: actor, Action: action,
		Result: "ok", RemoteAddr: remote, Detail: body, CreatedAt: s.now(),
	})
}

// RunTokenSweeper deletes expired API tokens every minute, auditing each.
func (s *Server) RunTokenSweeper(ctx context.Context) {
	t := time.NewTicker(tokenSweepTick)
	defer t.Stop()
	for {
		s.sweepExpiredTokens(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) sweepExpiredTokens(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	expired, err := s.Store.ListExpiredTokens(ctx, s.now())
	if err != nil {
		return
	}
	for _, tok := range expired {
		s.auditTokenEnd(ctx, nil, tok.ClusterID, "", "token.expire", tok, "expired")
		_ = s.Store.RevokeClusterToken(ctx, tok.ClusterID, tok.ID)
	}
}

// noteTokenUse records the caller's IP for a token, at most once a minute
// per token and IP.
func (s *Server) noteTokenUse(r *http.Request, tokenID string) {
	ip := s.clientIP(r)
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	if ip == "" {
		ip = "unknown"
	}
	key := tokenID + "|" + ip
	now := s.now()
	if v, ok := s.tokenUseSeen.Load(key); ok {
		if last, _ := v.(time.Time); now.Sub(last) < tokenUsageThrottle {
			return
		}
	}
	s.tokenUseSeen.Store(key, now)
	_ = s.Store.TouchTokenUsage(r.Context(), tokenID, ip, now)
}

// tokenForAdmin loads a token the caller may manage.
func (s *Server) tokenForAdmin(w http.ResponseWriter, r *http.Request) (*principal, *appdb.APIToken, bool) {
	p, err := s.require(w, r, rbac.IdentityTokenRevoke)
	if err != nil {
		return nil, nil, false
	}
	tok, err := s.Store.GetToken(r.Context(), r.PathValue("id"))
	if err != nil || tok == nil || tok.ClusterID != p.User.ClusterID {
		writeErr(w, http.StatusNotFound, "token not found")
		return nil, nil, false
	}
	if tok.UserID != p.User.ID && !rbac.Authorize(p.Grants, rbac.APIAccessManage) {
		writeErr(w, http.StatusNotFound, "token not found")
		return nil, nil, false
	}
	if !s.mayActOnUser(r.Context(), p, tok.UserID) {
		writeErr(w, http.StatusForbidden, "that token belongs to someone above you in the role hierarchy")
		return nil, nil, false
	}
	return p, tok, true
}

func (s *Server) tokenUsage(w http.ResponseWriter, r *http.Request) {
	_, tok, ok := s.tokenForAdmin(w, r)
	if !ok {
		return
	}
	usage, err := s.Store.ListTokenUsage(r.Context(), tok.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(usage))
	for _, u := range usage {
		items = append(items, map[string]any{"ip": u.IP, "first_at": u.FirstAt.UTC().Format(time.RFC3339), "last_at": u.LastAt.UTC().Format(time.RFC3339), "count": u.Count})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) rollToken(w http.ResponseWriter, r *http.Request) {
	p, tok, ok := s.tokenForAdmin(w, r)
	if !ok {
		return
	}
	raw, err := secutil.RandomHex(24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	plain := "ndl_" + raw
	if err := s.Store.RollToken(r.Context(), p.User.ClusterID, tok.ID, secutil.HashSHA256(plain), plain[:8]); err != nil {
		writeErr(w, http.StatusNotFound, "token not found")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "token.roll", "ok", tok.ID)
	writeJSON(w, http.StatusOK, map[string]any{"id": tok.ID, "token": plain, "prefix": plain[:8]})
}

func (s *Server) updateToken(w http.ResponseWriter, r *http.Request) {
	p, tok, ok := s.tokenForAdmin(w, r)
	if !ok {
		return
	}
	var req struct {
		Name     string `json:"name"`
		TTLHours *int   `json:"ttl_hours"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	expires := tok.ExpiresAt
	if req.TTLHours != nil {
		e, err := tokenExpiry(s.now(), *req.TTLHours)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		expires = e
	}
	if err := s.Store.UpdateToken(r.Context(), p.User.ClusterID, tok.ID, strings.TrimSpace(req.Name), expires); err != nil {
		writeErr(w, http.StatusNotFound, "token not found")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "token.update", "ok", tok.ID)
	updated, _ := s.Store.GetToken(r.Context(), tok.ID)
	if updated == nil {
		updated = tok
	}
	writeJSON(w, http.StatusOK, tokenPublicJSON(r, s, *updated, s.now()))
}

func (s *Server) deleteTokenByID(w http.ResponseWriter, r *http.Request) {
	p, tok, ok := s.tokenForAdmin(w, r)
	if !ok {
		return
	}
	if !s.hardDeleteToken(w, r, p, *tok) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hardDeleteToken audits a token's life, deletes it, and checks it is gone:
// a store that reports success without deleting fails closed.
func (s *Server) hardDeleteToken(w http.ResponseWriter, r *http.Request, p *principal, tok appdb.APIToken) bool {
	usage, _ := s.Store.ListTokenUsage(r.Context(), tok.ID)
	var err error
	if tok.UserID == p.User.ID {
		err = s.Store.RevokeToken(r.Context(), tok.ID, p.User.ID)
	} else {
		err = s.Store.RevokeClusterToken(r.Context(), p.User.ClusterID, tok.ID)
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, "token not found")
		return false
	}
	if still, gerr := s.Store.GetToken(r.Context(), tok.ID); gerr != nil || still != nil {
		writeErr(w, http.StatusInternalServerError, "could not revoke token")
		return false
	}
	body, _ := json.Marshal(tokenSummary(tok, usage, "deleted", s.now()))
	_ = s.Store.InsertAudit(r.Context(), appdb.AuditEvent{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, ActorUserID: p.User.ID, Action: "token.delete",
		Result: "ok", RemoteAddr: s.clientIP(r), Detail: body, CreatedAt: s.now(),
	})
	return true
}
