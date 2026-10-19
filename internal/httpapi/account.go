package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/mfa"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Account self-service: profile, password, picture, MFA and the
// account-level IP allowlist, plus the admin and console paths that undo an
// allowlist lockout.
//
// The allowlist needs MFA: it can only be turned on with MFA enrolled, and
// MFA cannot be removed while it is on. Someone signing in from an address
// that is not on their list may recover with their password and their MFA
// code; that only lifts the IP check, never a disabled account or a login
// lockout. Accounts without MFA ask an admin above them, and the root
// account without MFA uses `nodalctl recover-ip` on the host.

const (
	avatarMaxBytes     = 2 << 20
	allowlistMaxItems  = 64
	emailMaxLen        = 254
	usernameMaxLen     = 64
	ipRecoveryWindow   = 15 * time.Minute
	ipRecoveryPerShort = 3
	ipRecoveryPerDay   = 10
)

// errIPNotAllowed is returned for a session or token used from an address
// its account's allowlist does not hold.
var errIPNotAllowed = errors.New("this address is not on the account's IP allowlist")

// recoveryLimiter counts IP recovery attempts per key in a short window and
// a day.
type recoveryLimiter struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

func (l *recoveryLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string][]time.Time{}
	}
	keep := l.seen[key][:0]
	short := 0
	for _, t := range l.seen[key] {
		if now.Sub(t) < 24*time.Hour {
			keep = append(keep, t)
			if now.Sub(t) < ipRecoveryWindow {
				short++
			}
		}
	}
	l.seen[key] = keep
	if short >= ipRecoveryPerShort || len(keep) >= ipRecoveryPerDay {
		return false
	}
	l.seen[key] = append(l.seen[key], now)
	return true
}

type pendingIPRecovery struct {
	IP      string
	Expires time.Time
}

// ---- allowlist rules ----

func normalizeAllowEntry(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", errors.New("an entry is empty")
	}
	if !strings.Contains(v, "/") {
		addr, err := netip.ParseAddr(v)
		if err != nil {
			return "", fmt.Errorf("%q is not an IP address or CIDR range", v)
		}
		addr = addr.Unmap()
		return netip.PrefixFrom(addr, addr.BitLen()).String(), nil
	}
	pfx, err := netip.ParsePrefix(v)
	if err != nil {
		return "", fmt.Errorf("%q is not an IP address or CIDR range", v)
	}
	return pfx.Masked().String(), nil
}

func allowlistContains(list []appdb.IPAllowEntry, ip string) bool {
	addr, ok := parseIP(ip)
	if !ok {
		return false
	}
	for _, e := range list {
		pfx, err := netip.ParsePrefix(e.CIDR)
		if err == nil && pfx.Contains(addr) {
			return true
		}
	}
	return false
}

// ipBlocked reports whether user's allowlist refuses ip.
func (s *Server) ipBlocked(ctx context.Context, user appdb.User, ip string) bool {
	prof, err := s.Store.GetAccountProfile(ctx, user.ID)
	if err != nil || prof == nil || !prof.IPAllowlistOn {
		return false
	}
	return !allowlistContains(prof.IPAllowlist, ip)
}

func (s *Server) mfaEnabled(ctx context.Context, userID string) bool {
	m, _, _, err := s.Store.GetMFAMethod(ctx, userID)
	return err == nil && m != nil && m.Enabled
}

func (s *Server) profileOf(ctx context.Context, u appdb.User) appdb.AccountProfile {
	prof, _ := s.Store.GetAccountProfile(ctx, u.ID)
	if prof == nil {
		return appdb.AccountProfile{UserID: u.ID, ClusterID: u.ClusterID}
	}
	return *prof
}

