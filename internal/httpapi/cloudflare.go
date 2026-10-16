package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	cf "github.com/no-dal/ndl-ce/internal/cloudflare"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Cloudflare Tunnel Manager. Tokens are sealed at rest and only decrypted in
// memory for the call that needs them. Every call picks a credential that was
// found to reach the account or zone it acts on; a token that is rejected is
// marked and the next credential for that account is tried.

// cfState holds the Cloudflare inventory cache.
type cfState struct {
	mu        sync.Mutex
	inv       map[string]*cfInventory
	stale     map[string]bool
	refreshMu sync.Mutex
	sealer    *cf.Sealer
	// clients keeps one API client per saved token, so its connections and
	// rate pacing carry over between calls. Keyed by credential and sealed
	// token, so a rotated token gets a new client.
	clients map[string]*cf.Client
	// guide is the permissions guide read from Cloudflare's documentation.
	guideMu sync.Mutex
	guide   *cf.Guide
}

// Cloudflare's documentation changes rarely; a failed read is retried sooner.
const (
	cfGuideTTL      = 12 * time.Hour
	cfGuideRetryTTL = 10 * time.Minute
)

// cloudflarePermissionsGuide tells people which token permissions each
// feature needs, using Cloudflare's current documentation for the names,
// descriptions and token steps.
func (s *Server) cloudflarePermissionsGuide(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.CloudflareRead); err != nil {
		return
	}
	writeJSON(w, http.StatusOK, s.cfGuide(r.Context(), r.URL.Query().Get("refresh") == "1"))
}

func (s *Server) cfGuide(ctx context.Context, refresh bool) cf.Guide {
	s.cf.guideMu.Lock()
	defer s.cf.guideMu.Unlock()
	now := s.now()
	if g := s.cf.guide; g != nil && !refresh {
		ttl := cfGuideTTL
		if g.Source != "cloudflare-docs" {
			ttl = cfGuideRetryTTL
		}
		if now.Sub(g.FetchedAt) < ttl {
			return *g
		}
	}
	base := s.CFDocsURL
	if base == "" {
		base = cf.DocsBaseURL
	}
	g, err := cf.FetchGuide(ctx, nil, base)
	if err != nil && s.cf.guide != nil && s.cf.guide.Source == "cloudflare-docs" {
		// Keep the last good copy rather than falling back to the built-in one.
		return *s.cf.guide
	}
	g.FetchedAt = now
	s.cf.guide = &g
	return g
}

// cfInventoryTTL is how long a fetched inventory is served before a refresh
// runs in the background. Cloudflare allows 1200 requests per five minutes
// per user, shared with the dashboard, so the page never polls Cloudflare.
const cfInventoryTTL = 2 * time.Minute

func (s *Server) cfSealer() *cf.Sealer {
	s.cf.mu.Lock()
	defer s.cf.mu.Unlock()
	if s.cf.sealer == nil {
		s.cf.sealer = &cf.Sealer{Path: s.CFKeyPath}
	}
	return s.cf.sealer
}

// cfPublic requests a public URL the way a visitor would. Tests replace it.
func (s *Server) cfPublic(ctx context.Context, url string) uptimeResult {
	if s.CFPublicCheck != nil {
		return s.CFPublicCheck(ctx, url)
	}
	return checkUptime(ctx, url, "", "")
}

// auditBackground records an audit event for work that outlived its request.
func (s *Server) auditBackground(clusterID, actor, action, result, detail string) {
	body, _ := json.Marshal(map[string]string{"detail": detail})
	_ = s.Store.InsertAudit(context.Background(), appdb.AuditEvent{
		ID: uuid.NewString(), ClusterID: clusterID, ActorUserID: actor, Action: action, Result: result, Detail: body, CreatedAt: s.now(),
	})
}

func (s *Server) cfNewClient(token string) *cf.Client {
	c := &cf.Client{Token: token, BaseURL: s.CFBaseURL}
	if s.CFBaseURL != "" {
		c.MinGap = -1
	}
	return c
}

// cfCredScopes reads the accounts a credential was found to reach.
func cfCredScopes(c appdb.CloudflareCredential) []cf.AccountScope {
	var out []cf.AccountScope
	_ = json.Unmarshal(c.Scope, &out)
	return out
}

// cfForgetClient drops a removed token's cached client.
func (s *Server) cfForgetClient(credID string) {
	s.cf.mu.Lock()
	defer s.cf.mu.Unlock()
	for k := range s.cf.clients {
		if strings.HasPrefix(k, credID+":") {
			delete(s.cf.clients, k)
		}
	}
}

