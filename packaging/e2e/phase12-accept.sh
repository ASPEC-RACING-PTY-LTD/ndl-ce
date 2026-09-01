#!/bin/sh
# Phase 12 platform update API acceptance. Does not run apt or stop guests.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE12_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE12_ACCEPT_OK"
