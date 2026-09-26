#!/usr/bin/env bash
set -euo pipefail

src=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
tmp=$(mktemp -d)
trap 'if [ -n "${gpg_pid:-}" ]; then kill "$gpg_pid" 2>/dev/null || true; fi; rm -rf "$tmp"' EXIT

for tool in apt-get apt-ftparchive dpkg dpkg-deb dpkg-scanpackages gpg gpgv; do
  command -v "$tool" >/dev/null || {
    echo "Missing required integration-test tool: $tool" >&2
    exit 1
  }
done

export GNUPGHOME="$tmp/gnupg"
mkdir -m 700 "$GNUPGHOME"
gpg --batch --passphrase '' --quick-gen-key "No-DAL APT Test <apt-test@no-dal.invalid>" rsa2048 sign 1d
APT_SIGNING_KEY_FINGERPRINT=$(gpg --batch --with-colons --list-secret-keys | awk -F: '$1 == "fpr" { print $10; exit }')
export APT_SIGNING_KEY_FINGERPRINT

deb_dir="$tmp/debs"
site_dir="$tmp/site"
mkdir -p "$deb_dir"

make_test_deb() {
  local version=$1
  local root="$tmp/package-$version"
  mkdir -p "$root/DEBIAN" "$root/usr/share/nodal"
  cat > "$root/DEBIAN/control" <<EOF
Package: nodal
Version: $version
Architecture: all
Maintainer: No-DAL APT test <apt-test@no-dal.invalid>
Description: No-DAL signed repository upgrade test
EOF
  printf '%s\n' "$version" > "$root/usr/share/nodal/version"
  dpkg-deb --build "$root" "$deb_dir/nodal_${version}_all.deb" >/dev/null
}

make_test_deb 1.0.1
bash "$src/packaging/release/build-apt-repo.sh" "$deb_dir" "$site_dir"
gpg --batch --dearmor --output "$tmp/repository-keyring.gpg" "$site_dir/gpg"
gpgv --keyring "$tmp/repository-keyring.gpg" "$site_dir/debian/dists/trixie/InRelease"

apt_root="$tmp/apt"
install_root="$tmp/installed"
mkdir -p "$apt_root/lists/partial" "$apt_root/archives/partial" "$install_root/var/lib/dpkg"
touch "$install_root/var/lib/dpkg/status"
cp "$site_dir/gpg" "$tmp/nodal.asc"
cat > "$apt_root/sources.list" <<EOF
deb [signed-by=$tmp/nodal.asc] file:$site_dir/debian trixie main
EOF
cat > "$apt_root/apt.conf" <<EOF
Dir::Etc::sourcelist "$apt_root/sources.list";
Dir::Etc::sourceparts "-";
Dir::State::status "$install_root/var/lib/dpkg/status";
Dir::State::lists "$apt_root/lists";
Dir::Cache::archives "$apt_root/archives/";
APT::Architecture "amd64";
Debug::NoLocking "true";
EOF

apt_options=(-c "$apt_root/apt.conf")
download_apt_package() {
  local package=$1
  (
    cd "$apt_root/archives"
    apt-get "${apt_options[@]}" download "$package"
  )
}
apt-get "${apt_options[@]}" update
apt-get "${apt_options[@]}" --yes --download-only install nodal=1.0.1
download_apt_package nodal=1.0.1
old_deb="$apt_root/archives/nodal_1.0.1_all.deb"
test -f "$old_deb" || { echo "APT did not cache the downloaded package at $old_deb" >&2; exit 1; }
dpkg --root="$install_root" --install "$old_deb"
test "$(dpkg-query --root="$install_root" -W -f='${Version}' nodal)" = 1.0.1

make_test_deb 1.0.2
bash "$src/packaging/release/build-apt-repo.sh" "$deb_dir" "$site_dir"
gpg --batch --yes --dearmor --output "$tmp/repository-keyring.gpg" "$site_dir/gpg"
gpgv --keyring "$tmp/repository-keyring.gpg" "$site_dir/debian/dists/trixie/InRelease"
apt-get "${apt_options[@]}" update
apt-get "${apt_options[@]}" --yes --download-only install nodal
download_apt_package nodal
new_deb="$apt_root/archives/nodal_1.0.2_all.deb"
test -f "$new_deb" || { echo "APT did not cache the downloaded package at $new_deb" >&2; exit 1; }
dpkg --root="$install_root" --install "$new_deb"
test "$(dpkg-query --root="$install_root" -W -f='${Version}' nodal)" = 1.0.2

apt-get "${apt_options[@]}" --yes --allow-downgrades --download-only install nodal=1.0.1
test -f "$old_deb"
echo "Signed APT repository installs 1.0.1, upgrades to 1.0.2, and retains the old version for rollback."