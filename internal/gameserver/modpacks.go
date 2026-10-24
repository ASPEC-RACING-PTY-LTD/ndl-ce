package gameserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Modpack is one Minecraft modpack from Modrinth or CurseForge.
type Modpack struct {
	Provider     string   `json:"provider"`
	ID           string   `json:"id"`
	Slug         string   `json:"slug,omitempty"`
	Name         string   `json:"name"`
	Summary      string   `json:"summary,omitempty"`
	IconURL      string   `json:"icon_url,omitempty"`
	Downloads    int64    `json:"downloads"`
	GameVersions []string `json:"game_versions,omitempty"`
	Loaders      []string `json:"loaders,omitempty"`
	URL          string   `json:"url,omitempty"`
}

// ModpackVersion is one release of a modpack.
type ModpackVersion struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	GameVersion string `json:"game_version,omitempty"`
	Loader      string `json:"loader,omitempty"`
	Date        string `json:"date,omitempty"`
	Release     bool   `json:"release"`
	ServerPack  bool   `json:"server_pack,omitempty"`
	// Java is the Java release the version's Minecraft version needs.
	Java int `json:"java,omitempty"`
}

// ModpackTemplateID is the template a provider's packs install with.
func ModpackTemplateID(provider string) string {
	if provider == "curseforge" {
		return "ndl-minecraft-curseforge-pack"
	}
	return "ndl-minecraft-modrinth-pack"
}

// JavaImageLabelFor is the Java runtime label for a Minecraft version, or
// "" when the version is unknown.
func JavaImageLabelFor(mc string) string {
	need := javaForMinecraft(mc)
	switch {
	case need <= 0:
		return ""
	case need <= 8:
		return "Java 8"
	case need <= 17:
		return "Java 17"
	case need <= 21:
		return "Java 21"
	}
	return "Java 25"
}

var mcLoaders = map[string]bool{"forge": true, "neoforge": true, "fabric": true, "quilt": true}

// SearchModpacks finds Minecraft modpacks. An empty query lists the most
// downloaded.
func SearchModpacks(ctx context.Context, provider, query, cfKey string) ([]Modpack, error) {
	if provider == "curseforge" {
		return searchCFModpacks(ctx, query, cfKey)
	}
	return searchModrinthModpacks(ctx, query)
}

// ModpackVersions lists a modpack's versions, newest first.
func ModpackVersions(ctx context.Context, provider, id, cfKey string) ([]ModpackVersion, error) {
	if provider == "curseforge" {
		return cfModpackVersions(ctx, id, cfKey)
	}
	return modrinthModpackVersions(ctx, id)
}

func searchModrinthModpacks(ctx context.Context, query string) ([]Modpack, error) {
	facets, _ := json.Marshal([][]string{{"project_type:modpack"}})
	u := "https://api.modrinth.com/v2/search?limit=24&query=" + url.QueryEscape(strings.TrimSpace(query)) + "&facets=" + url.QueryEscape(string(facets))
	if strings.TrimSpace(query) == "" {
		u += "&index=downloads"
	}
	body, _, err := fetchURL(ctx, defaultHTTPClient(), u)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Hits []struct {
			ProjectID   string   `json:"project_id"`
			Slug        string   `json:"slug"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			IconURL     string   `json:"icon_url"`
			Downloads   int64    `json:"downloads"`
			Versions    []string `json:"versions"`
			Categories  []string `json:"categories"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("modrinth search is not valid JSON")
	}
	out := make([]Modpack, 0, len(parsed.Hits))
	for _, h := range parsed.Hits {
		var loaders []string
		for _, c := range h.Categories {
			if mcLoaders[c] {
				loaders = append(loaders, c)
			}
		}
		out = append(out, Modpack{
			Provider: "modrinth", ID: h.ProjectID, Slug: h.Slug, Name: h.Title, Summary: clip(h.Description, 240),
			IconURL: h.IconURL, Downloads: h.Downloads, GameVersions: newestFirst(h.Versions, 4), Loaders: loaders,
			URL: "https://modrinth.com/modpack/" + h.Slug,
		})
	}
	return out, nil
}

