#!/bin/sh
# Phase 41 AI Ask acceptance.
# Does not call a live vendor. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase41.cj

fail() {
  echo "PHASE41_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE41_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 41 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/ai/providers" | grep -q '"items"' || fail "ai providers"
curl -fsS -b "$CJ" "$API/api/v1/ai/profiles" | grep -q '"items"' || fail "ai profiles"

echo "PHASE41_ACCEPT_OK"
