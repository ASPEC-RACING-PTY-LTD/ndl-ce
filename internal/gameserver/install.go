package gameserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (r *Runtime) Install(ctx context.Context, srv Server, tmpl Template) error {
	dir, err := r.EnsureData(srv.ID)
	if err != nil {
		return err
	}
	if err := checkInstallRequirements(tmpl, srv.Env); err != nil {
		return err
	}
	script, image := installPlan(tmpl)
	if script == "" {
		return fmt.Errorf("this template has no installer")
	}
	if inst, ok := lookupInstaller(tmpl.InstallBuiltin); ok && inst.RuntimeImageInstall && strings.TrimSpace(srv.Image) != "" {
		image = srv.Image
	}
	path := filepath.Join(dir, ".ndl-install.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return err
	}
	if hasCap(tmpl.Capabilities, CapEULA) && strings.EqualFold(strings.TrimSpace(srv.Env["EULA"]), "true") {
		_ = os.WriteFile(filepath.Join(dir, "eula.txt"), []byte("eula=true\n"), 0o644)
	}
	r.progress(srv.ID, "downloading", "installing with "+tmpl.InstallMethod())
	nativeDone := false
	if err := r.installNative(ctx, srv, tmpl, dir); err == nil {
		nativeDone = true
	} else if !isSkipNative(err) {
		return err
	}
	if !nativeDone {
		if err := r.runInstallContainer(ctx, srv, dir, image, "/mnt/server/.ndl-install.sh"); err != nil {
			return err
		}
		// SteamCMD templates may overlay extra artifacts (a mod or a
		// community server build) after the Steam depot is installed.
		if hasAddonDownloads(tmpl) && r.Run == nil {
			r.progress(srv.ID, "downloading", "installing add-on files")
			if err := nativeDownloads(r, ctx, srv, tmpl, dir); err != nil {
				return err
			}
			if extra := strings.TrimSpace(tmpl.InstallScript); extra != "" {
				r.progress(srv.ID, "configuring", "running post-install steps")
				post := "#!/bin/sh\nset -e\ncd /mnt/server\n" + extra + "\n"
				if err := os.WriteFile(filepath.Join(dir, ".ndl-postinstall.sh"), []byte(post), 0o700); err != nil {
					return err
				}
				if err := r.runInstallContainer(ctx, srv, dir, "steamcmd/steamcmd:debian", "/mnt/server/.ndl-postinstall.sh"); err != nil {
					return err
				}
			}
		}
	} else if post := postInstallScript(tmpl); post != "" {
		r.progress(srv.ID, "configuring", "running post-install steps")
		postPath := filepath.Join(dir, ".ndl-postinstall.sh")
		if err := os.WriteFile(postPath, []byte(post), 0o700); err != nil {
			return err
		}
		if err := r.runInstallContainer(ctx, srv, dir, firstNonEmpty(tmpl.InstallImage, "debian:bookworm-slim"), "/mnt/server/.ndl-postinstall.sh"); err != nil {
			return err
		}
	}
	if len(tmpl.Dependencies) > 0 {
		r.progress(srv.ID, "dependencies", "preparing runtime image with "+strings.Join(tmpl.Dependencies, " "))
		if _, err := r.EnsureRuntimeImage(ctx, tmpl.DefaultImage, tmpl.Dependencies); err != nil {
			return fmt.Errorf("runtime dependencies failed: %w", err)
		}
	}
	r.progress(srv.ID, "ready", "install finished")
	return nil
}

func (r *Runtime) runInstallContainer(ctx context.Context, srv Server, dir, image, script string) error {
	args := []string{
		"run", "--rm",
		"--name", containerName(srv.ID) + "-install",
		"-v", dir + ":/mnt/server",
		"-w", "/mnt/server",
	}
	args = append(args, r.networkArgs()...)
	args = append(args, "--security-opt", "no-new-privileges")
	args = append(args, "--entrypoint", "/bin/sh")
	for _, k := range sortedKeys(srv.Env) {
		args = append(args, "-e", k+"="+srv.Env[k])
	}
	args = append(args, image, script)
	out, err := r.run(ctx, r.dockerBin(), args...)
	r.appendLog(srv.ID, string(out))
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)+" "+err.Error()))
	}
	return nil
}

