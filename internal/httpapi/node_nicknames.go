package httpapi

import (
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Node nicknames are names shown in the UI in place of a node's own name,
// shared by everyone in the cluster. They change nothing on the node: its
// hostname, ID and the names used by the CLI and API stay as they are.
// They are kept in the appearance document.

const maxNodeNickname = 48

// cleanNodeNickname trims a nickname and rejects control characters and
// over-long names. An empty result clears the nickname.
func cleanNodeNickname(raw string) (string, bool) {
	name := strings.Join(strings.Fields(raw), " ")
	if utf8.RuneCountInString(name) > maxNodeNickname {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return name, true
}

func (s *Server) getNodeNicknames(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	names := s.appearance(r.Context(), p.User.ClusterID).NodeNicknames
	if names == nil {
		names = map[string]string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nicknames": names, "can_edit": rbac.Authorize(p.Grants, rbac.NodeUpdate)})
}

func (s *Server) putNodeNickname(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeUpdate)
	if err != nil {
		return
	}
	var req struct {
		NodeID   string `json:"node_id"`
		Nickname string `json:"nickname"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.NodeID) == "" {
		writeErr(w, http.StatusBadRequest, "node_id is required")
		return
	}
	name, ok := cleanNodeNickname(req.Nickname)
	if !ok {
		writeErr(w, http.StatusBadRequest, "a nickname is at most 48 characters, without control characters")
		return
	}
	ctx := r.Context()
	a := s.appearance(ctx, p.User.ClusterID)
	if a.NodeNicknames == nil {
		a.NodeNicknames = map[string]string{}
	}
	id := strings.TrimSpace(req.NodeID)
	if name == "" {
		delete(a.NodeNicknames, id)
	} else {
		a.NodeNicknames[id] = name
	}
	if err := s.saveAppearance(ctx, p.User.ClusterID, p.User.Username, a); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "node.nickname.update", "ok", id)
	writeJSON(w, http.StatusOK, map[string]any{"nicknames": a.NodeNicknames, "can_edit": true})
}
