package gameserver

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Minecraft ecosystem installers. Loader installers that must execute Java
// (Forge, NeoForge, Quilt, Spigot BuildTools) run inside the server's own
// Java image so the selected Java version builds the files. Every modded
// installer writes ndl-java-args.txt so templates share one startup line:
//
//	java -Xms128M -Xmx{{SERVER_MEMORY}}M @ndl-java-args.txt nogui
const moddedJavaStartup = "java -Xms128M -Xmx{{SERVER_MEMORY}}M -Dterminal.jline=false -Dterminal.ansi=true @ndl-java-args.txt nogui"

func init() {
	registerInstaller(installer{Name: "papermc", Label: "PaperMC API", Kind: "Java", Native: nativePaperMC, Script: paperMCPlan, Validate: validatePaperMC, Update: updateJar})
	registerInstaller(installer{Name: "minecraft-loader", Label: "Mod loader installer", Kind: "Java", Script: loaderPlan, Validate: validateLoader, Update: updateJar, RuntimeImageInstall: true})
	registerInstaller(installer{Name: "minecraft-spigot", Label: "Spigot BuildTools", Kind: "Java", Script: fixedRuntimeScript(spigotInstallScript), Update: updateJar, RuntimeImageInstall: true})
	registerInstaller(installer{Name: "minecraft-sponge", Label: "Sponge downloads API", Kind: "Java", Native: nativeSponge, Script: fixedScript(spongeInstallScript), Update: updateJar})
	registerInstaller(installer{Name: "minecraft-bedrock", Label: "Mojang Bedrock download", Kind: "Standalone", Native: nativeBedrock, Script: fixedScript(bedrockInstallScript), Update: "Reinstall downloads the selected Bedrock build. server.properties, permissions.json, allowlist.json and worlds are kept. A rollback backup is taken first."})
	registerInstaller(installer{Name: "modrinth-modpack", Label: "Modrinth modpack", Kind: "Java", Native: nativeModrinthPack, Script: loaderPlan, Update: "Change the modpack version, then Reinstall. Mod files listed by the pack are replaced; worlds and configs outside the pack overrides are kept. A rollback backup is taken first.", RuntimeImageInstall: true})
}

func fixedRuntimeScript(s string) func(Template) (string, string) {
	return func(t Template) (string, string) { return s, t.DefaultImage }
}

func validatePaperMC(t Template) error {
	if t.Install == nil {
		return fmt.Errorf("papermc template needs install.project")
	}
	switch t.Install.Project {
	case "paper", "folia", "velocity", "waterfall":
		return nil
	}
	return fmt.Errorf("papermc project %q is not supported", t.Install.Project)
}

func validateLoader(t Template) error {
	if t.Install == nil {
		return fmt.Errorf("minecraft-loader template needs install.project")
	}
	switch t.Install.Project {
	case "forge", "neoforge", "quilt", "fabric":
		return nil
	}
	return fmt.Errorf("mod loader %q is not supported", t.Install.Project)
}

var mcReleasePattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,2}$`)

// compareVersions orders dotted numeric versions. Non-numeric parts compare
// as zero so "1.21.10" sorts after "1.21.9".
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// latestPaperMCVersion picks the highest stable release version of a Fill
// project. Pre-releases, release candidates and snapshots are skipped.
func latestPaperMCVersion(ctx context.Context, project string) (string, error) {
	body, _, err := fetchURL(ctx, defaultHTTPClient(), "https://fill.papermc.io/v3/projects/"+project)
	if err != nil {
		return "", fmt.Errorf("could not list %s versions: %w", project, err)
	}
	var parsed struct {
		Versions json.RawMessage `json:"versions"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("could not parse %s versions", project)
	}
	var all []string
	var flat []string
	if json.Unmarshal(parsed.Versions, &flat) == nil {
		all = flat
	} else {
		var grouped map[string][]string
		if err := json.Unmarshal(parsed.Versions, &grouped); err != nil {
			return "", fmt.Errorf("could not parse %s versions", project)
		}
		for _, vs := range grouped {
			all = append(all, vs...)
		}
	}
	best := ""
	for _, v := range all {
		if !mcReleasePattern.MatchString(v) {
			continue
		}
		if best == "" || compareVersions(v, best) > 0 {
			best = v
		}
	}
	if best == "" {
		return "", fmt.Errorf("could not resolve a stable %s version", project)
	}
	return best, nil
}

