package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/mfa"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
)

const (
	mfaIssuer             = "No-dal"
	clusterDestroyConfirm = "destroy-cluster"
	volumeUnlockReason    = "Volume encryption unlock is not available for Directory storage. LUKS and ZFS native encryption are later storage backends."
)

func (s *Server) writeMFAChallenge(w http.ResponseWriter, r *http.Request, user appdb.User) {
	raw, err := secutil.RandomHex(24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ch := appdb.MFAChallenge{
		ID:        uuid.NewString(),
		ClusterID: user.ClusterID,
		UserID:    user.ID,
		TokenHash: secutil.HashSHA256(raw),
		ExpiresAt: s.now().Add(5 * time.Minute),
	}
	if err := s.Store.CreateMFAChallenge(r.Context(), ch); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, user.ClusterID, user.ID, "auth.mfa.challenge", "ok", ch.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"mfa_required":     true,
		"mfa_challenge_id": ch.ID,
		"mfa_token":        raw,
	})
}

func (s *Server) verifyMFA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChallengeID string `json:"mfa_challenge_id"`
		Token       string `json:"mfa_token"`
		Code        string `json:"code"`
	}
	if err := readJSON(r, &req); err != nil || req.ChallengeID == "" || req.Code == "" {
		writeErr(w, http.StatusBadRequest, "mfa_challenge_id and code are required")
		return
	}
	ch, err := s.Store.GetMFAChallengeByHash(r.Context(), secutil.HashSHA256(req.Token))
	if err != nil || ch == nil || ch.ID != req.ChallengeID || ch.ConsumedAt != nil || s.now().After(ch.ExpiresAt) {
		writeErr(w, http.StatusUnauthorized, "mfa challenge is invalid")
		return
	}
	lockKey := "mfa|" + ch.UserID
	if err := s.lock().Check(lockKey, s.now()); err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	user, err := s.Store.GetUser(r.Context(), ch.UserID)
	if err != nil || user == nil {
		writeErr(w, http.StatusUnauthorized, "mfa challenge is invalid")
		return
	}
	method, secret, hashes, err := s.Store.GetMFAMethod(r.Context(), user.ID)
	if err != nil || method == nil || !method.Enabled {
		writeErr(w, http.StatusUnauthorized, "mfa is not enabled")
		return
	}
	ok := mfa.Verify(secret, req.Code, s.now())
	if !ok {
		h := mfa.HashRecovery(req.Code)
		if err := s.Store.ConsumeRecoveryHash(r.Context(), user.ID, h); err != nil {
			s.lock().Fail(lockKey, s.now())
			s.audit(r, user.ClusterID, user.ID, "auth.mfa.verify", "denied", ch.ID)
			writeErr(w, http.StatusUnauthorized, "invalid mfa code")
			return
		}
		_ = hashes
	}
	if err := s.Store.ConsumeMFAChallenge(r.Context(), ch.ID); err != nil {
		writeErr(w, http.StatusUnauthorized, "mfa challenge is invalid")
		return
	}
	s.lock().Success(lockKey)
	if err := s.issueSession(w, r, *user, 2); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, user.ClusterID, user.ID, "auth.mfa.verify", "ok", ch.ID)
	s.writeMe(w, r, *user, 2)
}

func (s *Server) enrollMFA(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityMFA)
	if err != nil {
		return
	}
	if p.TokenID != "" {
		writeErr(w, http.StatusForbidden, "API tokens cannot enroll MFA")
		return
	}
	if p.User.Kind == appdb.UserKindService {
		writeErr(w, http.StatusForbidden, "service principals cannot enroll MFA")
		return
	}
	existing, _, _, err := s.Store.GetMFAMethod(r.Context(), p.User.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing != nil && existing.Enabled {
		writeErr(w, http.StatusConflict, "mfa is already enabled")
		return
	}
	secret, err := mfa.GenerateSecret()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	plain, hashes, err := mfa.RecoveryCodes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	row := appdb.MFAMethod{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, UserID: p.User.ID,
		Kind: appdb.MFAKindTOTP, Enabled: false,
	}
	if err := s.Store.UpsertMFAMethod(r.Context(), row, secret, hashes); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "mfa.enroll", "ok", row.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"kind":           appdb.MFAKindTOTP,
		"secret":         secret,
		"otpauth_url":    mfa.OTPAuthURL(mfaIssuer, p.User.Username, secret),
		"recovery_codes": plain,
		"enabled":        false,
	})
}

func (s *Server) confirmMFA(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityMFA)
	if err != nil {
		return
	}
	if p.TokenID != "" {
		writeErr(w, http.StatusForbidden, "API tokens cannot confirm MFA")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &req); err != nil || req.Code == "" {
		writeErr(w, http.StatusBadRequest, "code is required")
		return
	}
	method, secret, _, err := s.Store.GetMFAMethod(r.Context(), p.User.ID)
	if err != nil || method == nil {
		writeErr(w, http.StatusNotFound, "mfa enrollment is not started")
		return
	}
	if !mfa.Verify(secret, req.Code, s.now()) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid mfa code")
		return
	}
	if err := s.Store.EnableMFAMethod(r.Context(), p.User.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not enable mfa")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "mfa.enable", "ok", method.ID)
	writeJSON(w, http.StatusOK, map[string]any{"kind": method.Kind, "enabled": true})
}

