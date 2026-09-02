#!/bin/sh
# Build real .deb packages (unless DEB_DIR already has them) and inspect
# the GENERATED maintainer scripts inside those debs, not the source
# templates. Catches leftover #DEBHELPER# expansion, stray executable
# prose, invalid shell, and a failed configure of the generated postinst.
set -eu

fail() {
  echo "MAINTAINER_SCRIPT_FAIL: $1" >&2
  exit 1
}

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SRC=${SRC:-$ROOT}
DEB_DIR=${DEB_DIR:-}
export DEBIAN_FRONTEND=noninteractive
export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"
export GO111MODULE=on
export CGO_ENABLED=0

if [ -x /usr/local/go/bin/go ]; then
  export PATH="/usr/local/go/bin:$PATH"
fi

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

need_cmd dpkg-deb
need_cmd dpkg-buildpackage

# shellcheck source=lib/ensure-node.sh
. "$SRC/packaging/e2e/lib/ensure-node.sh"

audit_source_templates() {
  debian="$SRC/packaging/debian"
  [ -d "$debian" ] || fail "missing $debian"
  found_lone=0
  for path in "$debian"/*; do
    [ -f "$path" ] || continue
    name=$(basename "$path")
    case "$name" in
      *.postinst|*.postrm|*.preinst|*.prerm) ;;
      *)
        if grep -F '#DEBHELPER#' "$path" >/dev/null 2>&1; then
          fail "$name contains #DEBHELPER# but is not a maintainer script"
        fi
        continue
        ;;
    esac
    lone=0
    lineno=0
    while IFS= read -r line || [ -n "$line" ]; do
      lineno=$((lineno + 1))
      case "$line" in
        *'#DEBHELPER#'*)
          if [ "$line" != '#DEBHELPER#' ]; then
            fail "$name:$lineno accidental #DEBHELPER# token (must be a lone line)"
          fi
          lone=$((lone + 1))
          ;;
      esac
    done < "$path"
    [ "$lone" -eq 1 ] || fail "$name must contain exactly one lone #DEBHELPER# token"
    found_lone=$((found_lone + 1))
  done
  [ "$found_lone" -ge 4 ] || fail "expected ndl-control and ndl-agent maintainer scripts"
}

build_debs() {
  need_cmd go
  ensure_node
  BUILD=${BUILD_DIR:-/tmp/nodal-maintainer-build}
  OUT_DEBS=${1:-/tmp/nodal-maintainer-debs}
  rm -rf "$BUILD"
  mkdir -p "$BUILD" "$OUT_DEBS"
  for item in cmd gen internal migrations proto systemd packaging ui api store go.mod go.sum; do
    if [ -e "$SRC/$item" ]; then
      cp -a "$SRC/$item" "$BUILD/"
    fi
  done
  rm -rf "$BUILD/ui/dist" "$BUILD/ui/node_modules"
  cp -a "$SRC/packaging/debian" "$BUILD/debian"
  find "$BUILD/debian" -type f -exec chmod a-x {} +
  chmod +x "$BUILD/debian/rules" \
    "$BUILD/debian/ndl-control.postinst" \
    "$BUILD/debian/ndl-control.postrm" \
    "$BUILD/debian/ndl-agent.postinst" \
    "$BUILD/debian/ndl-agent.postrm" \
    "$BUILD/debian/ndl-ui.postinst"

  rm -f /tmp/nodal_*.deb /tmp/ndl-*.deb /tmp/nodalctl_*.deb /tmp/nodal-*.deb
  (
    cd "$BUILD"
    dpkg-buildpackage -us -uc -b --no-sign
  ) >&2
  rm -f "$OUT_DEBS"/*.deb
  cp -a /tmp/nodal_*.deb /tmp/ndl-*.deb /tmp/nodalctl_*.deb "$OUT_DEBS/"
  ctrl=
  for f in "$OUT_DEBS"/ndl-control_*.deb; do
    [ -f "$f" ] || continue
    ctrl=$f
    break
  done
  [ -n "$ctrl" ] || fail "ndl-control .deb was not built"
  agent=
  for f in "$OUT_DEBS"/ndl-agent_*.deb; do
    [ -f "$f" ] || continue
    agent=$f
    break
  done
  [ -n "$agent" ] || fail "ndl-agent .deb was not built"
  ui=
  for f in "$OUT_DEBS"/ndl-ui_*.deb; do
    [ -f "$f" ] || continue
    ui=$f
    break
  done
  [ -n "$ui" ] || fail "ndl-ui .deb was not built"
}

extract_scripts() {
  deb=$1
  dest=$2
  rm -rf "$dest"
  mkdir -p "$dest"
  dpkg-deb -e "$deb" "$dest"
}

reject_stray_prose() {
  pkg=$1
  script=$2
  path=$3
  if grep -nE '^[[:space:]]*restarts[[:space:]]' "$path" >/dev/null; then
    fail "$pkg $script contains executable stray prose starting with restarts"
  fi
  if grep -nE '^[[:space:]]*restarts it once after configure' "$path" >/dev/null; then
    fail "$pkg $script contains leftover comment text as executable shell"
  fi
  if awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*$/ { next }
    /^[[:space:]]*[A-Za-z][A-Za-z0-9_]*[[:space:]].*\.[[:space:]]*$/ {
      print NR ": " $0
      found=1
    }
    END { exit found ? 1 : 0 }
  ' "$path"; then
    :
  else
    fail "$pkg $script has an executable English sentence (stray prose after helper substitution)"
  fi
}

inspect_script() {
  pkg=$1
  script=$2
  path=$3
  [ -f "$path" ] || fail "$pkg $script is missing from the built deb"
  if grep -F '#DEBHELPER#' "$path" >/dev/null; then
    fail "$pkg $script still contains unsubstituted #DEBHELPER#"
  fi
  sh -n "$path" || fail "$pkg $script is not valid shell"
  reject_stray_prose "$pkg" "$script" "$path"
  if grep -F 'cluster_leases' "$path" >/dev/null; then
    fail "$pkg $script must not delete writer lease rows"
  fi
}

inspect_control_postinst() {
  path=$1
  inspect_script ndl-control postinst "$path"
  grep -q 'deb-systemd-invoke' "$path" || fail "generated ndl-control.postinst missing deb-systemd-invoke"
  grep -q 'ndl-control.service' "$path" || fail "generated ndl-control.postinst missing ndl-control.service"
  grep -q '_dh_action=restart' "$path" || fail "generated ndl-control.postinst missing upgrade restart action"
  if grep -E '^[[:space:]]*systemctl[[:space:]]+start.*ndl-control' "$path" >/dev/null; then
    fail "generated ndl-control.postinst must not systemctl start ndl-control"
  fi
  grep -q 'ndl-agent.socket' "$path" || fail "generated ndl-control.postinst must start the agent socket"
  grep -q 'ndl-agent.service' "$path" || fail "generated ndl-control.postinst must start the agent service"
}

inspect_control_postrm() {
  path=$1
  inspect_script ndl-control postrm "$path"
  if grep -E 'remove\|upgrade\|deconfigure' "$path" >/dev/null; then
    fail "generated ndl-control.postrm must not stop ndl-control on upgrade"
  fi
}

inspect_agent_postinst() {
  path=$1
  inspect_script ndl-agent postinst "$path"
  grep -q 'ndl-agent.socket' "$path" || fail "generated ndl-agent.postinst missing ndl-agent.socket"
}

inspect_agent_postrm() {
  path=$1
  inspect_script ndl-agent postrm "$path"
  if grep -E 'remove\|upgrade\|deconfigure' "$path" >/dev/null; then
    fail "generated ndl-agent.postrm must not stop ndl-agent on upgrade"
  fi
}

inspect_ui_postinst() {
  path=$1
  inspect_script ndl-ui postinst "$path"
  grep -q 'try-restart' "$path" || fail "generated ndl-ui.postinst missing try-restart"
  grep -q 'ndl-control.service' "$path" || fail "generated ndl-ui.postinst missing ndl-control.service"
  if grep -E '^[[:space:]]*systemctl[[:space:]]+start.*ndl-control' "$path" >/dev/null; then
    fail "generated ndl-ui.postinst must not systemctl start ndl-control"
  fi
}

inspect_ui_deb() {
  deb=$1
  [ -f "$deb" ] || fail "ndl-ui .deb not found"
  listing=$(dpkg-deb -c "$deb")
  echo "$listing" | grep -q 'usr/share/ndl/ui/index.html' || fail "ndl-ui missing usr/share/ndl/ui/index.html"
  echo "$listing" | grep -q 'usr/share/ndl/ui/assets/' || fail "ndl-ui missing usr/share/ndl/ui/assets"
  tmp=$(mktemp -d)
  dpkg-deb -x "$deb" "$tmp"
  [ -f "$tmp/usr/share/ndl/ui/index.html" ] || fail "ndl-ui index.html missing after extract"
  grep -q '/assets/' "$tmp/usr/share/ndl/ui/index.html" || fail "ndl-ui index.html is not a Vite build"
  js=
  for f in "$tmp"/usr/share/ndl/ui/assets/*.js; do
    if [ -f "$f" ]; then
      js=$f
      break
    fi
  done
  [ -n "$js" ] || fail "ndl-ui shipped no hashed JS"
  css=
  for f in "$tmp"/usr/share/ndl/ui/assets/*.css; do
    if [ -f "$f" ]; then
      css=$f
      break
    fi
  done
  [ -n "$css" ] || fail "ndl-ui shipped no hashed CSS"
  rm -rf "$tmp"
}

run_as_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
    sudo -n "$@"
  else
    return 1
  fi
}

prove_stray_prose_rejected() {
  sample=$(mktemp)
  printf '%s\n' '#!/bin/sh' 'set -e' '  restarts it once after configure.' > "$sample"
  if sh -n "$sample"; then
    :
  else
    rm -f "$sample"
    fail "stray prose must be syntactically valid shell so sh -n alone is not enough"
  fi
  if grep -nE '^[[:space:]]*restarts[[:space:]]' "$sample" >/dev/null \
    && grep -nE '^[[:space:]]*restarts it once after configure' "$sample" >/dev/null; then
    :
  else
    rm -f "$sample"
    fail "stray prose detector missed executable leftover comment text"
  fi
  if command -v unshare >/dev/null 2>&1 && run_as_root true >/dev/null 2>&1; then
    if run_as_root unshare -m /bin/sh "$sample" configure; then
      rm -f "$sample"
      fail "configure must fail when leftover comment text is executable shell"
    fi
  fi
  rm -f "$sample"
}

run_generated_configure() {
  script=$1
  name=$2
  if ! command -v unshare >/dev/null 2>&1; then
    echo "configure sandbox skipped for $name (no unshare)"
    return
  fi
  if ! run_as_root true >/dev/null 2>&1; then
    echo "configure sandbox skipped for $name (no root for mount namespace)"
    return
  fi
  created=0
  if [ ! -d /usr/lib/ndl ]; then
    created=1
  fi
  unshare_script=$(cat <<'EOS'
set -e
mkdir -p /usr/lib/ndl /run
mount -t tmpfs tmpfs /usr/lib/ndl
mount -t tmpfs tmpfs /run
printf "%s\n" "#!/bin/sh" "exit 0" > /usr/lib/ndl/postinst-control.sh
chmod 0755 /usr/lib/ndl/postinst-control.sh
/bin/sh "$1" configure
EOS
)
  if run_as_root unshare -m /bin/sh -c "$unshare_script" sh "$script"; then
    :
  else
    if [ "$created" -eq 1 ]; then
      run_as_root rmdir /usr/lib/ndl 2>/dev/null || true
    fi
    fail "$name generated postinst configure failed"
  fi
  if [ "$created" -eq 1 ]; then
    run_as_root rmdir /usr/lib/ndl 2>/dev/null || true
  fi
}

audit_source_templates

if [ -z "$DEB_DIR" ]; then
  DEB_DIR=/tmp/nodal-maintainer-debs
  build_debs "$DEB_DIR"
else
  have_ctrl=
  for f in "$DEB_DIR"/ndl-control_*.deb; do
    [ -f "$f" ] || continue
    have_ctrl=$f
    break
  done
  if [ -z "$have_ctrl" ]; then
    build_debs "$DEB_DIR"
  fi
fi

CTRL=
for f in "$DEB_DIR"/ndl-control_*.deb; do
  [ -f "$f" ] || continue
  CTRL=$f
  break
done
AGENT=
for f in "$DEB_DIR"/ndl-agent_*.deb; do
  [ -f "$f" ] || continue
  AGENT=$f
  break
done
[ -n "$CTRL" ] || fail "ndl-control .deb not found in $DEB_DIR"
[ -n "$AGENT" ] || fail "ndl-agent .deb not found in $DEB_DIR"
UI_DEB=
for f in "$DEB_DIR"/ndl-ui_*.deb; do
  [ -f "$f" ] || continue
  UI_DEB=$f
  break
done
[ -n "$UI_DEB" ] || fail "ndl-ui .deb not found in $DEB_DIR"

EXTRACT=${EXTRACT_DIR:-/tmp/nodal-maintainer-extract}
extract_scripts "$CTRL" "$EXTRACT/ndl-control"
extract_scripts "$AGENT" "$EXTRACT/ndl-agent"
extract_scripts "$UI_DEB" "$EXTRACT/ndl-ui"

inspect_control_postinst "$EXTRACT/ndl-control/postinst"
inspect_control_postrm "$EXTRACT/ndl-control/postrm"
inspect_agent_postinst "$EXTRACT/ndl-agent/postinst"
inspect_agent_postrm "$EXTRACT/ndl-agent/postrm"
inspect_ui_postinst "$EXTRACT/ndl-ui/postinst"
inspect_ui_deb "$UI_DEB"

prove_stray_prose_rejected
# Only execute generated ndl-control.postinst. The agent postinst creates
# users and directories; inspecting it is enough.
run_generated_configure "$EXTRACT/ndl-control/postinst" ndl-control

echo "MAINTAINER_SCRIPT_OK"
echo "Inspected generated scripts from $CTRL and $AGENT"
