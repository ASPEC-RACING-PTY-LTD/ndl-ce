package gameserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var errSkipNative = errors.New("no native installer")

func isSkipNative(err error) bool {
	return errors.Is(err, errSkipNative)
}

func (r *Runtime) installNative(ctx context.Context, srv Server, tmpl Template, dir string) error {
	if r.Run != nil {
		return errSkipNative
	}
	switch tmpl.InstallBuiltin {
	case "paper":
		return r.installPaperNative(ctx, srv, dir)
	case "minecraft-vanilla":
		return r.installVanillaNative(ctx, srv, dir)
	case "minecraft-fabric":
		return r.installFabricNative(ctx, srv, dir)
	case "minecraft-purpur":
		return r.installPurpurNative(ctx, srv, dir)
	case "minecraft-velocity":
		return r.installVelocityNative(ctx, srv, dir)
	case "mindustry":
		return r.installMindustryNative(ctx, srv, dir)
	case "terraria":
		return r.installTerrariaNative(ctx, srv, dir)
	case "factorio":
		return r.installFactorioNative(ctx, srv, dir)
	case "fivem":
		return r.installFiveMNative(ctx, srv, dir)
	default:
		return errSkipNative
	}
}

func (r *Runtime) installPaperNative(ctx context.Context, srv Server, dir string) error {
	ver := firstNonEmpty(srv.Env["MC_VERSION"], "1.21.10")
	build := firstNonEmpty(srv.Env["BUILD_NUMBER"], "latest")
	jar := firstNonEmpty(srv.Env["SERVER_JARFILE"], "server.jar")
	metaURL := "https://fill.papermc.io/v3/projects/paper/versions/" + ver + "/builds/" + build
	body, _, err := fetchURL(ctx, defaultHTTPClient(), metaURL)
	if err != nil {
		return fmt.Errorf("could not resolve Paper %s build %s: %w", ver, build, err)
	}
	var parsed struct {
		ID        int `json:"id"`
		Downloads map[string]struct {
			URL string `json:"url"`
		} `json:"downloads"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("could not parse Paper metadata for %s", ver)
	}
	dl := parsed.Downloads["server:default"].URL
	if dl == "" {
		return fmt.Errorf("Paper %s has no server download", ver)
	}
	dest := filepath.Join(dir, jar)
	if err := fetchFile(ctx, dl, dest, maxBinaryBytes); err != nil {
		return fmt.Errorf("Paper download failed: %w", err)
	}
	if parsed.ID > 0 {
		build = fmt.Sprintf("%d", parsed.ID)
	}
	r.appendLog(srv.ID, "installed "+jar+" paper "+ver+" build "+build)
	return nil
}

func (r *Runtime) installMindustryNative(ctx context.Context, srv Server, dir string) error {
	ver := firstNonEmpty(srv.Env["MINDUSTRY_VERSION"], "latest")
	if ver == "latest" {
		body, _, err := fetchURL(ctx, defaultHTTPClient(), "https://api.github.com/repos/Anuken/Mindustry/releases/latest")
		if err != nil {
			return fmt.Errorf("could not resolve Mindustry release: %w", err)
		}
		var parsed struct {
			Tag string `json:"tag_name"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil || parsed.Tag == "" {
			return fmt.Errorf("could not resolve Mindustry release")
		}
		ver = parsed.Tag
	}
	url := "https://github.com/Anuken/Mindustry/releases/download/" + ver + "/server-release.jar"
	dest := filepath.Join(dir, "server-release.jar")
	if err := fetchFile(ctx, url, dest, maxBinaryBytes); err != nil {
		return fmt.Errorf("Mindustry download failed: %w", err)
	}
	r.appendLog(srv.ID, "installed Mindustry "+ver)
	return nil
}

func (r *Runtime) installFiveMNative(ctx context.Context, srv Server, dir string) error {
	art := firstNonEmpty(srv.Env["FIVEM_ARTIFACT"], "latest")
	if art == "latest" {
		body, _, err := fetchURL(ctx, downloadHTTPClient(), "https://runtime.fivem.net/artifacts/fivem/build_proot_linux/master/")
		if err != nil {
			return fmt.Errorf("could not list FiveM artifacts: %w", err)
		}
		art = pickFiveMArtifact(string(body))
		if art == "" {
			return fmt.Errorf("could not resolve a FiveM artifact")
		}
	}
	url := "https://runtime.fivem.net/artifacts/fivem/build_proot_linux/master/" + art + "/fx.tar.xz"
	archive := filepath.Join(dir, "fx.tar.xz")
	if err := fetchFile(ctx, url, archive, maxBinaryBytes); err != nil {
		return fmt.Errorf("FiveM download failed: %w", err)
	}
	cmd := exec.CommandContext(ctx, "tar", "-xJf", archive, "-C", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(archive)
		return fmt.Errorf("FiveM archive extract failed: %s", strings.TrimSpace(string(out)+" "+err.Error()))
	}
	_ = os.Remove(archive)
	if err := os.MkdirAll(filepath.Join(dir, "resources"), 0o750); err != nil {
		return err
	}
	cfg := filepath.Join(dir, "server.cfg")
	if _, err := os.Stat(cfg); err != nil {
		name := firstNonEmpty(srv.Env["SERVER_NAME"], "FiveM")
		max := firstNonEmpty(srv.Env["MAX_CLIENTS"], "32")
		lic := firstNonEmpty(srv.Env["FIVEM_LICENSE"], "changeme")
		body := fmt.Sprintf("endpoint_add_tcp \"0.0.0.0:30120\"\nendpoint_add_udp \"0.0.0.0:30120\"\nsv_hostname \"%s\"\nsv_maxclients %s\nsv_licenseKey %s\nensure mapmanager\nensure chat\nensure spawnmanager\nensure sessionmanager\n", name, max, lic)
		if err := os.WriteFile(cfg, []byte(body), 0o640); err != nil {
			return err
		}
	}
	r.appendLog(srv.ID, "installed FiveM artifact "+art)
	return nil
}

func pickFiveMArtifact(html string) string {
	reRec := regexp.MustCompile(`href=\s*"\.?/?([0-9]+-[a-f0-9]+)/fx\.tar\.xz"[^<]{0,120}is-primary`)
	if m := reRec.FindStringSubmatch(html); len(m) == 2 {
		return m[1]
	}
	re := regexp.MustCompile(`([0-9]+-[a-f0-9]+)/fx\.tar\.xz`)
	if m := re.FindStringSubmatch(html); len(m) == 2 {
		return m[1]
	}
	return ""
}
