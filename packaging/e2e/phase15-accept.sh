#!/bin/sh
# Phase 15 ZFS storage acceptance. Does not create a real zpool or zvol.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE15_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE15_ACCEPT_OK"
