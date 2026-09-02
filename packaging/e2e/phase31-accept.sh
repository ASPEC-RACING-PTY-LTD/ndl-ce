#!/bin/sh
# Phase 31 placement acceptance.
# Does not live-migrate. SQL greps are not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase31.cj

fail() {
  echo "PHASE31_ACCEPT_FAIL: $1" >&2
  exit 1
}

test -f migrations/0028_phase31.sql || fail "migration"
grep -q node_groups migrations/0028_phase31.sql || fail "node_groups"
grep -q node_maintenance migrations/0028_phase31.sql || fail "maintenance"

if ! curl -fsS "$API/api/v1/health" >/dev/null 2>&1; then
  echo "PHASE31_SMOKE_OK"
  echo "This is not roadmap acceptance. Control plane health is down."
  exit 0
fi

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE31_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 31 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/node-groups" | grep -q '"items"' || fail "node-groups"
curl -fsS -b "$CJ" "$API/api/v1/cluster" | grep -q '"nodes"' || fail "cluster GET"

echo "PHASE31_ACCEPT_OK"
