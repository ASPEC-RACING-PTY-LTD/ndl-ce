package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/ndltls"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const tlsEnableConfirm = "enable-tls"

func (s *Server) certsJSON(ctx context.Context, clusterID string) map[string]any {
	out := map[string]any{
		"enabled":     s.TLSRequired,
		"mode":        "",
		"common_name": "",
		"sans":        []string{},
		"fingerprint": "",
		"acme_status": ndltls.ACMENotConfigured,
		"tls_listen":  s.tlsListen(),
		"http_listen": s.httpListen(),
		"https_url":   s.httpsURL(),
		"trust_note":  "Trust the SHA-256 fingerprint. A self-signed certificate is not a public CA.",
	}
	row, err := s.Store.GetCertificate(ctx, clusterID)
	if err != nil || row == nil {
		return out
	}
	out["enabled"] = row.Enabled
	out["mode"] = row.Mode
	out["common_name"] = row.CommonName
	if row.SANs != nil {
		out["sans"] = row.SANs
	}
	out["fingerprint"] = row.Fingerprint
	out["acme_directory"] = row.ACMEDirectory
	out["acme_email"] = row.ACMEEmail
	acme := firstNonEmpty(row.ACMEStatus, ndltls.ACMENotConfigured)
	if acme == ndltls.ACMEIssued {
		// ProbeDirectory does not issue a certificate. Never report issued.
		acme = ndltls.ACMEPending
	}
	out["acme_status"] = acme
	if row.NotBefore != nil {
		out["not_before"] = row.NotBefore.UTC().Format(time.RFC3339)
	}
	if row.NotAfter != nil {
		out["not_after"] = row.NotAfter.UTC().Format(time.RFC3339)
	}
	if row.NextRenewalAt != nil {
		out["next_renewal_at"] = row.NextRenewalAt.UTC().Format(time.RFC3339)
	}
	if row.Enabled {
		out["enabled"] = true
	}
	out["restart_required"] = row.Enabled && (!s.TLSServing || s.CertDirty)
	return out
}

func (s *Server) getCerts(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsTLSRead)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, s.certsJSON(r.Context(), p.User.ClusterID))
}

func (s *Server) generateCert(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsTLSManage)
	if err != nil {
		return
	}
	if !s.confirmTLS(r) {
		writeErr(w, http.StatusConflict, "enabling TLS requires X-Nodal-Confirm: enable-tls")
		return
	}
	var req struct {
		CommonName string   `json:"common_name"`
		SANs       []string `json:"sans"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	mat, err := s.certDir().Generate(req.CommonName, req.SANs, s.now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.persistCert(r.Context(), p, mat, ndltls.ModeSelfSigned, "", "", "", ndltls.ACMENotConfigured); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "tls.generate", "ok", mat.Fingerprint)
	writeJSON(w, http.StatusOK, s.certsJSON(r.Context(), p.User.ClusterID))
}

func (s *Server) importCert(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsTLSManage)
	if err != nil {
		return
	}
	if !s.confirmTLS(r) {
		writeErr(w, http.StatusConflict, "enabling TLS requires X-Nodal-Confirm: enable-tls")
		return
	}
	var req struct {
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	mat, err := s.certDir().Import([]byte(req.CertPEM), []byte(req.KeyPEM))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.persistCert(r.Context(), p, mat, ndltls.ModeImported, "", "", "", ndltls.ACMENotConfigured); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "tls.import", "ok", mat.Fingerprint)
	writeJSON(w, http.StatusOK, s.certsJSON(r.Context(), p.User.ClusterID))
}

func (s *Server) acmeCert(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsTLSManage)
	if err != nil {
		return
	}
	if !s.confirmTLS(r) {
		writeErr(w, http.StatusConflict, "enabling TLS requires X-Nodal-Confirm: enable-tls")
		return
	}
	var req struct {
		Directory string `json:"directory"`
		Email     string `json:"email"`
		Domain    string `json:"domain"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if u, err := url.Parse(strings.TrimSpace(req.Directory)); err == nil && u.User != nil {
		writeErr(w, http.StatusBadRequest, "acme directory must not include credentials")
		return
	}
	status := ndltls.ACMEPending
	row := appdb.Certificate{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Mode: ndltls.ModeACME,
		ACMEDirectory: req.Directory, ACMEEmail: req.Email, ACMEDomain: req.Domain,
		ACMEStatus: status, CommonName: req.Domain, SANs: []string{req.Domain},
	}
	if existing, _ := s.Store.GetCertificate(r.Context(), p.User.ClusterID); existing != nil {
		row.ID = existing.ID
		row.Enabled = existing.Enabled
		row.Fingerprint = existing.Fingerprint
		row.CertPath = existing.CertPath
		row.KeyPath = existing.KeyPath
		row.NotBefore = existing.NotBefore
		row.NotAfter = existing.NotAfter
		row.LastGoodFingerprint = existing.LastGoodFingerprint
	}
	if err := ndltls.ProbeDirectory(r.Context(), req.Directory); err != nil {
		row.ACMEStatus = ndltls.ACMEFailed
		_ = s.Store.UpsertCertificate(r.Context(), row)
		s.audit(r, p.User.ClusterID, p.User.ID, "tls.acme", "failed", err.Error())
		writeJSON(w, http.StatusOK, s.certsJSON(r.Context(), p.User.ClusterID))
		return
	}
	_ = s.Store.UpsertCertificate(r.Context(), row)
	s.audit(r, p.User.ClusterID, p.User.ID, "tls.acme", "pending", req.Directory)
	writeJSON(w, http.StatusOK, s.certsJSON(r.Context(), p.User.ClusterID))
}

