package gameserver

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CurseForgeKeyEnv carries the CurseForge API key into an install. The
// control plane adds it from its sealed settings for the install only; it
// is never saved with the server.
const CurseForgeKeyEnv = "NDL_CURSEFORGE_API_KEY"

// curseForgeAPI is the CurseForge Core API. Every call needs a key from
// console.curseforge.com.
var curseForgeAPI = "https://api.curseforge.com"

const (
	cfGameMinecraft  = 432
	cfClassMods      = 6
	cfClassPlugins   = 5
	cfClassModpacks  = 4471
	cfClassResources = 12
	cfReleaseFile    = 1
)

// ErrNoCurseForgeKey means CurseForge was asked for without an API key.
var ErrNoCurseForgeKey = fmt.Errorf("add a CurseForge API key in Game Servers settings to use CurseForge")

var cfLoaderTypes = map[string]int{"forge": 1, "fabric": 4, "quilt": 5, "neoforge": 6}

// cfLoaderNames are the loader names CurseForge lists in a file's versions.
var cfLoaderNames = map[string]string{"forge": "Forge", "fabric": "Fabric", "quilt": "Quilt", "neoforge": "NeoForge"}

type cfClient struct {
	Key    string
	Client *http.Client
}

func (c cfClient) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return defaultHTTPClient()
}

