package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/ai"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) listAIProviders(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AIAsk)
	if err != nil {
		return
	}
	items, err := s.Store.ListAIProviders(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, prov := range items {
		key, _ := s.Store.AIProviderKey(r.Context(), p.User.ClusterID, prov.ID)
		out = append(out, aiProviderJSON(prov, key != ""))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createAIProvider(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AIManage)
	if err != nil {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Endpoint string `json:"endpoint"`
		Model    string `json:"model"`
		APIKey   string `json:"api_key"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	kind, err := ai.NormalizeKind(req.Kind)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	row := appdb.AIProvider{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: name, Kind: kind,
		Endpoint: strings.TrimSpace(req.Endpoint), Model: strings.TrimSpace(req.Model), Enabled: true,
	}
	if err := s.Store.CreateAIProvider(r.Context(), row, req.APIKey); err != nil {
		writeErr(w, http.StatusConflict, "could not record provider")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "ai.provider.create", "ok", row.ID)
	writeJSON(w, http.StatusCreated, aiProviderJSON(row, strings.TrimSpace(req.APIKey) != ""))
}

func (s *Server) listAIProfiles(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AIAsk)
	if err != nil {
		return
	}
	items, err := s.Store.ListAIProfiles(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, prof := range items {
		out = append(out, aiProfileJSON(prof))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createAIProfile(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AIManage)
	if err != nil {
		return
	}
	var req struct {
		Name       string   `json:"name"`
		ProviderID string   `json:"provider_id"`
		Mode       string   `json:"mode"`
		Grants     []string `json:"grants"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	mode, err := ai.NormalizeMode(req.Mode)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	grants := req.Grants
	if grants == nil {
		grants = ai.DefaultAskGrants()
	}
	if req.ProviderID != "" {
		prov, _ := s.Store.GetAIProvider(r.Context(), p.User.ClusterID, req.ProviderID)
		if prov == nil {
			writeErr(w, http.StatusNotFound, "provider not found")
			return
		}
	}
	row := appdb.AIProfile{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: name, ProviderID: strings.TrimSpace(req.ProviderID),
		Mode: mode, Grants: grants,
	}
	if err := s.Store.CreateAIProfile(r.Context(), row); err != nil {
		writeErr(w, http.StatusConflict, "could not record profile")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "ai.profile.create", "ok", row.ID)
	writeJSON(w, http.StatusCreated, aiProfileJSON(row))
}

func (s *Server) aiAsk(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AIAsk)
	if err != nil {
		return
	}
	var req struct {
		Prompt    string `json:"prompt"`
		ProfileID string `json:"profile_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeErr(w, http.StatusBadRequest, "prompt is required")
		return
	}
	grants := ai.DefaultAskGrants()
	var profile *appdb.AIProfile
	if strings.TrimSpace(req.ProfileID) != "" {
		profile, _ = s.Store.GetAIProfile(r.Context(), p.User.ClusterID, req.ProfileID)
		if profile == nil {
			writeErr(w, http.StatusNotFound, "profile not found")
			return
		}
		grants = profile.Grants
	}
	if !ai.CanQuery(grants) {
		writeErr(w, http.StatusForbidden, "profile without read cannot query")
		return
	}
	events, _ := s.Store.ListEvents(r.Context(), p.User.ClusterID, 20)
	var series []metrics.Series
	if s.Observer != nil {
		res, err := s.Observer.GetMetrics(r.Context(), time.Now().UTC().Add(-time.Hour), time.Now().UTC())
		if err == nil {
			series = res.Series
		}
	}
	ctx := ai.BuildContext(events, series, 20)
	answer := ai.LocalAnswer(prompt, ctx)
	providerStatus := "not_configured"
	providerKind := ai.KindLocal
	if profile != nil && profile.ProviderID != "" {
		prov, _ := s.Store.GetAIProvider(r.Context(), p.User.ClusterID, profile.ProviderID)
		if prov != nil && prov.Enabled {
			providerKind = prov.Kind
			if prov.Kind == ai.KindLocal {
				providerStatus = "local"
			} else {
				key, _ := s.Store.AIProviderKey(r.Context(), p.User.ClusterID, prov.ID)
				text, perr := s.completeAI(r.Context(), *prov, key, prompt, ctx)
				if perr != nil {
					providerStatus = "unavailable"
					answer = answer + " Provider unavailable. Local citations still apply. The platform is otherwise unchanged."
				} else if strings.TrimSpace(text) != "" {
					providerStatus = "answered"
					answer = ai.Redact(text) + " " + answer
				}
			}
		}
	}
	citations := append([]ai.Citation{}, ctx.Events...)
	citations = append(citations, ctx.Metrics...)
	s.audit(r, p.User.ClusterID, p.User.ID, "ai.ask", "ok", profileIDOrEmpty(profile))
	writeJSON(w, http.StatusOK, map[string]any{
		"answer":           ai.Redact(answer),
		"citations":        citations,
		"provider_status":  providerStatus,
		"provider_kind":    providerKind,
		"mode":             ai.ModeAsk,
		"mutate":           false,
		"service_identity": "ask",
	})
}

func (s *Server) completeAI(ctx context.Context, prov appdb.AIProvider, apiKey, prompt string, infra ai.Context) (string, error) {
	if prov.Kind == ai.KindLocal {
		return "", fmt.Errorf("local kind has no remote model")
	}
	completer := s.AICompleter
	if completer == nil {
		completer = ai.HTTPCompleter{Client: s.HTTPClient}
	}
	var b strings.Builder
	b.WriteString("You are a read-only No-dal Ask assistant. Do not mutate. Do not emit secrets.\nQuestion: ")
	b.WriteString(ai.Redact(prompt))
	b.WriteString("\nContext:\n")
	for _, c := range infra.Events {
		b.WriteString("- event ")
		b.WriteString(c.Summary)
		b.WriteByte('\n')
	}
	for _, c := range infra.Metrics {
		b.WriteString("- metric ")
		b.WriteString(c.Summary)
		b.WriteByte('\n')
	}
	return completer.Complete(ctx, ai.CompleteRequest{
		Kind: prov.Kind, Endpoint: prov.Endpoint, Model: prov.Model, APIKey: apiKey, Prompt: b.String(),
	})
}

func profileIDOrEmpty(p *appdb.AIProfile) string {
	if p == nil {
		return ""
	}
	return p.ID
}

func aiProviderJSON(p appdb.AIProvider, hasKey bool) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "kind": p.Kind, "endpoint": p.Endpoint, "model": p.Model,
		"enabled": p.Enabled, "has_credentials": hasKey,
	}
}

func aiProfileJSON(p appdb.AIProfile) map[string]any {
	grants := p.Grants
	if grants == nil {
		grants = []string{}
	}
	return map[string]any{
		"id": p.ID, "name": p.Name, "provider_id": p.ProviderID, "mode": p.Mode, "grants": grants,
	}
}
