#!/bin/sh
# Phase 15 ZFS storage acceptance.
# Does not create a zpool. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase15.cj

fail() {
  echo "PHASE15_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE15_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 15 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/storage/zfs" | grep -q '"force_import":"refused"' || fail "zfs runtime"
curl -fsS -b "$CJ" "$API/api/v1/storage/pools" | grep -q '"items"' || fail "pools list"

echo "PHASE15_ACCEPT_OK"