// newestFirst keeps the last n entries of an oldest-first list, newest first.
func newestFirst(list []string, n int) []string {
	out := []string{}
	for i := len(list) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, list[i])
	}
	return out
}

func modrinthModpackVersions(ctx context.Context, id string) ([]ModpackVersion, error) {
	id = strings.TrimSpace(id)
	if !modrinthSlugPattern.MatchString(id) {
		return nil, fmt.Errorf("Modrinth modpack %q is not a valid slug or ID", id)
	}
	body, _, err := fetchURL(ctx, defaultHTTPClient(), "https://api.modrinth.com/v2/project/"+url.PathEscape(id)+"/version")
	if err != nil {
		return nil, err
	}
	var vers []struct {
		ID            string   `json:"id"`
		VersionNumber string   `json:"version_number"`
		Name          string   `json:"name"`
		VersionType   string   `json:"version_type"`
		GameVersions  []string `json:"game_versions"`
		Loaders       []string `json:"loaders"`
		Date          string   `json:"date_published"`
	}
	if err := json.Unmarshal(body, &vers); err != nil {
		return nil, fmt.Errorf("modrinth versions are not valid JSON")
	}
	out := make([]ModpackVersion, 0, len(vers))
	for _, v := range vers {
		mv := ModpackVersion{ID: v.ID, Name: firstNonEmpty(v.VersionNumber, v.Name), Date: v.Date, Release: v.VersionType == "release"}
		if len(v.GameVersions) > 0 {
			mv.GameVersion = v.GameVersions[len(v.GameVersions)-1]
			mv.Java = javaForMinecraft(mv.GameVersion)
		}
		for _, l := range v.Loaders {
			if mcLoaders[l] {
				mv.Loader = l
			}
		}
		out = append(out, mv)
		if len(out) >= 50 {
			break
		}
	}
	return out, nil
}

func searchCFModpacks(ctx context.Context, query, key string) ([]Modpack, error) {
	mods, err := cfClient{Key: key}.search(ctx, cfClassModpacks, query, "", 24)
	if err != nil {
		return nil, err
	}
	out := make([]Modpack, 0, len(mods))
	for _, m := range mods {
		p := Modpack{
			Provider: "curseforge", ID: strconv.Itoa(m.ID), Slug: m.Slug, Name: m.Name, Summary: clip(m.Summary, 240),
			Downloads: m.DownloadCount, URL: m.Links.WebsiteURL,
		}
		if m.Logo != nil {
			p.IconURL = m.Logo.ThumbnailURL
		}
		seen := map[string]bool{}
		for _, f := range m.LatestFilesIndexes {
			if f.ModLoader != nil {
				for name, t := range cfLoaderTypes {
					if t == *f.ModLoader && !seen[name] {
						seen[name] = true
						p.Loaders = append(p.Loaders, name)
					}
				}
			}
		}
		gv := cfGameVersions(m)
		if len(gv) > 4 {
			gv = gv[:4]
		}
		p.GameVersions = gv
		out = append(out, p)
	}
	return out, nil
}

func cfModpackVersions(ctx context.Context, id, key string) ([]ModpackVersion, error) {
	api := cfClient{Key: key}
	modID, err := api.resolveProject(ctx, cfClassModpacks, id)
	if err != nil {
		return nil, err
	}
	files, err := api.files(ctx, modID, nil)
	if err != nil {
		return nil, err
	}
	out := []ModpackVersion{}
	for _, f := range files {
		if f.IsServerPack {
			continue
		}
		mv := ModpackVersion{ID: strconv.Itoa(f.ID), Name: f.DisplayName, Date: f.FileDate, Release: f.ReleaseType == cfReleaseFile, ServerPack: f.ServerPackFileID > 0}
		for _, gv := range f.GameVersions {
			low := strings.ToLower(gv)
			switch {
			case mcLoaders[low]:
				mv.Loader = low
			case javaForMinecraft(gv) > 0 && mv.GameVersion == "":
				mv.GameVersion = gv
				mv.Java = javaForMinecraft(gv)
			}
		}
		out = append(out, mv)
	}
	return out, nil
}
