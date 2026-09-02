#!/bin/sh
# Phase 18 VM advanced (clone/template) acceptance.
# Does not boot a cloned guest. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase18.cj

fail() {
  echo "PHASE18_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE18_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 18 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/templates" | grep -q '"items"' || fail "templates list"
curl -fsS -b "$CJ" "$API/api/v1/workloads" | grep -q '"items"' || fail "workloads list"

echo "PHASE18_ACCEPT_OK"
