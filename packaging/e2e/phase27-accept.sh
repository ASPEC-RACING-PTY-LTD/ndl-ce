#!/bin/sh
# Phase 27 advanced networking acceptance. Does not change live host bridges.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE27_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE27_ACCEPT_OK"
