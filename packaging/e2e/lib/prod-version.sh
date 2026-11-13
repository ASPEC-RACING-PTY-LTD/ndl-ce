# shellcheck shell=sh
# Prod builds must sort above every public release. Otherwise a node first
# installed from the public installer reports a higher number than the
# newer prod build, and apt treats the prod packages as a downgrade.
#
# prod_version BASE STAMP [TAG...] prints the highest of BASE and the
# plain-number TAGs (a leading v is dropped, pre-releases are ignored),
# followed by +prodSTAMP. 1.3.0+prod202611131210 sorts above 1.3.0 and
# below 1.3.1, so the next public release is still an upgrade, and each
# prod build sorts above the one before it.
prod_version() {
  best=$1
  stamp=$2
  shift 2
  for tag in "$@"; do
    v=${tag#v}
    case $v in
      '' | *[!0-9.]* | .* | *. | *..*) continue ;;
    esac
    if dpkg --compare-versions "$v" gt "$best"; then
      best=$v
    fi
  done
  printf '%s+prod%s\n' "$best" "$stamp"
}

# public_release_tags prints the tags of the public releases, best effort:
# with no network it prints nothing and the build still goes ahead.
public_release_tags() {
  url=${PUBLIC_RELEASES_URL:-https://api.github.com/repos/ASPEC-RACING-PTY-LTD/ndl-ce/releases?per_page=100}
  curl -fsS --max-time 15 "$url" 2>/dev/null |
    grep -o '"tag_name": *"[^"]*"' |
    sed 's/.*"\([^"]*\)"$/\1/' || true
}

# set_changelog_version FILE VERSION rewrites the version of the first entry.
set_changelog_version() {
  sed -i "1s/^\([a-z0-9][a-z0-9+.-]*\) ([^)]*)/\1 ($2)/" "$1"
}