func allowlistJSON(p appdb.AccountProfile) map[string]any {
	entries := make([]map[string]any, 0, len(p.IPAllowlist))
	for _, e := range p.IPAllowlist {
		item := map[string]any{"cidr": e.CIDR, "note": e.Note, "added_by": e.AddedBy}
		if !e.AddedAt.IsZero() {
			item["added_at"] = e.AddedAt.UTC().Format(time.RFC3339)
		}
		entries = append(entries, item)
	}
	return map[string]any{"enabled": p.IPAllowlistOn, "entries": entries}
}

type allowlistInput struct {
	Enabled bool `json:"enabled"`
	Entries []struct {
		CIDR string `json:"cidr"`
		Note string `json:"note"`
	} `json:"entries"`
}

// applyAllowlist validates and merges an allowlist edit into prof. Entries
// that already existed keep who added them and when.
func applyAllowlist(prof *appdb.AccountProfile, in allowlistInput, actor string, now time.Time) error {
	if len(in.Entries) > allowlistMaxItems {
		return fmt.Errorf("an allowlist holds at most %d entries", allowlistMaxItems)
	}
	old := map[string]appdb.IPAllowEntry{}
	for _, e := range prof.IPAllowlist {
		old[e.CIDR] = e
	}
	seen := map[string]bool{}
	next := make([]appdb.IPAllowEntry, 0, len(in.Entries))
	for _, e := range in.Entries {
		cidr, err := normalizeAllowEntry(e.CIDR)
		if err != nil {
			return err
		}
		if seen[cidr] {
			continue
		}
		seen[cidr] = true
		note := strings.TrimSpace(e.Note)
		if len(note) > 200 {
			note = note[:200]
		}
		if prev, ok := old[cidr]; ok {
			prev.Note = note
			next = append(next, prev)
			continue
		}
		next = append(next, appdb.IPAllowEntry{CIDR: cidr, Note: note, AddedBy: actor, AddedAt: now})
	}
	if in.Enabled && len(next) == 0 {
		return errors.New("add at least one address before turning the allowlist on")
	}
	prof.IPAllowlist = next
	prof.IPAllowlistOn = in.Enabled
	return nil
}

// ---- profile ----

func (s *Server) selfPrincipal(w http.ResponseWriter, r *http.Request) (*principal, bool) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return nil, false
	}
	if p.TokenID != "" {
		writeErr(w, http.StatusForbidden, "API tokens cannot change account settings")
		return nil, false
	}
	if p.User.ID == LocalRootUserID || p.User.Kind == appdb.UserKindService {
		writeErr(w, http.StatusForbidden, "this account has no profile")
		return nil, false
	}
	return p, true
}