// checkInstallRequirements refuses to start an install that cannot succeed,
// for example a SteamCMD app that only downloads for an owning account.
func checkInstallRequirements(t Template, env map[string]string) error {
	for _, req := range t.RequirementsAt(StageInstall) {
		if req.Env == "" {
			continue
		}
		if strings.TrimSpace(env[req.Env]) == "" {
			return fmt.Errorf("%s is required to install: %s", req.Env, req.Label)
		}
		if req.Kind == ReqSteamAccount && strings.EqualFold(strings.TrimSpace(env[req.Env]), "anonymous") {
			return fmt.Errorf("%s must be a Steam account that owns the game, not anonymous", req.Env)
		}
	}
	return nil
}

// postInstallScript is the template snippet that runs after a native
// install, inside the install image, with the server folder at /mnt/server.
func postInstallScript(t Template) string {
	extra := strings.TrimSpace(t.InstallScript)
	if extra == "" || t.InstallBuiltin == "" || t.InstallBuiltin == "steamcmd" {
		return ""
	}
	return "#!/bin/sh\nset -e\ncd /mnt/server\n" + extra + "\n"
}

func installPlan(t Template) (script, image string) {
	if inst, ok := lookupInstaller(t.InstallBuiltin); ok && inst.Script != nil {
		script, image = inst.Script(t)
		if extra := postInstallScript(t); extra != "" && t.InstallBuiltin != "steamcmd" {
			script = strings.TrimRight(script, "\n") + "\n" + strings.TrimSpace(t.InstallScript) + "\n"
		}
		if t.InstallImage != "" && t.InstallBuiltin != "steamcmd" && image == "debian:bookworm-slim" {
			image = t.InstallImage
		}
		return script, image
	}
	if t.InstallScript != "" && t.InstallImage != "" {
		return sanitizeImportedScript(t.InstallScript), t.InstallImage
	}
	return "", ""
}

func steamcmdPlan(t Template) (script, image string) {
	platform, appConfig, beta := "", "", ""
	if t.Install != nil {
		platform, appConfig, beta = t.Install.Platform, t.Install.AppConfig, t.Install.Beta
	}
	var pre []string
	if platform == "windows" {
		pre = append(pre, "+@sSteamCmdForcePlatformType windows")
	}
	var post []string
	if appConfig != "" {
		post = append(post, "+app_set_config "+appConfig)
	}
	script = strings.NewReplacer(
		"@@PRE@@", strings.Join(pre, " "),
		"@@APPCFG@@", strings.Join(post, " "),
		"@@BETA@@", beta,
	).Replace(steamcmdInstallScript)
	if extra := strings.TrimSpace(t.InstallScript); extra != "" && !hasAddonDownloads(t) {
		script = strings.TrimRight(script, "\n") + "\n" + extra + "\n"
	}
	return script, "steamcmd/steamcmd:debian"
}

// hasAddonDownloads reports a SteamCMD template that overlays extra files
// after the depot install. Its InstallScript then runs after the overlay.
func hasAddonDownloads(t Template) bool {
	return t.InstallBuiltin == "steamcmd" && t.Install != nil && len(t.Install.Downloads) > 0
}

func sanitizeImportedScript(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasPrefix(strings.TrimSpace(s), "#!") {
		s = "#!/bin/sh\nset -e\n" + s
	}
	return s
}

