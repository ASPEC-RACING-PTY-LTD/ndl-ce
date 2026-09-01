#!/bin/sh
# No-dal Community Edition bootstrap.
# Public URL: https://get.no-dal.com (inspect, then run as root).
# This script is not the platform. Packages own users, database, and units.
set -eu

supported="Debian 13 amd64 (tier1)"
key_url=${NODAL_APT_KEY_URL:-https://packages.no-dal.com/gpg}
repo_url=${NODAL_APT_REPO:-https://packages.no-dal.com/debian}

die() {
  echo "get-nodal: $*" >&2
  exit 1
}

[ "$(id -u)" -eq 0 ] || die "must run as root"
[ -r /etc/os-release ] || die "/etc/os-release is not readable"
[ -d /run/systemd/system ] || die "systemd is required"

arch=$(uname -m 2>/dev/null || echo unknown)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
esac

# shellcheck disable=SC1091
. /etc/os-release
id=${ID:-}
version_id=${VERSION_ID:-}
pretty=${PRETTY_NAME:-$id}

if [ "$id" != "debian" ] || [ "$version_id" != "13" ] || [ "$arch" != "amd64" ]; then
  die "No-dal does not currently support this host platform (${pretty:-unknown}, ${arch}). Currently supported host platforms: ${supported}"
fi

if [ "${NODAL_DEV_REPO:-}" != "1" ]; then
  case "$key_url" in
    https://*) ;;
    *) die "signing key URL must use HTTPS" ;;
  esac
  case "$repo_url" in
    https://*) ;;
    *) die "package repo URL must use HTTPS" ;;
  esac
fi

umask 022
mkdir -p /usr/share/keyrings /etc/apt/sources.list.d
tmp=$(mktemp)
# shellcheck disable=SC2064
trap 'rm -f "$tmp"' EXIT
curl -fsSL "$key_url" -o "$tmp"
keyring=/usr/share/keyrings/nodal.gpg
if grep -q "BEGIN PGP" "$tmp"; then
  keyring=/usr/share/keyrings/nodal.asc
fi
cp "$tmp" "$keyring"
chmod 0644 "$keyring"

cat > /etc/apt/sources.list.d/nodal.sources <<EOF
Types: deb
URIs: ${repo_url}
Suites: trixie
Components: main
Signed-By: ${keyring}
EOF

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y nodal

n=0
while [ "$n" -lt 60 ]; do
  if curl -fsS http://127.0.0.1:8080/api/v1/health >/dev/null 2>&1; then
    break
  fi
  n=$((n + 1))
  sleep 1
done
[ "$n" -lt 60 ] || die "control plane health check failed at http://127.0.0.1:8080/api/v1/health"

addr=127.0.0.1
ips=$(hostname -I 2>/dev/null || true)
# shellcheck disable=SC2086
for cand in $ips; do
  case "$cand" in
    *:*) ;;
    *)
      addr=$cand
      break
      ;;
  esac
done

echo "No-dal is installed."
echo "Until TLS is enabled, setup is at http://${addr}:8080/setup"
echo "After enabling TLS, open https://${addr}/setup"
