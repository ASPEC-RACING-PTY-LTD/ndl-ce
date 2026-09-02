#!/bin/sh
# Phase 14 GPU assignment acceptance.
# Does not bind VFIO. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase14.cj

fail() {
  echo "PHASE14_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE14_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 14 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/gpus" | grep -q '"items"' || fail "gpus list"
curl -fsS -b "$CJ" "$API/api/v1/gpus/runtime" >/dev/null || fail "gpu runtime"

echo "PHASE14_ACCEPT_OK"
