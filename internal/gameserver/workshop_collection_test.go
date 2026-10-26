package gameserver

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// steamStub answers GetPublishedFileDetails with a creator app per ID.
type steamStub map[string]int

func (s steamStub) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	app := 0
	for id, a := range s {
		if strings.Contains(string(body), "="+id) {
			app = a
		}
	}
	json := `{"response":{"publishedfiledetails":[{"publishedfileid":"x","title":"T","creator_app_id":` + strconv.Itoa(app) + `}]}}`
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(json)), Header: http.Header{}}, nil
}

func TestWorkshopCollectionFromContentFolder(t *testing.T) {
	ctx := context.Background()
	id := "8ba6c873-a1fd-4120-b306-9004faad9103"
	h := &LocalHost{RT: NewRuntime(t.TempDir())}
	addons := filepath.Join(h.RT.DataDir(id), "garrysmod", "addons")
	if err := os.MkdirAll(addons, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := ContentSpec{Provider: "workshop", InstallDir: "garrysmod/addons", GameID: "4000"}
	w := Workshop{Client: &http.Client{Transport: steamStub{"3520969473": workshopCollectionApp, "111": 4000}}}

	if got := WorkshopCollection(ctx, h, id, spec, w); got != "" {
		t.Fatalf("an empty folder has no collection: %q", got)
	}
	// A single addon is not a collection.
	_ = os.WriteFile(filepath.Join(addons, "workshop-111.txt"), []byte("workshop_id=111\n"), 0o640)
	if got := WorkshopCollection(ctx, h, id, spec, w); got != "" {
		t.Fatalf("an addon is not a collection: %q", got)
	}
	// An older pointer file is looked up, then rewritten as a collection.
	legacy := filepath.Join(addons, "workshop-3520969473.txt")
	_ = os.WriteFile(legacy, []byte("workshop_id=3520969473\n"), 0o640)
	if got := WorkshopCollection(ctx, h, id, spec, w); got != "3520969473" {
		t.Fatalf("legacy collection pointer: %q", got)
	}
	if raw, _ := os.ReadFile(legacy); string(raw) != "workshop_collection=3520969473\n" {
		t.Fatalf("pointer rewritten: %q", raw)
	}
	// Now no lookup is needed.
	offline := Workshop{Client: &http.Client{Transport: steamStub{}}}
	if got := WorkshopCollection(ctx, h, id, spec, offline); got != "3520969473" {
		t.Fatalf("collection pointer: %q", got)
	}
}

func TestWorkshopResolveMarksCollections(t *testing.T) {
	spec := ContentSpec{Provider: "workshop", InstallDir: "garrysmod/addons", GameID: "4000"}
	w := Workshop{Client: &http.Client{Transport: steamStub{"3520969473": workshopCollectionApp, "111": 4000, "222": 730}}}
	item, raw, err := w.Resolve(context.Background(), spec, "3520969473", "")
	if err != nil || item.Kind != "collection" || string(raw) != "workshop_collection=3520969473\n" {
		t.Fatalf("collection: %v %+v %q", err, item, raw)
	}
	item, raw, err = w.Resolve(context.Background(), spec, "111", "")
	if err != nil || item.Kind != "" || string(raw) != "workshop_id=111\n" {
		t.Fatalf("addon: %v %+v %q", err, item, raw)
	}
	if _, _, err := w.Resolve(context.Background(), spec, "222", ""); err == nil {
		t.Fatal("another game's item is still refused")
	}
}
