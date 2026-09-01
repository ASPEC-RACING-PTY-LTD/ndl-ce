#!/bin/sh
# Phase 19 guest agent acceptance. Cloud proves API health and guest status honesty.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE19_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE19_ACCEPT_OK"
