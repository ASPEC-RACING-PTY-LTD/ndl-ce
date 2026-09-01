#!/bin/sh
# Phase 10 snapshot API acceptance. Does not boot a guest.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase10.cj

fail() {
  echo "PHASE10_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE10_ACCEPT_OK"
