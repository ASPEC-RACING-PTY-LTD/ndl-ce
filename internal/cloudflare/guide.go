package cloudflare

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The permissions guide tells people which token permissions each No-dal
// feature needs and how to add them. Names, descriptions and token steps are
// read from Cloudflare's documentation (which publishes Markdown copies of
// its pages), so they follow Cloudflare's current wording. A built-in copy is
// used when the documentation cannot be reached.

// DocsBaseURL is where Cloudflare publishes its developer documentation.
const DocsBaseURL = "https://developers.cloudflare.com"

const (
	permissionsDocPath = "/fundamentals/api/reference/permissions/"
	createTokenDocPath = "/fundamentals/api/get-started/create-token/"
	accountTokensPath  = "/fundamentals/api/get-started/account-owned-tokens/"
	// DashboardTokensURL is where user tokens are created and edited.
	DashboardTokensURL = "https://dash.cloudflare.com/profile/api-tokens"
)

// GuidePermission is one permission a feature needs.
type GuidePermission struct {
	ID string `json:"id"`
	// Scope is Account or Zone, the first choice in the token editor.
	Scope string `json:"scope"`
	// Group and Level are the second and third choices, as the dashboard
	// shows them, for example "Cloudflare Tunnel" and "Edit".
	Group string `json:"group"`
	Level string `json:"level"`
	// Description is Cloudflare's own description of the permission.
	Description string `json:"description"`
	// InDocs is false when Cloudflare's current list does not name the
	// permission; it may have been renamed.
	InDocs bool `json:"in_docs"`
}

// GuideUseCase is one thing No-dal does and the permissions it needs.
type GuideUseCase struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Why   string   `json:"why"`
	Needs []string `json:"needs"`
	// Resources says which resources to pick for the permission.
	Resources string `json:"resources"`
	// Check is what a token's saved scope reports for it: accounts, tunnels,
	// zones or dns. Empty means Cloudflare only reports it when it is used.
	Check string `json:"check,omitempty"`
	// UserTokensOnly is true when account-owned tokens do not need it.
	UserTokensOnly bool `json:"user_tokens_only,omitempty"`
}

// Guide is the full permissions guide.
type Guide struct {
	// Source is "cloudflare-docs" or "built-in".
	Source       string            `json:"source"`
	FetchedAt    time.Time         `json:"fetched_at"`
	DocsUpdated  string            `json:"docs_updated,omitempty"`
	DocsURL      string            `json:"docs_url"`
	CreateURL    string            `json:"create_url"`
	AccountURL   string            `json:"account_tokens_url"`
	DashboardURL string            `json:"dashboard_url"`
	Steps        []string          `json:"steps"`
	Permissions  []GuidePermission `json:"permissions"`
	UseCases     []GuideUseCase    `json:"use_cases"`
	// Error says why the documentation could not be read, when Source is built-in.
	Error string `json:"error,omitempty"`
}

var guidePermissions = []GuidePermission{
	{ID: "account_settings_read", Scope: "Account", Group: "Account Settings", Level: "Read", Description: "Grants read access to Account resources, account membership, and account level features."},
	{ID: "tunnel_read", Scope: "Account", Group: "Cloudflare Tunnel", Level: "Read", Description: "Grants access to view Cloudflare Tunnels."},
	{ID: "tunnel_edit", Scope: "Account", Group: "Cloudflare Tunnel", Level: "Edit", Description: "Grants access to create and delete Cloudflare Tunnels."},
	{ID: "zone_read", Scope: "Zone", Group: "Zone", Level: "Read", Description: "Grants read access to zone management."},
	{ID: "dns_read", Scope: "Zone", Group: "DNS", Level: "Read", Description: "Grants read access to DNS."},
	{ID: "dns_edit", Scope: "Zone", Group: "DNS", Level: "Edit", Description: "Grants write access to DNS."},
}

var guideUseCases = []GuideUseCase{
	{ID: "accounts", Title: "Find your accounts", Why: "Lists the accounts a user token can reach. Account-owned tokens skip this; enter the account ID instead.",
		Needs: []string{"account_settings_read"}, Resources: "Include: All accounts, or the accounts to manage", Check: "accounts", UserTokensOnly: true},
	{ID: "tunnels_read", Title: "See tunnels and hostnames", Why: "Lists tunnels, their connectors and the public hostnames they route.",
		Needs: []string{"tunnel_read"}, Resources: "Include: the account", Check: "tunnels"},
	{ID: "tunnels_edit", Title: "Create tunnels and edit hostnames", Why: "Creates and renames tunnels, adds, edits, switches and moves hostnames, and deploys connectors.",
		Needs: []string{"tunnel_edit"}, Resources: "Include: the account"},
	{ID: "zones", Title: "Match hostnames to your domains", Why: "Finds the zone each hostname belongs to, so DNS and the account can be checked.",
		Needs: []string{"zone_read"}, Resources: "Include: All zones, or the zones you publish", Check: "zones"},
	{ID: "dns_read", Title: "Show DNS status", Why: "Shows whether each hostname has a proxied CNAME to its tunnel, and finds DNS conflicts.",
		Needs: []string{"dns_read"}, Resources: "Include: All zones, or the zones you publish", Check: "dns"},
	{ID: "dns_edit", Title: "Create and fix DNS records", Why: "Creates or repoints the CNAME when you add, publish or move a hostname. Without it routes are saved but DNS is left to you.",
		Needs: []string{"dns_edit"}, Resources: "Include: All zones, or the zones you publish"},
}

