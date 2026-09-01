#!/bin/sh
# Phase 17 operator UX acceptance. Presentation only; no agent RPCs.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE17_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE17_ACCEPT_OK"
