package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/license"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const (
	defaultEESocket = "/run/ndl/ee.sock"
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
		ClusterID: p.User.ClusterID, Status: appdb.LicenseGrace, LastChecked: &now, Edition: license.EditionCE,
		Reason: "Key stored. Licensing API was not reachable. Community Edition continues. Workloads are not stopped.",
	}
	doc, perr := s.probeActivate(r.Context(), key, p.User.ClusterID)
	applyLicenseProbe(&st, doc, perr)
	if err := s.Store.PutLicenseState(r.Context(), st, key); err != nil {
		writeErr(w, http.StatusConflict, "could not record license")
		return
	}
	s.persistEntitlementFile(st)
	s.audit(r, p.User.ClusterID, p.User.ID, "license.activate", st.Status, "")
	writeJSON(w, http.StatusOK, s.licenseJSON(r.Context(), p.User.ClusterID))
}

func (s *Server) importLicense(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsLicenseManage)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != license.ImportConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "importing a license requires X-Nodal-Confirm: import-license")
		return
	}
	var req struct {
		Entitlement json.RawMessage `json:"entitlement"`
		Key         string          `json:"key"`
	}
	if err := readJSON(r, &req); err != nil || len(req.Entitlement) == 0 {
		writeErr(w, http.StatusBadRequest, "entitlement is required")
		return
	}
	doc, ok := license.ParseDocument(req.Entitlement)
	if !ok {
		writeErr(w, http.StatusBadRequest, "entitlement is invalid")
		return
	}
	if err := license.Verify(doc, s.trust()); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "entitlement signature is invalid")
		return
	}
	now := time.Now().UTC()
	st := appdb.LicenseState{ClusterID: p.User.ClusterID, LastChecked: &now, EntitlementJSON: req.Entitlement, InstallationID: doc.InstallationID, Organization: doc.Organization}
	applyLicenseProbe(&st, &doc, nil)
	if err := s.Store.PutLicenseState(r.Context(), st, strings.TrimSpace(req.Key)); err != nil {
		writeErr(w, http.StatusConflict, "could not record license")
		return
	}
	s.persistEntitlementFile(st)
	s.audit(r, p.User.ClusterID, p.User.ID, "license.import", st.Status, "")
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
	snap := s.licenseSnapshot(ctx, clusterID)
	out := map[string]any{
		"edition":           snap.Edition,
		"status":            snap.Status,
		"reason":            snap.Reason,
		"has_key":           snap.HasKey,
		"key_suffix":        snap.KeySuffix,
		"workloads_stopped": false,
		"ee_blobs":          snap.EEBlobs,
		"ee_runtime":        snap.EERuntime,
		"contacts_api":      snap.ContactsAPI,
		"degraded":          snap.Degraded,
		"signed":            snap.Signed,
	}
	if snap.Organization != "" {
		out["organization"] = snap.Organization
	}
	if snap.InstallationID != "" {
		out["installation_id"] = snap.InstallationID
	}
	if snap.SubscriptionID != "" {
		out["subscription_id"] = snap.SubscriptionID
	}
	if snap.UpdateChannel != "" {
		out["update_channel"] = snap.UpdateChannel
	}
	if len(snap.Capabilities) > 0 {
		out["capabilities"] = snap.Capabilities
	}
	if snap.ExpiresAt != "" {
		out["expires_at"] = snap.ExpiresAt
	}
	if snap.GraceUntil != "" {
		out["grace_until"] = snap.GraceUntil
	}
	if snap.LastChecked != "" {
		out["last_checked"] = snap.LastChecked
	}
	return out
}

func (s *Server) trust() license.TrustBundle {
	if s.EETrust != nil {
		return s.EETrust
	}
	return license.LoadTrust("")
}

func (s *Server) licenseSnapshot(ctx context.Context, clusterID string) license.Snapshot {
	st, key, err := s.Store.GetLicenseState(ctx, clusterID)
	in := license.Input{HasKey: key != "", Key: key, RuntimePresent: s.eeRuntimePresent(), EEBlobsPresent: s.eeRuntimePresent()}
	if err == nil && st != nil {
		in.HasKey = in.HasKey || st.Status != appdb.LicenseAbsent
		in.Unreachable = st.Status == appdb.LicenseUnreachable
		if st.LastChecked != nil {
			in.LastChecked = *st.LastChecked
		}
		if len(st.EntitlementJSON) > 0 {
			if doc, ok := license.ParseDocument(st.EntitlementJSON); ok {
				in.Cached = &doc
				in.CachedSigned = license.Verify(doc, s.trust()) == nil
			}
		}
	}
	return license.Evaluate(in)
}

