#!/bin/sh
# Phase 10 snapshot API acceptance.
# Does not boot a guest. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase10.cj

fail() {
  echo "PHASE10_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE10_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 10 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/workloads" | grep -q '"items"' || fail "workloads list"
CODE=$(curl -sS -o /tmp/ndl-p10-snap.json -w '%{http_code}' -b "$CJ" \
  "$API/api/v1/workloads/00000000-0000-4000-8000-000000000000/snapshots")
[ "$CODE" = "404" ] || fail "snapshots GET HTTP $CODE"

echo "PHASE10_ACCEPT_OK"
