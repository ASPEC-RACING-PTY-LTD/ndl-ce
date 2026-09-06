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
	s.maybeFleetHeartbeat(clusterID, snap)
	return out
}

func (s *Server) maybeFleetHeartbeat(clusterID string, snap license.Snapshot) {
	if snap.Edition != license.EditionEE || !license.HasCapability(snap.Capabilities, license.CapFleetInventory) {
		return
	}
	if !s.eeRuntimePresent() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		nodeCount := 0
		if nodes, err := s.Store.ListClusterNodes(ctx, clusterID); err == nil {
			nodeCount = len(nodes)
		}
		workloadCount := 0
		if wls, err := s.Store.ListWorkloads(ctx, clusterID); err == nil {
			workloadCount = len(wls)
		}
		payload, _ := json.Marshal(map[string]any{
			"id": clusterID, "organization": snap.Organization, "installation_id": snap.InstallationID,
			"edition": snap.Edition, "status": snap.Status, "local": true,
			"node_count": nodeCount, "workload_count": workloadCount,
		})
		_, _, _ = s.eeJSON(ctx, http.MethodPost, "/api/v1/enterprise/fleet/heartbeat", payload, clusterID)
	}()
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

func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) {
	s.proxySSOStart(w, r, license.CapIdentityOIDC, "/api/v1/enterprise/identity/oidc/start")
}

func (s *Server) samlStart(w http.ResponseWriter, r *http.Request) {
	s.proxySSOStart(w, r, license.CapIdentitySAML, "/api/v1/enterprise/identity/saml/start")
}

func (s *Server) proxySSOStart(w http.ResponseWriter, r *http.Request, cap, path string) {
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
	if snap.Edition != license.EditionEE || !license.HasCapability(snap.Capabilities, cap) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "sso is not entitled", "workloads_stopped": false})
		return
	}
	base, tr, ok := s.eeDial()
	if !ok {
		writeErr(w, http.StatusNotFound, "enterprise runtime is not installed")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	eeReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+path, bytes.NewReader(body))
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(out.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(out.Body, 1<<20))
}

func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	payload, _ := json.Marshal(map[string]string{
		"provider_id": r.URL.Query().Get("provider_id"),
		"code":        r.URL.Query().Get("code"),
		"state":       r.URL.Query().Get("state"),
	})
	s.completeEnterpriseAssertion(w, r, "/api/v1/enterprise/identity/oidc/complete", payload, "auth.sso")
}

func (s *Server) samlACS(w http.ResponseWriter, r *http.Request) {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(ct, "json") {
		var req struct {
			SAMLResponse string `json:"saml_response"`
			RelayState   string `json:"relay_state"`
			ProviderID   string `json:"provider_id"`
		}
		if err := readJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		payload, _ := json.Marshal(map[string]string{
			"provider_id": req.ProviderID, "saml_response": req.SAMLResponse, "relay_state": req.RelayState,
		})
		s.completeEnterpriseAssertion(w, r, "/api/v1/enterprise/identity/saml/complete", payload, "auth.saml")
		return
	}
	_ = r.ParseForm()
	payload, _ := json.Marshal(map[string]string{
		"saml_response": strings.TrimSpace(r.FormValue("SAMLResponse")),
		"relay_state":   strings.TrimSpace(r.FormValue("RelayState")),
	})
	s.completeEnterpriseAssertion(w, r, "/api/v1/enterprise/identity/saml/complete", payload, "auth.saml")
}

func (s *Server) ssoProviders(w http.ResponseWriter, r *http.Request) {
	cluster, err := s.Store.GetCluster(r.Context())
	if err != nil || cluster == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	if !s.eeRuntimePresent() {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	snap := s.licenseSnapshot(r.Context(), cluster.ID)
	if snap.Edition != license.EditionEE {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	raw, status, err := s.eeJSON(r.Context(), http.MethodGet, "/api/v1/enterprise/identity/providers/public", nil, cluster.ID)
	if err != nil || status >= 300 {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) completeEnterpriseAssertion(w http.ResponseWriter, r *http.Request, path string, payload []byte, auditAction string) {
	cluster, err := s.Store.GetCluster(r.Context())
	if err != nil || cluster == nil {
		writeErr(w, http.StatusServiceUnavailable, "cluster is not ready")
		return
	}
	if !s.eeRuntimePresent() {
		writeErr(w, http.StatusNotFound, "enterprise runtime is not installed")
		return
	}
	raw, status, err := s.eeJSON(r.Context(), http.MethodPost, path, payload, cluster.ID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "enterprise runtime unreachable")
		return
	}
	if status >= 300 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
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
	user, err := s.ensureSSOUser(r.Context(), cluster.ID, assertion.Username)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	overlay := s.policyOverlay(r.Context(), cluster.ID)
	if err := s.enforceAdminCIDR(r, *user, overlay); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	method, _, _, err := s.Store.GetMFAMethod(r.Context(), user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "mfa state is unavailable")
		return
	}
	if overlay.RequireMFA && (method == nil || !method.Enabled) {
		writeErr(w, http.StatusForbidden, "mfa enrollment is required")
		return
	}
	if method != nil && method.Enabled {
		s.writeMFAChallenge(w, r, *user)
		return
	}
	if err := s.issueSession(w, r, *user, 1); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, cluster.ID, user.ID, auditAction, "ok", assertion.Subject)
	s.writeMe(w, r, *user, 1)
}