const paperInstallScript = `#!/bin/sh
set -e
if ! command -v curl >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates
fi
cd /mnt/server
VER="${MC_VERSION:-1.21.10}"
BUILD="${BUILD_NUMBER:-latest}"
JAR="${SERVER_JARFILE:-server.jar}"
UA="No-DAL-GameServers/1.0"
META=$(curl -fsSL -A "$UA" "https://fill.papermc.io/v3/projects/paper/versions/${VER}/builds/${BUILD}")
URL=$(printf '%s' "$META" | tr '"' '\n' | grep -m1 '^https://fill-data.papermc.io/')
if [ -z "$URL" ]; then
  echo "could not resolve Paper download for ${VER} ${BUILD}" >&2
  echo "$META" >&2
  exit 1
fi
curl -fL -A "$UA" "$URL" -o "$JAR"
test -s "$JAR"
echo "installed ${JAR} paper ${VER} build ${BUILD}"
`

const mindustryInstallScript = `#!/bin/sh
set -e
if ! command -v curl >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates
fi
cd /mnt/server
VER="${MINDUSTRY_VERSION:-latest}"
if [ "$VER" = "latest" ]; then
  VER=$(curl -fsSL https://api.github.com/repos/Anuken/Mindustry/releases/latest | sed -n 's/.*"tag_name": "\([^"]*\)".*/\1/p' | head -n 1)
fi
if [ -z "$VER" ]; then
  echo "could not resolve Mindustry release" >&2
  exit 1
fi
curl -fL "https://github.com/Anuken/Mindustry/releases/download/${VER}/server-release.jar" -o server-release.jar
test -s server-release.jar
echo "installed Mindustry ${VER}"
`

// steamcmdInstallScript retries app_update because SteamCMD commonly fails
// the first pass with transient "App state 0x..." errors, then places the
// Steam client libraries where dedicated servers look for them
// ($HOME/.steam/sdk32 and sdk64, with HOME set to the server folder).
const steamcmdInstallScript = `#!/bin/sh
set -e
cd /mnt/server
APP="${SRCDS_APPID:?SRCDS_APPID is required}"
LOGIN_USER="${STEAM_USER:-anonymous}"
BETA="${STEAM_BETA:-@@BETA@@}"
if ! [ -x /usr/bin/steamcmd ] && ! command -v steamcmd >/dev/null; then
  echo "steamcmd binary is missing from the installer image" >&2
  exit 1
fi
if [ -z "$LOGIN_USER" ]; then
  LOGIN_USER=anonymous
fi
set -- +force_install_dir /mnt/server
if [ "$LOGIN_USER" = "anonymous" ]; then
  set -- "$@" +login anonymous
else
  set -- "$@" +login "$LOGIN_USER" "${STEAM_PASS:-}" "${STEAM_GUARD:-}"
fi
set -- "$@" @@APPCFG@@ +app_update "$APP"
if [ -n "$BETA" ]; then
  set -- "$@" -beta "$BETA"
  if [ -n "${STEAM_BETA_PASSWORD:-}" ]; then
    set -- "$@" -betapassword "$STEAM_BETA_PASSWORD"
  fi
fi
set -- "$@" validate +quit
ok=0
for attempt in 1 2 3; do
  echo "steamcmd app_update ${APP} attempt ${attempt}"
  if steamcmd @@PRE@@ "$@"; then
    if [ -n "$(ls -A /mnt/server 2>/dev/null | grep -v '^\.ndl' | grep -v '^steamapps$' | head -n 1)" ]; then
      ok=1
      break
    fi
  fi
  sleep $((attempt * 5))
done
if [ "$ok" != 1 ]; then
  echo "steamcmd could not install app ${APP} after 3 attempts" >&2
  exit 1
fi
for arch in 32 64; do
  src=""
  for cand in "$HOME/.local/share/Steam/steamcmd/linux${arch}/steamclient.so" "$HOME/.steam/steamcmd/linux${arch}/steamclient.so" "/root/.local/share/Steam/steamcmd/linux${arch}/steamclient.so"; do
    if [ -f "$cand" ]; then src="$cand"; break; fi
  done
  if [ -n "$src" ]; then
    mkdir -p "/mnt/server/.steam/sdk${arch}"
    cp -f "$src" "/mnt/server/.steam/sdk${arch}/steamclient.so"
  fi
done
echo "steamcmd installed app ${APP}"
`

