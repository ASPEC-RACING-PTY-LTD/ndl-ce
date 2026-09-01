#!/bin/sh
# Phase 25 LVM-thin acceptance. Does not create a real VG or thin LV.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE25_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE25_ACCEPT_OK"