func (c cfClient) do(ctx context.Context, method, path string, body any, out any) error {
	if strings.TrimSpace(c.Key) == "" {
		return ErrNoCurseForgeKey
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, curseForgeAPI+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", strings.TrimSpace(c.Key))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "No-DAL-GameServers/1.0 (https://github.com/ASPEC-RACING-PTY-LTD/ndl-ce)")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("curseforge is unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	switch {
	case res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("curseforge rejected the API key (HTTP %d); check it in Game Servers settings", res.StatusCode)
	case res.StatusCode == http.StatusNotFound:
		return fmt.Errorf("curseforge has no such project or file")
	case res.StatusCode >= 400:
		return fmt.Errorf("curseforge returned HTTP %d", res.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("curseforge answer is not valid JSON")
	}
	return nil
}

type cfMod struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Summary       string `json:"summary"`
	ClassID       int    `json:"classId"`
	DownloadCount int64  `json:"downloadCount"`
	Logo          *struct {
		ThumbnailURL string `json:"thumbnailUrl"`
	} `json:"logo"`
	Links struct {
		WebsiteURL string `json:"websiteUrl"`
	} `json:"links"`
	LatestFilesIndexes []struct {
		GameVersion string `json:"gameVersion"`
		ModLoader   *int   `json:"modLoader"`
	} `json:"latestFilesIndexes"`
}

type cfFile struct {
	ID               int      `json:"id"`
	ModID            int      `json:"modId"`
	DisplayName      string   `json:"displayName"`
	FileName         string   `json:"fileName"`
	ReleaseType      int      `json:"releaseType"`
	FileDate         string   `json:"fileDate"`
	DownloadURL      string   `json:"downloadUrl"`
	GameVersions     []string `json:"gameVersions"`
	ServerPackFileID int      `json:"serverPackFileId"`
	IsServerPack     bool     `json:"isServerPack"`
	FileLength       int64    `json:"fileLength"`
	Hashes           []struct {
		Value string `json:"value"`
		Algo  int    `json:"algo"`
	} `json:"hashes"`
	Dependencies []struct {
		ModID        int `json:"modId"`
		RelationType int `json:"relationType"`
	} `json:"dependencies"`
}

func (f cfFile) sha1() digest {
	for _, h := range f.Hashes {
		if h.Algo == 1 {
			return digest{Algo: "sha1", Hex: h.Value}
		}
	}
	return digest{}
}

// downloadURL is the file's download address. A project that turned off
// third-party distribution has no downloadUrl; its files are still served
// from the CurseForge CDN under a fixed path.
func (f cfFile) downloadURL() string {
	if strings.TrimSpace(f.DownloadURL) != "" {
		return f.DownloadURL
	}
	return fmt.Sprintf("https://edge.forgecdn.net/files/%d/%d/%s", f.ID/1000, f.ID%1000, url.PathEscape(f.FileName))
}

func (c cfClient) search(ctx context.Context, classID int, query, loader string, size int) ([]cfMod, error) {
	q := url.Values{}
	q.Set("gameId", strconv.Itoa(cfGameMinecraft))
	q.Set("classId", strconv.Itoa(classID))
	q.Set("pageSize", strconv.Itoa(size))
	q.Set("sortOrder", "desc")
	if strings.TrimSpace(query) != "" {
		q.Set("searchFilter", strings.TrimSpace(query))
		q.Set("sortField", "1") // featured relevance
	} else {
		q.Set("sortField", "6") // total downloads
	}
	if t, ok := cfLoaderTypes[loader]; ok {
		q.Set("modLoaderType", strconv.Itoa(t))
	}
	var out struct {
		Data []cfMod `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/mods/search?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

var cfURLSlug = regexp.MustCompile(`curseforge\.com/minecraft/[a-z-]+/([A-Za-z0-9_-]+)`)

// resolveProject turns a project ID, a slug or a curseforge.com URL into a
// project ID in the given class.
func (c cfClient) resolveProject(ctx context.Context, classID int, ref string) (int, error) {
	ref = strings.TrimSpace(ref)
	if m := cfURLSlug.FindStringSubmatch(ref); m != nil {
		ref = m[1]
	}
	if id, err := strconv.Atoi(ref); err == nil && id > 0 {
		return id, nil
	}
	if !modrinthSlugPattern.MatchString(ref) {
		return 0, fmt.Errorf("CurseForge project %q is not an ID, slug or curseforge.com link", ref)
	}
	q := url.Values{}
	q.Set("gameId", strconv.Itoa(cfGameMinecraft))
	q.Set("classId", strconv.Itoa(classID))
	q.Set("slug", ref)
	var out struct {
		Data []cfMod `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/mods/search?"+q.Encode(), nil, &out); err != nil {
		return 0, err
	}
	if len(out.Data) == 0 {
		return 0, fmt.Errorf("CurseForge has no project %q", ref)
	}
	return out.Data[0].ID, nil
}

// files lists a project's files, newest first.
func (c cfClient) files(ctx context.Context, modID int, query url.Values) ([]cfFile, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("pageSize", "50")
	var out struct {
		Data []cfFile `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/mods/%d/files?%s", modID, query.Encode()), nil, &out); err != nil {
		return nil, err
	}
	sort.SliceStable(out.Data, func(i, j int) bool { return out.Data[i].FileDate > out.Data[j].FileDate })
	return out.Data, nil
}

func (c cfClient) file(ctx context.Context, modID, fileID int) (cfFile, error) {
	var out struct {
		Data cfFile `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/mods/%d/files/%d", modID, fileID), nil, &out)
	return out.Data, err
}

func (c cfClient) filesByID(ctx context.Context, ids []int) ([]cfFile, error) {
	var out struct {
		Data []cfFile `json:"data"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/mods/files", map[string]any{"fileIds": ids}, &out)
	return out.Data, err
}

func (c cfClient) modsByID(ctx context.Context, ids []int) ([]cfMod, error) {
	var out struct {
		Data []cfMod `json:"data"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/mods", map[string]any{"modIds": ids}, &out)
	return out.Data, err
}

// CurseForge is the CurseForge mod and plugin catalogue for Minecraft.
type CurseForge struct {
	Key    string
	Client *http.Client
}

func (CurseForge) ID() string { return "curseforge" }

func (cf CurseForge) api() cfClient { return cfClient{Key: cf.Key, Client: cf.Client} }

func cfClassFor(kind string) int {
	if kind == "plugin" {
		return cfClassPlugins
	}
	return cfClassMods
}

func (cf CurseForge) Search(ctx context.Context, spec ContentSpec, query string) ([]ContentItem, error) {
	mods, err := cf.api().search(ctx, cfClassFor(spec.Kind), query, spec.Loader, 20)
	if err != nil {
		return nil, err
	}
	out := make([]ContentItem, 0, len(mods))
	for _, m := range mods {
		out = append(out, ContentItem{
			Provider: "curseforge", ExternalID: strconv.Itoa(m.ID), Name: m.Name, Slug: m.Slug,
			Summary: clip(m.Summary, 240), GameVersions: cfGameVersions(m), SourceURL: m.Links.WebsiteURL,
		})
	}
	return out, nil
}

func cfGameVersions(m cfMod) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range m.LatestFilesIndexes {
		if f.GameVersion != "" && !seen[f.GameVersion] {
			seen[f.GameVersion] = true
			out = append(out, f.GameVersion)
		}
	}
	return out
}

func (cf CurseForge) Resolve(ctx context.Context, spec ContentSpec, externalID, version string) (ContentItem, []byte, error) {
	api := cf.api()
	modID, err := api.resolveProject(ctx, cfClassFor(spec.Kind), externalID)
	if err != nil {
		return ContentItem{}, nil, err
	}
	q := url.Values{}
	if t, ok := cfLoaderTypes[spec.Loader]; ok {
		q.Set("modLoaderType", strconv.Itoa(t))
	}
	files, err := api.files(ctx, modID, q)
	if err != nil {
		return ContentItem{}, nil, err
	}
	chosen, ok := pickCFFile(files, version, false)
	if !ok {
		return ContentItem{}, nil, fmt.Errorf("no CurseForge file matches this server loader")
	}
	raw, err := fetchBinary(ctx, chosen.downloadURL(), 128<<20)
	if err != nil {
		return ContentItem{}, nil, err
	}
	var deps []string
	for _, d := range chosen.Dependencies {
		if d.RelationType == 3 && d.ModID > 0 {
			deps = append(deps, strconv.Itoa(d.ModID))
		}
	}
	return ContentItem{
		Provider: "curseforge", ExternalID: strconv.Itoa(modID), Version: chosen.DisplayName,
		Filename: chosen.FileName, GameVersions: chosen.GameVersions, Dependencies: deps, SourceURL: chosen.downloadURL(),
	}, raw, nil
}

// pickCFFile chooses a file: the one named by version (ID, display name or
// file name), else the newest release, else the newest file. Server packs
// are skipped unless asked for.
func pickCFFile(files []cfFile, version string, serverPacks bool) (cfFile, bool) {
	version = strings.TrimSpace(version)
	usable := files[:0:0]
	for _, f := range files {
		if f.IsServerPack && !serverPacks {
			continue
		}
		usable = append(usable, f)
	}
	if version != "" && version != "latest" {
		for _, f := range usable {
			if strconv.Itoa(f.ID) == version || f.DisplayName == version || f.FileName == version {
				return f, true
			}
		}
		return cfFile{}, false
	}
	for _, f := range usable {
		if f.ReleaseType == cfReleaseFile {
			return f, true
		}
	}
	if len(usable) > 0 {
		return usable[0], true
	}
	return cfFile{}, false
}

// cfManifest is manifest.json in a CurseForge modpack.
type cfManifest struct {
	Minecraft struct {
		Version    string `json:"version"`
		ModLoaders []struct {
			ID      string `json:"id"`
			Primary bool   `json:"primary"`
		} `json:"modLoaders"`
	} `json:"minecraft"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Overrides string `json:"overrides"`
	Files     []struct {
		ProjectID int  `json:"projectID"`
		FileID    int  `json:"fileID"`
		Required  bool `json:"required"`
	} `json:"files"`
}

// loader splits "forge-47.2.0" into forge and 47.2.0.
func (m cfManifest) loader() (string, string) {
	var id string
	for _, l := range m.Minecraft.ModLoaders {
		if l.Primary || id == "" {
			id = l.ID
		}
	}
	name, ver, ok := strings.Cut(strings.ToLower(id), "-")
	if !ok {
		return "", ""
	}
	switch name {
	case "forge", "neoforge", "fabric", "quilt":
		return name, ver
	}
	return "", ""
}

// nativeCurseForgePack installs a CurseForge modpack: it reads the pack's
// manifest for the Minecraft version and loader, uses the pack's server
// pack when it has one (or downloads every required mod), applies the
// overrides, then installs the loader in the server's Java image.
func nativeCurseForgePack(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	api := cfClient{Key: srv.Env[CurseForgeKeyEnv], Client: httpClientWithTimeout(time.Minute)}
	modID, err := api.resolveProject(ctx, cfClassModpacks, srv.Env["MODPACK_ID"])
	if err != nil {
		return err
	}
	files, err := api.files(ctx, modID, nil)
	if err != nil {
		return fmt.Errorf("could not list the modpack's files: %w", err)
	}
	pack, ok := pickCFFile(files, firstNonEmpty(strings.TrimSpace(srv.Env["MODPACK_VERSION"]), "latest"), false)
	if !ok {
		return fmt.Errorf("CurseForge modpack %d has no version %s", modID, srv.Env["MODPACK_VERSION"])
	}
	stage := filepath.Join(dir, ".ndl-modpack")
	if err := os.MkdirAll(stage, 0o750); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	packPath := filepath.Join(stage, "pack.zip")
	r.progress(srv.ID, "downloading", "modpack "+pack.DisplayName)
	if err := fetchFileRetry(ctx, pack.downloadURL(), packPath, 1<<30, pack.sha1(), 3); err != nil {
		return fmt.Errorf("modpack download failed: %w", err)
	}
	zr, err := zip.OpenReader(packPath)
	if err != nil {
		return fmt.Errorf("modpack is not a valid zip: %w", err)
	}
	defer zr.Close()
	var manifest cfManifest
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		raw, _ := io.ReadAll(io.LimitReader(rc, 16<<20))
		rc.Close()
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return fmt.Errorf("modpack manifest is not valid JSON")
		}
	}
	loader, loaderVersion := manifest.loader()
	if manifest.Minecraft.Version == "" || loader == "" {
		return fmt.Errorf("modpack manifest does not name a Minecraft version and a Forge, NeoForge, Fabric or Quilt loader")
	}
	if need := javaForMinecraft(manifest.Minecraft.Version); need > 0 {
		if have := javaOfImage(firstNonEmpty(srv.Image, t.DefaultImage)); have > 0 && have < need {
			return fmt.Errorf("modpack %s targets Minecraft %s, which needs Java %d; the selected runtime is Java %d. Pick the Java %d runtime and reinstall", firstNonEmpty(manifest.Name, pack.DisplayName), manifest.Minecraft.Version, need, have, need)
		}
	}

	// Client overrides first (configs, scripts), then the server pack or
	// the listed mods on top.
	overrides := firstNonEmpty(strings.Trim(manifest.Overrides, "/"), "overrides") + "/"
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, overrides) || f.FileInfo().IsDir() {
			continue
		}
		if err := writeZipFile(dir, strings.TrimPrefix(f.Name, overrides), f); err != nil {
			return err
		}
	}
	installed := 0
	if pack.ServerPackFileID > 0 {
		installed, err = installCFServerPack(ctx, r, api, srv, modID, pack.ServerPackFileID, stage, dir)
	} else {
		installed, err = installCFManifestFiles(ctx, r, api, srv, manifest, dir)
	}
	if err != nil {
		return err
	}
	envFile := fmt.Sprintf("MC_VERSION=%s\nLOADER=%s\nLOADER_VERSION=%s\n", shellQuote(manifest.Minecraft.Version), loader, shellQuote(loaderVersion))
	if err := os.WriteFile(filepath.Join(dir, ".ndl-modpack.env"), []byte(envFile), 0o640); err != nil {
		return err
	}
	r.appendLog(srv.ID, fmt.Sprintf("modpack %s %s: Minecraft %s, %s %s, %d server files", firstNonEmpty(manifest.Name, pack.DisplayName), firstNonEmpty(manifest.Version, pack.DisplayName), manifest.Minecraft.Version, loader, loaderVersion, installed))
	script := strings.ReplaceAll(loaderInstallScript, "@@LOADER@@", loader)
	if err := os.WriteFile(filepath.Join(dir, ".ndl-loader.sh"), []byte(script), 0o700); err != nil {
		return err
	}
	r.progress(srv.ID, "configuring", "installing "+loader+" "+loaderVersion)
	return r.runInstallContainer(ctx, srv, dir, firstNonEmpty(srv.Image, t.DefaultImage), "/mnt/server/.ndl-loader.sh")
}

// installCFServerPack unpacks the pack author's server pack, which leaves
// out client-only mods. A single top folder in the zip is dropped.
func installCFServerPack(ctx context.Context, r *Runtime, api cfClient, srv Server, modID, fileID int, stage, dir string) (int, error) {
	sp, err := api.file(ctx, modID, fileID)
	if err != nil {
		return 0, fmt.Errorf("could not read the server pack: %w", err)
	}
	spPath := filepath.Join(stage, "server.zip")
	r.progress(srv.ID, "downloading", "server pack "+sp.DisplayName)
	if err := fetchFileRetry(ctx, sp.downloadURL(), spPath, 4<<30, sp.sha1(), 3); err != nil {
		return 0, fmt.Errorf("server pack download failed: %w", err)
	}
	strip, count, err := zipLayout(spPath)
	if err != nil {
		return 0, err
	}
	if err := unzipStrip(spPath, dir, strip); err != nil {
		return 0, fmt.Errorf("server pack could not be unpacked: %w", err)
	}
	return count, nil
}

// zipLayout reports whether every entry sits under one top folder (1) or
// not (0), and how many files the zip holds.
func zipLayout(path string) (int, int, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return 0, 0, fmt.Errorf("server pack is not a valid zip: %w", err)
	}
	defer zr.Close()
	top := ""
	count := 0
	single := true
	for _, f := range zr.File {
		name := strings.Trim(strings.ReplaceAll(f.Name, "\\", "/"), "/")
		first, rest, nested := strings.Cut(name, "/")
		if !f.FileInfo().IsDir() {
			count++
			if !nested || rest == "" {
				single = false
			}
		}
		if top == "" {
			top = first
		} else if first != top {
			single = false
		}
	}
	if single && top != "" {
		return 1, count, nil
	}
	return 0, count, nil
}

// installCFManifestFiles downloads each required mod the manifest lists,
// with SHA-1 checks. Resource packs and shaders are client-only and skipped.
func installCFManifestFiles(ctx context.Context, r *Runtime, api cfClient, srv Server, m cfManifest, dir string) (int, error) {
	var fileIDs, modIDs []int
	for _, f := range m.Files {
		if f.Required && f.FileID > 0 {
			fileIDs = append(fileIDs, f.FileID)
			modIDs = append(modIDs, f.ProjectID)
		}
	}
	if len(fileIDs) == 0 {
		return 0, nil
	}
	files, err := api.filesByID(ctx, fileIDs)
	if err != nil {
		return 0, fmt.Errorf("could not read the modpack's file list: %w", err)
	}
	classes := map[int]int{}
	if mods, err := api.modsByID(ctx, modIDs); err == nil {
		for _, md := range mods {
			classes[md.ID] = md.ClassID
		}
	}
	done := 0
	for _, f := range files {
		folder := "mods"
		switch classes[f.ModID] {
		case cfClassResources, 6552: // resource packs, shaders
			continue
		case cfClassPlugins:
			folder = "plugins"
		}
		name := safeContentName(f.FileName)
		if name == "" {
			return done, fmt.Errorf("modpack file name %q is unsafe", f.FileName)
		}
		if err := fetchFileRetry(ctx, f.downloadURL(), filepath.Join(dir, folder, name), 512<<20, f.sha1(), 3); err != nil {
			return done, fmt.Errorf("modpack file %s failed: %w", f.FileName, err)
		}
		done++
		if done%10 == 0 || done == len(files) {
			r.progress(srv.ID, "downloading", fmt.Sprintf("modpack files %d/%d", done, len(files)))
		}
	}
	return done, nil
}

func init() {
	registerInstaller(installer{
		Name: "curseforge-modpack", Label: "CurseForge modpack", Kind: "Java", Native: nativeCurseForgePack, Script: loaderPlan,
		Update:              "Change the modpack version, then Reinstall. Mod files from the pack are replaced; worlds and configs outside the pack are kept. A rollback backup is taken first.",
		RuntimeImageInstall: true,
	})
}
