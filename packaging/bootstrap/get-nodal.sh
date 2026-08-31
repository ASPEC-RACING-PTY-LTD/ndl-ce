#!/bin/sh
# No-dal Community Edition bootstrap (Phase 0 stub).
# Intended public URL: https://get.no-dal.com (inspect, then run as root)
# This script is not the platform. It only detects the host and
# describes repository plus metapackage install. Inspect before run.
set -eu

supported="Debian 13 amd64 (tier1)"

die() {
  echo "get-nodal: $*" >&2
  exit 1
}

arch=$(uname -m 2>/dev/null || echo unknown)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
esac

id=""
version_id=""
pretty=""
if [ -f /etc/os-release ]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  id=${ID:-}
  version_id=${VERSION_ID:-}
  pretty=${PRETTY_NAME:-$id}
fi

if [ "$id" != "debian" ] || [ "$version_id" != "13" ] || [ "$arch" != "amd64" ]; then
  die "No-dal does not currently support this host platform (${pretty:-unknown}, ${arch}). Currently supported host platforms: ${supported}"
fi

echo "No-dal host check: Debian 13 amd64"
echo "Would configure the signed No-dal package repository (placeholder)."
echo "Would run: apt-get install -y nodal"
echo "Phase 0 stub: no packages were installed."
echo "Open the management URL after a Phase 1 install to finish setup."
