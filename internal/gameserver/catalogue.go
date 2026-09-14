package gameserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
)

// DefaultSources are public egg indexes. They are fetched, never vendored.
func DefaultSources() []Source {
	return []Source{
		{ID: "pelican-minecraft", Name: "Pelican Minecraft", URL: "https://api.github.com/repos/pelican-eggs/minecraft/contents/java?ref=main", Kind: "github-dir", Enabled: true},
		{ID: "pelican-steamcmd", Name: "Pelican SteamCMD", URL: "https://api.github.com/repos/pelican-eggs/games-steamcmd/contents?ref=main", Kind: "github-dir", Enabled: true},
		{ID: "pelican-games", Name: "Pelican standalone games", URL: "https://api.github.com/repos/pelican-eggs/games/contents?ref=main", Kind: "github-dir", Enabled: true},
	}
}

type githubEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	HTMLURL     string `json:"html_url"`
	DownloadURL string `json:"download_url"`
	URL         string `json:"url"`
}

// CatalogueItem is a discoverable template card.
type CatalogueItem struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Game           string   `json:"game"`
	Implementation string   `json:"implementation"`
	Family         string   `json:"family"`
	Summary        string   `json:"summary"`
	Source         string   `json:"source"`
	ImportURL      string   `json:"import_url,omitempty"`
	Builtin        bool     `json:"builtin"`
	Tags           []string `json:"tags,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	Aliases        []string `json:"aliases,omitempty"`
	RuntimeKind    string   `json:"runtime_kind,omitempty"`
	Hint           string   `json:"hint,omitempty"`
}

func catalogueItemFromTemplate(t Template, source string, builtin bool) CatalogueItem {
	return CatalogueItem{
		ID: t.ID, Name: t.Name, Game: t.Game, Implementation: t.Implementation,
		Family: t.Family, Summary: t.Summary, Source: source, Builtin: builtin,
		Tags: t.Tags, Capabilities: t.Capabilities, Aliases: t.Aliases,
		RuntimeKind: t.RuntimeKind(), Hint: t.Hint,
	}
}

func builtinCatalogue() []CatalogueItem {
	var out []CatalogueItem
	for _, t := range BuiltinTemplates() {
		out = append(out, catalogueItemFromTemplate(t, "builtin", true))
	}
	return out
}

func matchCatalogue(items []CatalogueItem, q string) []CatalogueItem {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return items
	}
	var out []CatalogueItem
	for _, item := range items {
		if catalogueQueryMatch(item, q) {
			out = append(out, item)
		}
	}
	return out
}

func catalogueQueryMatch(item CatalogueItem, q string) bool {
	parts := []string{
		item.ID, item.Name, item.Game, item.Implementation, item.Family, item.Summary,
		item.RuntimeKind, item.Hint, strings.Join(item.Tags, " "), strings.Join(item.Aliases, " "),
	}
	if item.Builtin {
		parts = append(parts, "built in", "builtin")
	}
	hay := strings.ToLower(strings.Join(parts, " "))
	if strings.Contains(hay, q) {
		return true
	}
	for _, alias := range item.Aliases {
		if strings.EqualFold(strings.TrimSpace(alias), q) {
			return true
		}
	}
	return false
}

func filterCatalogue(items []CatalogueItem, group string) []CatalogueItem {
	group = strings.ToLower(strings.TrimSpace(group))
	if group == "" || group == "all" {
		return items
	}
	var out []CatalogueItem
	for _, item := range items {
		switch group {
		case "builtin", "built-in", "built in":
			if item.Builtin {
				out = append(out, item)
			}
		case "steamcmd", "steam":
			if strings.EqualFold(item.RuntimeKind, "SteamCMD") || strings.EqualFold(item.Family, "steam") || strings.EqualFold(item.Family, "source") {
				out = append(out, item)
			}
		case "standalone":
			if strings.EqualFold(item.RuntimeKind, "Standalone") {
				out = append(out, item)
			}
		case "minecraft", "mc":
			if strings.EqualFold(item.Family, "minecraft") {
				out = append(out, item)
			}
		case "java":
			if strings.EqualFold(item.RuntimeKind, "Java") {
				out = append(out, item)
			}
		default:
			if catalogueQueryMatch(item, group) {
				out = append(out, item)
			}
		}
	}
	return out
}

func importFromURL(ctx context.Context, client *http.Client, raw string) (Template, error) {
	if err := validatePublicURL(raw); err != nil {
		return Template{}, err
	}
	body, _, err := fetchURL(ctx, client, raw)
	if err != nil {
		return Template{}, err
	}
	t, err := ParseEggJSON(body, raw)
	if err != nil {
		return Template{}, err
	}
	t.UpdateURL = raw
	return t, nil
}

func expandGithubDir(ctx context.Context, client *http.Client, raw string) ([]CatalogueItem, error) {
	body, _, err := fetchURL(ctx, client, raw)
	if err != nil {
		return nil, err
	}
	var entries []githubEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("catalogue index is not a GitHub directory listing")
	}
	var out []CatalogueItem
	for _, e := range entries {
		if e.Type != "dir" && !strings.HasSuffix(strings.ToLower(e.Name), ".json") {
			continue
		}
		item := CatalogueItem{
			ID:      "remote-" + slug(e.Path),
			Name:    prettyName(e.Name),
			Game:    slug(e.Name),
			Summary: "Imported from " + e.HTMLURL,
			Source:  raw,
			Tags:    []string{path.Base(e.Path)},
		}
		if e.DownloadURL != "" && strings.HasSuffix(strings.ToLower(e.Name), ".json") {
			item.ImportURL = e.DownloadURL
		} else if e.URL != "" {
			item.ImportURL = e.URL
		}
		out = append(out, item)
	}
	return out, nil
}

func prettyName(s string) string {
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.TrimSuffix(s, ".json")
	if s == "" {
		return "Template"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
