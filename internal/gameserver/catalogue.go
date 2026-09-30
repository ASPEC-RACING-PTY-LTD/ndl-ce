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
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Game           string        `json:"game"`
	Implementation string        `json:"implementation"`
	Family         string        `json:"family"`
	Summary        string        `json:"summary"`
	Source         string        `json:"source"`
	ImportURL      string        `json:"import_url,omitempty"`
	Builtin        bool          `json:"builtin"`
	Tags           []string      `json:"tags,omitempty"`
	Capabilities   []string      `json:"capabilities,omitempty"`
	Aliases        []string      `json:"aliases,omitempty"`
	RuntimeKind    string        `json:"runtime_kind,omitempty"`
	Hint           string        `json:"hint,omitempty"`
	GameTitle      string        `json:"game_title,omitempty"`
	Category       string        `json:"category,omitempty"`
	Engine         string        `json:"engine,omitempty"`
	InstallMethod  string        `json:"install_method,omitempty"`
	Architectures  []string      `json:"architectures,omitempty"`
	Requirements   []Requirement `json:"requirements,omitempty"`
	DefaultMemory  int           `json:"default_memory_mb,omitempty"`
	MinMemory      int           `json:"min_memory_mb,omitempty"`
	DefaultDisk    int           `json:"default_disk_mb,omitempty"`
	DefaultCPUs    int           `json:"default_cpus,omitempty"`
	Ports          []string      `json:"ports,omitempty"`
	Verification   string        `json:"verification,omitempty"`
	SourceRef      string        `json:"source_ref,omitempty"`
	DocsURL        string        `json:"docs_url,omitempty"`
	LogoURL        string        `json:"logo_url,omitempty"`
	LogoKind       string        `json:"logo_kind,omitempty"`
	GameLogoURL    string        `json:"game_logo_url,omitempty"`
	GameLogoKind   string        `json:"game_logo_kind,omitempty"`
}

// CatalogueItemFromTemplate builds the catalogue card for any template,
// builtin or imported.
func CatalogueItemFromTemplate(t Template, source string, builtin bool) CatalogueItem {
	return catalogueItemFromTemplate(t, source, builtin)
}

func catalogueItemFromTemplate(t Template, source string, builtin bool) CatalogueItem {
	var ports []string
	for _, p := range t.DefaultPorts {
		ports = append(ports, PortKey(p.ContainerPort, p.Protocol))
	}
	reqs := append([]Requirement(nil), t.Requirements...)
	for _, env := range t.StartRequires {
		covered := false
		for _, r := range reqs {
			if r.Env == env {
				covered = true
			}
		}
		if !covered {
			reqs = append(reqs, Requirement{Kind: ReqToken, Stage: StageStart, Env: env, Label: env})
		}
	}
	logo, logoKind := TemplateLogo(t)
	gameLogo, gameLogoKind := GameLogo(firstNonEmpty(t.GameTitle, t.Name))
	return CatalogueItem{
		LogoURL: logo, LogoKind: logoKind, GameLogoURL: gameLogo, GameLogoKind: gameLogoKind,
		ID: t.ID, Name: t.Name, Game: t.Game, Implementation: t.Implementation,
		Family: t.Family, Summary: t.Summary, Source: source, Builtin: builtin,
		Tags: t.Tags, Capabilities: t.Capabilities, Aliases: t.Aliases,
		RuntimeKind: t.RuntimeKind(), Hint: t.Hint,
		GameTitle: firstNonEmpty(t.GameTitle, t.Name), Category: t.Category, Engine: t.Engine,
		InstallMethod: t.InstallMethod(), Architectures: t.Arches(), Requirements: reqs,
		DefaultMemory: t.DefaultMemoryMB, MinMemory: t.MinMemoryMB, DefaultDisk: t.DefaultDiskMB,
		DefaultCPUs: t.DefaultCPUs, Ports: ports, Verification: VerificationLevel(t),
		SourceRef: t.SourceRef, DocsURL: t.DocsURL,
	}
}

func builtinCatalogue() []CatalogueItem {
	var out []CatalogueItem
	for _, t := range BuiltinTemplates() {
		if t.Hidden {
			continue
		}
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
		item.GameTitle, item.Category, item.Engine, item.InstallMethod, strings.Join(item.Architectures, " "),
	}
	for _, r := range item.Requirements {
		parts = append(parts, r.Kind, strings.ReplaceAll(r.Kind, "_", " "))
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
		case "arm64", "amd64":
			for _, a := range item.Architectures {
				if a == group {
					out = append(out, item)
					break
				}
			}
		case "no-credentials", "free":
			if !needsCredentials(item.Requirements) {
				out = append(out, item)
			}
		default:
			if strings.EqualFold(item.Category, group) || catalogueQueryMatch(item, group) {
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

func needsCredentials(reqs []Requirement) bool {
	for _, r := range reqs {
		if r.Stage == StageOptional || r.Kind == ReqEULA {
			continue
		}
		return true
	}
	return false
}
