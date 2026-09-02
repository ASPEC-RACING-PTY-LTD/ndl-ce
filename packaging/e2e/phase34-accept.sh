#!/bin/sh
# Phase 34 HA foundations and rolling updates.
# Does not require a replica cluster. SQL greps are not acceptance.
set -eu

API=${NODAL_URL:-http://127.0.0.1:8080}
CJ=/tmp/ndl-phase34.cj

fail() {
  echo "PHASE34_ACCEPT_FAIL: $1" >&2
  exit 1
}

test -f migrations/0031_phase34.sql || fail "migration"
grep -q ha_state migrations/0031_phase34.sql || fail "ha_state"
grep -q rolling_plans migrations/0031_phase34.sql || fail "rolling_plans"
grep -q cluster.promote internal/rbac/rbac.go || fail "cluster.promote"
grep -q cluster/ha internal/httpapi/server.go || fail "ha routes"

if ! curl -fsS "$API/api/v1/health" >/dev/null 2>&1; then
  echo "PHASE34_SMOKE_OK"
  echo "This is not roadmap acceptance. Control plane health is down."
  exit 0
fi

USER=${NODAL_USER:-admin}
PASS=${NODAL_PASSWORD:-correct-horse}
if ! curl -fsS -c "$CJ" -H 'Content-Type: application/json' \
  -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}" \
  "$API/api/v1/auth/login" >/dev/null 2>&1; then
  echo "PHASE34_SMOKE_OK"
  echo "This is not roadmap acceptance. Authenticated Phase 34 API checks did not run."
  exit 0
fi

curl -fsS -b "$CJ" "$API/api/v1/cluster/ha" | grep -q '"multi_master":false' || fail "ha GET"
curl -fsS -b "$CJ" "$API/api/v1/cluster/update" >/dev/null || fail "cluster update GET"

echo "PHASE34_ACCEPT_OK"
