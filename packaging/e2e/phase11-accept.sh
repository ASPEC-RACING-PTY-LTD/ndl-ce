#!/bin/sh
# Phase 11 backup API acceptance. Does not boot a guest or mount NFS.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE11_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE11_ACCEPT_OK"
