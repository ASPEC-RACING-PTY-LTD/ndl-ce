package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/appmanifest"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storetrust"
)

const (
	officialKeyName       = "nodal-official"
	revokeStoreKeyConfirm = "revoke-store-key"
)

func (s *Server) listStoreKeys(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreRead)
	if err != nil {
		return
	}
	s.ensureOfficialTrust(r.Context(), p.User.ClusterID)
	items, err := s.Store.ListSigningKeys(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, k := range items {
		out = append(out, s.signingKeyJSON(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createStoreKey(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreVerify)
	if err != nil {
		return
	}
	var req struct {
		Name  string `json:"name"`
		Class string `json:"class"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	class := strings.TrimSpace(req.Class)
	if class == "" {
		class = appmanifest.ClassVerified
	}
	if class != appmanifest.ClassVerified && class != appmanifest.ClassOfficial {
		writeErr(w, http.StatusUnprocessableEntity, "class must be verified or official")
		return
	}
	kp, err := storetrust.Generate()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	row := appdb.SigningKey{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: strings.TrimSpace(req.Name),
		Class: class, PublicKey: storetrust.EncodePublic(kp.Public), Status: appdb.StoreKeyActive, CreatedAt: s.now(),
	}
	if err := s.Store.CreateSigningKey(r.Context(), row, storetrust.EncodePrivate(kp.Private)); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "store.key.create", "ok", row.ID)
	writeJSON(w, http.StatusCreated, s.signingKeyJSON(row))
}

func (s *Server) revokeStoreKey(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreVerify)
	if err != nil {
		return
	}
	if strings.TrimSpace(r.Header.Get(confirmHeader)) != revokeStoreKeyConfirm {
		writeErr(w, http.StatusUnprocessableEntity, "revoking a Store signing key requires X-Nodal-Confirm: revoke-store-key")
		return
	}
	key, err := s.Store.GetSigningKey(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || key == nil {
		writeErr(w, http.StatusNotFound, "signing key not found")
		return
	}
	if err := s.Store.RevokeSigningKey(r.Context(), p.User.ClusterID, key.ID, s.now()); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "store.key.revoke", "ok", key.ID)
	key.Status = appdb.StoreKeyRevoked
	now := s.now()
	key.RevokedAt = &now
	writeJSON(w, http.StatusOK, s.signingKeyJSON(*key))
}

func (s *Server) signStoreApp(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreVerify)
	if err != nil {
		return
	}
	s.seedOfficialStore(r.Context(), p.User.ClusterID)
	pkg, err := s.Store.GetStorePackage(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || pkg == nil {
		writeErr(w, http.StatusNotFound, "store package not found")
		return
	}
	var req struct {
		KeyID string `json:"key_id"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.KeyID) == "" {
		writeErr(w, http.StatusBadRequest, "key_id is required")
		return
	}
	sig, err := s.signPackage(r.Context(), p.User.ClusterID, *pkg, strings.TrimSpace(req.KeyID))
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "store.sign", "ok", pkg.ID)
	writeJSON(w, http.StatusCreated, s.signatureJSON(*sig))
}

func (s *Server) verifyStoreApp(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreVerify)
	if err != nil {
		return
	}
	s.ensureOfficialTrust(r.Context(), p.User.ClusterID)
	pkg, err := s.Store.GetStorePackage(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || pkg == nil {
		writeErr(w, http.StatusNotFound, "store package not found")
		return
	}
	out, err := s.runStoreVerify(r.Context(), p.User.ClusterID, *pkg)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "store.verify", out["status"].(string), pkg.ID)
	code := http.StatusOK
	if out["status"] == appdb.StoreVerifyFail {
		code = http.StatusUnprocessableEntity
	}
	writeJSON(w, code, out)
}

func (s *Server) getStoreAppScans(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreRead)
	if err != nil {
		return
	}
	s.ensureOfficialTrust(r.Context(), p.User.ClusterID)
	pkg, err := s.Store.GetStorePackage(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || pkg == nil {
		writeErr(w, http.StatusNotFound, "store package not found")
		return
	}
	v, _ := s.Store.LatestStoreVerification(r.Context(), p.User.ClusterID, pkg.ID)
	if v == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "status": "not_verified"})
		return
	}
	rows, err := s.Store.ListScanResults(r.Context(), p.User.ClusterID, v.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"kind": row.Kind, "status": row.Status, "detail": row.Detail})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "status": v.Status, "reason": v.Reason, "trust_class": v.TrustClass, "verification_id": v.ID})
}