var builtinSteps = []string{
	"Decide between a user token (My Profile > API Tokens) and an account-owned token (Manage Account > API Tokens). Account-owned tokens are not tied to a person; enter the account ID with them.",
	"Open API Tokens in the Cloudflare dashboard and select Create Token.",
	"Select Create Custom Token (or edit an existing token).",
	"Name the token, for example No-dal.",
	"Under Permissions, add one row per permission below: pick the scope (Account or Zone), then the permission, then Read or Edit.",
	"Under Account Resources and Zone Resources, include the accounts and zones No-dal should manage.",
	"Optionally restrict client IP addresses and set an expiry date.",
	"Select Continue to summary, check it, then Create Token.",
	"Copy the token once and paste it into Replace token on the account card here. Editing an existing token's permissions keeps its value; select Check again afterwards.",
}

// BuiltinGuide is the guide without Cloudflare's documentation.
func BuiltinGuide(reason string) Guide {
	perms := make([]GuidePermission, len(guidePermissions))
	copy(perms, guidePermissions)
	return Guide{
		Source: "built-in", DocsURL: DocsBaseURL + permissionsDocPath, CreateURL: DocsBaseURL + createTokenDocPath,
		AccountURL: DocsBaseURL + accountTokensPath, DashboardURL: DashboardTokensURL,
		Steps: append([]string(nil), builtinSteps...), Permissions: perms, UseCases: append([]GuideUseCase(nil), guideUseCases...),
		Error: reason,
	}
}

// FetchGuide reads permission names, descriptions and token steps from
// Cloudflare's documentation at base. Only text is used; nothing from the
// pages is executed or rendered as HTML.
func FetchGuide(ctx context.Context, hc *http.Client, base string) (Guide, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	base = strings.TrimRight(base, "/")
	perms, err := fetchDoc(ctx, hc, base+permissionsDocPath+"index.md")
	if err != nil {
		return BuiltinGuide(err.Error()), err
	}
	g := BuiltinGuide("")
	g.Source = "cloudflare-docs"
	g.DocsUpdated = docUpdated(perms)
	found := 0
	for i := range g.Permissions {
		p := &g.Permissions[i]
		if desc, ok := findPermission(perms, p.Scope, p.Group, p.Level); ok {
			p.Description, p.InDocs = desc, true
			found++
		}
	}
	if found == 0 {
		err := fmt.Errorf("the permissions page did not list the expected permissions")
		return BuiltinGuide(err.Error()), err
	}
	// The token steps are a nice-to-have; keep the built-in ones on failure.
	if create, err := fetchDoc(ctx, hc, base+createTokenDocPath+"index.md"); err == nil {
		if steps := tokenSteps(create); len(steps) >= 4 {
			g.Steps = steps
		}
	}
	return g, nil
}

func fetchDoc(ctx context.Context, hc *http.Client, url string) (string, error) {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/markdown, text/plain")
	res, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("Cloudflare's documentation could not be reached")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Cloudflare's documentation answered HTTP %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

var docUpdatedRe = regexp.MustCompile(`Last updated ([A-Z][a-z]{2} \d{1,2}, \d{4})`)

func docUpdated(doc string) string {
	if m := docUpdatedRe.FindStringSubmatch(doc); m != nil {
		return m[1]
	}
	return ""
}

// findPermission finds a permission's description in the scope's section of
// the permissions page. The page lists dashboard names ("Edit") and API names
// ("Write"); either counts.
func findPermission(doc, scope, group, level string) (string, bool) {
	section := docSection(doc, "## "+scope+" permissions")
	names := []string{group + " " + level}
	if level == "Edit" {
		names = append(names, group+" Write")
	}
	for _, line := range strings.Split(section, "\n") {
		cells := tableCells(line)
		if len(cells) < 2 {
			continue
		}
		for _, n := range names {
			if strings.EqualFold(cells[0], n) {
				return plainText(cells[1]), true
			}
		}
	}
	return "", false
}

func docSection(doc, heading string) string {
	i := strings.Index(doc, heading)
	if i < 0 {
		return ""
	}
	rest := doc[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return nil
	}
	parts := strings.Split(strings.Trim(line, "|"), " | ")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

var (
	mdImageRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdLinkRe  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdCodeRe  = regexp.MustCompile("`([^`]*)`")
	mdStepRe  = regexp.MustCompile(`^(\d{1,2})\.\s+(.+)$`)
	spaceRe   = regexp.MustCompile(`\s+`)
)

// plainText strips Markdown images, links, emphasis and code marks.
func plainText(s string) string {
	s = mdImageRe.ReplaceAllString(s, "")
	s = mdLinkRe.ReplaceAllString(s, "$1")
	s = mdCodeRe.ReplaceAllString(s, "$1")
	s = strings.NewReplacer("**", "", "↗︎", "", "*", "").Replace(s)
	s = spaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.NewReplacer(" ,", ",", " .", ".").Replace(s))
}

// tokenSteps reads the numbered steps of "Create API token".
func tokenSteps(doc string) []string {
	start := strings.Index(doc, "# Create API token")
	if start < 0 {
		return nil
	}
	var out []string
	next := 1
	for _, line := range strings.Split(doc[start:], "\n") {
		m := mdStepRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if m[1] != fmt.Sprint(next) {
			if len(out) > 0 {
				break
			}
			continue
		}
		if t := plainText(m[2]); t != "" {
			out = append(out, t)
		}
		next++
		if next > 20 {
			break
		}
	}
	return out
}
