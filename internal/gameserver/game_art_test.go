package gameserver

import "testing"

// TestGameArtReferencesExist catches typos: every art entry must name a
// real game title or template, and IDs must be well formed.
func TestGameArtReferencesExist(t *testing.T) {
	titles := map[string]bool{}
	ids := map[string]bool{}
	for _, tmpl := range BuiltinTemplates() {
		titles[tmpl.GameTitle] = true
		ids[tmpl.ID] = true
	}
	for title, art := range gameArt {
		if !titles[title] {
			t.Errorf("gameArt has %q, which is not a GameTitle of any template", title)
		}
		if (art.SteamAppID == "") == (art.GitHub == "") {
			t.Errorf("gameArt %q must set exactly one of SteamAppID or GitHub", title)
		}
		if art.SteamAppID != "" && !steamStoreIDPattern.MatchString(art.SteamAppID) {
			t.Errorf("gameArt %q steam id %q is not numeric", title, art.SteamAppID)
		}
		if art.GitHub != "" && !githubLoginPattern.MatchString(art.GitHub) {
			t.Errorf("gameArt %q github login %q is invalid", title, art.GitHub)
		}
	}
	for id, login := range variantArt {
		if !ids[id] {
			t.Errorf("variantArt has %q, which is not a template ID", id)
		}
		if !githubLoginPattern.MatchString(login) {
			t.Errorf("variantArt %q login %q is invalid", id, login)
		}
	}
	item := catalogueItemFromTemplate(mustTemplate(t, "ndl-minecraft-paper"), "builtin", true)
	if item.LogoURL == "" || item.LogoKind != LogoIcon {
		t.Fatalf("distribution logo missing: %+v", item)
	}
}

func mustTemplate(t *testing.T, id string) Template {
	t.Helper()
	tmpl, ok := templateByID(id)
	if !ok {
		t.Fatalf("missing %s", id)
	}
	return tmpl
}
