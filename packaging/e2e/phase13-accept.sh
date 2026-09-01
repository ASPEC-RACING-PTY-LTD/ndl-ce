#!/bin/sh
# Phase 13 identity completion acceptance. Does not enroll hardware authenticators.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE13_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE13_ACCEPT_OK"