func (s *Server) probeActivate(ctx context.Context, key, clusterID string) (*license.Document, error) {
	req := license.ActivationRequest{Edition: license.EditionCE, ClusterID: clusterID}
	if a, ok := any(s.LicenseProbe).(interface {
		Activate(context.Context, string, license.ActivationRequest) (*license.Document, error)
	}); ok && s.LicenseProbe != nil {
		return a.Activate(ctx, key, req)
	}
	if s.LicenseProbe != nil {
		return nil, s.LicenseProbe.Check(ctx, key)
	}
	return (license.HTTPProbe{Client: s.HTTPClient, Cluster: clusterID, Trust: s.trust()}).Activate(ctx, key, req)
}

func applyLicenseProbe(st *appdb.LicenseState, doc *license.Document, err error) {
	if doc != nil {
		raw, _ := json.Marshal(doc)
		st.EntitlementJSON = raw
		st.Organization = doc.Organization
		st.InstallationID = doc.InstallationID
	}
	if err == nil {
		st.Status = appdb.LicenseActive
		st.Reason = license.ReasonUnsignedActive
		if doc != nil && doc.Signature != "" {
			st.Reason = license.ReasonActiveNoRuntime
			st.Edition = license.EditionCE
		}
		return
	}
	if errors.Is(err, license.ErrNotEntitled) {
		st.Status = appdb.LicenseGrace
		st.Reason = license.ReasonGraceNotEntitled
		return
	}
	st.Status = appdb.LicenseUnreachable
	st.Reason = license.ReasonGraceUnreachable
}

func (s *Server) persistEntitlementFile(st appdb.LicenseState) {
	if len(st.EntitlementJSON) == 0 {
		return
	}
	dir := os.Getenv("NODAL_LICENSE_DIR")
	if dir == "" {
		dir = "/var/lib/ndl/license"
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	_ = os.WriteFile(dir+"/entitlement.json", st.EntitlementJSON, 0o640)
}

func (s *Server) eeRuntimePresent() bool {
	if s.EEPresent != nil {
		return s.EEPresent()
	}
	sock := s.EESocket
	if sock == "" {
		sock = os.Getenv("NODAL_EE_SOCKET")
	}
	if sock == "" {
		sock = defaultEESocket
	}
	if _, err := os.Stat(sock); err == nil {
		return true
	}
	url := s.EEURL
	if url == "" {
		url = os.Getenv("NODAL_EE_URL")
	}
	if url == "" {
		return false
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 300 * time.Millisecond}
	}
	res, err := client.Get(strings.TrimRight(url, "/") + "/healthz")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode == http.StatusOK
}

func (s *Server) enterpriseStatus(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.SettingsLicenseRead)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, s.licenseJSON(r.Context(), p.User.ClusterID))
}

func (s *Server) proxyEnterprise(w http.ResponseWriter, r *http.Request) {
	p, err := s.principal(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if !s.eeRuntimePresent() {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "enterprise runtime is not installed", "workloads_stopped": false, "edition": license.EditionCE,
		})
		return
	}
	snap := s.licenseSnapshot(r.Context(), p.User.ClusterID)
	if snap.Edition != license.EditionEE {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "enterprise capability is not entitled", "degraded": true, "workloads_stopped": false, "status": snap.Status,
		})
		return
	}
	out, err := s.doEE(r, p)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "enterprise runtime unreachable", "workloads_stopped": false})
		return
	}
	defer out.Body.Close()
	for k, vals := range out.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(out.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(out.Body, 8<<20))
}

func (s *Server) doEE(r *http.Request, p *principal) (*http.Response, error) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	base, tr, ok := s.eeDial()
	if !ok {
		return nil, errors.New("enterprise runtime is not installed")
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, base+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	req.Header.Set("X-Nodal-User-Id", p.User.ID)
	req.Header.Set("X-Nodal-Cluster-Id", p.User.ClusterID)
	req.Header.Set("X-Nodal-Roles", strings.Join(p.Roles, ","))
	req.Header.Set("X-Nodal-Permissions", strings.Join(p.Grants, ","))
	client := &http.Client{Timeout: 15 * time.Second, Transport: tr}
	return client.Do(req)
}

func (s *Server) eeDial() (string, http.RoundTripper, bool) {
	sock := s.EESocket
	if sock == "" {
		sock = os.Getenv("NODAL_EE_SOCKET")
	}
	if sock == "" {
		sock = defaultEESocket
	}
	if _, err := os.Stat(sock); err == nil {
		return "http://ee", &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		}, true
	}
	url := strings.TrimSpace(s.EEURL)
	if url == "" {
		url = strings.TrimSpace(os.Getenv("NODAL_EE_URL"))
	}
	if url == "" {
		return "", nil, false
	}
	return strings.TrimRight(url, "/"), http.DefaultTransport, true
}

