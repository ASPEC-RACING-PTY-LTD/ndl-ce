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
	script, image := installPlan(tmpl)
	if script == "" {
		return fmt.Errorf("this template has no installer")
	}
	path := filepath.Join(dir, ".ndl-install.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return err
	}
	if hasCap(tmpl.Capabilities, CapEULA) && strings.EqualFold(strings.TrimSpace(srv.Env["EULA"]), "true") {
		_ = os.WriteFile(filepath.Join(dir, "eula.txt"), []byte("eula=true\n"), 0o644)
	}
	if err := r.installNative(ctx, srv, tmpl, dir); err == nil {
		return nil
	} else if !isSkipNative(err) {
		return err
	}
	args := []string{
		"run", "--rm",
		"--name", containerName(srv.ID) + "-install",
		"-v", dir + ":/mnt/server",
		"-w", "/mnt/server",
	}
	args = append(args, r.networkArgs()...)
	args = append(args, "--security-opt", "no-new-privileges")
	args = append(args, "--entrypoint", "/bin/sh")
	for k, v := range srv.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, image, "/mnt/server/.ndl-install.sh")
	out, err := r.run(ctx, r.dockerBin(), args...)
	r.appendLog(srv.ID, string(out))
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)+" "+err.Error()))
	}
	return nil
}

func installPlan(t Template) (script, image string) {
	switch t.InstallBuiltin {
	case "paper":
		return paperInstallScript, "debian:bookworm-slim"
	case "minecraft-vanilla":
		return vanillaInstallScript, "debian:bookworm-slim"
	case "minecraft-fabric":
		return fabricInstallScript, "debian:bookworm-slim"
	case "minecraft-purpur":
		return purpurInstallScript, "debian:bookworm-slim"
	case "minecraft-velocity":
		return velocityInstallScript, "debian:bookworm-slim"
	case "mindustry":
		return mindustryInstallScript, "debian:bookworm-slim"
	case "terraria":
		return terrariaInstallScript, "debian:bookworm-slim"
	case "factorio":
		return factorioInstallScript, "debian:bookworm-slim"
	case "steamcmd":
		return steamcmdPlan(t)
	case "fivem":
		return fivemInstallScript, "alpine:3.21"
	}
	if t.InstallScript != "" && t.InstallImage != "" {
		return sanitizeImportedScript(t.InstallScript), t.InstallImage
	}
	return "", ""
}

func steamcmdPlan(t Template) (script, image string) {
	script = steamcmdInstallScript
	if extra := strings.TrimSpace(t.InstallScript); extra != "" {
		script = strings.TrimRight(script, "\n") + "\n" + extra + "\n"
	}
	return script, firstNonEmpty(t.DefaultImage, "steamcmd/steamcmd:debian")
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

const steamcmdInstallScript = `#!/bin/sh
set -e
cd /mnt/server
APP="${SRCDS_APPID:?SRCDS_APPID is required}"
USER="${STEAM_USER:-anonymous}"
PASS="${STEAM_PASS:-}"
GUARD="${STEAM_GUARD:-}"
BETA="${STEAM_BETA:-}"
if ! [ -x /usr/bin/steamcmd ] && ! command -v steamcmd >/dev/null; then
  echo "steamcmd binary is missing from the installer image" >&2
  exit 1
fi
if [ -z "$USER" ] || [ "$USER" = "anonymous" ]; then
  if [ -n "$BETA" ]; then
    steamcmd +force_install_dir /mnt/server +login anonymous +app_update "$APP" -beta "$BETA" validate +quit
  else
    steamcmd +force_install_dir /mnt/server +login anonymous +app_update "$APP" validate +quit
  fi
else
  if [ -n "$BETA" ]; then
    steamcmd +force_install_dir /mnt/server +login "$USER" "$PASS" "$GUARD" +app_update "$APP" -beta "$BETA" validate +quit
  else
    steamcmd +force_install_dir /mnt/server +login "$USER" "$PASS" "$GUARD" +app_update "$APP" validate +quit
  fi
fi
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
