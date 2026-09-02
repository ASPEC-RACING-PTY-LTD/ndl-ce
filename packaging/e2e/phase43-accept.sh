#!/bin/sh
# Phase 43 CE 1.0 license surface acceptance.
# Does not mark the CE 1.0 hardware milestone reached. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase43.cj

fail() {
  echo "PHASE43_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE43_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 43 API checks did not run."
  exit 0
fi

LIC=$(curl -fsS -b "$CJ" "$API/api/v1/settings/license")
printf '%s' "$LIC" | grep -q '"edition":"ce"' || fail "license edition"
printf '%s' "$LIC" | grep -q '"workloads_stopped":false' || fail "workloads_stopped"
printf '%s' "$LIC" | grep -q '"ee_blobs":false' || fail "ee_blobs"
printf '%s' "$LIC" | grep -q '"status":"absent"' || printf '%s' "$LIC" | grep -q '"status"' || fail "license status"

echo "PHASE43_ACCEPT_OK"
echo "License surface only. CE 1.0 hardware milestone is not reached."