func (s *Server) profileJSON(ctx context.Context, u appdb.User) map[string]any {
	prof := s.profileOf(ctx, u)
	roles, _ := s.Store.UserRoles(ctx, u.ID)
	out := map[string]any{
		"user_id":         u.ID,
		"username":        u.Username,
		"display_name":    u.DisplayName,
		"email":           prof.Email,
		"email_secondary": prof.EmailSecondary,
		"email_verified":  prof.EmailVerifiedAt != nil,
		"roles":           roles,
		"mfa_enabled":     s.mfaEnabled(ctx, u.ID),
		"ip_allowlist":    allowlistJSON(prof),
	}
	if !u.CreatedAt.IsZero() {
		out["created_at"] = u.CreatedAt.UTC().Format(time.RFC3339)
	}
	if u.LastLoginAt != nil {
		out["last_login_at"] = u.LastLoginAt.UTC().Format(time.RFC3339)
	}
	if av, _ := s.Store.GetUserAvatar(ctx, u.ID); av != nil {
		out["avatar_updated_at"] = av.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (s *Server) getMyProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfPrincipal(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.profileJSON(r.Context(), p.User))
}

func cleanEmail(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if len(v) > emailMaxLen {
		return "", errors.New("email address is too long")
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Address != v || !strings.Contains(v, "@") {
		return "", fmt.Errorf("%q is not an email address", v)
	}
	return strings.ToLower(v), nil
}

func (s *Server) patchMyProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfPrincipal(w, r)
	if !ok {
		return
	}
	var req struct {
		DisplayName     *string `json:"display_name"`
		Email           *string `json:"email"`
		EmailSecondary  *string `json:"email_secondary"`
		CurrentPassword string  `json:"current_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx := r.Context()
	u, _ := s.Store.GetUser(ctx, p.User.ID)
	if u == nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	prof := s.profileOf(ctx, *u)
	changed := []string{}
	if req.Email != nil || req.EmailSecondary != nil {
		// Changing where account mail goes needs the password.
		if !auth.VerifyPassword(req.CurrentPassword, u.PasswordHash) {
			s.audit(r, u.ClusterID, u.ID, "account.email.update", "denied", "wrong password")
			writeErr(w, http.StatusForbidden, "current password is wrong")
			return
		}
	}
	if req.Email != nil {
		v, err := cleanEmail(*req.Email)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if v != prof.Email {
			prof.Email = v
			prof.EmailVerifiedAt = nil
			changed = append(changed, "email")
		}
	}
	if req.EmailSecondary != nil {
		v, err := cleanEmail(*req.EmailSecondary)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if v != "" && v == prof.Email {
			writeErr(w, http.StatusBadRequest, "the secondary email must differ from the primary one")
			return
		}
		if v != prof.EmailSecondary {
			prof.EmailSecondary = v
			changed = append(changed, "email_secondary")
		}
	}
	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if len(name) > 128 {
			writeErr(w, http.StatusBadRequest, "display name is too long")
			return
		}
		if name != u.DisplayName {
			u.DisplayName = name
			if err := s.Store.UpdateUser(ctx, *u); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			changed = append(changed, "display_name")
		}
	}
	prof.UpdatedAt = s.now()
	if err := s.Store.UpsertAccountProfile(ctx, prof); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(changed) > 0 {
		s.audit(r, u.ClusterID, u.ID, "account.profile.update", "ok", strings.Join(changed, ","))
	}
	writeJSON(w, http.StatusOK, s.profileJSON(ctx, *u))
}

func (s *Server) changeMyPassword(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfPrincipal(w, r)
	if !ok {
		return
	}
	var req struct {
		Current       string `json:"current_password"`
		New           string `json:"new_password"`
		SignOutOthers *bool  `json:"sign_out_others"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx := r.Context()
	key := "pw|" + p.User.ID
	if err := s.lock().Check(key, s.now()); err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	u, _ := s.Store.GetUser(ctx, p.User.ID)
	if u == nil || !auth.VerifyPassword(req.Current, u.PasswordHash) {
		s.lock().Fail(key, s.now())
		s.audit(r, p.User.ClusterID, p.User.ID, "account.password.change", "denied", "wrong password")
		writeErr(w, http.StatusForbidden, "current password is wrong")
		return
	}
	if req.New == req.Current {
		writeErr(w, http.StatusBadRequest, "the new password must differ from the current one")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.UpdatePassword(ctx, u.ID, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.lock().Success(key)
	s.platformOwnerSignedIn(u.ClusterID, *u, req.New)
	if req.SignOutOthers == nil || *req.SignOutOthers {
		// Revoke every session, then give this browser a fresh one.
		_ = s.Store.RevokeUserSessions(ctx, u.ID)
		if err := s.issueSession(w, r, *u, p.AAL); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.audit(r, u.ClusterID, u.ID, "account.password.change", "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) signOutOtherSessions(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfPrincipal(w, r)
	if !ok {
		return
	}
	if err := s.Store.RevokeUserSessions(r.Context(), p.User.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.issueSession(w, r, p.User, p.AAL); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "account.sessions.revoke_others", "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

// ---- profile picture ----

func (s *Server) putMyAvatar(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfPrincipal(w, r)
	if !ok {
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, avatarMaxBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read the picture")
		return
	}
	if len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "the picture is empty")
		return
	}
	if len(data) > avatarMaxBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "the picture must be 2 MB or smaller")
		return
	}
	// Trust the bytes, not the header.
	ct := http.DetectContentType(data)
	switch ct {
	case "image/png", "image/jpeg", "image/webp":
	default:
		writeErr(w, http.StatusUnsupportedMediaType, "use a PNG, JPEG or WebP picture")
		return
	}
	if err := s.Store.SetUserAvatar(r.Context(), appdb.UserAvatar{
		UserID: p.User.ID, ClusterID: p.User.ClusterID, ContentType: ct, Data: data, UpdatedAt: s.now(),
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "account.avatar.update", "ok", "")
	writeJSON(w, http.StatusOK, s.profileJSON(r.Context(), p.User))
}

func (s *Server) deleteMyAvatar(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfPrincipal(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteUserAvatar(r.Context(), p.User.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "account.avatar.delete", "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getUserAvatar(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	u, _ := s.Store.GetUser(r.Context(), id)
	if u == nil || u.ClusterID != p.User.ClusterID {
		writeErr(w, http.StatusNotFound, "no picture")
		return
	}
	av, err := s.Store.GetUserAvatar(r.Context(), id)
	if err != nil || av == nil {
		writeErr(w, http.StatusNotFound, "no picture")
		return
	}
	w.Header().Set("Content-Type", av.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(av.Data)
}

// ---- MFA removal ----

func (s *Server) disableMyMFA(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfPrincipal(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx := r.Context()
	prof := s.profileOf(ctx, p.User)
	if prof.IPAllowlistOn {
		writeErr(w, http.StatusConflict, "turn off your IP allowlist first; it needs MFA")
		return
	}
	key := "mfa|" + p.User.ID
	if err := s.lock().Check(key, s.now()); err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	u, _ := s.Store.GetUser(ctx, p.User.ID)
	method, secret, _, err := s.Store.GetMFAMethod(ctx, p.User.ID)
	if err != nil || method == nil || !method.Enabled {
		writeErr(w, http.StatusConflict, "MFA is not on")
		return
	}
	if u == nil || !auth.VerifyPassword(req.Password, u.PasswordHash) || !mfa.Verify(secret, req.Code, s.now()) {
		s.lock().Fail(key, s.now())
		s.audit(r, p.User.ClusterID, p.User.ID, "account.mfa.disable", "denied", "")
		writeErr(w, http.StatusForbidden, "password or code is wrong")
		return
	}
	if err := s.Store.DeleteUserMFA(ctx, p.User.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.lock().Success(key)
	s.audit(r, p.User.ClusterID, p.User.ID, "account.mfa.disable", "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

// ---- IP allowlist (self) ----

func (s *Server) getMyIP(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.IdentityRead); err != nil {
		return
	}
	c := s.resolveClient(r)
	out := map[string]any{
		"ip":        s.clientIP(r),
		"peer":      peerAddr(r).String(),
		"via_proxy": c.Proxy != nil,
	}
	if c.Proxy != nil {
		out["proxy"] = map[string]any{"cidr": c.Proxy.CIDR, "kind": c.Proxy.Kind, "source": c.Proxy.Source}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getMyAllowlist(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfPrincipal(w, r)
	if !ok {
		return
	}
	out := allowlistJSON(s.profileOf(r.Context(), p.User))
	out["current_ip"] = s.clientIP(r)
	out["mfa_enabled"] = s.mfaEnabled(r.Context(), p.User.ID)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putMyAllowlist(w http.ResponseWriter, r *http.Request) {
	p, ok := s.selfPrincipal(w, r)
	if !ok {
		return
	}
	var in allowlistInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx := r.Context()
	if in.Enabled && !s.mfaEnabled(ctx, p.User.ID) {
		writeErr(w, http.StatusConflict, "set up MFA before turning on an IP allowlist")
		return
	}
	prof := s.profileOf(ctx, p.User)
	if err := applyAllowlist(&prof, in, p.User.Username, s.now()); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ip := s.clientIP(r)
	if prof.IPAllowlistOn && !allowlistContains(prof.IPAllowlist, ip) {
		writeErr(w, http.StatusConflict, "your current address "+ip+" is not on the list; add it first so you are not locked out")
		return
	}
	prof.UpdatedAt = s.now()
	if err := s.Store.UpsertAccountProfile(ctx, prof); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "account.ip_allowlist.update", "ok", fmt.Sprintf("enabled=%t entries=%d", prof.IPAllowlistOn, len(prof.IPAllowlist)))
	out := allowlistJSON(prof)
	out["current_ip"] = ip
	out["mfa_enabled"] = true
	writeJSON(w, http.StatusOK, out)
}

// ---- IP allowlist (admin, in the IAM user modal) ----

func (s *Server) adminUserTarget(w http.ResponseWriter, r *http.Request, perm string) (*principal, *appdb.User, bool) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return nil, nil, false
	}
	u, err := s.Store.GetUser(r.Context(), r.PathValue("id"))
	if err != nil || u == nil || u.ClusterID != p.User.ClusterID {
		writeErr(w, http.StatusNotFound, "user not found")
		return nil, nil, false
	}
	if !s.guardUserHierarchy(w, r, p, u.ID) {
		return nil, nil, false
	}
	return p, u, true
}

func (s *Server) getUserAllowlist(w http.ResponseWriter, r *http.Request) {
	_, u, ok := s.adminUserTarget(w, r, rbac.UsersRead)
	if !ok {
		return
	}
	out := allowlistJSON(s.profileOf(r.Context(), *u))
	out["mfa_enabled"] = s.mfaEnabled(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putUserAllowlist(w http.ResponseWriter, r *http.Request) {
	p, u, ok := s.adminUserTarget(w, r, rbac.UsersUpdate)
	if !ok {
		return
	}
	var in allowlistInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx := r.Context()
	if in.Enabled && !s.mfaEnabled(ctx, u.ID) {
		writeErr(w, http.StatusConflict, "this user has no MFA; an IP allowlist needs it")
		return
	}
	prof := s.profileOf(ctx, *u)
	if err := applyAllowlist(&prof, in, p.User.Username, s.now()); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	prof.UpdatedAt = s.now()
	if err := s.Store.UpsertAccountProfile(ctx, prof); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "user.ip_allowlist.update", "ok",
		fmt.Sprintf("%s enabled=%t entries=%d", u.ID, prof.IPAllowlistOn, len(prof.IPAllowlist)))
	out := allowlistJSON(prof)
	out["mfa_enabled"] = s.mfaEnabled(ctx, u.ID)
	writeJSON(w, http.StatusOK, out)
}

// unlockUser clears failed-login lockouts for a user.
func (s *Server) unlockUser(w http.ResponseWriter, r *http.Request) {
	p, u, ok := s.adminUserTarget(w, r, rbac.UsersUpdate)
	if !ok {
		return
	}
	n := s.lock().ClearSuffix("|" + strings.ToLower(u.Username))
	s.lock().Success("mfa|" + u.ID)
	s.lock().Success("pw|" + u.ID)
	s.audit(r, p.User.ClusterID, p.User.ID, "user.unlock", "ok", fmt.Sprintf("%s cleared=%d", u.ID, n))
	w.WriteHeader(http.StatusNoContent)
}

// resetUserMFA removes a user's MFA so they can enrol again. Their IP
// allowlist goes off with it, since the allowlist needs MFA.
func (s *Server) resetUserMFA(w http.ResponseWriter, r *http.Request) {
	p, u, ok := s.adminUserTarget(w, r, rbac.UsersUpdate)
	if !ok {
		return
	}
	if u.ID == p.User.ID {
		writeErr(w, http.StatusConflict, "use your own Account page to change your MFA")
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != "reset-mfa" {
		writeErr(w, http.StatusUnprocessableEntity, "resetting MFA requires X-Nodal-Confirm: reset-mfa")
		return
	}
	ctx := r.Context()
	if err := s.Store.DeleteUserMFA(ctx, u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	prof := s.profileOf(ctx, *u)
	if prof.IPAllowlistOn {
		prof.IPAllowlistOn = false
		prof.UpdatedAt = s.now()
		_ = s.Store.UpsertAccountProfile(ctx, prof)
	}
	_ = s.Store.RevokeUserSessions(ctx, u.ID)
	s.audit(r, p.User.ClusterID, p.User.ID, "user.mfa.reset", "ok", u.ID)
	w.WriteHeader(http.StatusNoContent)
}

// ---- login-time recovery ----

// refuseBlockedLogin answers a correct-password login from an address the
// user's allowlist does not hold. With MFA it offers recovery; without, it
// says who can help. It returns true when it wrote the response.
func (s *Server) refuseBlockedLogin(w http.ResponseWriter, r *http.Request, user appdb.User) bool {
	ip := s.clientIP(r)
	if !s.ipBlocked(r.Context(), user, ip) {
		return false
	}
	if !s.mfaEnabled(r.Context(), user.ID) {
		s.audit(r, user.ClusterID, user.ID, "auth.login", "denied", "ip not on allowlist: "+ip)
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "this account only signs in from its allowed addresses, and " + ip + " is not one of them. Ask an administrator to add it, or the root account can use `nodalctl recover-ip` on the host.",
			"code":  "ip_not_allowed",
			"ip":    ip,
		})
		return true
	}
	now := s.now()
	if !s.ipRecoveryLimit.allow("u|"+user.ID, now) || !s.ipRecoveryLimit.allow("ip|"+ip, now) {
		s.audit(r, user.ClusterID, user.ID, "auth.ip_recovery", "denied", "rate limited: "+ip)
		writeErr(w, http.StatusTooManyRequests, "too many recovery attempts; try again later")
		return true
	}
	s.audit(r, user.ClusterID, user.ID, "auth.ip_recovery", "started", ip)
	rec := httpCapture{header: http.Header{}}
	s.writeMFAChallenge(&rec, r, user)
	var body map[string]any
	if rec.code != 0 && rec.code != http.StatusOK || json.Unmarshal(rec.body, &body) != nil {
		writeErr(w, http.StatusInternalServerError, "could not start recovery")
		return true
	}
	if id, _ := body["mfa_challenge_id"].(string); id != "" {
		s.ipRecovery.Store(id, pendingIPRecovery{IP: ip, Expires: now.Add(5 * time.Minute)})
	}
	body["ip_recovery"] = true
	body["ip"] = ip
	body["message"] = "This address is not on your IP allowlist. Enter your MFA code to recover access from " + ip + "; it will be added to your list."
	writeJSON(w, http.StatusOK, body)
	return true
}

// finishIPRecovery runs after a successful MFA check for a recovery
// challenge: it adds the verified address and tells the user and admins.
func (s *Server) finishIPRecovery(r *http.Request, user appdb.User, challengeID string) error {
	v, ok := s.ipRecovery.LoadAndDelete(challengeID)
	if !ok {
		return nil
	}
	rec := v.(pendingIPRecovery)
	ip := s.clientIP(r)
	if s.now().After(rec.Expires) || ip != rec.IP {
		s.audit(r, user.ClusterID, user.ID, "auth.ip_recovery", "denied", "address changed or expired")
		return errors.New("recovery must finish from the same address within 5 minutes")
	}
	ctx := r.Context()
	prof := s.profileOf(ctx, user)
	cidr, err := normalizeAllowEntry(ip)
	if err != nil {
		return err
	}
	if !allowlistContains(prof.IPAllowlist, ip) {
		prof.IPAllowlist = append(prof.IPAllowlist, appdb.IPAllowEntry{
			CIDR: cidr, Note: "added by recovery " + s.now().UTC().Format("2006-01-02 15:04 UTC"), AddedBy: "recovery", AddedAt: s.now(),
		})
	}
	prof.UpdatedAt = s.now()
	if err := s.Store.UpsertAccountProfile(ctx, prof); err != nil {
		return err
	}
	s.audit(r, user.ClusterID, user.ID, "auth.ip_recovery", "ok", cidr)
	s.emitEvent(ctx, user.ClusterID, "", "security.ip_recovery", map[string]string{
		"user_id": user.ID, "username": user.Username, "ip": cidr,
		"message": user.Username + " recovered access from " + cidr + " with MFA. If this was not them, remove the address and sign them out.",
	})
	if payload, err := json.Marshal(map[string]any{
		"type": "security.ip_recovery", "username": user.Username, "ip": cidr, "at": s.now().UTC().Format(time.RFC3339),
		"text": "IP allowlist recovery: " + user.Username + " added " + cidr + " after a password and MFA check. If this wasn't them, remove the address in IAM and revoke their sessions.",
	}); err == nil {
		go s.notify(context.WithoutCancel(ctx), user.ClusterID, payload)
	}
	return nil
}

// httpCapture records a response so a handler can be reused inside another.
type httpCapture struct {
	header http.Header
	code   int
	body   []byte
}

func (c *httpCapture) Header() http.Header { return c.header }
func (c *httpCapture) Write(b []byte) (int, error) {
	c.body = append(c.body, b...)
	return len(b), nil
}
func (c *httpCapture) WriteHeader(code int) { c.code = code }

// ---- console recovery (root over the local socket only) ----

func (s *Server) consoleIPRecovery(w http.ResponseWriter, r *http.Request) {
	p, err := s.principal(r)
	if err != nil || p == nil || p.User.ID != LocalRootUserID {
		writeErr(w, http.StatusForbidden, "console recovery only works as root on the host (nodalctl recover-ip)")
		return
	}
	var req struct {
		Username     string `json:"username"`
		AddIP        string `json:"add_ip"`
		Disable      bool   `json:"disable_allowlist"`
		ClearLockout bool   `json:"clear_lockout"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Username) == "" {
		writeErr(w, http.StatusBadRequest, "username is required")
		return
	}
	ctx := r.Context()
	u, _ := s.Store.GetUserByName(ctx, p.User.ClusterID, strings.TrimSpace(req.Username))
	if u == nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	prof := s.profileOf(ctx, *u)
	did := []string{}
	if req.AddIP != "" {
		cidr, err := normalizeAllowEntry(req.AddIP)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		found := false
		for _, e := range prof.IPAllowlist {
			found = found || e.CIDR == cidr
		}
		if !found {
			prof.IPAllowlist = append(prof.IPAllowlist, appdb.IPAllowEntry{CIDR: cidr, Note: "added from the host console", AddedBy: "console", AddedAt: s.now()})
		}
		did = append(did, "added "+cidr)
	}
	if req.Disable && prof.IPAllowlistOn {
		prof.IPAllowlistOn = false
		did = append(did, "allowlist off")
	}
	if req.AddIP != "" || req.Disable {
		prof.UpdatedAt = s.now()
		if err := s.Store.UpsertAccountProfile(ctx, prof); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.ClearLockout {
		s.lock().ClearSuffix("|" + strings.ToLower(u.Username))
		s.lock().Success("mfa|" + u.ID)
		did = append(did, "lockout cleared")
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "console.ip_recovery", "ok", u.Username+": "+strings.Join(did, ", "))
	if len(did) > 0 {
		s.notifyUser(ctx, p.User.ClusterID, u.ID, noteIPRecovery, "Sign-in access recovered from the host",
			"Someone with root on the host changed your sign-in access: "+strings.Join(did, ", ")+".", "/me?tab=security")
	}
	out := allowlistJSON(prof)
	out["username"] = u.Username
	out["done"] = did
	out["disabled_account"] = u.DisabledAt != nil
	writeJSON(w, http.StatusOK, out)
}

// ---- trusted proxies (admin settings) ----

func (s *Server) getTrustedProxies(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsSecurityManage)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, s.trustedProxiesJSON(r, p.User.ClusterID))
}

func (s *Server) trustedProxiesJSON(r *http.Request, clusterID string) map[string]any {
	ctx := r.Context()
	stored, _ := s.Store.ListTrustedProxies(ctx, clusterID)
	off := map[string]bool{}
	manual := []map[string]any{}
	for _, t := range stored {
		if t.Detected {
			off[t.ID] = t.Disabled
			continue
		}
		manual = append(manual, map[string]any{"id": t.ID, "cidr": t.CIDR, "kind": t.Kind, "note": t.Note, "enabled": !t.Disabled})
	}
	detected := []map[string]any{}
	for _, t := range s.detectedProxies(ctx, clusterID) {
		detected = append(detected, map[string]any{"id": t.ID, "cidr": t.CIDR, "kind": t.Kind, "source": t.Source, "enabled": !off[t.ID]})
	}
	sort.Slice(detected, func(i, j int) bool { return detected[i]["id"].(string) < detected[j]["id"].(string) })
	c := s.resolveClient(r)
	current := map[string]any{"ip": s.clientIP(r), "peer": peerAddr(r).String(), "via_proxy": c.Proxy != nil}
	headers := map[string]string{}
	for _, h := range []string{"CF-Connecting-IP", "X-Forwarded-For", "X-Real-IP"} {
		if v := r.Header.Get(h); v != "" {
			headers[h] = v
		}
	}
	current["headers"] = headers
	return map[string]any{"detected": detected, "manual": manual, "current": current}
}

func (s *Server) putTrustedProxies(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsSecurityManage)
	if err != nil {
		return
	}
	var req struct {
		Manual []struct {
			CIDR    string `json:"cidr"`
			Kind    string `json:"kind"`
			Note    string `json:"note"`
			Enabled *bool  `json:"enabled"`
		} `json:"manual"`
		DetectedOff []string `json:"detected_off"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(req.Manual) > 32 {
		writeErr(w, http.StatusBadRequest, "at most 32 trusted proxies")
		return
	}
	items := []appdb.TrustedProxy{}
	for i, m := range req.Manual {
		cidr, err := normalizeAllowEntry(m.CIDR)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		pfx, _ := netip.ParsePrefix(cidr)
		if pfx.Bits() == 0 {
			writeErr(w, http.StatusBadRequest, "a trusted proxy cannot be every address")
			return
		}
		kind := m.Kind
		if kind != appdb.ProxyKindCloudflared {
			kind = appdb.ProxyKindGeneric
		}
		items = append(items, appdb.TrustedProxy{
			ID: fmt.Sprintf("manual:%d", i), ClusterID: p.User.ClusterID, CIDR: cidr, Kind: kind,
			Note: strings.TrimSpace(m.Note), Disabled: m.Enabled != nil && !*m.Enabled, CreatedAt: s.now(),
		})
	}
	for _, id := range req.DetectedOff {
		if strings.HasPrefix(id, "auto:") {
			items = append(items, appdb.TrustedProxy{ID: id, ClusterID: p.User.ClusterID, Detected: true, Disabled: true, CreatedAt: s.now()})
		}
	}
	if err := s.Store.ReplaceTrustedProxies(r.Context(), p.User.ClusterID, items); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.invalidateProxies()
	s.audit(r, p.User.ClusterID, p.User.ID, "settings.trusted_proxies.update", "ok", fmt.Sprintf("manual=%d detected_off=%d", len(req.Manual), len(req.DetectedOff)))
	writeJSON(w, http.StatusOK, s.trustedProxiesJSON(r, p.User.ClusterID))
}
