package gameserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	// Workshop payloads are fetched by SteamCMD at server start via the
	// collection / item ID. We store a small pointer file instead of a binary.
	marker := []byte("workshop_id=" + item.ExternalID + "\n")
	item.Filename = "workshop-" + item.ExternalID + ".txt"
	return item, marker, nil
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
		if app, err := strconv.Atoi(spec.GameID); err == nil && row.CreatorAppID != 0 && row.CreatorAppID != app {
			return ContentItem{}, fmt.Errorf("Workshop item %s belongs to a different game", id)
		}
	}
	name := strings.TrimSpace(row.Title)
	if name == "" {
		name = "Workshop " + id
	}
	return ContentItem{
		Provider: "workshop", ExternalID: id, Name: name, Slug: id,
		Summary: clip(row.Description, 240), SourceURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=" + id,
	}, nil
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
