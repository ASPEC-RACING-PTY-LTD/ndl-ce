#!/bin/sh
# Phase 22 application stacks. Cloud proves API health.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE22_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE22_ACCEPT_OK"