func (s *Server) getMFA(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityMFA)
	if err != nil {
		return
	}
	method, _, _, err := s.Store.GetMFAMethod(r.Context(), p.User.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if method == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "kind": "not_configured"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": method.Enabled, "kind": method.Kind})
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AuditRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListAuditEvents(r.Context(), p.User.ClusterID, 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, e := range items {
		row := map[string]any{
			"id": e.ID, "action": e.Action, "result": e.Result,
			"created_at": e.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if e.ActorUserID != "" {
			row["actor_user_id"] = e.ActorUserID
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	items, err := s.Store.ListGroups(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, g := range items {
		members, _ := s.Store.ListGroupMembers(r.Context(), p.User.ClusterID, g.ID)
		out = append(out, map[string]any{"id": g.ID, "name": g.Name, "member_ids": members})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityGroupManage)
	if err != nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	g := appdb.Group{ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name)}
	if err := s.Store.CreateGroup(r.Context(), g); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "group.create", "ok", g.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"id": g.ID, "name": g.Name, "member_ids": []string{}})
}

func (s *Server) addGroupMember(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityGroupManage)
	if err != nil {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := readJSON(r, &req); err != nil || req.UserID == "" {
		writeErr(w, http.StatusBadRequest, "user_id is required")
		return
	}
	groupID := r.PathValue("id")
	g, err := s.Store.GetGroup(r.Context(), p.User.ClusterID, groupID)
	if err != nil || g == nil {
		writeErr(w, http.StatusNotFound, "group not found")
		return
	}
	if err := s.Store.AddGroupMember(r.Context(), p.User.ClusterID, g.ID, req.UserID); err != nil {
		writeErr(w, http.StatusNotFound, "group not found")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "group.member.add", "ok", req.UserID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) bindGroupRole(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityGroupManage)
	if err != nil {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "role is required")
		return
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		writeErr(w, http.StatusBadRequest, "role is required")
		return
	}
	switch role {
	case rbac.Operator, rbac.Viewer:
	default:
		writeErr(w, http.StatusForbidden, "groups cannot grant admin; use operator or viewer")
		return
	}
	groupID := r.PathValue("id")
	g, err := s.Store.GetGroup(r.Context(), p.User.ClusterID, groupID)
	if err != nil || g == nil {
		writeErr(w, http.StatusNotFound, "group not found")
		return
	}
	if err := s.Store.BindGroupRole(r.Context(), p.User.ClusterID, g.ID, role); err != nil {
		writeErr(w, http.StatusNotFound, "group not found")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "group.role.bind", "ok", role)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": role})
}

func (s *Server) createServicePrincipal(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityService)
	if err != nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	u := appdb.User{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID,
		Username: "svc-" + strings.TrimSpace(req.Name), PasswordHash: "!", Kind: appdb.UserKindService,
	}
	if err := s.Store.CreateUser(r.Context(), u); err != nil {
		writeErr(w, http.StatusConflict, "service principal exists")
		return
	}
	if err := s.Store.BindRole(r.Context(), p.User.ClusterID, u.ID, rbac.Operator); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	raw, err := secutil.RandomHex(24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	plain := "ndl_" + raw
	tok := appdb.APIToken{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, UserID: u.ID,
		Name: "service", TokenHash: secutil.HashSHA256(plain), Prefix: plain[:8],
	}
	if err := s.Store.CreateToken(r.Context(), tok); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sp := appdb.ServicePrincipal{ID: uuid.NewString(), ClusterID: p.User.ClusterID, UserID: u.ID, Name: strings.TrimSpace(req.Name)}
	if err := s.Store.CreateServicePrincipal(r.Context(), sp); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record service principal")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "service-principal.create", "ok", sp.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": sp.ID, "name": sp.Name, "user_id": u.ID, "token": plain, "kind": appdb.UserKindService,
	})
}

func (s *Server) listServicePrincipals(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityService)
	if err != nil {
		return
	}
	items, err := s.Store.ListServicePrincipals(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, sp := range items {
		out = append(out, map[string]any{"id": sp.ID, "name": sp.Name, "user_id": sp.UserID, "kind": appdb.UserKindService})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) requireAAL(w http.ResponseWriter, p *principal, min int) bool {
	if p.AAL >= min {
		return true
	}
	writeErr(w, http.StatusForbidden, "step-up authentication is required")
	return false
}

func (s *Server) revealSecret(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SecretReveal)
	if err != nil {
		return
	}
	if !s.requireAAL(w, p, 2) {
		return
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":  "secret reveal is not configured for this name",
		"status": "not_configured",
	})
}

func (s *Server) destroyCluster(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ClusterDestroy)
	if err != nil {
		return
	}
	if !s.requireAAL(w, p, 2) {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != clusterDestroyConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "destroy requires X-Nodal-Confirm: destroy-cluster")
		return
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":  "cluster destroy is not implemented in Community Edition",
		"status": "not_implemented",
	})
}

func (s *Server) unlockVolume(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SecretUse)
	if err != nil {
		return
	}
	if !s.requireAAL(w, p, 2) {
		return
	}
	enc, err := s.Store.GetVolumeEncryption(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if enc == nil || !enc.Encrypted || enc.EncryptionKind == appdb.EncryptionNone {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": volumeUnlockReason, "status": "not_configured"})
		return
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": volumeUnlockReason, "status": "unsupported"})
}
