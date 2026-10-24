package gameserver

import (
	"archive/zip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPickCFFilePrefersNewestRelease(t *testing.T) {
	files := []cfFile{
		{ID: 5, DisplayName: "Pack 2.1 beta", ReleaseType: 2},
		{ID: 4, DisplayName: "Pack 2.0 server", ReleaseType: 1, IsServerPack: true},
		{ID: 3, DisplayName: "Pack 2.0", ReleaseType: 1},
		{ID: 2, DisplayName: "Pack 1.0", ReleaseType: 1},
	}
	if f, ok := pickCFFile(files, "latest", false); !ok || f.ID != 3 {
		t.Fatalf("latest must be the newest release that is not a server pack: %+v", f)
	}
	if f, ok := pickCFFile(files, "Pack 1.0", false); !ok || f.ID != 2 {
		t.Fatalf("a named version must be found: %+v", f)
	}
	if f, ok := pickCFFile(files, "5", false); !ok || f.ID != 5 {
		t.Fatalf("a file ID must be found: %+v", f)
	}
	if _, ok := pickCFFile(files, "Pack 9", false); ok {
		t.Fatal("an unknown version must not fall back to another file")
	}
}

func TestCFManifestLoader(t *testing.T) {
	var m cfManifest
	m.Minecraft.ModLoaders = []struct {
		ID      string `json:"id"`
		Primary bool   `json:"primary"`
	}{{ID: "fabric-0.15.0"}, {ID: "neoforge-21.1.77", Primary: true}}
	if l, v := m.loader(); l != "neoforge" || v != "21.1.77" {
		t.Fatalf("primary loader: %s %s", l, v)
	}
	m.Minecraft.ModLoaders = m.Minecraft.ModLoaders[:0]
	if l, _ := m.loader(); l != "" {
		t.Fatal("no loader must be reported as none")
	}
}

func TestCFDownloadURLFallsBackToCDN(t *testing.T) {
	f := cfFile{ID: 4712345, FileName: "Some Mod 1.2.jar"}
	if got := f.downloadURL(); got != "https://edge.forgecdn.net/files/4712/345/Some%20Mod%201.2.jar" {
		t.Fatalf("CDN fallback: %s", got)
	}
	f.DownloadURL = "https://example.com/a.jar"
	if f.downloadURL() != "https://example.com/a.jar" {
		t.Fatal("the API download URL wins")
	}
}

func TestZipLayoutDetectsOneTopFolder(t *testing.T) {
	write := func(names ...string) string {
		p := filepath.Join(t.TempDir(), "x.zip")
		out, _ := os.Create(p)
		zw := zip.NewWriter(out)
		for _, n := range names {
			w, _ := zw.Create(n)
			_, _ = w.Write([]byte("x"))
		}
		_ = zw.Close()
		_ = out.Close()
		return p
	}
	if strip, n, err := zipLayout(write("ATM9 Server/mods/a.jar", "ATM9 Server/startserver.sh")); err != nil || strip != 1 || n != 2 {
		t.Fatalf("one top folder: %d %d %v", strip, n, err)
	}
	if strip, _, _ := zipLayout(write("mods/a.jar", "server.properties")); strip != 0 {
		t.Fatal("files at the top must not be stripped")
	}
}

func TestJavaImageLabelFor(t *testing.T) {
	cases := map[string]string{"1.12.2": "Java 8", "1.18.2": "Java 17", "1.20.1": "Java 17", "1.20.6": "Java 21", "1.21.1": "Java 21", "26.1": "Java 25", "": ""}
	for mc, want := range cases {
		if got := JavaImageLabelFor(mc); got != want {
			t.Fatalf("%s: got %q want %q", mc, got, want)
		}
	}
	for _, label := range []string{"Java 8", "Java 17", "Java 21", "Java 25"} {
		if _, ok := javaImages()[label]; !ok {
			t.Fatalf("%s must be a runtime the modpack templates offer", label)
		}
	}
}

func TestCurseForgeClientSendsKeyAndExplainsRejection(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		if gotKey != "good" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":12,"name":"All the Mods 9","slug":"all-the-mods-9","downloadCount":5,"logo":{"thumbnailUrl":"https://x/y.png"},"latestFilesIndexes":[{"gameVersion":"1.20.1","modLoader":1}]}]}`))
	}))
	defer srv.Close()
	old := curseForgeAPI
	curseForgeAPI = srv.URL
	defer func() { curseForgeAPI = old }()

	packs, err := SearchModpacks(context.Background(), "curseforge", "atm", "good")
	if err != nil || len(packs) != 1 || packs[0].ID != "12" || packs[0].IconURL == "" || len(packs[0].Loaders) != 1 || packs[0].Loaders[0] != "forge" {
		t.Fatalf("search: %+v %v", packs, err)
	}
	if gotKey != "good" {
		t.Fatal("the API key must be sent")
	}
	if _, err := SearchModpacks(context.Background(), "curseforge", "atm", "bad"); err == nil || !strings.Contains(err.Error(), "rejected the API key") {
		t.Fatalf("a bad key must be explained: %v", err)
	}
	if _, err := SearchModpacks(context.Background(), "curseforge", "atm", ""); err != ErrNoCurseForgeKey {
		t.Fatalf("no key must say how to add one: %v", err)
	}
}
