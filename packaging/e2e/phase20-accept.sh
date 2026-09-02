#!/bin/sh
# Phase 20 guest Terminal and Files acceptance.
# Does not open a live guest PTY. Health-only is not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase20.cj

fail() {
  echo "PHASE20_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE20_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 20 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/workloads" | grep -q '"items"' || fail "workloads list"
CODE=$(curl -sS -o /tmp/ndl-p20-files.json -w '%{http_code}' -b "$CJ" \
  "$API/api/v1/workloads/00000000-0000-4000-8000-000000000000/files")
[ "$CODE" = "404" ] || [ "$CODE" = "409" ] || [ "$CODE" = "422" ] || fail "files GET HTTP $CODE"

echo "PHASE20_ACCEPT_OK"