func (s *Server) persistCert(ctx context.Context, p *principal, mat ndltls.Material, mode, dir, email, domain, acmeStatus string) error {
	nb, na := mat.NotBefore, mat.NotAfter
	row := appdb.Certificate{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Mode: mode, Enabled: true,
		CommonName: mat.CommonName, SANs: mat.SANs, Fingerprint: mat.Fingerprint,
		NotBefore: &nb, NotAfter: &na, CertPath: mat.CertPath, KeyPath: mat.KeyPath,
		ACMEDirectory: dir, ACMEEmail: email, ACMEDomain: domain, ACMEStatus: acmeStatus,
		LastGoodFingerprint: mat.Fingerprint,
	}
	if existing, _ := s.Store.GetCertificate(ctx, p.User.ClusterID); existing != nil {
		row.ID = existing.ID
		if existing.Fingerprint != "" {
			row.LastGoodFingerprint = existing.Fingerprint
		}
		if dir == "" {
			row.ACMEDirectory = existing.ACMEDirectory
			row.ACMEEmail = existing.ACMEEmail
			row.ACMEDomain = existing.ACMEDomain
			row.ACMEStatus = existing.ACMEStatus
		}
	}
	if err := s.Store.UpsertCertificate(ctx, row); err != nil {
		return err
	}
	s.TLSRequired = true
	s.CertDirty = true
	return nil
}

func (s *Server) confirmTLS(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("X-Nodal-Confirm")) == tlsEnableConfirm
}

func (s *Server) certDir() ndltls.Dir {
	if s.CertDir.Root != "" {
		return s.CertDir
	}
	return ndltls.Dir{}
}

func (s *Server) tlsListen() string {
	if s.TLSListen != "" {
		return s.TLSListen
	}
	return ":443"
}

func (s *Server) httpListen() string {
	if s.HTTPListen != "" {
		return s.HTTPListen
	}
	if s.TLSRequired {
		return ":80"
	}
	return ":8080"
}

func (s *Server) httpsURL() string {
	if s.HTTPSURL != "" {
		return s.HTTPSURL
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	return "https://" + host + "/"
}

func (s *Server) cookieSecure(r *http.Request) bool {
	return s.TLSRequired || r.TLS != nil
}

func (s *Server) requireTLS(w http.ResponseWriter, r *http.Request) bool {
	if !s.TLSRequired {
		return true
	}
	if r.TLS != nil {
		return true
	}
	writeErr(w, http.StatusForbidden, "TLS is required")
	return false
}
