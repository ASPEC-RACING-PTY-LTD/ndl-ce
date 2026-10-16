#!/usr/bin/env bash
# Build changed parts of NDL-CE and publish them as a signed live update.
#
#   scripts/ndl-live-publish.sh [ui] [control] [agent] [--notes "what changed"]
#
# With no component names it publishes all three. The bundle is signed with
# NDL_LIVE_KEY (default ~/.config/ndl-live/ndl-live-signing.key; create it
# with "go run ./cmd/ndl-live keygen ~/.config/ndl-live"). Then:
#   - on the host itself, as root, it is queued with ndl-live install;
#   - elsewhere, set NDL_LIVE_URL and NDL_LIVE_TOKEN (an Owner API token)
#     and it is uploaded to POST /api/v1/live-updates/bundles.
# Nothing is committed or pushed; no package is built.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KEY="${NDL_LIVE_KEY:-$HOME/.config/ndl-live/ndl-live-signing.key}"
NOTES=""
COMPONENTS=()
while [ $# -gt 0 ]; do
  case "$1" in
    ui|control|agent) COMPONENTS+=("$1") ;;
    --notes) NOTES="$2"; shift ;;
    --allow-migrations) ALLOW_MIGRATIONS=1 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done
[ ${#COMPONENTS[@]} -gt 0 ] || COMPONENTS=(ui control agent)
[ -f "$KEY" ] || { echo "no signing key at $KEY" >&2; exit 1; }

BASE="$(dpkg-parsechangelog -l "$ROOT/packaging/debian/changelog" -SVersion)"
SEQ="$(date -u +%Y%m%d%H%M%S)"
VERSION="${BASE}+live.${SEQ}"
ID="${SEQ}-$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo local)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/src/bin"
LDFLAGS="-X github.com/no-dal/ndl-ce/internal/buildinfo.Version=${BASE} -X github.com/no-dal/ndl-ce/internal/buildinfo.Build=${ID}"

for c in "${COMPONENTS[@]}"; do
  case "$c" in
    ui)
      echo "building the web UI"
      (cd "$ROOT/ui" && VITE_NDL_BUILD="$ID" npm run -s build >/dev/null)
      cp -a "$ROOT/ui/dist" "$WORK/src/ui"
      ;;
    control|agent)
      echo "building ndl-$c"
      (cd "$ROOT" && CGO_ENABLED=0 go build -ldflags "$LDFLAGS" -o "$WORK/src/bin/ndl-$c" "./cmd/ndl-$c")
      ;;
  esac
done
rmdir "$WORK/src/bin" 2>/dev/null || true

BUNDLE="$ROOT/out/live/${ID}.ndlu"
mkdir -p "$(dirname "$BUNDLE")"
ARGS=(-key "$KEY" -out "$BUNDLE" -version "$VERSION" -base "$BASE" -id "$ID" -src "$WORK/src" -notes "$NOTES")
if [ -d "$WORK/src/bin" ] && [ -f "$WORK/src/bin/ndl-control" ]; then
  ARGS+=(-migrations "$ROOT/migrations")
fi
[ -n "${ALLOW_MIGRATIONS:-}" ] && ARGS+=(-allow-migrations)
(cd "$ROOT" && go run ./cmd/ndl-live pack "${ARGS[@]}")

if [ -n "${NDL_LIVE_URL:-}" ]; then
  [ -n "${NDL_LIVE_TOKEN:-}" ] || { echo "set NDL_LIVE_TOKEN to an Owner API token" >&2; exit 1; }
  curl -fsS -X POST -H "Authorization: Bearer $NDL_LIVE_TOKEN" -H "Content-Type: application/octet-stream" \
    --data-binary @"$BUNDLE" "${NDL_LIVE_URL%/}/api/v1/live-updates/bundles"
  echo
elif [ "$(id -u)" = 0 ] && command -v ndl-live >/dev/null 2>&1; then
  ndl-live install "$BUNDLE"
else
  echo "bundle written to $BUNDLE"
fi
