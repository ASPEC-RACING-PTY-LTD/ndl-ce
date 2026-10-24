package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func curseForgeAAD(clusterID string) string {
	return "gameserver-curseforge/" + clusterID
}

// curseForgeKey is the saved CurseForge API key, or "".
func (s *Server) curseForgeKey(ctx context.Context, clusterID string) string {
	st, err := s.Store.GetGameServerSettings(ctx, clusterID)
	if err != nil || st == nil || st.CurseForgeKeySealed == "" {
		return ""
	}
	key, err := s.cfSealer().Open(curseForgeAAD(clusterID), st.CurseForgeKeySealed)
	if err != nil {
		return ""
	}
	return key
}

// contentProvider is the catalogue for a server's mods. CurseForge gets the
// saved API key; source picks CurseForge or Modrinth for Minecraft mods.
func (s *Server) contentProvider(ctx context.Context, clusterID string, spec gameserver.ContentSpec, source string) (gameserver.ContentProvider, gameserver.ContentSpec) {
	source = strings.TrimSpace(source)
	if spec.GameID == "minecraft" && (source == "modrinth" || source == "curseforge") {
		spec.Provider = source
	}
	if spec.Provider == "curseforge" {
		return gameserver.CurseForge{Key: s.curseForgeKey(ctx, clusterID)}, spec
	}
	return gameserver.ProviderFor(spec), spec
}

// getGameServerSettings is GET /game-servers/settings. The key is never
// read back, only whether one is saved.
func (s *Server) getGameServerSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"curseforge_key_set": s.curseForgeKey(r.Context(), p.User.ClusterID) != ""})
}

// putGameServerSettings is PUT /game-servers/settings.
func (s *Server) putGameServerSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerAdmin)
	if !ok {
		return
	}
	var req struct {
		CurseForgeKey   string `json:"curseforge_key"`
		ClearCurseForge bool   `json:"clear_curseforge"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	st, err := s.Store.GetGameServerSettings(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	next := appdb.GameServerSettings{ClusterID: p.User.ClusterID, UpdatedBy: p.User.Username}
	if st != nil {
		next.CurseForgeKeySealed = st.CurseForgeKeySealed
	}
	key := strings.TrimSpace(req.CurseForgeKey)
	switch {
	case req.ClearCurseForge:
		next.CurseForgeKeySealed = ""
	case key != "":
		// Check the key before saving it, so a typo shows now and not
		// halfway through an install.
		if _, err := gameserver.SearchModpacks(r.Context(), "curseforge", "", key); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, gameserver.HumanError(err.Error()))
			return
		}
		sealed, err := s.cfSealer().Seal(curseForgeAAD(p.User.ClusterID), key)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not seal the API key")
			return
		}
		next.CurseForgeKeySealed = sealed
	}
	if err := s.Store.PutGameServerSettings(r.Context(), next); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "gameserver.settings.update", "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"curseforge_key_set": next.CurseForgeKeySealed != ""})
}

// searchModpacks is GET /game-servers/modpacks?provider=&q=.
func (s *Server) searchModpacks(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerCreate)
	if !ok {
		return
	}
	provider := modpackProvider(r.URL.Query().Get("provider"))
	key := s.curseForgeKey(r.Context(), p.User.ClusterID)
	if provider == "curseforge" && key == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "provider": provider, "needs_key": true})
		return
	}
	items, err := gameserver.SearchModpacks(r.Context(), provider, r.URL.Query().Get("q"), key)
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	if items == nil {
		items = []gameserver.Modpack{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "provider": provider, "template_id": gameserver.ModpackTemplateID(provider)})
}

// modpackVersions is GET /game-servers/modpacks/{provider}/{id}/versions.
// Each version names the Java runtime its Minecraft version needs.
func (s *Server) modpackVersions(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerCreate)
	if !ok {
		return
	}
	provider := modpackProvider(r.PathValue("provider"))
	items, err := gameserver.ModpackVersions(r.Context(), provider, r.PathValue("id"), s.curseForgeKey(r.Context(), p.User.ClusterID))
	if err != nil {
		writeErr(w, http.StatusBadGateway, gameserver.HumanError(err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, v := range items {
		out = append(out, map[string]any{
			"id": v.ID, "name": v.Name, "game_version": v.GameVersion, "loader": v.Loader, "date": v.Date,
			"release": v.Release, "server_pack": v.ServerPack, "image_label": gameserver.JavaImageLabelFor(v.GameVersion),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "template_id": gameserver.ModpackTemplateID(provider)})
}

func modpackProvider(v string) string {
	if strings.TrimSpace(v) == "curseforge" {
		return "curseforge"
	}
	return "modrinth"
}

// contentSources are the catalogues a server's Mods tab can switch between.
func contentSources(spec gameserver.ContentSpec) []string {
	if spec.GameID == "minecraft" && (spec.Kind == "mod" || spec.Kind == "plugin") {
		return []string{"modrinth", "curseforge"}
	}
	return []string{spec.Provider}
}
