#!/bin/sh
# Build fresh Vite assets into $1/dist. Fails closed on a missing,
# empty, or placeholder dist so ndl-ui cannot ship stale files.
set -eu

UI=${1:-}
if [ -z "$UI" ] || [ ! -f "$UI/package.json" ]; then
  echo "ndl-ui: UI source is missing" >&2
  exit 1
fi
if ! command -v node >/dev/null 2>&1; then
  echo "ndl-ui: node is required to build Vite assets" >&2
  exit 1
fi
if ! command -v pnpm >/dev/null 2>&1; then
  if ! command -v corepack >/dev/null 2>&1; then
    echo "ndl-ui: pnpm or corepack is required to build Vite assets" >&2
    exit 1
  fi
  corepack enable
  corepack prepare pnpm@10.10.0 --activate
fi
if ! command -v pnpm >/dev/null 2>&1; then
  echo "ndl-ui: pnpm is required to build Vite assets" >&2
  exit 1
fi

rm -rf "$UI/dist"
cd "$UI"
pnpm install --frozen-lockfile
pnpm build

if [ ! -f "$UI/dist/index.html" ]; then
  echo "ndl-ui: Vite did not write dist/index.html" >&2
  exit 1
fi
if [ ! -d "$UI/dist/assets" ]; then
  echo "ndl-ui: Vite did not write dist/assets" >&2
  exit 1
fi
js=
for f in "$UI"/dist/assets/*.js; do
  if [ -f "$f" ]; then
    js=$f
    break
  fi
done
if [ -z "$js" ]; then
  echo "ndl-ui: Vite dist has no JS assets" >&2
  exit 1
fi
css=
for f in "$UI"/dist/assets/*.css; do
  if [ -f "$f" ]; then
    css=$f
    break
  fi
done
if [ -z "$css" ]; then
  echo "ndl-ui: Vite dist has no CSS assets" >&2
  exit 1
fi
if ! grep -q '/assets/' "$UI/dist/index.html"; then
  echo "ndl-ui: dist/index.html is not a Vite build" >&2
  exit 1
fi
