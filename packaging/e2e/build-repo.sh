#!/bin/sh
# Build binary Debian packages and a GPG-signed APT repo for local e2e.
# Uses the persistent development signing key. Does not rotate it.
set -eu

SRC=${SRC:-/src}
OUT=${OUT:-/out}
export DEBIAN_FRONTEND=noninteractive
export GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}
export GO111MODULE=on
export CGO_ENABLED=0

apt-get update
apt-get install -y --no-install-recommends \
  ca-certificates curl git gnupg dpkg-dev debhelper \
  build-essential fakeroot apt-utils gzip openssl xz-utils

if ! command -v go >/dev/null 2>&1 || ! go version | grep -Eq 'go1\.(2[4-9]|[3-9][0-9])'; then
  apt-get install -y --no-install-recommends golang-go || true
fi
if ! command -v go >/dev/null 2>&1 || ! go version | grep -Eq 'go1\.(2[4-9]|[3-9][0-9])'; then
  curl -fsSL https://go.dev/dl/go1.24.6.linux-amd64.tar.gz | tar -C /usr/local -xz
  export PATH=/usr/local/go/bin:$PATH
fi
go version

if [ ! -d "$SRC/ui/dist" ]; then
  echo "ui/dist is missing; ndl-ui would be empty" >&2
  exit 1
fi

BUILD=/tmp/nodal-0.1.0
rm -rf "$BUILD"
mkdir -p "$BUILD"
# Copy only what the package build needs (avoid node_modules).
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

rm -f /tmp/nodal_*.deb /tmp/ndl-*.deb /tmp/nodalctl_*.deb /tmp/nodal-*.deb
cd "$BUILD"
dpkg-buildpackage -us -uc -b --no-sign
if [ -z "$OUT" ] || [ "$OUT" = "/" ]; then
  echo "OUT is not a usable output directory" >&2
  exit 1
fi
rm -rf "$OUT/debs"
mkdir -p "$OUT/debs"
cp -a /tmp/nodal_*.deb /tmp/ndl-*.deb /tmp/nodalctl_*.deb "$OUT/debs/"

# Signed APT repo: URIs .../debian Suites: trixie Components: main
rm -rf "$OUT/debian"
mkdir -p "$OUT/debian/pool/main/n/nodal" \
  "$OUT/debian/dists/trixie/main/binary-amd64" \
  "$OUT/debian/dists/trixie/main/binary-all"
cp -a "$OUT/debs/"*.deb "$OUT/debian/pool/main/n/nodal/"

cd "$OUT/debian"
dpkg-scanpackages --arch amd64 pool /dev/null > dists/trixie/main/binary-amd64/Packages
dpkg-scanpackages --arch all pool /dev/null > dists/trixie/main/binary-all/Packages
gzip -9kf dists/trixie/main/binary-amd64/Packages
gzip -9kf dists/trixie/main/binary-all/Packages

# Include Architecture: all packages in the amd64 index (apt on amd64 reads that).
{
  cat dists/trixie/main/binary-amd64/Packages
  echo
  cat dists/trixie/main/binary-all/Packages
} > dists/trixie/main/binary-amd64/Packages.merged
mv dists/trixie/main/binary-amd64/Packages.merged dists/trixie/main/binary-amd64/Packages
gzip -9kf dists/trixie/main/binary-amd64/Packages

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
Description: Temporary Phase 1 test repository
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

# shellcheck source=lib/sign-repo.sh
. "$SRC/packaging/e2e/lib/sign-repo.sh"
sign_release dists/trixie
chmod -R a+rX "$OUT/debian" "$OUT/gpg" "$OUT/debs"

cp -a "$SRC/packaging/bootstrap/get-nodal.sh" "$OUT/get-nodal.sh"
sed -i 's/\r$//' "$OUT/get-nodal.sh"

# Reuse the development TLS material. Rotating it breaks an already-installed guest.
if [ -f "$OUT/ca.crt" ] && [ -f "$OUT/server.crt" ] && [ -f "$OUT/server.key" ]; then
  echo "Reusing existing e2e TLS certificates in $OUT"
else
  openssl req -x509 -newkey rsa:2048 -sha256 -days 7 -nodes \
    -subj "/CN=No-dal E2E CA" \
    -keyout "$OUT/ca.key" -out "$OUT/ca.crt"
  openssl req -newkey rsa:2048 -nodes \
    -subj "/CN=packages.no-dal.com" \
    -keyout "$OUT/server.key" -out "$OUT/server.csr"
  cat > "$OUT/server.ext" <<'EOF'
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = DNS:get.no-dal.com,DNS:packages.no-dal.com
EOF
  openssl x509 -req -in "$OUT/server.csr" -CA "$OUT/ca.crt" -CAkey "$OUT/ca.key" \
    -CAcreateserial -out "$OUT/server.crt" -days 7 -sha256 -extfile "$OUT/server.ext"
fi

cat > "$OUT/nginx.conf" <<'EOF'
events { worker_connections 64; }
http {
  include /etc/nginx/mime.types;
  server {
    listen 443 ssl;
    server_name get.no-dal.com;
    ssl_certificate /repo/server.crt;
    ssl_certificate_key /repo/server.key;
    root /repo;
    index get-nodal.sh;
    default_type text/plain;
    location / {
      try_files $uri /get-nodal.sh =404;
    }
  }
  server {
    listen 443 ssl default_server;
    server_name packages.no-dal.com;
    ssl_certificate /repo/server.crt;
    ssl_certificate_key /repo/server.key;
    location = /gpg {
      alias /repo/gpg;
      default_type application/pgp-keys;
    }
    location /debian/ {
      alias /repo/debian/;
      autoindex on;
    }
  }
}
EOF

echo "Built packages:"
ls -l "$OUT/debs"
echo "Repo ready."
