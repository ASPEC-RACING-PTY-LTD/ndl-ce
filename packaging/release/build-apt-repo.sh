#!/usr/bin/env bash
set -euo pipefail

deb_dir=${1:?usage: build-apt-repo.sh DEB_DIR SITE_DIR}
site_dir=${2:?usage: build-apt-repo.sh DEB_DIR SITE_DIR}
repo_dir="$site_dir/debian"
pool_dir="$repo_dir/pool/main/n/nodal"
dist_dir="$repo_dir/dists/trixie"

: "${GNUPGHOME:?GNUPGHOME must point to the APT signing keyring}"
: "${APT_SIGNING_KEY_FINGERPRINT:?APT_SIGNING_KEY_FINGERPRINT is required}"

command -v dpkg-deb >/dev/null
command -v dpkg-scanpackages >/dev/null
command -v apt-ftparchive >/dev/null
command -v gpg >/dev/null

if ! gpg --batch --with-colons --list-secret-keys "$APT_SIGNING_KEY_FINGERPRINT" |
  awk -F: '$1 == "sec" { found = 1 } END { exit !found }'; then
  echo "APT signing secret key $APT_SIGNING_KEY_FINGERPRINT is unavailable" >&2
  exit 1
fi

rm -rf "$repo_dir"
mkdir -p \
  "$pool_dir" \
  "$dist_dir/main/binary-amd64" \
  "$dist_dir/main/binary-all"
mkdir -p "$site_dir"
printf '%s\n' "${APT_PAGES_DOMAIN:-packages.no-dal.com}" > "$site_dir/CNAME"

deb_count=0
shopt -s nullglob
for deb in "$deb_dir"/*.deb; do
  package=$(dpkg-deb -f "$deb" Package)
  architecture=$(dpkg-deb -f "$deb" Architecture)
  case "$package" in
    nodal|nodalctl|ndl-agent|ndl-control|ndl-guest|ndl-network-rollback|ndl-ct-prepare|ndl-lxc-nesting-apparmor|ndl-qemu-launch|ndl-oci-launch|nodal-feature-*) ;;
    *) echo "Refusing unexpected package in APT repository: $package" >&2; exit 1 ;;
  esac
  case "$architecture" in
    amd64|all) ;;
    *) echo "Refusing unsupported package architecture: $package ($architecture)" >&2; exit 1 ;;
  esac
  cp -n "$deb" "$pool_dir/$(basename "$deb")"
  deb_count=$((deb_count + 1))
done
shopt -u nullglob

if [ "$deb_count" -eq 0 ]; then
  echo "No Debian packages found in $deb_dir" >&2
  exit 1
fi

cd "$repo_dir"
dpkg-scanpackages --multiversion --arch amd64 pool /dev/null \
  > "$dist_dir/main/binary-amd64/Packages"
dpkg-scanpackages --multiversion --arch all pool /dev/null \
  > "$dist_dir/main/binary-all/Packages"
cat "$dist_dir/main/binary-all/Packages" >> "$dist_dir/main/binary-amd64/Packages"
gzip -9kf "$dist_dir/main/binary-amd64/Packages"
gzip -9kf "$dist_dir/main/binary-all/Packages"

apt-ftparchive \
  -o APT::FTPArchive::Release::Origin="No-DAL" \
  -o APT::FTPArchive::Release::Label="No-DAL" \
  -o APT::FTPArchive::Release::Suite="trixie" \
  -o APT::FTPArchive::Release::Codename="trixie" \
  -o APT::FTPArchive::Release::Architectures="amd64 all" \
  -o APT::FTPArchive::Release::Components="main" \
  -o APT::FTPArchive::Release::Description="No-DAL signed Alpha packages" \
  release "$dist_dir" > "$dist_dir/Release"

sign_args=(--batch --yes --pinentry-mode loopback --local-user "${APT_SIGNING_KEY_FINGERPRINT}!")
if [ -n "${APT_SIGNING_KEY_PASSPHRASE_FILE:-}" ]; then
  sign_args+=(--passphrase-file "$APT_SIGNING_KEY_PASSPHRASE_FILE")
fi
gpg "${sign_args[@]}" --clearsign --output "$dist_dir/InRelease" "$dist_dir/Release"
gpg "${sign_args[@]}" --detach-sign --output "$dist_dir/Release.gpg" "$dist_dir/Release"
gpg --batch --armor --export "$APT_SIGNING_KEY_FINGERPRINT" > "$site_dir/gpg"
chmod -R a+rX "$site_dir"

echo "Built signed APT repository in $repo_dir with $deb_count package artifact(s)."