func (s *Server) cfClientFor(ctx context.Context, clusterID string, cred appdb.CloudflareCredential) (*cf.Client, error) {
	sealed, err := s.Store.GetCloudflareToken(ctx, clusterID, cred.ID)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(sealed))
	key := cred.ID + ":" + hex.EncodeToString(sum[:8])
	s.cf.mu.Lock()
	c := s.cf.clients[key]
	s.cf.mu.Unlock()
	if c != nil {
		return c, nil
	}
	tok, err := s.cfSealer().Open(cred.ID, sealed)
	if err != nil {
		return nil, err
	}
	c = s.cfNewClient(tok)
	s.cf.mu.Lock()
	if s.cf.clients == nil {
		s.cf.clients = map[string]*cf.Client{}
	}
	for k := range s.cf.clients {
		if strings.HasPrefix(k, cred.ID+":") {
			delete(s.cf.clients, k)
		}
	}
	s.cf.clients[key] = c
	s.cf.mu.Unlock()
	return c, nil
}

// cfCandidates lists usable credentials that reach an account (and zone when
// zoneID is set), healthiest and most recently verified first.
func (s *Server) cfCandidates(ctx context.Context, clusterID, accountID, zoneID string) []appdb.CloudflareCredential {
	creds, _ := s.Store.ListCloudflareCredentials(ctx, clusterID)
	var out []appdb.CloudflareCredential
	for _, c := range creds {
		if c.Status == cfStatusInvalid {
			continue
		}
		for _, sc := range cfCredScopes(c) {
			if accountID != "" && sc.Account.ID != accountID {
				continue
			}
			if zoneID != "" {
				found := false
				for _, z := range sc.Zones {
					if z.ID == zoneID {
						found = true
					}
				}
				if !found {
					continue
				}
			}
			out = append(out, c)
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Status == cfStatusActive) != (out[j].Status == cfStatusActive) {
			return out[i].Status == cfStatusActive
		}
		ai, aj := out[i].LastVerifiedAt, out[j].LastVerifiedAt
		return ai != nil && (aj == nil || ai.After(*aj))
	})
	return out
}

// Credential states.
const (
	cfStatusActive  = "active"
	cfStatusLimited = "limited"
	cfStatusInvalid = "invalid"
	cfStatusError   = "error"
)

var errCFNoCredential = errors.New("no saved Cloudflare token can reach this account; add one under Accounts")

// cfWith runs fn with each credential that reaches accountID (and zoneID)
// until one succeeds. A rejected token is marked invalid; a missing
// permission moves on to the next token; rate limits and outages stop.
func (s *Server) cfWith(ctx context.Context, clusterID, accountID, zoneID string, fn func(*cf.Client) error) error {
	cands := s.cfCandidates(ctx, clusterID, accountID, zoneID)
	if len(cands) == 0 {
		return errCFNoCredential
	}
	var last error
	for _, cred := range cands {
		c, err := s.cfClientFor(ctx, clusterID, cred)
		if err != nil {
			last = err
			continue
		}
		err = fn(c)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, cf.ErrAuth):
			s.cfMarkCredential(ctx, cred, cfStatusInvalid, err.Error())
			last = err
		case errors.Is(err, cf.ErrPermission):
			last = fmt.Errorf("%w (token %q)", err, cred.Name)
		default:
			return err
		}
	}
	return last
}

func (s *Server) cfMarkCredential(ctx context.Context, cred appdb.CloudflareCredential, status, reason string) {
	cred.Status, cred.StatusReason = status, reason
	_ = s.Store.PutCloudflareCredential(context.WithoutCancel(ctx), cred)
}

