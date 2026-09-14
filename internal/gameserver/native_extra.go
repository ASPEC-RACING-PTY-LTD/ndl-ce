package gameserver

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (r *Runtime) installVanillaNative(ctx context.Context, srv Server, dir string) error {
	ver := firstNonEmpty(srv.Env["MC_VERSION"], "latest")
	jar := firstNonEmpty(srv.Env["SERVER_JARFILE"], "server.jar")
	body, _, err := fetchURL(ctx, defaultHTTPClient(), "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json")
	if err != nil {
		return fmt.Errorf("could not load the Minecraft version manifest: %w", err)
	}
	var manifest struct {
		Latest struct {
			Release string `json:"release"`
		} `json:"latest"`
		Versions []struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return fmt.Errorf("Minecraft version manifest is not valid JSON")
	}
	if ver == "latest" {
		ver = manifest.Latest.Release
	}
	var metaURL string
	for _, item := range manifest.Versions {
		if item.ID == ver {
			metaURL = item.URL
			break
		}
	}
	if metaURL == "" {
		return fmt.Errorf("Minecraft version %s was not found", ver)
	}
	meta, _, err := fetchURL(ctx, defaultHTTPClient(), metaURL)
	if err != nil {
		return fmt.Errorf("could not load Minecraft %s metadata: %w", ver, err)
	}
	var parsed struct {
		Downloads map[string]struct {
			URL string `json:"url"`
		} `json:"downloads"`
	}
	if err := json.Unmarshal(meta, &parsed); err != nil {
		return fmt.Errorf("Minecraft %s metadata is not valid JSON", ver)
	}
	dl := parsed.Downloads["server"].URL
	if dl == "" {
		return fmt.Errorf("Minecraft %s has no server download", ver)
	}
	if err := fetchFile(ctx, dl, filepath.Join(dir, jar), maxBinaryBytes); err != nil {
		return fmt.Errorf("vanilla download failed: %w", err)
	}
	r.appendLog(srv.ID, "installed "+jar+" vanilla "+ver)
	return nil
}

func (r *Runtime) installFabricNative(ctx context.Context, srv Server, dir string) error {
	mc := firstNonEmpty(srv.Env["MC_VERSION"], "1.21.8")
	loader := firstNonEmpty(srv.Env["FABRIC_LOADER"], "latest")
	jar := firstNonEmpty(srv.Env["SERVER_JARFILE"], "server.jar")
	if loader == "latest" {
		body, _, err := fetchURL(ctx, defaultHTTPClient(), "https://meta.fabricmc.net/v2/versions/loader")
		if err != nil {
			return fmt.Errorf("could not list Fabric loaders: %w", err)
		}
		var vers []struct {
			Version string `json:"version"`
			Stable  bool   `json:"stable"`
		}
		if err := json.Unmarshal(body, &vers); err != nil {
			return fmt.Errorf("Fabric loader list is not valid JSON")
		}
		for _, item := range vers {
			if item.Stable && item.Version != "" {
				loader = item.Version
				break
			}
		}
	}
	instBody, _, err := fetchURL(ctx, defaultHTTPClient(), "https://meta.fabricmc.net/v2/versions/installer")
	if err != nil {
		return fmt.Errorf("could not list Fabric installers: %w", err)
	}
	var installers []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := json.Unmarshal(instBody, &installers); err != nil {
		return fmt.Errorf("Fabric installer list is not valid JSON")
	}
	installer := ""
	for _, item := range installers {
		if item.Stable && item.Version != "" {
			installer = item.Version
			break
		}
	}
	if loader == "" || loader == "latest" || installer == "" {
		return fmt.Errorf("could not resolve a stable Fabric loader")
	}
	url := "https://meta.fabricmc.net/v2/versions/loader/" + mc + "/" + loader + "/" + installer + "/server/jar"
	if err := fetchFile(ctx, url, filepath.Join(dir, jar), maxBinaryBytes); err != nil {
		return fmt.Errorf("Fabric download failed: %w", err)
	}
	_ = os.MkdirAll(filepath.Join(dir, "mods"), 0o750)
	r.appendLog(srv.ID, "installed "+jar+" fabric "+mc+" loader "+loader)
	return nil
}

func (r *Runtime) installPurpurNative(ctx context.Context, srv Server, dir string) error {
	ver := firstNonEmpty(srv.Env["MC_VERSION"], "1.21.8")
	jar := firstNonEmpty(srv.Env["SERVER_JARFILE"], "server.jar")
	url := "https://api.purpurmc.org/v2/purpur/" + ver + "/latest/download"
	if err := fetchFile(ctx, url, filepath.Join(dir, jar), maxBinaryBytes); err != nil {
		return fmt.Errorf("Purpur download failed: %w", err)
	}
	r.appendLog(srv.ID, "installed "+jar+" purpur "+ver)
	return nil
}