func (s *Server) ensureSSOUser(ctx context.Context, clusterID, username string) (*appdb.User, error) {
	user, err := s.Store.GetUserByName(ctx, clusterID, username)
	if err != nil {
		return nil, err
	}
	if user != nil {
		return user, nil
	}
	hash, err := auth.HashPassword(uuid.NewString() + uuid.NewString())
	if err != nil {
		return nil, err
	}
	u := appdb.User{ID: uuid.NewString(), ClusterID: clusterID, Username: username, PasswordHash: hash, Kind: appdb.UserKindPerson}
	if err := s.Store.CreateUser(ctx, u); err != nil {
		return nil, errors.New("could not create sso user")
	}
	_ = s.Store.BindRole(ctx, clusterID, u.ID, rbac.Viewer)
	return &u, nil
}

func (s *Server) ldapBindUser(ctx context.Context, clusterID, username, password string) (*appdb.User, error) {
	if username == "" || password == "" || !s.eeRuntimePresent() {
		return nil, errors.New("ldap unavailable")
	}
	snap := s.licenseSnapshot(ctx, clusterID)
	if snap.Edition != license.EditionEE || !license.HasCapability(snap.Capabilities, license.CapIdentityLDAP) {
		return nil, errors.New("ldap is not entitled")
	}
	payload, _ := json.Marshal(map[string]string{"username": username, "password": password})
	raw, status, err := s.eeJSON(ctx, http.MethodPost, "/api/v1/enterprise/identity/ldap/bind", payload, clusterID)
	if err != nil || status >= 300 {
		return nil, errors.New("ldap credentials are invalid")
	}
	var assertion struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &assertion); err != nil || assertion.Username == "" {
		return nil, errors.New("invalid ldap assertion")
	}
	return s.ensureSSOUser(ctx, clusterID, assertion.Username)
}

type policyOverlay struct {
	RequireSSO         bool     `json:"require_sso"`
	RequireAuditExport bool     `json:"require_audit_export"`
	DenyLocalPassword  bool     `json:"deny_local_password"`
	AdminCIDRs         []string `json:"admin_cidrs"`
	MaxSessionHours    int      `json:"max_session_hours"`
	RequireMFA         bool     `json:"require_mfa"`
}

func (o policyOverlay) BlocksLocalPassword() bool {
	return o.RequireSSO || o.DenyLocalPassword
}

func (s *Server) policyOverlay(ctx context.Context, clusterID string) policyOverlay {
	var out policyOverlay
	if !s.eeRuntimePresent() {
		return out
	}
	snap := s.licenseSnapshot(ctx, clusterID)
	if snap.Edition != license.EditionEE || !license.HasCapability(snap.Capabilities, license.CapPolicyAdvanced) {
		return out
	}
	raw, status, err := s.eeJSON(ctx, http.MethodGet, "/api/v1/enterprise/policy", nil, clusterID)
	if err != nil || status >= 300 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s *Server) sessionTTL(ctx context.Context, clusterID string) time.Duration {
	o := s.policyOverlay(ctx, clusterID)
	if o.MaxSessionHours > 0 {
		return time.Duration(o.MaxSessionHours) * time.Hour
	}
	return sessionTTL
}

func (s *Server) enforceAdminCIDR(r *http.Request, user appdb.User, overlay policyOverlay) error {
	if len(overlay.AdminCIDRs) == 0 {
		return nil
	}
	roles, err := s.Store.UserRoles(r.Context(), user.ID)
	if err != nil {
		return err
	}
	admin := false
	for _, role := range roles {
		if role == rbac.Admin {
			admin = true
			break
		}
	}
	if !admin {
		return nil
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return errors.New("administrator sign-in is restricted")
	}
	for _, cidr := range overlay.AdminCIDRs {
		_, n, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err != nil {
			continue
		}
		if n.Contains(ip) {
			return nil
		}
	}
	return errors.New("administrator sign-in is restricted")
}

func (s *Server) eeJSON(ctx context.Context, method, path string, body []byte, clusterID string) ([]byte, int, error) {
	base, tr, ok := s.eeDial()
	if !ok {
		return nil, 0, errors.New("enterprise runtime is not installed")
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodal-Cluster-Id", clusterID)
	req.Header.Set("X-Nodal-Roles", "admin")
	client := &http.Client{Timeout: 15 * time.Second, Transport: tr}
	out, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer out.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(out.Body, 1<<20))
	return raw, out.StatusCode, nil
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
