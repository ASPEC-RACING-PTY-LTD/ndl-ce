#!/bin/sh
# Rebuild debs and refresh the signed APT indexes without rotating GPG or TLS.
set -eu

SRC=${SRC:-/src}
OUT=${OUT:-/out}
export DEBIAN_FRONTEND=noninteractive
export GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}
export GO111MODULE=on
export CGO_ENABLED=0
export GOCACHE=${GOCACHE:-/gocache/cache}
export GOMODCACHE=${GOMODCACHE:-/gocache/mod}

if [ ! -d "$SRC/ui/dist" ]; then
  echo "ui/dist is missing" >&2
  exit 1
fi
if [ ! -d "$OUT/gnupg" ]; then
  echo "existing GNUPGHOME $OUT/gnupg is required" >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1 || ! go version | grep -Eq 'go1\.(2[4-9]|[3-9][0-9])'; then
  if [ -x /usr/local/go/bin/go ]; then
    export PATH=/usr/local/go/bin:$PATH
  fi
fi
if ! command -v go >/dev/null 2>&1 || ! go version | grep -Eq 'go1\.(2[4-9]|[3-9][0-9])'; then
  curl -fsSL https://go.dev/dl/go1.24.6.linux-amd64.tar.gz | tar -C /usr/local -xz
  export PATH=/usr/local/go/bin:$PATH
fi
go version

BUILD=/tmp/nodal-rebuild
rm -rf "$BUILD"
mkdir -p "$BUILD"
for item in cmd gen internal migrations proto systemd packaging ui api go.mod go.sum; do
  if [ -e "$SRC/$item" ]; then
    cp -a "$SRC/$item" "$BUILD/"
  fi
done
cp -a "$SRC/packaging/debian" "$BUILD/debian"
find "$BUILD/debian" -type f -exec chmod a-x {} +
chmod +x "$BUILD/debian/rules" \
  "$BUILD/debian/ndl-control.postinst" \
  "$BUILD/debian/ndl-control.postrm" \
  "$BUILD/debian/ndl-agent.postinst" \
  "$BUILD/debian/ndl-agent.postrm"

cd "$BUILD"
dpkg-buildpackage -us -uc -b --no-sign
mkdir -p "$OUT/debs"
rm -f "$OUT/debs/"*.deb
cp -a /tmp/nodal_*.deb /tmp/ndl-*.deb /tmp/nodalctl_*.deb "$OUT/debs/"

mkdir -p "$OUT/debian/pool/main/n/nodal" \
  "$OUT/debian/dists/trixie/main/binary-amd64" \
  "$OUT/debian/dists/trixie/main/binary-all"
rm -f "$OUT/debian/pool/main/n/nodal/"*.deb
cp -a "$OUT/debs/"*.deb "$OUT/debian/pool/main/n/nodal/"

cd "$OUT/debian"
dpkg-scanpackages --arch amd64 pool /dev/null > dists/trixie/main/binary-amd64/Packages
dpkg-scanpackages --arch all pool /dev/null > dists/trixie/main/binary-all/Packages
{
  cat dists/trixie/main/binary-amd64/Packages
  echo
  cat dists/trixie/main/binary-all/Packages
} > dists/trixie/main/binary-amd64/Packages.merged
mv dists/trixie/main/binary-amd64/Packages.merged dists/trixie/main/binary-amd64/Packages
gzip -9kf dists/trixie/main/binary-amd64/Packages
gzip -9kf dists/trixie/main/binary-all/Packages

now=$(date -u -R)
amd64_sz=$(wc -c < dists/trixie/main/binary-amd64/Packages)
amd64_gz=$(wc -c < dists/trixie/main/binary-amd64/Packages.gz)
all_sz=$(wc -c < dists/trixie/main/binary-all/Packages)
all_gz=$(wc -c < dists/trixie/main/binary-all/Packages.gz)
amd64_md5=$(md5sum dists/trixie/main/binary-amd64/Packages | awk '{print $1}')
amd64_gz_md5=$(md5sum dists/trixie/main/binary-amd64/Packages.gz | awk '{print $1}')
all_md5=$(md5sum dists/trixie/main/binary-all/Packages | awk '{print $1}')
all_gz_md5=$(md5sum dists/trixie/main/binary-all/Packages.gz | awk '{print $1}')
amd64_sha=$(sha256sum dists/trixie/main/binary-amd64/Packages | awk '{print $1}')
amd64_gz_sha=$(sha256sum dists/trixie/main/binary-amd64/Packages.gz | awk '{print $1}')
all_sha=$(sha256sum dists/trixie/main/binary-all/Packages | awk '{print $1}')
all_gz_sha=$(sha256sum dists/trixie/main/binary-all/Packages.gz | awk '{print $1}')

cat > dists/trixie/Release <<EOF
Origin: No-dal
Label: No-dal
Suite: trixie
Codename: trixie
Architectures: amd64 all
Components: main
Description: Temporary Phase 2 test repository
Date: ${now}
MD5Sum:
 ${amd64_md5} ${amd64_sz} main/binary-amd64/Packages
 ${amd64_gz_md5} ${amd64_gz} main/binary-amd64/Packages.gz
 ${all_md5} ${all_sz} main/binary-all/Packages
 ${all_gz_md5} ${all_gz} main/binary-all/Packages.gz
SHA256:
 ${amd64_sha} ${amd64_sz} main/binary-amd64/Packages
 ${amd64_gz_sha} ${amd64_gz} main/binary-amd64/Packages.gz
 ${all_sha} ${all_sz} main/binary-all/Packages
 ${all_gz_sha} ${all_gz} main/binary-all/Packages.gz
EOF

export GNUPGHOME="$OUT/gnupg"
chmod 700 "$GNUPGHOME" || true
rm -f dists/trixie/InRelease dists/trixie/Release.gpg
gpg --batch --yes --clearsign -o dists/trixie/InRelease dists/trixie/Release
gpg --batch --yes --detach-sign -o dists/trixie/Release.gpg dists/trixie/Release
chmod -R a+rX "$OUT/debian" "$OUT/debs"
echo "Rebuilt packages:"
ls -l "$OUT/debs"
