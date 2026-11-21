package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/helix"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

// The settings side of the Helix integration. Everything here needs
// helix.manage (SuperAdmin), except engaging the kill switch, which
// helix.kill (Admin and above) may do. None of it accepts a Helix
// credential: these routes use people's sessions and API tokens only.

func (s *Server) requireHelixManage(w http.ResponseWriter, r *http.Request) (*principal, bool) {
	if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), helixTokenPrefix) {
		writeErr(w, http.StatusForbidden, "a Helix credential cannot change the Helix integration")
		return nil, false
	}
	p, err := s.require(w, r, rbac.HelixManage)
	if err != nil {
		return nil, false
	}
	return p, true
}

func helixCredentialJSON(c helix.Credential, now time.Time) map[string]any {
	out := map[string]any{"id": c.ID, "name": c.Name, "prefix": c.Prefix, "created_at": c.CreatedAt.UTC().Format(time.RFC3339),
		"created_by": c.CreatedBy, "active": c.Active(now), "last_used_ip": c.LastUsedIP}
	if c.ExpiresAt != nil {
		out["expires_at"] = c.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if c.RevokedAt != nil {
		out["revoked_at"], out["revoked_by"] = c.RevokedAt.UTC().Format(time.RFC3339), c.RevokedBy
	}
	if c.LastUsedAt != nil {
		out["last_used_at"] = c.LastUsedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func helixKillJSON(k helix.Kill) map[string]any {
	out := map[string]any{"engaged": k.Engaged, "by": k.By, "reason": k.Reason}
	if k.At != nil {
		out["at"] = k.At.UTC().Format(time.RFC3339)
	}
	return out
}

func (s *Server) helixView(ctx context.Context, clusterID string, c helix.Config) map[string]any {
	now := s.now()
	creds := []map[string]any{}
	for _, cr := range c.Credentials {
		creds = append(creds, helixCredentialJSON(cr, now))
	}
	helixExpireApprovals(&c, now)
	approvals := []map[string]any{}
	pending := 0
	for _, a := range c.Approvals {
		if a.Status == helix.ApprovalPending {
			pending++
		}
		approvals = append(approvals, helixApprovalJSON(a))
	}
	wls, _ := s.Store.ListWorkloads(ctx, clusterID)
	sort.Slice(wls, func(i, j int) bool { return strings.ToLower(wls[i].Name) < strings.ToLower(wls[j].Name) })
	machines := []map[string]any{}
	for _, wl := range wls {
		item := map[string]any{"id": wl.ID, "name": wl.Name, "type": helixMachineType(wl), "status": wl.Status, "exec_capable": helixExecCapable(wl)}
		if g, ok := c.Grant(wl.ID); ok {
			item["grant"] = g
		}
		machines = append(machines, item)
	}
	ops := s.helixRunningOps()
	running := []helixOp{}
	for _, o := range ops {
		if o.Op != "events" {
			running = append(running, o)
		}
	}
	seq, _ := s.Store.LatestHelixEventSeq(ctx, clusterID)
	return map[string]any{
		"enabled": c.Enabled, "kill": helixKillJSON(c.Kill), "credentials": creds, "allowed_ips": c.AllowedIPs,
		"global_events": c.GlobalEvents, "thresholds": c.Thresholds, "machines": machines, "approvals": approvals,
		"pending_approvals": pending, "running": running, "listening": len(ops) - len(running), "latest_event": seq,
		"capabilities": helix.AllCaps, "api_base": "/api/v1/helix/v1",
	}
}

func (s *Server) getHelix(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHelixManage(w, r)
	if !ok {
		return
	}
	c, err := s.helixLoad(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.helixView(r.Context(), p.User.ClusterID, c))
}

func (s *Server) putHelixSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHelixManage(w, r)
	if !ok {
		return
	}
	var in struct {
		Enabled      *bool             `json:"enabled"`
		AllowedIPs   *[]string         `json:"allowed_ips"`
		GlobalEvents *bool             `json:"global_events"`
		Thresholds   *helix.Thresholds `json:"thresholds"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	var ips []string
	if in.AllowedIPs != nil {
		var err error
		if ips, err = helix.CleanAllowedIPs(*in.AllowedIPs); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	if t := in.Thresholds; t != nil && (t.CPUPercent < 10 || t.CPUPercent > 100 || t.MemoryPercent < 10 || t.MemoryPercent > 100) {
		writeErr(w, http.StatusUnprocessableEntity, "thresholds are percentages from 10 to 100")
		return
	}
	var changes []string
	c, err := s.helixUpdate(r.Context(), p.User.ClusterID, p.User.Username, func(c *helix.Config) error {
		if in.Enabled != nil && *in.Enabled != c.Enabled {
			c.Enabled = *in.Enabled
			changes = append(changes, fmt.Sprintf("enabled=%v", c.Enabled))
		}
		if in.AllowedIPs != nil {
			c.AllowedIPs = ips
			changes = append(changes, "allowed addresses: "+firstNonEmpty(strings.Join(ips, ", "), "any"))
		}
		if in.GlobalEvents != nil {
			c.GlobalEvents = *in.GlobalEvents
			changes = append(changes, fmt.Sprintf("global events=%v", c.GlobalEvents))
		}
		if in.Thresholds != nil {
			c.Thresholds = *in.Thresholds
			changes = append(changes, fmt.Sprintf("thresholds cpu %d%% memory %d%%", c.Thresholds.CPUPercent, c.Thresholds.MemoryPercent))
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if in.Enabled != nil && !*in.Enabled {
		s.helixStopAll(r.Context(), p.User.ClusterID)
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "helix.settings", "ok", strings.Join(changes, "; "))
	writeJSON(w, http.StatusOK, s.helixView(r.Context(), p.User.ClusterID, c))
}

func (s *Server) putHelixMachines(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHelixManage(w, r)
	if !ok {
		return
	}
	var in struct {
		Grants []helix.Grant `json:"grants"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "send grants")
		return
	}
	clean := []helix.Grant{}
	seen := map[string]bool{}
	var names []string
	for _, g := range in.Grants {
		wl, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, g.MachineID)
		if err != nil || wl == nil {
			writeErr(w, http.StatusUnprocessableEntity, "machine "+g.MachineID+" does not exist")
			return
		}
		if seen[wl.ID] {
			continue
		}
		seen[wl.ID] = true
		cg, err := helix.CleanGrant(g, !helixExecCapable(*wl))
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, wl.Name+": "+err.Error())
			return
		}
		if wl.Kind == vmspec.KindVM || !helixExecCapable(*wl) {
			cg.RunAs, cg.ExecUsers = "root", nil
		}
		if len(cg.Caps) == 0 {
			continue
		}
		clean = append(clean, cg)
		flags := ""
		if cg.Protected {
			flags = " protected"
		} else if cg.Destructive {
			flags = " destructive-unattended"
		}
		names = append(names, wl.Name+" ["+strings.Join(cg.Caps, ",")+flags+"]")
	}
	c, err := s.helixUpdate(r.Context(), p.User.ClusterID, p.User.Username, func(c *helix.Config) error {
		c.Grants = clean
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "helix.grants", "ok", firstNonEmpty(strings.Join(names, "; "), "no machines"))
	writeJSON(w, http.StatusOK, s.helixView(r.Context(), p.User.ClusterID, c))
}

