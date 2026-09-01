#!/bin/sh
# Phase 18 VM advanced acceptance. Cloud proves API health; clone/import/USB are unit-tested.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE18_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE18_ACCEPT_OK"
