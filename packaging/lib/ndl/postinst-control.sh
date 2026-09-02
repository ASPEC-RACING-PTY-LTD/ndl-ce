#!/bin/sh
# ndl-control host prepare fallback. Used when nodalctl host-prepare
# and /usr/lib/ndl/host-prepare are not available.
# Creates the service user, state dirs, PostgreSQL role/database, and
# /etc/ndl/control.env. Does not generate a factory password.
# Does not delete /var/lib/ndl.
set -eu

die() {
  echo "ndl-control: $*" >&2
  exit 1
}

as_postgres() {
  su -s /bin/sh postgres -c 'psql -X -v ON_ERROR_STOP=1 -Atq -f -'
}

ensure_user() {
  if ! getent group ndl-control >/dev/null; then
    addgroup --system ndl-control
  fi
  if ! getent passwd ndl-control >/dev/null; then
    adduser --system --ingroup ndl-control --home /var/lib/ndl \
      --no-create-home --shell /usr/sbin/nologin \
      --gecos "No-dal control plane" ndl-control
  fi
}

ensure_dirs() {
  mkdir -p /var/lib/ndl
  chown root:ndl-control /var/lib/ndl
  # 0751 lets mapped unprivileged container uids traverse to rootfs.
  # Secrets stay 0600 under this directory; /var/lib/ndl/control stays 0750.
  chmod 0751 /var/lib/ndl
  mkdir -p /etc/ndl
  chown root:ndl-control /etc/ndl
  chmod 0750 /etc/ndl
  mkdir -p /var/lib/ndl/certs
  chown ndl-control:ndl-control /var/lib/ndl/certs
  chmod 0700 /var/lib/ndl/certs
}

ensure_postgres_ready() {
  if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
    systemctl start postgresql >/dev/null 2>&1 || true
  fi
  n=0
  while [ "$n" -lt 30 ]; do
    if command -v pg_isready >/dev/null 2>&1 && pg_isready -q; then
      return
    fi
    n=$((n + 1))
    sleep 1
  done
  die "PostgreSQL is not ready. Start the local PostgreSQL 16 cluster and retry."
}

ensure_cluster() {
  command -v pg_lsclusters >/dev/null 2>&1 || die "pg_lsclusters not found; install postgresql"
  versions=$(pg_lsclusters --no-header 2>/dev/null | awk '{print $1}' | sort -u)
  for v in $versions; do
    case "$v" in
      16|17)
        return
        ;;
    esac
  done
  found=$(pg_lsclusters --no-header 2>/dev/null | awk '{print $1 "/" $2}' | tr '\n' ' ')
  found=${found% }
  if [ -n "$found" ]; then
    die "found PostgreSQL cluster(s) (${found}) but not version 16 or 17. Refusing to modify an unrelated cluster."
  fi
  die "no PostgreSQL cluster found. Install postgresql (Debian 13 default) or postgresql-16."
}

db_scalar() {
  printf '%s\n' "$1" | as_postgres
}

ensure_role_and_db() {
  role=$(db_scalar "SELECT 1 FROM pg_roles WHERE rolname = 'ndl-control';") || true
  if [ "$role" != "1" ]; then
    db_scalar "CREATE ROLE \"ndl-control\" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE;" >/dev/null
  fi

  db=$(db_scalar "SELECT 1 FROM pg_database WHERE datname = 'nodal';") || true
  if [ "$db" = "1" ]; then
    owner=$(db_scalar "SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'nodal';")
    if [ "$owner" != "ndl-control" ]; then
      die "database 'nodal' exists and is owned by '${owner}', not ndl-control. Refusing to drop or reuse an unrelated database."
    fi
    return
  fi
  db_scalar "CREATE DATABASE nodal OWNER \"ndl-control\";" >/dev/null
}

ensure_peer_auth() {
  hba=$(db_scalar "SHOW hba_file;")
  [ -n "$hba" ] && [ -f "$hba" ] || die "could not read PostgreSQL hba_file"
  if grep -E '^[[:space:]]*local[[:space:]]+(all|nodal)[[:space:]]+(all|ndl-control|"ndl-control")[[:space:]]+peer' "$hba" >/dev/null; then
    return
  fi
  die "PostgreSQL local peer auth is not configured for database nodal / role ndl-control. Refusing to rewrite an unknown pg_hba.conf."
}

write_env() {
  umask 077
  cat > /etc/ndl/control.env <<EOF
# Written by ndl-control postinst. Peer auth over the Unix socket.
# No password is stored. This is not a factory credential.
NODAL_PGHOST=/var/run/postgresql
NODAL_PGUSER=ndl-control
NODAL_PGDATABASE=nodal
NODAL_DSN=postgresql:///nodal?host=/var/run/postgresql
EOF
  chown root:ndl-control /etc/ndl/control.env
  chmod 0640 /etc/ndl/control.env
}

command -v adduser >/dev/null 2>&1 || die "adduser is required"
command -v addgroup >/dev/null 2>&1 || die "addgroup is required"
ensure_user
ensure_dirs
ensure_cluster
ensure_postgres_ready
command -v psql >/dev/null 2>&1 || die "psql is required"
id postgres >/dev/null 2>&1 || die "unix user postgres is missing"
ensure_role_and_db
ensure_peer_auth
write_env

ensure_identity() {
  mkdir -p /var/lib/ndl/control
  chown ndl-control:ndl-control /var/lib/ndl/control
  chmod 0750 /var/lib/ndl/control
  if [ ! -f /var/lib/ndl/host.key ]; then
    if command -v openssl >/dev/null 2>&1; then
      openssl rand -out /var/lib/ndl/host.key 32
    else
      dd if=/dev/urandom of=/var/lib/ndl/host.key bs=32 count=1 status=none
    fi
    chmod 0600 /var/lib/ndl/host.key
  fi
  if [ ! -f /etc/ndl/setup.token.hash ]; then
    token=$(openssl rand -hex 32 2>/dev/null || dd if=/dev/urandom bs=32 count=1 status=none | od -An -tx1 | tr -d ' \n')
    hash=$(printf '%s' "$token" | sha256sum | awk '{print $1}')
    printf '%s\n' "$token" > /var/lib/ndl/setup.token
    chmod 0600 /var/lib/ndl/setup.token
    printf '%s\n' "$hash" > /etc/ndl/setup.token.hash
    chown root:ndl-control /etc/ndl/setup.token.hash
    chmod 0640 /etc/ndl/setup.token.hash
    if [ -w /dev/console ]; then
      echo "No-dal setup token: ${token}" >/dev/console
    fi
  fi
}

ensure_identity
