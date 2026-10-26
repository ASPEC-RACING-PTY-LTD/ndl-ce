package gameserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Workshop looks up Steam Workshop items by published-file ID. Steam does not
// offer an unauthenticated full-text catalogue, so search is ID-based.
type Workshop struct {
	Client *http.Client
}

func (w Workshop) ID() string { return "workshop" }

func (w Workshop) Search(ctx context.Context, spec ContentSpec, query string) ([]ContentItem, error) {
	id := strings.TrimSpace(query)
	if id == "" || !isDigits(id) {
		return []ContentItem{}, nil
	}
	item, err := w.lookup(ctx, spec, id)
	if err != nil {
		return nil, err
	}
	return []ContentItem{item}, nil
}

func (w Workshop) Resolve(ctx context.Context, spec ContentSpec, externalID, version string) (ContentItem, []byte, error) {
	item, err := w.lookup(ctx, spec, strings.TrimSpace(externalID))
	if err != nil {
		return ContentItem{}, nil, err
	}
	if version != "" && version != "latest" {
		item.Version = version
	}
	// The server downloads a collection itself when it starts (see
	// WorkshopCollection), so only a small pointer file is stored.
	key := workshopItemKey
	if w.isCollection(item) {
		key = workshopCollectionKey
	}
	marker := []byte(key + "=" + item.ExternalID + "\n")
	item.Filename = "workshop-" + item.ExternalID + ".txt"
	return item, marker, nil
}

// Steam Workshop collections are published under their own app ID.
const workshopCollectionApp = 766

const (
	workshopItemKey       = "workshop_id"
	workshopCollectionKey = "workshop_collection"
)

func (w Workshop) isCollection(item ContentItem) bool {
	return item.Kind == "collection"
}

// WorkshopCollection finds the Workshop collection a server's content folder
// points at, so the server can be started with it. Pointer files written
// before collections were told apart only hold an ID; those are looked up
// once and, when they are a collection, rewritten as one. It returns "" when
// there is none or Steam cannot be reached.
func WorkshopCollection(ctx context.Context, h Host, serverID string, spec ContentSpec, w Workshop) string {
	dir := strings.Trim(spec.InstallDir, "/")
	list, err := h.ListDir(ctx, serverID, dir)
	if err != nil {
		return ""
	}
	for _, e := range list.Items {
		if e.Dir || !strings.HasPrefix(e.Name, "workshop-") || !strings.HasSuffix(e.Name, ".txt") {
			continue
		}
		rel := dir + "/" + e.Name
		raw, err := h.ReadFile(ctx, serverID, rel, 256)
		if err != nil {
			continue
		}
		key, id, _ := strings.Cut(strings.TrimSpace(string(raw)), "=")
		if !isDigits(id) {
			continue
		}
		switch key {
		case workshopCollectionKey:
			return id
		case workshopItemKey:
			lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			item, err := w.lookup(lookupCtx, spec, id)
			cancel()
			if err == nil && w.isCollection(item) {
				_ = h.WriteFile(ctx, serverID, rel, []byte(workshopCollectionKey+"="+id+"\n"))
				return id
			}
		}
	}
	return ""
}

func (w Workshop) lookup(ctx context.Context, spec ContentSpec, id string) (ContentItem, error) {
	if !isDigits(id) {
		return ContentItem{}, fmt.Errorf("Workshop items are looked up by numeric ID")
	}
	form := url.Values{}
	form.Set("itemcount", "1")
	form.Set("publishedfileids[0]", id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/", strings.NewReader(form.Encode()))
	if err != nil {
		return ContentItem{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "No-DAL-GameServers/1.0 (https://github.com/ASPEC-RACING-PTY-LTD/ndl-ce)")
	if err := validatePublicURL(req.URL.String()); err != nil {
		return ContentItem{}, err
	}
	client := w.Client
	if client == nil {
		client = defaultHTTPClient()
	}
	res, err := client.Do(req)
	if err != nil {
		return ContentItem{}, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return ContentItem{}, fmt.Errorf("Steam Workshop returned HTTP %d", res.StatusCode)
	}
	var parsed struct {
		Response struct {
			PublishedFileDetails []struct {
				PublishedFileID string `json:"publishedfileid"`
				Title           string `json:"title"`
				Description     string `json:"description"`
				FileURL         string `json:"file_url"`
				CreatorAppID    int    `json:"creator_app_id"`
			} `json:"publishedfiledetails"`
		} `json:"response"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return ContentItem{}, fmt.Errorf("Steam Workshop response is not valid JSON")
	}
	if len(parsed.Response.PublishedFileDetails) == 0 {
		return ContentItem{}, fmt.Errorf("Workshop item %s was not found", id)
	}
	row := parsed.Response.PublishedFileDetails[0]
	if spec.GameID != "" {
		if app, err := strconv.Atoi(spec.GameID); err == nil && row.CreatorAppID != 0 && row.CreatorAppID != app && row.CreatorAppID != workshopCollectionApp {
			return ContentItem{}, fmt.Errorf("Workshop item %s belongs to a different game", id)
		}
	}
	name := strings.TrimSpace(row.Title)
	if name == "" {
		name = "Workshop " + id
	}
	item := ContentItem{
		Provider: "workshop", ExternalID: id, Name: name, Slug: id,
		Summary: clip(row.Description, 240), SourceURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=" + id,
	}
	if row.CreatorAppID == workshopCollectionApp {
		item.Kind = "collection"
	}
	return item, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