const fivemInstallScript = `#!/bin/sh
set -e
cd /mnt/server
apk add --no-cache curl tar xz >/dev/null
ART="${FIVEM_ARTIFACT:-latest}"
if [ "$ART" = "latest" ]; then
  echo "FiveM artifacts require a Cfx.re recommended build URL. Using the public recommended listing."
  LIST=$(curl -fsSL https://runtime.fivem.net/artifacts/fivem/build_proot_linux/master/)
  ART=$(printf '%s' "$LIST" | tr '"' '\n' | sed -n 's#^\./\([0-9][0-9]*-[a-f0-9][a-f0-9]*\)/fx.tar.xz$#\1#p' | head -n 1)
fi
if [ -z "$ART" ]; then
  echo "could not resolve a FiveM artifact" >&2
  exit 1
fi
curl -fL "https://runtime.fivem.net/artifacts/fivem/build_proot_linux/master/${ART}/fx.tar.xz" -o fx.tar.xz
tar -xJf fx.tar.xz
rm -f fx.tar.xz
mkdir -p resources
if [ ! -f server.cfg ]; then
  printf 'endpoint_add_tcp "0.0.0.0:30120"\nendpoint_add_udp "0.0.0.0:30120"\nsv_hostname "%s"\nsv_maxclients %s\nsv_licenseKey %s\nensure mapmanager\nensure chat\nensure spawnmanager\nensure sessionmanager\n' "${SERVER_NAME:-FiveM}" "${MAX_CLIENTS:-32}" "${FIVEM_LICENSE:-changeme}" > server.cfg
fi
echo "installed FiveM artifact ${ART}"
`

const vanillaInstallScript = `#!/bin/sh
set -e
if ! command -v curl >/dev/null || ! command -v python3 >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates python3
fi
cd /mnt/server
VER="${MC_VERSION:-latest}"
JAR="${SERVER_JARFILE:-server.jar}"
UA="No-DAL-GameServers/1.0"
MANIFEST=$(curl -fsSL -A "$UA" https://piston-meta.mojang.com/mc/game/version_manifest_v2.json)
if [ "$VER" = "latest" ]; then
  VER=$(printf '%s' "$MANIFEST" | python3 -c 'import json,sys; print(json.load(sys.stdin)["latest"]["release"])')
fi
URL=$(printf '%s' "$MANIFEST" | python3 -c 'import json,sys; ver=sys.argv[1]; data=json.load(sys.stdin); print(next(v["url"] for v in data["versions"] if v["id"]==ver))' "$VER")
META=$(curl -fsSL -A "$UA" "$URL")
DL=$(printf '%s' "$META" | python3 -c 'import json,sys; print(json.load(sys.stdin)["downloads"]["server"]["url"])')
curl -fL -A "$UA" "$DL" -o "$JAR"
test -s "$JAR"
echo "installed ${JAR} vanilla ${VER}"
`

const fabricInstallScript = `#!/bin/sh
set -e
if ! command -v curl >/dev/null || ! command -v python3 >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates python3
fi
cd /mnt/server
MC="${MC_VERSION:-1.21.8}"
LOADER="${FABRIC_LOADER:-latest}"
JAR="${SERVER_JARFILE:-server.jar}"
UA="No-DAL-GameServers/1.0"
if [ "$LOADER" = "latest" ]; then
  LOADER=$(curl -fsSL -A "$UA" https://meta.fabricmc.net/v2/versions/loader | python3 -c 'import json,sys; print(next(v["version"] for v in json.load(sys.stdin) if v.get("stable")))')
fi
INSTALLER=$(curl -fsSL -A "$UA" https://meta.fabricmc.net/v2/versions/installer | python3 -c 'import json,sys; print(next(v["version"] for v in json.load(sys.stdin) if v.get("stable")))')
curl -fL -A "$UA" "https://meta.fabricmc.net/v2/versions/loader/${MC}/${LOADER}/${INSTALLER}/server/jar" -o "$JAR"
test -s "$JAR"
mkdir -p mods
echo "installed ${JAR} fabric ${MC} loader ${LOADER}"
`

