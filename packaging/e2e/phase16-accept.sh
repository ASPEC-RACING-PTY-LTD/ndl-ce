#!/bin/sh
# Phase 16 observability acceptance. Does not require journald follow or SMTP.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE16_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
echo "PHASE16_ACCEPT_OK"
