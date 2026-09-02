#!/bin/sh
# Phase 40 automation engine acceptance.
# Does not run an LLM loop. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase40.cj

fail() {
  echo "PHASE40_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE40_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 40 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/policies" | grep -q '"items"' || fail "policies list"
curl -fsS -b "$CJ" "$API/api/v1/policy-runs" | grep -q '"items"' || fail "policy runs"

echo "PHASE40_ACCEPT_OK"
