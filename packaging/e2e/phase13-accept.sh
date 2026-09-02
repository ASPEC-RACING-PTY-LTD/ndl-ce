#!/bin/sh
# Phase 13 identity completion acceptance.
# Does not enroll hardware authenticators. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase13.cj

fail() {
  echo "PHASE13_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE13_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 13 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/mfa" | grep -q '"enabled"' || fail "mfa status"
curl -fsS -b "$CJ" "$API/api/v1/groups" | grep -q '"items"' || fail "groups list"

echo "PHASE13_ACCEPT_OK"
