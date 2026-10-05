package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) patchMe(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.IdentityRead)
	if err != nil {
		return
	}
	var req struct {
		UXLevel       string    `json:"ux_level"`
		ExpertAck     bool      `json:"expert_ack"`
		WorkloadSort  *string   `json:"workload_sort"`
		WorkloadOrder *[]string `json:"workload_order"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	pref, err := s.Store.GetUserPrefs(r.Context(), p.User.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pref == nil {
		pref = &appdb.UserPrefs{UserID: p.User.ID, ClusterID: p.User.ClusterID, UXLevel: appdb.UXGuided}
	}
	if req.ExpertAck && pref.ExpertAckAt == nil {
		now := s.now()
		pref.ExpertAckAt = &now
	}
	if req.UXLevel != "" {
		switch req.UXLevel {
		case appdb.UXGuided, appdb.UXAdvanced, appdb.UXExpert:
		default:
			writeErr(w, http.StatusBadRequest, "ux_level must be guided, advanced, or expert")
			return
		}
		if req.UXLevel == appdb.UXExpert && pref.ExpertAckAt == nil {
			writeErr(w, http.StatusUnprocessableEntity, "expert requires a one-time acknowledgement")
			return
		}
		pref.UXLevel = req.UXLevel
	}
	if req.WorkloadSort != nil {
		switch *req.WorkloadSort {
		case "", "name", "custom":
			pref.WorkloadSort = *req.WorkloadSort
		default:
			writeErr(w, http.StatusBadRequest, "workload_sort must be name or custom")
			return
		}
	}
	if req.WorkloadOrder != nil {
		order, err := cleanWorkloadOrder(*req.WorkloadOrder)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		pref.WorkloadOrder = order
	}
	pref.UpdatedAt = s.now()
	if err := s.Store.UpsertUserPrefs(r.Context(), *pref); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "prefs.update", "ok", pref.UXLevel)
	s.writeMe(w, r, p.User, p.AAL)
}

// maxWorkloadOrder bounds a saved custom Workloads order.
const maxWorkloadOrder = 5000

// cleanWorkloadOrder drops blanks and duplicates from a saved order. Ids are
// only used for ordering, never to read or change a workload.
func cleanWorkloadOrder(in []string) ([]string, error) {
	if len(in) > maxWorkloadOrder {
		return nil, errors.New("workload_order has too many entries")
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if len(id) > 128 {
			return nil, errors.New("workload_order has an invalid id")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func prefsJSON(p *appdb.UserPrefs) (level string, ack bool, ackAt string) {
	level = appdb.UXGuided
	if p != nil && p.UXLevel != "" {
		level = p.UXLevel
	}
	if p != nil && p.ExpertAckAt != nil {
		ack = true
		ackAt = p.ExpertAckAt.UTC().Format(time.RFC3339)
	}
	return level, ack, ackAt
}