func (s *Server) eeTransport() http.RoundTripper {
	_, tr, ok := s.eeDial()
	if ok {
		return tr
	}
	return http.DefaultTransport
}

func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) {
	cluster, err := s.Store.GetCluster(r.Context())
	if err != nil || cluster == nil {
		writeErr(w, http.StatusServiceUnavailable, "cluster is not ready")
		return
	}
	if !s.eeRuntimePresent() {
		writeErr(w, http.StatusNotFound, "enterprise runtime is not installed")
		return
	}
	snap := s.licenseSnapshot(r.Context(), cluster.ID)
	if snap.Edition != license.EditionEE || !license.HasCapability(snap.Capabilities, license.CapIdentityOIDC) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "sso is not entitled", "workloads_stopped": false})
		return
	}
	base, tr, ok := s.eeDial()
	if !ok {
		writeErr(w, http.StatusNotFound, "enterprise runtime is not installed")
		return
	}
	eeReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/api/v1/enterprise/identity/oidc/start", r.Body)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	eeReq.Header.Set("Content-Type", "application/json")
	eeReq.Header.Set("X-Nodal-Cluster-Id", cluster.ID)
	eeReq.Header.Set("X-Nodal-Roles", "admin")
	client := &http.Client{Timeout: 15 * time.Second, Transport: tr}
	out, err := client.Do(eeReq)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "enterprise runtime unreachable")
		return
	}
	defer out.Body.Close()
	w.WriteHeader(out.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(out.Body, 1<<20))
}

func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	cluster, err := s.Store.GetCluster(r.Context())
	if err != nil || cluster == nil {
		writeErr(w, http.StatusServiceUnavailable, "cluster is not ready")
		return
	}
	if !s.eeRuntimePresent() {
		writeErr(w, http.StatusNotFound, "enterprise runtime is not installed")
		return
	}
	payload, _ := json.Marshal(map[string]string{
		"provider_id": r.URL.Query().Get("provider_id"),
		"code":        r.URL.Query().Get("code"),
		"state":       r.URL.Query().Get("state"),
	})
	base, tr, ok := s.eeDial()
	if !ok {
		writeErr(w, http.StatusNotFound, "enterprise runtime is not installed")
		return
	}
	eeReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/api/v1/enterprise/identity/oidc/complete", bytes.NewReader(payload))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	eeReq.Header.Set("Content-Type", "application/json")
	eeReq.Header.Set("X-Nodal-Cluster-Id", cluster.ID)
	eeReq.Header.Set("X-Nodal-Roles", "admin")
	client := &http.Client{Timeout: 15 * time.Second, Transport: tr}
	out, err := client.Do(eeReq)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "enterprise runtime unreachable")
		return
	}
	defer out.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(out.Body, 1<<20))
	if out.StatusCode >= 300 {
		w.WriteHeader(out.StatusCode)
		_, _ = w.Write(raw)
		return
	}
	var assertion struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Subject  string `json:"subject"`
	}
	if err := json.Unmarshal(raw, &assertion); err != nil || assertion.Username == "" {
		writeErr(w, http.StatusBadGateway, "invalid sso assertion")
		return
	}
	user, err := s.Store.GetUserByName(r.Context(), cluster.ID, assertion.Username)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if user == nil {
		hash, err := auth.HashPassword(uuid.NewString() + uuid.NewString())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: assertion.Username, PasswordHash: hash, Kind: appdb.UserKindPerson}
		if err := s.Store.CreateUser(r.Context(), u); err != nil {
			writeErr(w, http.StatusConflict, "could not create sso user")
			return
		}
		_ = s.Store.BindRole(r.Context(), cluster.ID, u.ID, rbac.Viewer)
		user = &u
	}
	if err := s.issueSession(w, r, *user, 1); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, cluster.ID, user.ID, "auth.sso", "ok", assertion.Subject)
	s.writeMe(w, r, *user, 1)
}

func (s *Server) forwardAudit(clusterID, actor, action, result string) {
	payload, _ := json.Marshal(map[string]string{
		"id": uuid.NewString(), "cluster_id": clusterID, "actor_user_id": actor, "action": action, "result": result,
		"created_at": time.Now().UTC().Format(time.RFC3339),
	})
	base, tr, ok := s.eeDial()
	if !ok {
		return
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/enterprise/compliance/ingest", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Cluster-Id", clusterID)
	client := &http.Client{Timeout: 3 * time.Second, Transport: tr}
	res, err := client.Do(req)
	if err != nil {
		return
	}
	_ = res.Body.Close()
}
