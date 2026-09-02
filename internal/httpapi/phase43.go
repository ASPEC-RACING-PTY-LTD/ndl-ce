package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/license"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) getLicense(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsLicenseRead)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, s.licenseJSON(r.Context(), p.User.ClusterID))
}

func (s *Server) activateLicense(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsLicenseManage)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != license.ActivateConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "activating a license requires X-Nodal-Confirm: activate-license")
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Key) == "" {
		writeErr(w, http.StatusBadRequest, "key is required")
		return
	}
	key := strings.TrimSpace(req.Key)
	now := time.Now().UTC()
	st := appdb.LicenseState{
		ClusterID: p.User.ClusterID, Status: appdb.LicenseGrace, LastChecked: &now,
		Reason: "Key stored. Licensing API was not reachable. Community Edition continues. Workloads are not stopped.",
	}
	if s.LicenseProbe != nil {
		if err := s.LicenseProbe.Check(r.Context(), key); err != nil {
			st.Status = appdb.LicenseUnreachable
			st.Reason = "Licensing API unreachable. Grace applies. Workloads are not stopped."
		} else {
			st.Status = appdb.LicenseActive
			st.Reason = "Key accepted. This installation remains Community Edition until signed EE artifacts exist. Workloads are not stopped."
		}
	} else {
		if err := (license.HTTPProbe{Client: s.HTTPClient}).Check(r.Context(), key); err != nil {
			st.Status = appdb.LicenseUnreachable
			st.Reason = "Licensing API unreachable. Grace applies. Workloads are not stopped."
		} else {
			st.Status = appdb.LicenseActive
			st.Reason = "Key accepted. This installation remains Community Edition until signed EE artifacts exist. Workloads are not stopped."
		}
	}
	if err := s.Store.PutLicenseState(r.Context(), st, key); err != nil {
		writeErr(w, http.StatusConflict, "could not record license")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "license.activate", st.Status, "")
	writeJSON(w, http.StatusOK, s.licenseJSON(r.Context(), p.User.ClusterID))
}

func (s *Server) clearLicense(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsLicenseManage)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != license.ClearConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "clearing a license requires X-Nodal-Confirm: clear-license")
		return
	}
	if err := s.Store.ClearLicense(r.Context(), p.User.ClusterID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "license.clear", "ok", "")
	writeJSON(w, http.StatusOK, s.licenseJSON(r.Context(), p.User.ClusterID))
}

func (s *Server) licenseJSON(ctx context.Context, clusterID string) map[string]any {
	st, key, err := s.Store.GetLicenseState(ctx, clusterID)
	if err != nil || st == nil {
		st = &appdb.LicenseState{ClusterID: clusterID, Status: appdb.LicenseAbsent, Reason: "Community Edition. License activation is not required."}
	}
	out := map[string]any{
		"edition":           license.EditionCE,
		"status":            st.Status,
		"reason":            st.Reason,
		"has_key":           key != "",
		"key_suffix":        license.Last4(key),
		"workloads_stopped": false,
		"ee_blobs":          false,
		"contacts_api":      key != "",
	}
	if st.LastChecked != nil {
		out["last_checked"] = st.LastChecked.UTC().Format(time.RFC3339)
	}
	return out
}