// cfHTTPStatus maps a Cloudflare error to a response code for the browser.
func cfHTTPStatus(err error) int {
	switch {
	case errors.Is(err, errCFNoCredential):
		return http.StatusFailedDependency
	case errors.Is(err, cf.ErrAuth), errors.Is(err, cf.ErrPermission):
		// The saved token was refused, not the signed-in user. 403 would
		// read as a No-dal permission problem, so this is a failed dependency.
		return http.StatusFailedDependency
	case errors.Is(err, cf.ErrRateLimited):
		return http.StatusTooManyRequests
	case errors.Is(err, cf.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, cf.ErrConflict), errors.Is(err, cf.ErrConfigChanged):
		return http.StatusConflict
	case errors.Is(err, cf.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return http.StatusBadGateway
	}
	return http.StatusUnprocessableEntity
}

func cfWriteErr(w http.ResponseWriter, err error) {
	writeErr(w, cfHTTPStatus(err), err.Error())
}

// --- credentials ---

func cfCredentialJSON(c appdb.CloudflareCredential) map[string]any {
	accounts := []map[string]any{}
	for _, sc := range cfCredScopes(c) {
		accounts = append(accounts, map[string]any{
			"id": sc.Account.ID, "name": sc.Account.Name, "zones": len(sc.Zones), "tunnels": sc.Tunnels, "dns": sc.DNS, "listed": sc.Listed, "problems": sc.Problems,
		})
	}
	out := map[string]any{
		"id": c.ID, "name": c.Name, "account_hint": c.AccountHint, "account_owned": c.AccountOwned,
		"token_last4": c.TokenLast4, "status": c.Status, "status_reason": c.StatusReason, "accounts": accounts,
		"created_by": c.CreatedBy, "created_at": c.CreatedAt.UTC().Format(time.RFC3339),
	}
	if c.LastVerifiedAt != nil {
		out["last_verified_at"] = c.LastVerifiedAt.UTC().Format(time.RFC3339)
	}
	if c.ExpiresAt != nil {
		out["expires_at"] = c.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return out
}

var cfAccountIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// cfDiscover verifies token and maps what it reaches. The token is used only
// for this call.
func (s *Server) cfDiscover(ctx context.Context, token, accountHint string) (cf.Scope, error) {
	dctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return cf.Discover(dctx, s.cfNewClient(token), accountHint)
}

func applyScope(c *appdb.CloudflareCredential, sc cf.Scope, now time.Time) {
	raw, _ := json.Marshal(sc.Accounts)
	c.Scope = raw
	c.AccountOwned = sc.Token.AccountOwned
	c.ExpiresAt = sc.Token.ExpiresOn
	c.LastVerifiedAt = &now
	c.Status, c.StatusReason = cfStatusActive, ""
	var problems []string
	for _, a := range sc.Accounts {
		for _, p := range a.Problems {
			problems = append(problems, a.Account.Name+": "+p)
		}
	}
	if len(problems) > 0 {
		c.Status, c.StatusReason = cfStatusLimited, strings.Join(problems, "; ")
	}
}

type cfCredentialIn struct {
	Name           string `json:"name"`
	Token          string `json:"token"`
	AccountID      string `json:"account_id"`
	AllowNarrower  bool   `json:"allow_narrower"`
	ReplaceAccount bool   `json:"-"`
}

func (in *cfCredentialIn) clean() error {
	in.Name = strings.TrimSpace(in.Name)
	in.Token = strings.TrimSpace(in.Token)
	in.AccountID = strings.ToLower(strings.TrimSpace(in.AccountID))
	if in.AccountID != "" && !cfAccountIDRe.MatchString(in.AccountID) {
		return fmt.Errorf("account ID must be the 32-character ID from the Cloudflare dashboard")
	}
	if strings.ContainsAny(in.Token, " \t\r\n") {
		return fmt.Errorf("the token contains spaces; paste only the token")
	}
	if len(in.Name) > 80 {
		return fmt.Errorf("name is too long")
	}
	return nil
}

func (s *Server) listCloudflareCredentials(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareRead)
	if err != nil {
		return
	}
	creds, err := s.Store.ListCloudflareCredentials(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(creds))
	for _, c := range creds {
		items = append(items, cfCredentialJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createCloudflareCredential(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareCredentials)
	if err != nil {
		return
	}
	var in cfCredentialIn
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := in.clean(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Name == "" || in.Token == "" {
		writeErr(w, http.StatusBadRequest, "name and token are required")
		return
	}
	sc, err := s.cfDiscover(r.Context(), in.Token, in.AccountID)
	if err != nil {
		s.audit(r, p.User.ClusterID, p.User.ID, "cloudflare.credential.create", "rejected", in.Name)
		writeErr(w, cfHTTPStatus(err), "the token was not saved: "+err.Error())
		return
	}
	cred := appdb.CloudflareCredential{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: in.Name, AccountHint: in.AccountID,
		TokenLast4: cf.Last4(in.Token), CreatedBy: p.User.Username,
	}
	applyScope(&cred, sc, s.now())
	sealed, err := s.cfSealer().Seal(cred.ID, in.Token)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not encrypt the token: "+err.Error())
		return
	}
	if err := s.Store.PutCloudflareCredential(r.Context(), cred); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.Store.PutCloudflareToken(r.Context(), p.User.ClusterID, cred.ID, sealed); err != nil {
		_ = s.Store.DeleteCloudflareCredential(r.Context(), p.User.ClusterID, cred.ID)
		writeErr(w, http.StatusInternalServerError, "could not store the token")
		return
	}
	s.cfInvalidate(p.User.ClusterID)
	s.audit(r, p.User.ClusterID, p.User.ID, "cloudflare.credential.create", "ok", cred.Name)
	writeJSON(w, http.StatusCreated, cfCredentialJSON(cred))
}

// updateCloudflareCredential renames a credential or rotates its token. A new
// token is verified before it replaces the old one, and one that reaches fewer
// accounts is refused unless allow_narrower is set.
func (s *Server) updateCloudflareCredential(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareCredentials)
	if err != nil {
		return
	}
	cred, err := s.Store.GetCloudflareCredential(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || cred == nil {
		writeErr(w, http.StatusNotFound, "credential not found")
		return
	}
	var in cfCredentialIn
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := in.clean(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	next := *cred
	if in.Name != "" {
		next.Name = in.Name
	}
	if in.AccountID != "" {
		next.AccountHint = in.AccountID
	}
	var sealed string
	action := "cloudflare.credential.update"
	if in.Token != "" {
		action = "cloudflare.credential.rotate"
		sc, err := s.cfDiscover(r.Context(), in.Token, next.AccountHint)
		if err != nil {
			writeErr(w, cfHTTPStatus(err), "the new token was not saved: "+err.Error())
			return
		}
		reach := map[string]bool{}
		for _, a := range sc.Accounts {
			reach[a.Account.ID] = true
		}
		var lost []string
		for _, a := range cfCredScopes(*cred) {
			if !reach[a.Account.ID] {
				lost = append(lost, a.Account.Name)
			}
		}
		if len(lost) > 0 && !in.AllowNarrower {
			writeErr(w, http.StatusConflict, "the new token cannot reach "+strings.Join(lost, ", ")+"; confirm to replace it anyway")
			return
		}
		applyScope(&next, sc, s.now())
		next.TokenLast4 = cf.Last4(in.Token)
		if sealed, err = s.cfSealer().Seal(next.ID, in.Token); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not encrypt the token")
			return
		}
	}
	if err := s.Store.PutCloudflareCredential(r.Context(), next); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if sealed != "" {
		if err := s.Store.PutCloudflareToken(r.Context(), p.User.ClusterID, next.ID, sealed); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not store the token")
			return
		}
	}
	s.cfInvalidate(p.User.ClusterID)
	s.audit(r, p.User.ClusterID, p.User.ID, action, "ok", next.Name)
	writeJSON(w, http.StatusOK, cfCredentialJSON(next))
}

// validateCloudflareCredential re-checks a saved token and refreshes what it reaches.
func (s *Server) validateCloudflareCredential(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	cred, err := s.Store.GetCloudflareCredential(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || cred == nil {
		writeErr(w, http.StatusNotFound, "credential not found")
		return
	}
	sealed, err := s.Store.GetCloudflareToken(r.Context(), p.User.ClusterID, cred.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tok, err := s.cfSealer().Open(cred.ID, sealed)
	if err != nil {
		s.cfMarkCredential(r.Context(), *cred, cfStatusError, err.Error())
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	next := *cred
	sc, derr := s.cfDiscover(r.Context(), tok, cred.AccountHint)
	switch {
	case derr == nil:
		applyScope(&next, sc, s.now())
	case errors.Is(derr, cf.ErrAuth):
		next.Status, next.StatusReason = cfStatusInvalid, derr.Error()
	default:
		next.Status, next.StatusReason = cfStatusError, derr.Error()
	}
	_ = s.Store.PutCloudflareCredential(r.Context(), next)
	s.cfInvalidate(p.User.ClusterID)
	s.audit(r, p.User.ClusterID, p.User.ID, "cloudflare.credential.validate", next.Status, next.Name)
	writeJSON(w, http.StatusOK, cfCredentialJSON(next))
}

// deleteCloudflareCredential forgets a token. Nothing in Cloudflare changes.
func (s *Server) deleteCloudflareCredential(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareCredentials)
	if err != nil {
		return
	}
	cred, err := s.Store.GetCloudflareCredential(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || cred == nil {
		writeErr(w, http.StatusNotFound, "credential not found")
		return
	}
	s.cfForgetClient(cred.ID)
	if err := s.Store.DeleteCloudflareCredential(r.Context(), p.User.ClusterID, cred.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.cfInvalidate(p.User.ClusterID)
	s.audit(r, p.User.ClusterID, p.User.ID, "cloudflare.credential.delete", "ok", cred.Name)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "note": "Only No-dal's copy of the token was removed. Tunnels, routes and DNS in Cloudflare are unchanged."})
}
