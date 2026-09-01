#!/bin/sh
# Phase 30 cluster-join acceptance. Does not require a second physical host.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}

fail() {
  echo "PHASE30_ACCEPT_FAIL: $1" >&2
  exit 1
}

curl -fsS "$API/api/v1/health" >/dev/null || fail "health"
test -f migrations/0027_phase30.sql || fail "migration"
grep -q join_tokens migrations/0027_phase30.sql || fail "join_tokens"
grep -q cluster_leases migrations/0027_phase30.sql || fail "leases"
echo "PHASE30_ACCEPT_OK"