func nativePaperMC(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	project := t.Install.Project
	ver := firstNonEmpty(strings.TrimSpace(srv.Env["MC_VERSION"]), "latest")
	build := firstNonEmpty(strings.TrimSpace(srv.Env["BUILD_NUMBER"]), "latest")
	jar := firstNonEmpty(srv.Env["SERVER_JARFILE"], "server.jar")
	if ver == "latest" {
		resolved, err := latestPaperMCVersion(ctx, project)
		if err != nil {
			return err
		}
		ver = resolved
	}
	if !safeVersionPattern.MatchString(ver) || !safeVersionPattern.MatchString(build) {
		return fmt.Errorf("version or build is not valid")
	}
	metaURL := "https://fill.papermc.io/v3/projects/" + project + "/versions/" + ver + "/builds/" + build
	body, _, err := fetchURL(ctx, defaultHTTPClient(), metaURL)
	if err != nil {
		return fmt.Errorf("could not resolve %s %s build %s: %w", project, ver, build, err)
	}
	var parsed struct {
		ID        int `json:"id"`
		Downloads map[string]struct {
			URL       string `json:"url"`
			Checksums struct {
				SHA256 string `json:"sha256"`
			} `json:"checksums"`
		} `json:"downloads"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("could not parse %s metadata for %s", project, ver)
	}
	dl := parsed.Downloads["server:default"]
	if dl.URL == "" {
		return fmt.Errorf("%s %s has no server download", project, ver)
	}
	r.progress(srv.ID, "downloading", fmt.Sprintf("%s %s build %d", project, ver, parsed.ID))
	if err := fetchFileRetry(ctx, dl.URL, filepath.Join(dir, jar), maxBinaryBytes, digest{Algo: "sha256", Hex: dl.Checksums.SHA256}, 3); err != nil {
		return fmt.Errorf("%s download failed: %w", project, err)
	}
	r.appendLog(srv.ID, fmt.Sprintf("installed %s %s %s build %d (sha256 verified)", jar, project, ver, parsed.ID))
	return nil
}

func paperMCPlan(t Template) (string, string) {
	project := "paper"
	if t.Install != nil && t.Install.Project != "" {
		project = t.Install.Project
	}
	return strings.ReplaceAll(paperMCInstallScript, "@@PROJECT@@", project), "debian:bookworm-slim"
}

const paperMCInstallScript = `#!/bin/sh
set -e
if ! command -v curl >/dev/null || ! command -v python3 >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates python3
fi
cd /mnt/server
PROJECT="@@PROJECT@@"
VER="${MC_VERSION:-latest}"
BUILD="${BUILD_NUMBER:-latest}"
JAR="${SERVER_JARFILE:-server.jar}"
UA="No-DAL-GameServers/1.0"
if [ "$VER" = "latest" ]; then
  VER=$(curl -fsSL -A "$UA" "https://fill.papermc.io/v3/projects/${PROJECT}" | python3 -c 'import json,re,sys
d=json.load(sys.stdin)["versions"]
vs=[v for g in (d.values() if isinstance(d,dict) else [d]) for v in g]
vs=[v for v in vs if re.match(r"^[0-9]+(\.[0-9]+){1,2}$", v)]
print(max(vs, key=lambda v: [int(x) for x in v.split(".")]))')
fi
META=$(curl -fsSL -A "$UA" "https://fill.papermc.io/v3/projects/${PROJECT}/versions/${VER}/builds/${BUILD}")
URL=$(printf '%s' "$META" | python3 -c 'import json,sys; print(json.load(sys.stdin)["downloads"]["server:default"]["url"])')
SUM=$(printf '%s' "$META" | python3 -c 'import json,sys; print(json.load(sys.stdin)["downloads"]["server:default"]["checksums"]["sha256"])')
curl -fL --retry 3 -A "$UA" "$URL" -o "$JAR"
echo "${SUM}  ${JAR}" | sha256sum -c -
echo "installed ${JAR} ${PROJECT} ${VER} build ${BUILD}"
`

func loaderPlan(t Template) (string, string) {
	loader := ""
	if t.Install != nil {
		loader = t.Install.Project
	}
	return strings.ReplaceAll(loaderInstallScript, "@@LOADER@@", loader), t.DefaultImage
}

// loaderInstallScript installs Forge, NeoForge, Quilt or Fabric into
// /mnt/server and writes ndl-java-args.txt. A modpack install may leave
// .ndl-modpack.env with MC_VERSION, LOADER and LOADER_VERSION.
const loaderInstallScript = `#!/bin/sh
set -e
cd /mnt/server
if ! command -v curl >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates
fi
LOADER="@@LOADER@@"
if [ -f .ndl-modpack.env ]; then
  . ./.ndl-modpack.env
fi
MC="${MC_VERSION:?MC_VERSION is required}"
LV="${LOADER_VERSION:-latest}"
UA="No-DAL-GameServers/1.0"
fetch() { curl -fL --retry 3 -A "$UA" "$1" -o "$2"; }
case "$LOADER" in
forge)
  if [ "$LV" = "latest" ] || [ "$LV" = "recommended" ]; then
    PROMOS=$(curl -fsSL -A "$UA" https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json | tr ',{}' '\n\n\n')
    PICK=$(printf '%s\n' "$PROMOS" | grep "\"${MC}-${LV}\"" | sed 's/.*: *"\([^"]*\)".*/\1/' | head -n 1)
    if [ -z "$PICK" ]; then
      PICK=$(printf '%s\n' "$PROMOS" | grep "\"${MC}-latest\"" | sed 's/.*: *"\([^"]*\)".*/\1/' | head -n 1)
    fi
    LV="$PICK"
  fi
  if [ -z "$LV" ]; then echo "Forge has no build for Minecraft ${MC}" >&2; exit 1; fi
  fetch "https://maven.minecraftforge.net/net/minecraftforge/forge/${MC}-${LV}/forge-${MC}-${LV}-installer.jar" .ndl-installer.jar
  java -jar .ndl-installer.jar --installServer
  ARGS="libraries/net/minecraftforge/forge/${MC}-${LV}/unix_args.txt"
  if [ -f "$ARGS" ]; then
    cp "$ARGS" ndl-java-args.txt
  else
    JAR=$(ls forge-${MC}-${LV}*.jar 2>/dev/null | grep -v installer | head -n 1)
    if [ -z "$JAR" ]; then echo "Forge installer produced no server jar" >&2; exit 1; fi
    printf -- '-jar %s\n' "$JAR" > ndl-java-args.txt
  fi
  ;;
neoforge)
  if [ "$LV" = "latest" ]; then
    # 1.21.1 -> 21.1, 1.21 -> 21.0; year versions 26.1 -> 26.1.0, 26.1.2 -> 26.1.2
    case "$MC" in
      1.*) KEY="${MC#1.}"; case "$KEY" in *.*) ;; *) KEY="${KEY}.0" ;; esac ;;
      *) KEY="$MC"; case "$KEY" in *.*.*) ;; *) KEY="${KEY}.0" ;; esac ;;
    esac
    LV=$(curl -fsSL -A "$UA" https://maven.neoforged.net/releases/net/neoforged/neoforge/maven-metadata.xml | grep -o '<version>[^<]*</version>' | sed 's/<[^>]*>//g' | grep "^${KEY}\." | grep -v -- '-beta' | tail -n 1)
  fi
  if [ -z "$LV" ]; then echo "NeoForge has no stable build for Minecraft ${MC}" >&2; exit 1; fi
  fetch "https://maven.neoforged.net/releases/net/neoforged/neoforge/${LV}/neoforge-${LV}-installer.jar" .ndl-installer.jar
  java -jar .ndl-installer.jar --installServer
  ARGS="libraries/net/neoforged/neoforge/${LV}/unix_args.txt"
  if [ ! -f "$ARGS" ]; then echo "NeoForge installer did not produce ${ARGS}" >&2; exit 1; fi
  cp "$ARGS" ndl-java-args.txt
  ;;
quilt)
  QI=$(curl -fsSL -A "$UA" https://maven.quiltmc.org/repository/release/org/quiltmc/quilt-installer/maven-metadata.xml | grep -o '<release>[^<]*</release>' | sed 's/<[^>]*>//g' | head -n 1)
  if [ -z "$QI" ]; then echo "could not resolve the Quilt installer" >&2; exit 1; fi
  fetch "https://maven.quiltmc.org/repository/release/org/quiltmc/quilt-installer/${QI}/quilt-installer-${QI}.jar" .ndl-installer.jar
  if [ "$LV" = "latest" ]; then
    java -jar .ndl-installer.jar install server "$MC" --download-server --install-dir=.
  else
    java -jar .ndl-installer.jar install server "$MC" "$LV" --download-server --install-dir=.
  fi
  test -f quilt-server-launch.jar
  printf -- '-jar quilt-server-launch.jar\n' > ndl-java-args.txt
  ;;
fabric)
  if [ "$LV" = "latest" ]; then
    LV=$(curl -fsSL -A "$UA" https://meta.fabricmc.net/v2/versions/loader | tr '{' '\n' | grep '"stable": *true' | sed 's/.*"version": *"\([^"]*\)".*/\1/' | head -n 1)
  fi
  FI=$(curl -fsSL -A "$UA" https://meta.fabricmc.net/v2/versions/installer | tr '{' '\n' | grep '"stable": *true' | sed 's/.*"version": *"\([^"]*\)".*/\1/' | head -n 1)
  fetch "https://meta.fabricmc.net/v2/versions/loader/${MC}/${LV}/${FI}/server/jar" fabric-server-launch.jar
  printf -- '-jar fabric-server-launch.jar\n' > ndl-java-args.txt
  ;;
*)
  echo "unknown mod loader ${LOADER}" >&2
  exit 1
  ;;
esac
rm -f .ndl-installer.jar .ndl-installer.jar.log
mkdir -p mods
echo "installed ${LOADER} ${LV} for Minecraft ${MC}"
`

const spigotInstallScript = `#!/bin/sh
set -e
if ! command -v git >/dev/null || ! command -v curl >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends git curl ca-certificates
fi
VER="${MC_VERSION:-latest}"
JAR="${SERVER_JARFILE:-server.jar}"
WORK=/tmp/ndl-buildtools
rm -rf "$WORK"
mkdir -p "$WORK"
cd "$WORK"
curl -fL --retry 3 -A "No-DAL-GameServers/1.0" https://hub.spigotmc.org/jenkins/job/BuildTools/lastSuccessfulBuild/artifact/target/BuildTools.jar -o BuildTools.jar
java -jar BuildTools.jar --rev "$VER" --output-dir /mnt/server --final-name "$JAR"
test -s "/mnt/server/${JAR}"
echo "built Spigot ${VER} as ${JAR}"
`

const spongeInstallScript = `#!/bin/sh
set -e
if ! command -v curl >/dev/null || ! command -v python3 >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates python3
fi
cd /mnt/server
VER="${SPONGE_VERSION:-recommended}"
JAR="${SERVER_JARFILE:-server.jar}"
META=$(curl -fsSL "https://dl-api.spongepowered.org/v1/org.spongepowered/spongevanilla/downloads/${VER}")
URL=$(printf '%s' "$META" | python3 -c 'import json,sys; print(json.load(sys.stdin)["artifacts"][""]["url"])')
SUM=$(printf '%s' "$META" | python3 -c 'import json,sys; print(json.load(sys.stdin)["artifacts"][""]["sha1"])')
curl -fL --retry 3 "$URL" -o "$JAR"
echo "${SUM}  ${JAR}" | sha1sum -c -
echo "installed SpongeVanilla ${VER}"
`

func nativeSponge(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	ver := firstNonEmpty(strings.TrimSpace(srv.Env["SPONGE_VERSION"]), "recommended")
	jar := firstNonEmpty(srv.Env["SERVER_JARFILE"], "server.jar")
	if !safeVersionPattern.MatchString(ver) {
		return fmt.Errorf("Sponge version %q is not valid", ver)
	}
	body, _, err := fetchURL(ctx, defaultHTTPClient(), "https://dl-api.spongepowered.org/v1/org.spongepowered/spongevanilla/downloads/"+url.PathEscape(ver))
	if err != nil {
		return fmt.Errorf("could not resolve SpongeVanilla %s: %w", ver, err)
	}
	var meta struct {
		Version   string `json:"version"`
		Artifacts map[string]struct {
			URL  string `json:"url"`
			SHA1 string `json:"sha1"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return fmt.Errorf("SpongeVanilla metadata is not valid JSON")
	}
	art := meta.Artifacts[""]
	if art.URL == "" {
		return fmt.Errorf("SpongeVanilla %s has no server artifact", ver)
	}
	if err := fetchFileRetry(ctx, art.URL, filepath.Join(dir, jar), maxBinaryBytes, digest{Algo: "sha1", Hex: art.SHA1}, 3); err != nil {
		return fmt.Errorf("SpongeVanilla download failed: %w", err)
	}
	r.appendLog(srv.ID, "installed SpongeVanilla "+firstNonEmpty(meta.Version, ver)+" (sha1 verified)")
	return nil
}

