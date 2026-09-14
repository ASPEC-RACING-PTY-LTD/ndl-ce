#!/bin/sh
# Prove build-ui.sh cannot accept a placeholder or leftover dist.
# Uses a fake pnpm so this check does not need a full Vite install.
set -eu

fail() {
  echo "UI_BUILD_FAIL: $1" >&2
  exit 1
}

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
BUILD_UI="$ROOT/packaging/lib/ndl/build-ui.sh"
[ -f "$BUILD_UI" ] || fail "missing packaging/lib/ndl/build-ui.sh"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

cat > "$TMP/bin/node" <<'EOF'
#!/bin/sh
echo v24.8.0
EOF
chmod +x "$TMP/bin/node"

write_placeholder_pnpm() {
  cat > "$TMP/bin/pnpm" <<'EOF'
#!/bin/sh
dir=$(pwd)
mkdir -p "$dir/dist"
printf '%s\n' '<!doctype html><title>ndl-ui</title>' > "$dir/dist/index.html"
EOF
  chmod +x "$TMP/bin/pnpm"
}

write_vite_pnpm() {
  cat > "$TMP/bin/pnpm" <<'EOF'
#!/bin/sh
dir=$(pwd)
mkdir -p "$dir/dist/assets"
printf '%s\n' '<!doctype html><script type="module" src="/assets/index-abc.js"></script>' > "$dir/dist/index.html"
echo 'console.log(1)' > "$dir/dist/assets/index-abc.js"
echo '.sidebar-nav{display:flex}' > "$dir/dist/assets/index-abc.css"
EOF
  chmod +x "$TMP/bin/pnpm"
}

export PATH="$TMP/bin:$PATH"

UI="$TMP/ui-placeholder"
mkdir -p "$UI/dist/assets"
printf '%s\n' '{"name":"ndl-ui"}' > "$UI/package.json"
echo stale > "$UI/dist/stale.js"
echo stale-index > "$UI/dist/index.html"
write_placeholder_pnpm
if sh "$BUILD_UI" "$UI"; then
  fail "placeholder dist must not pass build-ui.sh"
fi

UI2="$TMP/ui-ok"
mkdir -p "$UI2/dist"
printf '%s\n' '{"name":"ndl-ui"}' > "$UI2/package.json"
echo leftover > "$UI2/dist/old.css"
write_vite_pnpm
sh "$BUILD_UI" "$UI2" || fail "Vite-like dist must pass build-ui.sh"
[ -f "$UI2/dist/index.html" ] || fail "missing index.html"
[ -f "$UI2/dist/assets/index-abc.js" ] || fail "missing JS asset"
[ -f "$UI2/dist/assets/index-abc.css" ] || fail "missing CSS asset"
[ ! -f "$UI2/dist/old.css" ] || fail "stale dist file survived rm -rf"

echo "UI_BUILD_OK"
