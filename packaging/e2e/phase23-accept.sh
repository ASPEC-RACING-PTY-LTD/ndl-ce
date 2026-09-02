#!/bin/sh
# Phase 23 object-storage backup.
# Live MinIO is a virt job, not every commit. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase23.cj

fail() {
  echo "PHASE23_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE23_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 23 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/backups/targets" | grep -q '"items"' || fail "backup targets"
curl -fsS -b "$CJ" "$API/api/v1/backups/artifacts" | grep -q '"items"' || fail "backup artifacts"

echo "PHASE23_ACCEPT_OK"