// bedrockKeep are files a Bedrock update must never overwrite.
var bedrockKeep = map[string]bool{"server.properties": true, "permissions.json": true, "allowlist.json": true, "whitelist.json": true}

func nativeBedrock(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	ver := firstNonEmpty(strings.TrimSpace(srv.Env["BEDROCK_VERSION"]), "latest")
	var dl string
	if ver == "latest" {
		body, _, err := fetchURL(ctx, defaultHTTPClient(), "https://net-secondary.web.minecraft-services.net/api/v1.0/download/links")
		if err != nil {
			return fmt.Errorf("could not read the Bedrock download links: %w", err)
		}
		var parsed struct {
			Result struct {
				Links []struct {
					Type string `json:"downloadType"`
					URL  string `json:"downloadUrl"`
				} `json:"links"`
			} `json:"result"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return fmt.Errorf("Bedrock download links are not valid JSON")
		}
		for _, l := range parsed.Result.Links {
			if l.Type == "serverBedrockLinux" {
				dl = l.URL
			}
		}
		if dl == "" {
			return fmt.Errorf("Bedrock download links have no Linux server")
		}
	} else {
		if !regexp.MustCompile(`^[0-9]+(\.[0-9]+){2,3}$`).MatchString(ver) {
			return fmt.Errorf("Bedrock version %q is not valid", ver)
		}
		dl = "https://www.minecraft.net/bedrockdedicatedserver/bin-linux/bedrock-server-" + ver + ".zip"
	}
	archive := filepath.Join(dir, ".ndl-bedrock.zip")
	r.progress(srv.ID, "downloading", "fetching "+dl)
	if err := fetchFileRetry(ctx, dl, archive, maxBinaryBytes, digest{}, 3); err != nil {
		return fmt.Errorf("Bedrock download failed: %w", err)
	}
	defer os.Remove(archive)
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("Bedrock archive is not a zip: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel, err := cleanArchiveRel(f.Name)
		if err != nil {
			return err
		}
		if bedrockKeep[rel] {
			if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
				continue
			}
		}
		if err := writeZipFile(dir, rel, f); err != nil {
			return err
		}
	}
	_ = os.Chmod(filepath.Join(dir, "bedrock_server"), 0o755)
	r.appendLog(srv.ID, "installed Bedrock dedicated server from "+dl)
	return nil
}

const bedrockInstallScript = `#!/bin/sh
set -e
if ! command -v curl >/dev/null || ! command -v unzip >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates unzip
fi
cd /mnt/server
VER="${BEDROCK_VERSION:-latest}"
if [ "$VER" = "latest" ]; then
  URL=$(curl -fsSL https://net-secondary.web.minecraft-services.net/api/v1.0/download/links | tr '{' '\n' | grep serverBedrockLinux | sed 's/.*"downloadUrl": *"\([^"]*\)".*/\1/' | head -n 1)
else
  URL="https://www.minecraft.net/bedrockdedicatedserver/bin-linux/bedrock-server-${VER}.zip"
fi
curl -fL --retry 3 "$URL" -o .ndl-bedrock.zip
for f in server.properties permissions.json allowlist.json; do [ -f "$f" ] && cp "$f" "$f.ndl-keep"; done
unzip -o .ndl-bedrock.zip
for f in server.properties permissions.json allowlist.json; do [ -f "$f.ndl-keep" ] && mv "$f.ndl-keep" "$f"; done
rm -f .ndl-bedrock.zip
chmod +x bedrock_server
echo "installed Bedrock from ${URL}"
`

type mrpackIndex struct {
	Game         string            `json:"game"`
	Name         string            `json:"name"`
	VersionID    string            `json:"versionId"`
	Dependencies map[string]string `json:"dependencies"`
	Files        []struct {
		Path      string            `json:"path"`
		Hashes    map[string]string `json:"hashes"`
		Env       map[string]string `json:"env"`
		Downloads []string          `json:"downloads"`
		FileSize  int64             `json:"fileSize"`
	} `json:"files"`
}

var modrinthSlugPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{2,64}$`)

// nativeModrinthPack installs a Modrinth .mrpack for a server: it downloads
// every file the pack marks as needed on the server with sha512 checks,
// applies overrides and server-overrides, then runs the matching loader
// installer in the server's Java image.
func nativeModrinthPack(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	slugID := strings.TrimSpace(srv.Env["MODPACK_ID"])
	if !modrinthSlugPattern.MatchString(slugID) {
		return fmt.Errorf("Modrinth modpack ID %q is not a valid project slug or ID", slugID)
	}
	want := firstNonEmpty(strings.TrimSpace(srv.Env["MODPACK_VERSION"]), "latest")
	body, _, err := fetchURL(ctx, defaultHTTPClient(), "https://api.modrinth.com/v2/project/"+url.PathEscape(slugID)+"/version")
	if err != nil {
		return fmt.Errorf("could not list Modrinth versions for %s: %w", slugID, err)
	}
	var versions []struct {
		ID            string `json:"id"`
		VersionNumber string `json:"version_number"`
		VersionType   string `json:"version_type"`
		Files         []struct {
			URL      string            `json:"url"`
			Filename string            `json:"filename"`
			Primary  bool              `json:"primary"`
			Hashes   map[string]string `json:"hashes"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &versions); err != nil || len(versions) == 0 {
		return fmt.Errorf("Modrinth project %s has no versions", slugID)
	}
	chosen := -1
	for i, v := range versions {
		if want == "latest" && v.VersionType == "release" {
			chosen = i
			break
		}
		if v.ID == want || v.VersionNumber == want {
			chosen = i
			break
		}
	}
	if chosen < 0 {
		if want != "latest" {
			return fmt.Errorf("Modrinth project %s has no version %s", slugID, want)
		}
		chosen = 0
	}
	ver := versions[chosen]
	var packURL string
	var packHash digest
	for _, f := range ver.Files {
		if strings.HasSuffix(f.Filename, ".mrpack") && (f.Primary || packURL == "") {
			packURL = f.URL
			packHash = digest{Algo: "sha512", Hex: f.Hashes["sha512"]}
		}
	}
	if packURL == "" {
		return fmt.Errorf("Modrinth version %s is not a modpack (.mrpack)", ver.VersionNumber)
	}
	stage := filepath.Join(dir, ".ndl-modpack")
	if err := os.MkdirAll(stage, 0o750); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	packPath := filepath.Join(stage, "pack.mrpack")
	r.progress(srv.ID, "downloading", "modpack "+slugID+" "+ver.VersionNumber)
	if err := fetchFileRetry(ctx, packURL, packPath, 512<<20, packHash, 3); err != nil {
		return fmt.Errorf("modpack download failed: %w", err)
	}
	zr, err := zip.OpenReader(packPath)
	if err != nil {
		return fmt.Errorf("modpack is not a valid mrpack: %w", err)
	}
	defer zr.Close()
	var index mrpackIndex
	for _, f := range zr.File {
		if f.Name == "modrinth.index.json" {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			raw, _ := io.ReadAll(io.LimitReader(rc, 16<<20))
			rc.Close()
			if err := json.Unmarshal(raw, &index); err != nil {
				return fmt.Errorf("modpack index is not valid JSON")
			}
		}
	}
	if index.Game != "minecraft" || index.Dependencies["minecraft"] == "" {
		return fmt.Errorf("modpack index does not describe a Minecraft pack")
	}
	total := 0
	for _, f := range index.Files {
		if f.Env["server"] == "unsupported" {
			continue
		}
		total++
	}
	done := 0
	for _, f := range index.Files {
		if f.Env["server"] == "unsupported" {
			continue
		}
		rel, err := cleanArchiveRel(f.Path)
		if err != nil || rel == "." {
			return fmt.Errorf("modpack file path %q is unsafe", f.Path)
		}
		if len(f.Downloads) == 0 {
			return fmt.Errorf("modpack file %s has no download", f.Path)
		}
		want := digest{Algo: "sha512", Hex: f.Hashes["sha512"]}
		if want.Hex == "" {
			want = digest{Algo: "sha1", Hex: f.Hashes["sha1"]}
		}
		var last error
		for _, src := range f.Downloads {
			if last = fetchFileRetry(ctx, src, filepath.Join(dir, filepath.FromSlash(rel)), 512<<20, want, 2); last == nil {
				break
			}
		}
		if last != nil {
			return fmt.Errorf("modpack file %s failed: %w", f.Path, last)
		}
		done++
		if done%10 == 0 || done == total {
			r.progress(srv.ID, "downloading", fmt.Sprintf("modpack files %d/%d", done, total))
		}
	}
	for _, prefix := range []string{"overrides/", "server-overrides/"} {
		for _, f := range zr.File {
			if !strings.HasPrefix(f.Name, prefix) || f.FileInfo().IsDir() {
				continue
			}
			if err := writeZipFile(dir, strings.TrimPrefix(f.Name, prefix), f); err != nil {
				return err
			}
		}
	}
	loader, loaderVersion := "", ""
	for _, key := range []string{"fabric-loader", "quilt-loader", "forge", "neoforge"} {
		if v := index.Dependencies[key]; v != "" {
			loader, loaderVersion = strings.TrimSuffix(key, "-loader"), v
		}
	}
	if loader == "" {
		return fmt.Errorf("modpack does not declare a supported loader")
	}
	if need := javaForMinecraft(index.Dependencies["minecraft"]); need > 0 {
		if have := javaOfImage(firstNonEmpty(srv.Image, t.DefaultImage)); have > 0 && have < need {
			return fmt.Errorf("modpack %s targets Minecraft %s, which needs Java %d; the selected runtime is Java %d. Pick the Java %d runtime and reinstall", firstNonEmpty(index.Name, slugID), index.Dependencies["minecraft"], need, have, need)
		}
	}
	envFile := fmt.Sprintf("MC_VERSION=%s\nLOADER=%s\nLOADER_VERSION=%s\n", shellQuote(index.Dependencies["minecraft"]), loader, shellQuote(loaderVersion))
	if err := os.WriteFile(filepath.Join(dir, ".ndl-modpack.env"), []byte(envFile), 0o640); err != nil {
		return err
	}
	r.appendLog(srv.ID, fmt.Sprintf("modpack %s %s: Minecraft %s, %s %s, %d server files", firstNonEmpty(index.Name, slugID), firstNonEmpty(index.VersionID, ver.VersionNumber), index.Dependencies["minecraft"], loader, loaderVersion, done))
	script := strings.ReplaceAll(loaderInstallScript, "@@LOADER@@", loader)
	if err := os.WriteFile(filepath.Join(dir, ".ndl-loader.sh"), []byte(script), 0o700); err != nil {
		return err
	}
	r.progress(srv.ID, "configuring", "installing "+loader+" "+loaderVersion)
	return r.runInstallContainer(ctx, srv, dir, firstNonEmpty(srv.Image, t.DefaultImage), "/mnt/server/.ndl-loader.sh")
}

// javaImages is the shared Java runtime choice for Minecraft templates.
func javaImages() map[string]string {
	return map[string]string{
		"Java 8":  "eclipse-temurin:8-jre",
		"Java 11": "eclipse-temurin:11-jre",
		"Java 17": "eclipse-temurin:17-jre",
		"Java 21": "eclipse-temurin:21-jre",
		"Java 25": "eclipse-temurin:25-jre",
	}
}

// JavaImageLabels lists the Java choices in ascending order.
func JavaImageLabels() []string {
	out := make([]string, 0, 5)
	for k := range javaImages() {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.TrimPrefix(out[i], "Java "))
		b, _ := strconv.Atoi(strings.TrimPrefix(out[j], "Java "))
		return a < b
	})
	return out
}

// javaForMinecraft is the minimum Java release a Minecraft version needs
// (Mojang raised it at 1.17, 1.18, 1.20.5 and 26.1).
func javaForMinecraft(mc string) int {
	parts := strings.Split(mc, ".")
	if len(parts) < 2 {
		return 0
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0
	}
	patch := 0
	if len(parts) > 2 {
		patch, _ = strconv.Atoi(parts[2])
	}
	switch {
	case major >= 26:
		return 25
	case major != 1:
		return 0
	case minor > 20 || (minor == 20 && patch >= 5):
		return 21
	case minor >= 18:
		return 17
	case minor == 17:
		return 16
	default:
		return 8
	}
}

// javaOfImage reads the Java release from an eclipse-temurin tag.
func javaOfImage(image string) int {
	if !strings.HasPrefix(image, "eclipse-temurin:") {
		return 0
	}
	tag := strings.TrimPrefix(image, "eclipse-temurin:")
	n, err := strconv.Atoi(strings.SplitN(tag, "-", 2)[0])
	if err != nil {
		return 0
	}
	return n
}
