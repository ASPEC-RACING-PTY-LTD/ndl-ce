package cloudflare

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const permissionsPage = `# API token permissions

Last updated Oct 12, 2026|Copy as Markdown

## User permissions

| Name | Description |
| --- | --- |
| API Tokens Read | Grants read access to API tokens. |

## Account permissions

| Name | Description |
| --- | --- |
| Account Settings Read | Grants read access to [Account resources](https://developers.cloudflare.com/fundamentals/account/). |
| Cloudflare Tunnel Read | Grants access to view [Cloudflare Tunnels](https://x). |
| Cloudflare Tunnel Edit | Grants access to create and delete [Cloudflare Tunnels](https://x). |

## Zone permissions

| Name | Description |
| --- | --- |
| DNS Read | Grants read access to [DNS](https://developers.cloudflare.com/dns/). |
| DNS Write | Grants write access to [DNS](https://developers.cloudflare.com/dns/). |
| Zone Read | Grants read access to zone management. |

## On this page
`

const createPage = `# Create API token

1. Determine if you want a user token or an [Account API token](https://x).
2. From the [Cloudflare dashboard ↗︎](https://dash.cloudflare.com/profile/api-tokens/), go to **My Profile** > **API Tokens**.
3. Select **Create Token**.
4. Select a template.![shot](https://img)
5. Modify the token's permissions.

Warning

1. Something else
`

func TestFetchGuideReadsCloudflareDocs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case permissionsDocPath + "index.md":
			_, _ = w.Write([]byte(permissionsPage))
		case createTokenDocPath + "index.md":
			_, _ = w.Write([]byte(createPage))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	g, err := FetchGuide(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if g.Source != "cloudflare-docs" || g.DocsUpdated != "Oct 12, 2026" {
		t.Fatalf("%+v", g)
	}
	byID := map[string]GuidePermission{}
	for _, p := range g.Permissions {
		byID[p.ID] = p
	}
	if p := byID["dns_edit"]; !p.InDocs || p.Description != "Grants write access to DNS." {
		t.Fatalf("dns edit should match DNS Write: %+v", p)
	}
	if p := byID["account_settings_read"]; p.Description != "Grants read access to Account resources." {
		t.Fatalf("links must be stripped: %q", p.Description)
	}
	if len(g.Steps) != 5 || g.Steps[1] != "From the Cloudflare dashboard, go to My Profile > API Tokens." {
		t.Fatalf("steps %q", g.Steps)
	}
	for _, s := range g.Steps {
		if strings.Contains(s, "](") || strings.Contains(s, "**") || strings.Contains(s, "Something else") {
			t.Fatalf("step not cleaned: %q", s)
		}
	}
}

func TestFetchGuideFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", 503) }))
	defer srv.Close()
	g, err := FetchGuide(context.Background(), srv.Client(), srv.URL)
	if err == nil || g.Source != "built-in" || len(g.Permissions) != 6 || len(g.UseCases) == 0 || g.Error == "" {
		t.Fatalf("fallback %+v %v", g, err)
	}
}

func TestPaceAllowsBurstThenSpaces(t *testing.T) {
	c := &Client{MinGap: 20 * time.Millisecond, Burst: 3}
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := c.pace(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 10*time.Millisecond {
		t.Fatalf("burst waited %s", d)
	}
	for i := 0; i < 2; i++ {
		_ = c.pace(context.Background())
	}
	if d := time.Since(start); d < 35*time.Millisecond {
		t.Fatalf("calls past the burst were not spaced: %s", d)
	}
}

func TestListedConnectorsGroupsByClient(t *testing.T) {
	tun := Tunnel{Status: "healthy", Connections: []ListedConnection{
		{ClientID: "a", ClientVersion: "2026.1", ColoName: "SYD"},
		{ClientID: "a", ClientVersion: "2026.1", ColoName: "MEL"},
		{ClientID: "b", ClientVersion: "2025.9", ColoName: "SYD"},
	}}
	cs, ok := tun.ListedConnectors()
	if !ok || len(cs) != 2 || len(cs[0].Connections) != 2 || cs[1].Version != "2025.9" {
		t.Fatalf("%+v %v", cs, ok)
	}
	if _, ok := (Tunnel{Status: "healthy"}).ListedConnectors(); ok {
		t.Fatal("a connected tunnel without listed connections must be read separately")
	}
	if cs, ok := (Tunnel{Status: "inactive"}).ListedConnectors(); !ok || len(cs) != 0 {
		t.Fatal("an inactive tunnel has no connectors")
	}
}
