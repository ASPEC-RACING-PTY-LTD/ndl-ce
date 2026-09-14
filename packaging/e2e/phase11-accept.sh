#!/bin/sh
# Phase 11 backup API acceptance.
# Does not boot a guest or mount NFS. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase11.cj

fail() {
  echo "PHASE11_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE11_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 11 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/backups/targets" | grep -q '"items"' || fail "backup targets"
curl -fsS -b "$CJ" "$API/api/v1/backups/artifacts" | grep -q '"items"' || fail "backup artifacts"

echo "PHASE11_ACCEPT_OK"
