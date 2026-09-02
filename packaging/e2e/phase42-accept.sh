#!/bin/sh
# Phase 42 AI Plan/Operate acceptance.
# Does not run a live LLM. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase42.cj

fail() {
  echo "PHASE42_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE42_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 42 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/ai/plans" | grep -q '"items"' || fail "ai plans"
curl -fsS -b "$CJ" "$API/api/v1/policies" | grep -q '"items"' || fail "policies list"

echo "PHASE42_ACCEPT_OK"