const purpurInstallScript = `#!/bin/sh
set -e
if ! command -v curl >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates
fi
cd /mnt/server
VER="${MC_VERSION:-1.21.8}"
JAR="${SERVER_JARFILE:-server.jar}"
UA="No-DAL-GameServers/1.0"
curl -fL -A "$UA" "https://api.purpurmc.org/v2/purpur/${VER}/latest/download" -o "$JAR"
test -s "$JAR"
echo "installed ${JAR} purpur ${VER}"
`

const velocityInstallScript = `#!/bin/sh
set -e
if ! command -v curl >/dev/null || ! command -v python3 >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates python3
fi
cd /mnt/server
VER="${MC_VERSION:-latest}"
BUILD="${BUILD_NUMBER:-latest}"
JAR="${SERVER_JARFILE:-velocity.jar}"
UA="No-DAL-GameServers/1.0"
if [ "$VER" = "latest" ]; then
  VER=$(curl -fsSL -A "$UA" https://fill.papermc.io/v3/projects/velocity | python3 -c 'import json,sys
d=json.load(sys.stdin)["versions"]
order=["3.0.0","4.0.0","1.1.0","1.0.0"]
cands=[]
for fam in order:
  cands.extend(d.get(fam) or [])
if isinstance(d, dict):
  for fam, vs in d.items():
    if fam not in order:
      cands.extend(vs or [])
pick=next((v for v in cands if v and "SNAPSHOT" not in v), "")
print(pick)
')
fi
META=$(curl -fsSL -A "$UA" "https://fill.papermc.io/v3/projects/velocity/versions/${VER}/builds/${BUILD}")
URL=$(printf '%s' "$META" | tr '"' '\n' | grep -m1 '^https://fill-data.papermc.io/')
if [ -z "$URL" ]; then
  echo "could not resolve Velocity download for ${VER} ${BUILD}" >&2
  echo "$META" >&2
  exit 1
fi
curl -fL -A "$UA" "$URL" -o "$JAR"
test -s "$JAR"
echo "installed ${JAR} velocity ${VER} build ${BUILD}"
`

const terrariaInstallScript = `#!/bin/sh
set -e
if ! command -v curl >/dev/null || ! command -v unzip >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates unzip
fi
cd /mnt/server
VER="${TERRARIA_VERSION:-1449}"
UA="No-DAL-GameServers/1.0"
curl -fL -A "$UA" "https://terraria.org/api/download/pc-dedicated-server/terraria-server-${VER}.zip" -o terraria.zip
unzip -o terraria.zip
if [ -d "${VER}/Linux" ]; then
  cp -a "${VER}/Linux/." .
  rm -rf "$VER"
fi
rm -f terraria.zip
chmod +x TerrariaServer.bin.x86_64 2>/dev/null || true
mkdir -p Worlds
echo "installed Terraria dedicated ${VER}"
`

const factorioInstallScript = `#!/bin/sh
set -e
if ! command -v curl >/dev/null || ! command -v tar >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl ca-certificates xz-utils
fi
cd /mnt/server
VER="${FACTORIO_VERSION:-stable}"
UA="No-DAL-GameServers/1.0"
curl -fL -A "$UA" "https://factorio.com/get-download/${VER}/headless/linux64" -o factorio.tar.xz
tar -xJf factorio.tar.xz --strip-components=1
rm -f factorio.tar.xz
if [ ! -f server-settings.json ] && [ -f data/server-settings.example.json ]; then
  cp data/server-settings.example.json server-settings.json
fi
mkdir -p saves
echo "installed Factorio headless ${VER}"
`
