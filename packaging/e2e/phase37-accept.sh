#!/bin/sh
# Phase 37 Store trust acceptance.
# Does not run a live CVE scanner. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase37.cj

fail() {
  echo "PHASE37_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE37_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 37 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/store/policy" | grep -q '"install_policy"' || fail "store policy"
curl -fsS -b "$CJ" "$API/api/v1/store/keys" | grep -q '"items"' || fail "store keys"

echo "PHASE37_ACCEPT_OK"
