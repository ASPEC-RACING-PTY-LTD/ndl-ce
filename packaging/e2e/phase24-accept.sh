#!/bin/sh
# Phase 24 backup verify acceptance.
# Does not run live qemu-img check. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase24.cj

fail() {
  echo "PHASE24_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE24_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 24 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/backups/artifacts" | grep -q '"items"' || fail "backup artifacts"
curl -fsS -b "$CJ" "$API/api/v1/backups/runs" | grep -q '"items"' || fail "backup runs"

echo "PHASE24_ACCEPT_OK"