func (s *Server) getStorePolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreRead)
	if err != nil {
		return
	}
	pol, err := s.Store.GetStorePolicy(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.storePolicyJSON(*pol))
}

func (s *Server) setStorePolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.StoreVerify)
	if err != nil {
		return
	}
	var req struct {
		InstallPolicy string `json:"install_policy"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	pol := strings.TrimSpace(req.InstallPolicy)
	if pol != appdb.StorePolicyCommunity && pol != appdb.StorePolicyVerifiedOnly {
		writeErr(w, http.StatusUnprocessableEntity, "install_policy must be community-allowed or verified-only")
		return
	}
	row := appdb.StorePolicy{ClusterID: p.User.ClusterID, InstallPolicy: pol, UpdatedAt: s.now()}
	if err := s.Store.SetStorePolicy(r.Context(), row); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "store.policy", "ok", pol)
	writeJSON(w, http.StatusOK, s.storePolicyJSON(row))
}

func (s *Server) ensureOfficialTrust(ctx context.Context, clusterID string) {
	s.seedOfficialStore(ctx, clusterID)
	key, _ := s.Store.GetSigningKeyByName(ctx, clusterID, officialKeyName)
	if key == nil {
		kp, err := storetrust.Generate()
		if err != nil {
			return
		}
		row := appdb.SigningKey{
			ID: uuid.NewString(), ClusterID: clusterID, Name: officialKeyName,
			Class: appmanifest.ClassOfficial, PublicKey: storetrust.EncodePublic(kp.Public),
			Status: appdb.StoreKeyActive, CreatedAt: s.now(),
		}
		if err := s.Store.CreateSigningKey(ctx, row, storetrust.EncodePrivate(kp.Private)); err != nil {
			return
		}
		key = &row
	}
	pkgs, _ := s.Store.ListStorePackages(ctx, clusterID)
	for _, pkg := range pkgs {
		if pkg.Class != appmanifest.ClassOfficial {
			continue
		}
		if existing, _ := s.Store.LatestPackageSignature(ctx, clusterID, pkg.ID); existing != nil && existing.PayloadSHA256 == storetrust.PayloadSHA256([]byte(pkg.ManifestYAML)) && existing.KeyID == key.ID {
			continue
		}
		if key.Status != appdb.StoreKeyActive {
			continue
		}
		_, _ = s.signPackage(ctx, clusterID, pkg, key.ID)
	}
}

func (s *Server) signPackage(ctx context.Context, clusterID string, pkg appdb.StorePackage, keyID string) (*appdb.PackageSignature, error) {
	key, err := s.Store.GetSigningKey(ctx, clusterID, keyID)
	if err != nil || key == nil {
		return nil, errNotFound("signing key not found")
	}
	if key.Status != appdb.StoreKeyActive {
		return nil, errUnprocessable("signing key is revoked")
	}
	privB64, err := s.Store.SigningPrivate(ctx, clusterID, key.ID)
	if err != nil {
		return nil, errUnprocessable(err.Error())
	}
	priv, err := storetrust.ParsePrivate(privB64)
	if err != nil {
		return nil, errUnprocessable(err.Error())
	}
	raw := []byte(pkg.ManifestYAML)
	sigB64, err := storetrust.Sign(priv, raw)
	if err != nil {
		return nil, errUnprocessable(err.Error())
	}
	row := appdb.PackageSignature{
		ID: uuid.NewString(), ClusterID: clusterID, PackageID: pkg.ID, KeyID: key.ID,
		Algorithm: storetrust.AlgorithmEd25519, SignatureB64: sigB64,
		PayloadSHA256: storetrust.PayloadSHA256(raw), CreatedAt: s.now(),
	}
	if err := s.Store.CreatePackageSignature(ctx, row); err != nil {
		return nil, errConflict(err.Error())
	}
	return &row, nil
}

func (s *Server) runStoreVerify(ctx context.Context, clusterID string, pkg appdb.StorePackage) (map[string]any, error) {
	m, err := appmanifest.ParseYAML([]byte(pkg.ManifestYAML))
	if err != nil {
		return nil, err
	}
	checks := storetrust.Analyze(*m)
	sigCheck := storetrust.Check{Kind: storetrust.CheckSignature, Status: storetrust.StatusFail, Detail: "Unsigned Community package. Signatures are required for Verified and Official."}
	var key *appdb.SigningKey
	if sig, _ := s.Store.LatestPackageSignature(ctx, clusterID, pkg.ID); sig != nil {
		key, _ = s.Store.GetSigningKey(ctx, clusterID, sig.KeyID)
		if key == nil {
			sigCheck = storetrust.Check{Kind: storetrust.CheckSignature, Status: storetrust.StatusFail, Detail: "Signing key is missing. Tamper fails closed."}
		} else if key.Status != appdb.StoreKeyActive {
			sigCheck = storetrust.Check{Kind: storetrust.CheckSignature, Status: storetrust.StatusFail, Detail: "Signing key is revoked. New installs that require this signature are refused."}
		} else {
			pub, perr := storetrust.ParsePublic(key.PublicKey)
			if perr != nil {
				sigCheck = storetrust.Check{Kind: storetrust.CheckSignature, Status: storetrust.StatusFail, Detail: perr.Error()}
			} else if verr := storetrust.Verify(pub, []byte(pkg.ManifestYAML), sig.SignatureB64); verr != nil {
				sigCheck = storetrust.Check{Kind: storetrust.CheckSignature, Status: storetrust.StatusFail, Detail: verr.Error()}
			} else if sig.PayloadSHA256 != storetrust.PayloadSHA256([]byte(pkg.ManifestYAML)) {
				sigCheck = storetrust.Check{Kind: storetrust.CheckSignature, Status: storetrust.StatusFail, Detail: "signature does not match manifest; tamper fails closed"}
			} else {
				sigCheck = storetrust.Check{Kind: storetrust.CheckSignature, Status: storetrust.StatusPass, Detail: "Ed25519 signature matches the stored manifest bytes."}
			}
		}
	}
	checks = append([]storetrust.Check{sigCheck}, checks...)
	status := appdb.StoreVerifyPass
	reason := "scan report visible; CVE scanner unavailable is not a pass for vulnerabilities"
	trust := appmanifest.ClassCommunity
	if storetrust.Failed(checks) || sigCheck.Status != storetrust.StatusPass {
		status = appdb.StoreVerifyFail
		reason = "verification failed"
		for _, c := range checks {
			if c.Status == storetrust.StatusFail {
				reason = c.Detail
				break
			}
		}
	} else if key != nil && key.Class == appmanifest.ClassOfficial && pkg.Class == appmanifest.ClassOfficial {
		trust = appmanifest.ClassOfficial
		reason = "Official signature valid. Static scans passed. CVE scanner is not installed."
	} else if key != nil {
		trust = appmanifest.ClassVerified
		reason = "Verified signature valid. Static scans passed. CVE scanner is not installed."
	} else {
		status = appdb.StoreVerifyFail
		reason = sigCheck.Detail
	}
	v := appdb.StoreVerification{
		ID: uuid.NewString(), ClusterID: clusterID, PackageID: pkg.ID,
		Status: status, Reason: reason, TrustClass: trust, CreatedAt: s.now(),
	}
	if key != nil {
		v.KeyID = key.ID
	}
	if err := s.Store.CreateStoreVerification(ctx, v); err != nil {
		return nil, err
	}
	rows := make([]appdb.ScanResult, 0, len(checks))
	now := s.now()
	for _, c := range checks {
		rows = append(rows, appdb.ScanResult{
			ID: uuid.NewString(), ClusterID: clusterID, PackageID: pkg.ID, VerificationID: v.ID,
			Kind: c.Kind, Status: c.Status, Detail: c.Detail, CreatedAt: now,
		})
	}
	if err := s.Store.CreateScanResults(ctx, rows); err != nil {
		return nil, err
	}
	return s.verificationJSON(v, rows), nil
}

func (s *Server) enforceStoreTrust(ctx context.Context, clusterID string, pkg appdb.StorePackage) error {
	s.ensureOfficialTrust(ctx, clusterID)
	pol, _ := s.Store.GetStorePolicy(ctx, clusterID)
	policy := appdb.StorePolicyCommunity
	if pol != nil {
		policy = pol.InstallPolicy
	}
	sig, _ := s.Store.LatestPackageSignature(ctx, clusterID, pkg.ID)
	if sig != nil {
		key, _ := s.Store.GetSigningKey(ctx, clusterID, sig.KeyID)
		if key == nil || key.Status != appdb.StoreKeyActive {
			return errUnprocessable("signing key is revoked; new installs are refused")
		}
		pub, err := storetrust.ParsePublic(key.PublicKey)
		if err != nil {
			return errUnprocessable("signature does not match manifest; tamper fails closed")
		}
		if err := storetrust.Verify(pub, []byte(pkg.ManifestYAML), sig.SignatureB64); err != nil {
			return errUnprocessable(err.Error())
		}
	}
	if policy == appdb.StorePolicyVerifiedOnly {
		if sig == nil {
			return errUnprocessable("verified-only policy refuses unsigned Community packages")
		}
		v, _ := s.Store.LatestStoreVerification(ctx, clusterID, pkg.ID)
		if v == nil || v.Status != appdb.StoreVerifyPass {
			out, err := s.runStoreVerify(ctx, clusterID, pkg)
			if err != nil {
				return err
			}
			if out["status"] != appdb.StoreVerifyPass {
				return errUnprocessable(storeReasonString(out["reason"]))
			}
		}
	}
	return nil
}

func storeReasonString(v any) string {
	s, _ := v.(string)
	if s == "" {
		return "verification failed"
	}
	return s
}

func (s *Server) signingKeyJSON(k appdb.SigningKey) map[string]any {
	out := map[string]any{
		"id": k.ID, "name": k.Name, "class": k.Class, "status": k.Status, "public_key": k.PublicKey,
	}
	if k.RevokedAt != nil {
		out["revoked_at"] = k.RevokedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (s *Server) signatureJSON(sig appdb.PackageSignature) map[string]any {
	return map[string]any{
		"id": sig.ID, "package_id": sig.PackageID, "key_id": sig.KeyID,
		"algorithm": sig.Algorithm, "payload_sha256": sig.PayloadSHA256,
	}
}

func (s *Server) storePolicyJSON(p appdb.StorePolicy) map[string]any {
	return map[string]any{"install_policy": p.InstallPolicy}
}

func (s *Server) verificationJSON(v appdb.StoreVerification, rows []appdb.ScanResult) map[string]any {
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"kind": row.Kind, "status": row.Status, "detail": row.Detail})
	}
	return map[string]any{
		"id": v.ID, "package_id": v.PackageID, "status": v.Status, "reason": v.Reason,
		"trust_class": v.TrustClass, "key_id": v.KeyID, "checks": items, "kubelet_started": false,
	}
}
