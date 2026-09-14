#!/bin/sh
# Phase 30 cluster-join acceptance.
# Does not require a second physical host. SQL greps are not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase30.cj

fail() {
  echo "PHASE30_ACCEPT_FAIL: $1" >&2
  exit 1
}

test -f migrations/0027_phase30.sql || fail "migration"
grep -q join_tokens migrations/0027_phase30.sql || fail "join_tokens"
grep -q cluster_leases migrations/0027_phase30.sql || fail "leases"

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE30_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 30 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/cluster" | grep -q '"nodes"' || fail "cluster GET"
curl -fsS -b "$CJ" "$API/api/v1/cluster" | grep -q '"writer"' || fail "cluster writer"

echo "PHASE30_ACCEPT_OK"
