package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func joinAccessAAD(clusterID string) string {
	return "cluster-join-access/" + clusterID
}

// joinSettingsJSON never includes the service token secret, only whether
// one is saved.
func joinSettingsJSON(st *appdb.ClusterJoinSettings) map[string]any {
	out := map[string]any{"join_url": "", "access_client_id": "", "access_secret_set": false}
	if st != nil {
		out["join_url"] = st.JoinURL
		out["access_client_id"] = st.AccessClientID
		out["access_secret_set"] = st.AccessSecretSealed != ""
	}
	return out
}

// getJoinSettings is GET /cluster/join-settings.
func (s *Server) getJoinSettings(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ClusterJoin)
	if err != nil {
		return
	}
	st, err := s.Store.GetClusterJoinSettings(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, joinSettingsJSON(st))
}

// putJoinSettings is PUT /cluster/join-settings. An empty secret keeps the
// saved one; clear_access removes the service token.
func (s *Server) putJoinSettings(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ClusterJoin)
	if err != nil {
		return
	}
	if !s.requireWriter(w, r, p.User.ClusterID) {
		return
	}
	var req struct {
		JoinURL            string `json:"join_url"`
		AccessClientID     string `json:"access_client_id"`
		AccessClientSecret string `json:"access_client_secret"`
		ClearAccess        bool   `json:"clear_access"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	joinURL := strings.TrimRight(strings.TrimSpace(req.JoinURL), "/")
	if joinURL != "" {
		u, perr := url.Parse(joinURL)
		if perr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			writeErr(w, http.StatusBadRequest, "join URL must be an http or https address, such as https://nodal.example.com")
			return
		}
	}
	cur, err := s.Store.GetClusterJoinSettings(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	next := appdb.ClusterJoinSettings{ClusterID: p.User.ClusterID, JoinURL: joinURL, UpdatedBy: p.User.Username}
	if cur != nil {
		next.AccessClientID, next.AccessSecretSealed = cur.AccessClientID, cur.AccessSecretSealed
	}
	id := strings.TrimSpace(req.AccessClientID)
	secret := strings.TrimSpace(req.AccessClientSecret)
	switch {
	case req.ClearAccess:
		next.AccessClientID, next.AccessSecretSealed = "", ""
	case id != "":
		if secret == "" && (next.AccessSecretSealed == "" || id != next.AccessClientID) {
			writeErr(w, http.StatusBadRequest, "enter the service token's client secret")
			return
		}
		next.AccessClientID = id
		if secret != "" {
			sealed, serr := s.cfSealer().Seal(joinAccessAAD(p.User.ClusterID), secret)
			if serr != nil {
				writeErr(w, http.StatusInternalServerError, "could not seal the client secret")
				return
			}
			next.AccessSecretSealed = sealed
		}
	case secret != "":
		writeErr(w, http.StatusBadRequest, "enter the service token's client ID")
		return
	}
	if err := s.Store.PutClusterJoinSettings(r.Context(), next); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "cluster.join_settings.update", "ok", "")
	writeJSON(w, http.StatusOK, joinSettingsJSON(&next))
}

// joinAccess returns the saved Cloudflare Access service token, if any.
func (s *Server) joinAccess(ctx context.Context, clusterID string) (*appdb.ClusterJoinSettings, string) {
	st, err := s.Store.GetClusterJoinSettings(ctx, clusterID)
	if err != nil || st == nil {
		return nil, ""
	}
	if st.AccessClientID == "" || st.AccessSecretSealed == "" {
		return st, ""
	}
	secret, err := s.cfSealer().Open(joinAccessAAD(clusterID), st.AccessSecretSealed)
	if err != nil {
		return st, ""
	}
	return st, secret
}