func (s *Server) createHelixCredential(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHelixManage(w, r)
	if !ok {
		return
	}
	var in struct {
		Name    string `json:"name"`
		TTLDays int    `json:"ttl_days"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 64 {
		writeErr(w, http.StatusBadRequest, "name the credential (at most 64 characters)")
		return
	}
	if in.TTLDays < 0 || in.TTLDays > 365 {
		writeErr(w, http.StatusBadRequest, "ttl_days is 0 (never expires) to 365")
		return
	}
	raw, err := secutil.RandomHex(32)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	plain := helixTokenPrefix + raw
	now := s.now()
	cred := helix.Credential{ID: uuid.NewString(), Name: name, Prefix: plain[:10], Hash: secutil.HashSHA256(plain), CreatedAt: now, CreatedBy: p.User.Username}
	if in.TTLDays > 0 {
		exp := now.Add(time.Duration(in.TTLDays) * 24 * time.Hour)
		cred.ExpiresAt = &exp
	}
	c, err := s.helixUpdate(r.Context(), p.User.ClusterID, p.User.Username, func(c *helix.Config) error {
		active := 0
		for _, x := range c.Credentials {
			if x.Active(now) {
				active++
			}
		}
		if active >= helixMaxCredentials {
			return errors.New("revoke a credential first: at most 10 can be active")
		}
		c.Credentials = append(c.Credentials, cred)
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "helix.credential.create", "ok", name+" ("+cred.Prefix+"…)")
	s.notifyWithPermission(context.WithoutCancel(r.Context()), p.User.ClusterID, rbac.HelixManage, p.User.ID, noteTokenCreated,
		"A Helix credential was created", p.User.Username+" created the Helix credential "+name+".", "/helix")
	writeJSON(w, http.StatusCreated, map[string]any{"credential": helixCredentialJSON(cred, now), "token": plain, "view": s.helixView(r.Context(), p.User.ClusterID, c)})
}

func (s *Server) revokeHelixCredential(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHelixManage(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	now := s.now()
	name := ""
	c, err := s.helixUpdate(r.Context(), p.User.ClusterID, p.User.Username, func(c *helix.Config) error {
		for i := range c.Credentials {
			if c.Credentials[i].ID == id {
				name = c.Credentials[i].Name
				if c.Credentials[i].RevokedAt == nil {
					c.Credentials[i].RevokedAt, c.Credentials[i].RevokedBy = &now, p.User.Username
				}
				return nil
			}
		}
		return errors.New("credential not found")
	})
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "helix.credential.revoke", "ok", name)
	writeJSON(w, http.StatusOK, s.helixView(r.Context(), p.User.ClusterID, c))
}

func (s *Server) decideHelixApproval(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHelixManage(w, r)
	if !ok {
		return
	}
	decision := r.PathValue("decision")
	if decision != "approve" && decision != "deny" {
		writeErr(w, http.StatusNotFound, "approve or deny")
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	_ = readJSON(r, &in)
	now := s.now()
	var got helix.Approval
	c, err := s.helixUpdate(r.Context(), p.User.ClusterID, p.User.Username, func(c *helix.Config) error {
		helixExpireApprovals(c, now)
		for i := range c.Approvals {
			a := &c.Approvals[i]
			if a.ID != r.PathValue("id") {
				continue
			}
			if a.Status != helix.ApprovalPending {
				return fmt.Errorf("this request is already %s", a.Status)
			}
			a.Status, a.DecidedAt, a.DecidedBy, a.Note = helix.ApprovalDenied, &now, p.User.Username, helix.Summary(in.Note, 300)
			if decision == "approve" {
				a.Status, a.ExpiresAt = helix.ApprovalApproved, now.Add(helix.ApprovedTTL)
			}
			got = *a
			return nil
		}
		return errors.New("approval not found")
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "helix.approval."+decision, "ok", got.MachineName+": "+got.Summary)
	s.helixEmit(r.Context(), p.User.ClusterID, "helix.approval_"+got.Status, "info", got.MachineID, map[string]any{"approval_id": got.ID, "by": p.User.Username, "note": got.Note})
	writeJSON(w, http.StatusOK, s.helixView(r.Context(), p.User.ClusterID, c))
}

func (s *Server) getHelixKill(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.HelixKill)
	if err != nil {
		return
	}
	c, err := s.helixLoad(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": c.Enabled, "kill": helixKillJSON(c.Kill), "running": len(s.helixRunningOps())})
}

// engageHelixKill stops Helix at once: every request is refused, running
// commands and Helix's deployments are cancelled, and (optionally) every
// credential is revoked. Only a SuperAdmin can release it.
func (s *Server) engageHelixKill(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), helixTokenPrefix) {
		writeErr(w, http.StatusForbidden, "a Helix credential cannot use the kill switch")
		return
	}
	p, err := s.require(w, r, rbac.HelixKill)
	if err != nil {
		return
	}
	var in struct {
		Reason            string `json:"reason"`
		RevokeCredentials bool   `json:"revoke_credentials"`
	}
	_ = readJSON(r, &in)
	now := s.now()
	revoked := 0
	c, err := s.helixUpdate(r.Context(), p.User.ClusterID, p.User.Username, func(c *helix.Config) error {
		c.Kill = helix.Kill{Engaged: true, At: &now, By: p.User.Username, Reason: helix.Summary(in.Reason, 300)}
		if in.RevokeCredentials {
			for i := range c.Credentials {
				if c.Credentials[i].RevokedAt == nil {
					c.Credentials[i].RevokedAt, c.Credentials[i].RevokedBy = &now, p.User.Username+" (kill switch)"
					revoked++
				}
			}
		}
		for i := range c.Approvals {
			if a := &c.Approvals[i]; a.Status == helix.ApprovalPending || a.Status == helix.ApprovalApproved {
				a.Status = helix.ApprovalExpired
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ops, deploys := s.helixStopAll(context.WithoutCancel(r.Context()), p.User.ClusterID)
	detail := fmt.Sprintf("stopped %d operation(s) and %d deployment(s); revoked %d credential(s); reason: %s", ops, deploys, revoked, firstNonEmpty(in.Reason, "none given"))
	s.audit(r, p.User.ClusterID, p.User.ID, "helix.kill.engage", "ok", detail)
	go s.notifyWithPermission(context.Background(), p.User.ClusterID, rbac.HelixManage, p.User.ID, "helix.kill",
		"The Helix kill switch was engaged", p.User.Username+" engaged it: "+detail, "/helix")
	writeJSON(w, http.StatusOK, map[string]any{"kill": helixKillJSON(c.Kill), "stopped_operations": ops, "cancelled_deployments": deploys, "revoked_credentials": revoked})
}

func (s *Server) releaseHelixKill(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHelixManage(w, r)
	if !ok {
		return
	}
	var was helix.Kill
	c, err := s.helixUpdate(r.Context(), p.User.ClusterID, p.User.Username, func(c *helix.Config) error {
		was = c.Kill
		c.Kill = helix.Kill{}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "helix.kill.release", "ok", "engaged by "+firstNonEmpty(was.By, "nobody"))
	writeJSON(w, http.StatusOK, s.helixView(r.Context(), p.User.ClusterID, c))
}

// helixActivity is the audit trail for Helix: its own calls and the
// settings changes people made.
func (s *Server) helixActivity(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHelixManage(w, r)
	if !ok {
		return
	}
	rows, err := s.Store.ListAuditEvents(r.Context(), p.User.ClusterID, 5000)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := []map[string]any{}
	for _, a := range rows {
		if !strings.HasPrefix(a.Action, "helix.") {
			continue
		}
		var detail map[string]any
		_ = json.Unmarshal(a.Detail, &detail)
		items = append(items, map[string]any{
			"id": a.ID, "at": a.CreatedAt.UTC().Format(time.RFC3339), "action": a.Action, "result": a.Result,
			"actor": firstNonEmpty(a.ActorUsername, a.ActorUserID), "ip": a.RemoteAddr, "detail": detail,
		})
		if len(items) >= 200 {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
