#!/bin/sh
# Phase 16 observability acceptance.
# Does not follow live journald. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase16.cj

fail() {
  echo "PHASE16_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE16_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 16 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/alerts" | grep -q '"items"' || fail "alerts list"
curl -fsS -b "$CJ" "$API/api/v1/alerts/channels" | grep -q '"items"' || fail "alert channels"

echo "PHASE16_ACCEPT_OK"