func (r *Runtime) installVelocityNative(ctx context.Context, srv Server, dir string) error {
	ver := firstNonEmpty(srv.Env["MC_VERSION"], "latest")
	build := firstNonEmpty(srv.Env["BUILD_NUMBER"], "latest")
	jar := firstNonEmpty(srv.Env["SERVER_JARFILE"], "velocity.jar")
	if ver == "latest" {
		resolved, err := latestVelocityVersion(ctx)
		if err != nil {
			return err
		}
		ver = resolved
	}
	metaURL := "https://fill.papermc.io/v3/projects/velocity/versions/" + ver + "/builds/" + build
	body, _, err := fetchURL(ctx, defaultHTTPClient(), metaURL)
	if err != nil {
		return fmt.Errorf("could not resolve Velocity %s build %s: %w", ver, build, err)
	}
	var parsed struct {
		ID        int `json:"id"`
		Downloads map[string]struct {
			URL string `json:"url"`
		} `json:"downloads"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("could not parse Velocity metadata for %s", ver)
	}
	dl := parsed.Downloads["server:default"].URL
	if dl == "" {
		return fmt.Errorf("Velocity %s has no server download", ver)
	}
	if err := fetchFile(ctx, dl, filepath.Join(dir, jar), maxBinaryBytes); err != nil {
		return fmt.Errorf("Velocity download failed: %w", err)
	}
	if parsed.ID > 0 {
		build = fmt.Sprintf("%d", parsed.ID)
	}
	r.appendLog(srv.ID, "installed "+jar+" velocity "+ver+" build "+build)
	return nil
}

func (r *Runtime) installTerrariaNative(ctx context.Context, srv Server, dir string) error {
	ver := firstNonEmpty(srv.Env["TERRARIA_VERSION"], "1449")
	archive := filepath.Join(dir, "terraria.zip")
	url := "https://terraria.org/api/download/pc-dedicated-server/terraria-server-" + ver + ".zip"
	if err := fetchFile(ctx, url, archive, maxBinaryBytes); err != nil {
		return fmt.Errorf("Terraria download failed: %w", err)
	}
	if err := unzipLinuxDedicated(archive, dir, ver); err != nil {
		_ = os.Remove(archive)
		return err
	}
	_ = os.Remove(archive)
	_ = os.MkdirAll(filepath.Join(dir, "Worlds"), 0o750)
	bin := filepath.Join(dir, "TerrariaServer.bin.x86_64")
	_ = os.Chmod(bin, 0o755)
	r.appendLog(srv.ID, "installed Terraria dedicated "+ver)
	return nil
}

func (r *Runtime) installFactorioNative(ctx context.Context, srv Server, dir string) error {
	ver := firstNonEmpty(srv.Env["FACTORIO_VERSION"], "stable")
	archive := filepath.Join(dir, "factorio.tar.xz")
	url := "https://factorio.com/get-download/" + ver + "/headless/linux64"
	if err := fetchFile(ctx, url, archive, maxBinaryBytes); err != nil {
		return fmt.Errorf("Factorio download failed: %w", err)
	}
	cmd := exec.CommandContext(ctx, "tar", "-xJf", archive, "-C", dir, "--strip-components=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(archive)
		return fmt.Errorf("Factorio archive extract failed: %s", strings.TrimSpace(string(out)+" "+err.Error()))
	}
	_ = os.Remove(archive)
	example := filepath.Join(dir, "data", "server-settings.example.json")
	settings := filepath.Join(dir, "server-settings.json")
	if _, err := os.Stat(settings); err != nil {
		if raw, readErr := os.ReadFile(example); readErr == nil {
			_ = os.WriteFile(settings, raw, 0o640)
		}
	}
	_ = os.MkdirAll(filepath.Join(dir, "saves"), 0o750)
	r.appendLog(srv.ID, "installed Factorio headless "+ver)
	return nil
}

func latestVelocityVersion(ctx context.Context) (string, error) {
	body, _, err := fetchURL(ctx, defaultHTTPClient(), "https://fill.papermc.io/v3/projects/velocity")
	if err != nil {
		return "", fmt.Errorf("could not list Velocity versions: %w", err)
	}
	var parsed struct {
		Versions json.RawMessage `json:"versions"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("could not parse Velocity versions")
	}
	var list []string
	if err := json.Unmarshal(parsed.Versions, &list); err == nil && len(list) > 0 {
		if pick := firstStableVersion(list); pick != "" {
			return pick, nil
		}
	}
	var grouped map[string][]string
	if err := json.Unmarshal(parsed.Versions, &grouped); err != nil || len(grouped) == 0 {
		return "", fmt.Errorf("could not resolve a Velocity version")
	}
	var cands []string
	for _, fam := range []string{"3.0.0", "4.0.0", "1.1.0", "1.0.0"} {
		cands = append(cands, grouped[fam]...)
	}
	for fam, vs := range grouped {
		if fam == "3.0.0" || fam == "4.0.0" || fam == "1.1.0" || fam == "1.0.0" {
			continue
		}
		cands = append(cands, vs...)
	}
	if pick := firstStableVersion(cands); pick != "" {
		return pick, nil
	}
	return "", fmt.Errorf("could not resolve a Velocity version")
}

func firstStableVersion(vers []string) string {
	for _, v := range vers {
		if strings.TrimSpace(v) != "" && !strings.Contains(v, "SNAPSHOT") {
			return v
		}
	}
	for _, v := range vers {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func unzipLinuxDedicated(zipPath, dest, ver string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("Terraria archive is not a zip: %w", err)
	}
	defer r.Close()
	prefix := ver + "/Linux/"
	copied := 0
	for _, f := range r.File {
		name := f.Name
		rel := name
		switch {
		case strings.HasPrefix(name, prefix):
			rel = strings.TrimPrefix(name, prefix)
		case strings.Contains(name, "/Linux/"):
			rel = name[strings.Index(name, "/Linux/")+len("/Linux/"):]
		default:
			continue
		}
		if rel == "" || strings.HasSuffix(rel, "/") {
			continue
		}
		if err := writeZipFile(dest, rel, f); err != nil {
			return err
		}
		copied++
	}
	if copied == 0 {
		return fmt.Errorf("Terraria archive did not contain a Linux dedicated server")
	}
	return nil
}

func writeZipFile(dest, rel string, f *zip.File) error {
	clean := filepath.Clean(rel)
	if strings.HasPrefix(clean, "..") {
		return fmt.Errorf("Terraria archive path is unsafe")
	}
	outPath := filepath.Join(dest, clean)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o750); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, rc)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
