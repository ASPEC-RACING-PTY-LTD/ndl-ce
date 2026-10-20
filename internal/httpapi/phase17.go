package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
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
		// UIPrefs sets interface preference keys; a null value removes one.
		UIPrefs map[string]json.RawMessage `json:"ui_prefs"`
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
	if req.UIPrefs != nil {
		var cur map[string]json.RawMessage
		if pref != nil {
			cur = pref.UI
		}
		set, remove, err := cleanUIPrefs(cur, req.UIPrefs)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if next, ok := set[designProfilesKey]; ok {
			limit := s.appearance(r.Context(), p.User.ClusterID).limit()
			if err := checkDesignProfiles(cur[designProfilesKey], next, limit); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if err := s.Store.MergeUserUIPrefs(r.Context(), p.User.ID, p.User.ClusterID, set, remove); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Interface preferences change often and grant nothing, so they are
		// not audited and need no other field.
		if req.UXLevel == "" && !req.ExpertAck && req.WorkloadSort == nil && req.WorkloadOrder == nil {
			s.writeMe(w, r, p.User, p.AAL)
			return
		}
		if pref, err = s.Store.GetUserPrefs(r.Context(), p.User.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
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

// Limits for saved interface preferences.
const (
	maxUIPrefKeys   = 200
	maxUIPrefValue  = 16 << 10
	maxUIPrefsTotal = 256 << 10
)

var uiPrefKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,63}$`)

// cleanUIPrefs checks a preferences patch against the saved keys. It returns
// the keys to set and the keys to remove.
func cleanUIPrefs(cur, patch map[string]json.RawMessage) (map[string]json.RawMessage, []string, error) {
	set := map[string]json.RawMessage{}
	var remove []string
	sizes := map[string]int{}
	for k, v := range cur {
		sizes[k] = len(k) + len(v)
	}
	for k, v := range patch {
		if !uiPrefKeyRe.MatchString(k) {
			return nil, nil, fmt.Errorf("ui_prefs key %q is invalid", k)
		}
		trimmed := bytes.TrimSpace(v)
		if len(trimmed) == 0 || string(trimmed) == "null" {
			remove = append(remove, k)
			delete(sizes, k)
			continue
		}
		if len(trimmed) > maxUIPrefValue && !(k == designProfilesKey && len(trimmed) <= maxProfilesBytes) {
			return nil, nil, fmt.Errorf("ui_prefs %s is too large", k)
		}
		if !json.Valid(trimmed) {
			return nil, nil, fmt.Errorf("ui_prefs %s is not valid JSON", k)
		}
		set[k] = append(json.RawMessage(nil), trimmed...)
		sizes[k] = len(k) + len(trimmed)
	}
	total := 0
	for _, n := range sizes {
		total += n
	}
	if len(sizes) > maxUIPrefKeys {
		return nil, nil, errors.New("too many ui_prefs keys")
	}
	if total > maxUIPrefsTotal {
		return nil, nil, errors.New("ui_prefs are too large")
	}
	sort.Strings(remove)
	return set, remove, nil
}
