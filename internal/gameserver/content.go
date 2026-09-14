package gameserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type ContentProvider interface {
	ID() string
	Search(ctx context.Context, spec ContentSpec, query string) ([]ContentItem, error)
	Resolve(ctx context.Context, spec ContentSpec, externalID, version string) (ContentItem, []byte, error)
}

type Modrinth struct {
	Client *http.Client
}

func (m Modrinth) ID() string { return "modrinth" }

func (m Modrinth) Search(ctx context.Context, spec ContentSpec, query string) ([]ContentItem, error) {
	facets := [][]string{{"project_type:" + contentType(spec.Kind)}}
	if spec.Loader != "" {
		facets = append(facets, []string{"categories:" + spec.Loader})
	}
	if spec.GameID == "minecraft" {
		facets = append(facets, []string{"categories:fabric", "categories:paper", "categories:bukkit", "categories:spigot"})
		if spec.Loader != "" {
			facets = [][]string{{"project_type:" + contentType(spec.Kind)}, {"categories:" + spec.Loader}}
		}
	}
	facetJSON, _ := json.Marshal(facets)
	u := "https://api.modrinth.com/v2/search?limit=20&query=" + url.QueryEscape(query) + "&facets=" + url.QueryEscape(string(facetJSON))
	body, _, err := fetchURL(ctx, m.client(), u)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Hits []struct {
			ProjectID   string   `json:"project_id"`
			Title       string   `json:"title"`
			Slug        string   `json:"slug"`
			Description string   `json:"description"`
			Versions    []string `json:"versions"`
			Categories  []string `json:"categories"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("modrinth search is not valid JSON")
	}
	var out []ContentItem
	for _, hit := range parsed.Hits {
		out = append(out, ContentItem{
			Provider: "modrinth", ExternalID: hit.ProjectID, Name: hit.Title, Slug: hit.Slug,
			Summary: clip(hit.Description, 240), GameVersions: hit.Versions, SourceURL: "https://modrinth.com/project/" + hit.Slug,
		})
	}
	return out, nil
}

func (m Modrinth) Resolve(ctx context.Context, spec ContentSpec, externalID, version string) (ContentItem, []byte, error) {
	versURL := "https://api.modrinth.com/v2/project/" + url.PathEscape(externalID) + "/version"
	body, _, err := fetchURL(ctx, m.client(), versURL)
	if err != nil {
		return ContentItem{}, nil, err
	}
	var vers []struct {
		ID            string   `json:"id"`
		VersionNumber string   `json:"version_number"`
		GameVersions  []string `json:"game_versions"`
		Loaders       []string `json:"loaders"`
		Dependencies  []struct {
			ProjectID  string `json:"project_id"`
			Dependency string `json:"dependency_type"`
		} `json:"dependencies"`
		Files []struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
			Primary  bool   `json:"primary"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &vers); err != nil || len(vers) == 0 {
		return ContentItem{}, nil, fmt.Errorf("no Modrinth versions for this project")
	}
	chosen := vers[0]
	matched := false
	if version != "" && version != "latest" {
		for _, v := range vers {
			if v.VersionNumber == version || v.ID == version {
				chosen = v
				matched = true
				break
			}
		}
	}
	if !matched {
		for _, v := range vers {
			if loaderFits(spec.Loader, v.Loaders) {
				chosen = v
				matched = true
				break
			}
		}
		if !matched {
			return ContentItem{}, nil, fmt.Errorf("no Modrinth version matches this server loader")
		}
	}
	var fileURL, filename string
	for _, f := range chosen.Files {
		if f.Primary || fileURL == "" {
			fileURL, filename = f.URL, f.Filename
		}
	}
	if fileURL == "" {
		return ContentItem{}, nil, fmt.Errorf("this version has no downloadable file")
	}
	raw, err := fetchBinary(ctx, fileURL, 64<<20)
	if err != nil {
		return ContentItem{}, nil, err
	}
	var deps []string
	for _, d := range chosen.Dependencies {
		if d.Dependency == "required" && d.ProjectID != "" {
			deps = append(deps, d.ProjectID)
		}
	}
	item := ContentItem{
		Provider: "modrinth", ExternalID: externalID, Version: chosen.VersionNumber,
		Filename: filename, GameVersions: chosen.GameVersions, Dependencies: deps, SourceURL: fileURL,
	}
	return item, raw, nil
}

func (m Modrinth) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return defaultHTTPClient()
}

func loaderFits(want string, loaders []string) bool {
	if want == "" {
		return true
	}
	aliases := map[string][]string{
		"paper":  {"paper", "bukkit", "spigot", "purpur"},
		"bukkit": {"paper", "bukkit", "spigot"},
		"fabric": {"fabric"},
		"forge":  {"forge", "neoforge"},
	}
	allow := aliases[want]
	if len(allow) == 0 {
		allow = []string{want}
	}
	for _, have := range loaders {
		h := strings.ToLower(have)
		for _, a := range allow {
			if h == a {
				return true
			}
		}
	}
	return false
}

func contentType(kind string) string {
	switch kind {
	case "plugin":
		return "plugin"
	case "mod":
		return "mod"
	case "resource":
		return "mod"
	default:
		return "mod"
	}
}

func providerFor(spec ContentSpec) ContentProvider {
	switch spec.Provider {
	case "modrinth":
		return Modrinth{}
	case "workshop":
		return Workshop{}
	default:
		return nil
	}
}

func safeContentName(name string) string {
	name = pathBase(name)
	if name == "" || name == "." || strings.Contains(name, "..") {
		return ""
	}
	return name
}

func pathBase(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	return p
}
